package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Runtime struct {
	Config     Config
	Paths      Paths
	FS         *FileSystem
	Audit      Auditor
	token      atomic.Value
	generation atomic.Uint64
	logger     *slog.Logger
}

func NewRuntime(c Config, p Paths, logger *slog.Logger) (*Runtime, error) {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	a := Auditor{Path: p.AuditFile, MaxBytes: 2 << 20, Keep: 3}
	f, err := OpenFileSystem(c, a)
	if err != nil {
		return nil, err
	}
	t, err := LoadToken(p.TokenFile)
	if err != nil {
		f.Close()
		return nil, err
	}
	r := &Runtime{Config: c, Paths: p, FS: f, Audit: a, logger: logger}
	r.token.Store(t)
	r.generation.Store(1)
	return r, nil
}
func (r *Runtime) Close() error { return r.FS.Close() }
func (r *Runtime) RotateToken() (string, error) {
	t, err := NewToken()
	if err != nil {
		return "", err
	}
	if err := SaveToken(r.Paths.TokenFile, t); err != nil {
		return "", err
	}
	r.token.Store(t)
	r.generation.Add(1)
	r.Audit.Log("token_rotate", "ok", "", "")
	return t, nil
}
func (r *Runtime) CurrentToken() (string, error) {
	t, err := LoadToken(r.Paths.TokenFile)
	if err != nil {
		return "", err
	}
	r.token.Store(t)
	return t, nil
}

func addTyped[In, Out any](s *mcp.Server, name, desc string, h func(context.Context, In) (Out, error)) {
	mcp.AddTool(s, &mcp.Tool{Name: name, Description: desc}, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		out, err := h(ctx, in)
		return nil, out, err
	})
}
func hasGrant(c Config, operation string) bool {
	for _, g := range c.Permissions {
		if g.Operation == operation {
			return true
		}
	}
	return false
}

func (r *Runtime) MCPServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: AppName, Title: "Secure Host MCP", Version: Version}, &mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{}, KeepAlive: 30 * time.Second})
	addTyped(s, "fs_roots", "List authorized folders (internally named roots) and their effective properties.", r.FS.Roots)
	addTyped(s, "fs_stat", "Inspect metadata for an allowed root and path.", r.FS.Stat)
	addTyped(s, "fs_list", "List an allowed directory with a bounded result set.", r.FS.List)
	addTyped(s, "fs_read", "Read bounded bytes from an allowed regular file; binary data is returned as base64.", r.FS.Read)
	addTyped(s, "fs_search", "Search bounded regular files under an allowed path.", r.FS.Search)
	if hasGrant(r.Config, "write") {
		addTyped(s, "fs_write", "Write a bounded file only inside explicitly granted write roots.", r.FS.Write)
		addTyped(s, "fs_mkdir", "Create a directory only inside explicitly granted write roots.", r.FS.Mkdir)
		addTyped(s, "fs_rename", "Rename a path only when source and destination are explicitly writable.", r.FS.Rename)
	}
	if hasGrant(r.Config, "delete") {
		addTyped(s, "fs_delete", "Delete a path only inside explicitly granted delete roots.", r.FS.Delete)
	}
	addTyped(s, "command_status", "Report controlled-command and Trusted Shell availability, risks, and device-owner setup steps.", func(context.Context, CommandStatusInput) (CommandCapabilityStatus, error) {
		return commandCapabilityStatus(r.Config), nil
	})
	addTyped(s, "exec_run", "Run one device-owner-approved executable rule without a shell. When disabled, returns setup guidance.", func(ctx context.Context, in ExecInput) (CommandRunOutput, error) {
		return runControlledCommand(ctx, r.Config, r.Audit, in)
	})
	addTyped(s, "shell_run", "Run a command through explicitly enabled high-risk Trusted Shell. It is not confined by authorized folders and is not a sandbox.", func(ctx context.Context, in ShellInput) (ShellOutput, error) {
		return RunTrustedShell(ctx, r.Config, r.Audit, in)
	})
	return s
}

func (r *Runtime) Handler() http.Handler {
	sdk := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return r.MCPServer() }, &mcp.StreamableHTTPOptions{Stateless: false, JSONResponse: false, Logger: r.logger, SessionTimeout: DurationSeconds(r.Config.Limits.SessionTimeoutSec), DisableLocalhostProtection: false})
	var h http.Handler = sdk
	h = r.pathOnly(h)
	h = r.bodyLimit(h)
	h = r.origin(h)
	h = r.auth(h)
	h = r.timeout(h)
	return h
}
func (r *Runtime) timeout(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		ctx, cancel := context.WithTimeout(q.Context(), DurationSeconds(r.Config.Limits.RequestTimeoutSec))
		defer cancel()
		next.ServeHTTP(w, q.WithContext(ctx))
	})
}
func (r *Runtime) pathOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		if q.URL.Path != r.Config.Path {
			http.NotFound(w, q)
			return
		}
		next.ServeHTTP(w, q)
	})
}
func (r *Runtime) bodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		q.Body = http.MaxBytesReader(w, q.Body, r.Config.Limits.MaxBodyBytes)
		next.ServeHTTP(w, q)
	})
}
func (r *Runtime) origin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		o := q.Header.Get("Origin")
		if o != "" && !slicesContains(r.Config.Origins, o) {
			r.Audit.Log("http_origin", "deny", "", "")
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, q)
	})
}
func slicesContains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
func (r *Runtime) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		values := q.Header.Values("Authorization")
		const p = "Bearer "
		token, tokenErr := r.CurrentToken()
		valid := len(values) == 1 && strings.HasPrefix(values[0], p)
		provided := ""
		if valid {
			provided = strings.TrimPrefix(values[0], p)
			valid = provided != "" && !strings.ContainsAny(provided, " \t\r\n")
		}
		if tokenErr != nil || !valid || !TokenEqual(provided, token) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="host-mcp"`)
			r.Audit.Log("http_auth", "deny", "", "")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		gen := r.generation.Load()
		q = q.WithContext(context.WithValue(q.Context(), generationKey{}, gen))
		next.ServeHTTP(w, q)
	})
}

type generationKey struct{}

func (r *Runtime) Serve(ctx context.Context) error {
	ln, err := net.Listen("tcp4", r.Config.Listen)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return fmt.Errorf("listen %s: address already in use; a managed host-mcp service or another foreground instance may already be running. Check `host-mcp service status` and `host-mcp status`; run `host-mcp service stop` before switching to manual `host-mcp serve`: %w", r.Config.Listen, err)
		}
		return err
	}
	srv := &http.Server{Handler: r.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: DurationSeconds(r.Config.Limits.RequestTimeoutSec), WriteTimeout: DurationSeconds(r.Config.Limits.RequestTimeoutSec) + 10*time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 16 << 10}
	r.Audit.Log("serve", "start", r.Config.Listen, r.Config.Path)
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
		return nil
	}
}

func JSON(v any) string { b, _ := json.MarshalIndent(v, "", "  "); return string(b) }
func HTTPStatus(c Config, p Paths) (map[string]any, error) {
	tokenErr := CheckPrivate(p.TokenFile, false)
	configErr := CheckPrivate(p.ConfigFile, false)
	conn, err := net.DialTimeout("tcp4", c.Listen, 500*time.Millisecond)
	running := err == nil
	if conn != nil {
		conn.Close()
	}
	return map[string]any{"version": Version, "profile": c.Profile, "listen": c.Listen, "path": c.Path, "running": running, "config_secure": configErr == nil, "token_secure": tokenErr == nil, "config_error": errString(configErr), "token_error": errString(tokenErr), "commands": commandCapabilityStatus(c)}, nil
}
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
func errorJSON(err error) string { return fmt.Sprintf(`{"error":%q}`, err.Error()) }
