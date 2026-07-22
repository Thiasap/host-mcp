package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fixedDetector Profile

func (f fixedDetector) Detect() (Profile, error) { return Profile(f), nil }
func testConfig(t *testing.T) (Config, Paths) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "root")
	home := filepath.Join(base, "home")
	if err := os.MkdirAll(filepath.Join(root, "work"), 0700); err != nil {
		t.Fatal(err)
	}
	cfg, e := DefaultConfig(ProfileLinux, home)
	if e != nil {
		t.Fatal(e)
	}
	cfg.Roots = []RootConfig{{"workspace", root, "Workspace", false, true, "user"}}
	cfg.Permissions = []Grant{{"workspace", "read", "."}, {"workspace", "write", "work"}, {"workspace", "delete", "work"}}
	p, e := ResolvePaths(home)
	if e != nil {
		t.Fatal(e)
	}
	if e = SaveConfig(p.ConfigFile, cfg); e != nil {
		t.Fatal(e)
	}
	if e = SaveToken(p.TokenFile, "test-token"); e != nil {
		t.Fatal(e)
	}
	return cfg, p
}
func TestProfilesAndDefaultDeny(t *testing.T) {
	for _, p := range []Profile{ProfileLinux, ProfileWSL} {
		c, e := DefaultConfig(p, t.TempDir())
		if e != nil || len(c.Roots) != 0 || len(c.Permissions) != 0 {
			t.Fatalf("%s default: %+v %v", p, c, e)
		}
	}
	c, _ := DefaultConfig(ProfileWSL, t.TempDir())
	c.Roots = []RootConfig{{"bad", "/mnt/c", "Bad", true, false, "user"}}
	if c.Validate() == nil {
		t.Fatal("WSL /mnt root accepted")
	}
	c.Roots = []RootConfig{{"bad", "/proc", "Bad", true, false, "user"}}
	if c.Validate() == nil {
		t.Fatal("/proc root accepted")
	}
}
func TestListenValidation(t *testing.T) {
	cfg, err := DefaultConfig(ProfileLinux, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != DefaultListen {
		t.Fatalf("default listen %q, want %q", cfg.Listen, DefaultListen)
	}
	valid := []string{"127.0.0.1:8765", "192.168.43.1:8765", "10.0.0.4:443", "203.0.113.10:65535", "0.0.0.0:8765"}
	for _, listen := range valid {
		t.Run("valid_"+strings.ReplaceAll(listen, ":", "_"), func(t *testing.T) {
			candidate := cfg
			candidate.Listen = listen
			if err := candidate.Validate(); err != nil {
				t.Fatalf("%s rejected: %v", listen, err)
			}
		})
	}
	invalid := []string{"", ":8765", "localhost:8765", "host.local:8765", "[::1]:8765", "[::]:8765", "[::ffff:192.168.43.1]:8765", "224.0.0.1:8765", "239.255.255.250:8765", "127.0.0.1", "127.0.0.1:http", "127.0.0.1:0", "127.0.0.1:65536"}
	for _, listen := range invalid {
		t.Run("invalid_"+strings.NewReplacer(":", "_", "[", "", "]", "").Replace(listen), func(t *testing.T) {
			candidate := cfg
			candidate.Listen = listen
			if err := candidate.Validate(); err == nil {
				t.Fatalf("%s accepted", listen)
			}
		})
	}
}
func TestConfigSetListen(t *testing.T) {
	_, p := testConfig(t)
	var out, stderr bytes.Buffer
	cli := CLI{Out: &out, Err: &stderr, In: strings.NewReader(""), Home: p.Home, IsTTY: func() bool { return false }}

	cli.Args = []string{"config", "set-listen", "192.168.43.1:8765"}
	if cli.Run() == 0 {
		t.Fatal("non-TTY set-listen without --yes accepted")
	}
	cfg, err := LoadConfig(p.ConfigFile)
	if err != nil || cfg.Listen != DefaultListen {
		t.Fatalf("configuration changed after rejected operation: %+v %v", cfg, err)
	}

	out.Reset()
	stderr.Reset()
	cli.Args = []string{"config", "set-listen", "0.0.0.0:8765", "--dry-run"}
	if cli.Run() != 0 {
		t.Fatal(stderr.String())
	}
	cfg, err = LoadConfig(p.ConfigFile)
	if err != nil || cfg.Listen != DefaultListen {
		t.Fatalf("dry-run changed configuration: %+v %v", cfg, err)
	}
	if !strings.Contains(out.String(), `"non_loopback": true`) {
		t.Fatalf("dry-run preview missing exposure flag: %s", out.String())
	}

	out.Reset()
	stderr.Reset()
	cli.Args = []string{"config", "set-listen", "192.168.43.1:8765", "--yes"}
	if cli.Run() != 0 {
		t.Fatal(stderr.String())
	}
	cfg, err = LoadConfig(p.ConfigFile)
	if err != nil || cfg.Listen != "192.168.43.1:8765" {
		t.Fatalf("listen not saved: %+v %v", cfg, err)
	}
	if !strings.Contains(out.String(), "host-mcp service restart") {
		t.Fatalf("restart hint missing: %s", out.String())
	}

	out.Reset()
	stderr.Reset()
	cli.Args = []string{"config", "set-listen", "localhost:8765", "--yes"}
	if cli.Run() == 0 {
		t.Fatal("hostname listen accepted")
	}
	cfg, err = LoadConfig(p.ConfigFile)
	if err != nil || cfg.Listen != "192.168.43.1:8765" {
		t.Fatalf("invalid listen changed configuration: %+v %v", cfg, err)
	}
}
func TestStrictConfigAndRootIDs(t *testing.T) {
	cfg, p := testConfig(t)
	if e := cfg.Validate(); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(p.ConfigFile)
	b = []byte(strings.TrimSuffix(string(b), "}\n") + `,"unexpected":true}`)
	if e := os.WriteFile(p.ConfigFile, b, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := LoadConfig(p.ConfigFile); e == nil {
		t.Fatal("unknown config field accepted")
	}
	cfg.Roots[0].ID = "Bad ID"
	if cfg.Validate() == nil {
		t.Fatal("invalid root ID accepted")
	}
}
func TestNamedRootsAndMutationSafety(t *testing.T) {
	cfg, p := testConfig(t)
	fsys, e := OpenFileSystem(cfg, Auditor{Path: p.AuditFile})
	if e != nil {
		t.Fatal(e)
	}
	defer fsys.Close()
	file := filepath.Join(cfg.Roots[0].Path, "work", "x.txt")
	if e = os.WriteFile(file, []byte("old"), 0600); e != nil {
		t.Fatal(e)
	}
	out, e := fsys.Read(context.Background(), ReadInput{Root: "workspace", Path: "work/x.txt"})
	if e != nil || out.Data != "old" {
		t.Fatalf("read: %+v %v", out, e)
	}
	if _, e = fsys.Read(context.Background(), ReadInput{Root: "workspace", Path: "../x"}); e == nil {
		t.Fatal("traversal accepted")
	}
	if _, e = fsys.Write(context.Background(), WriteInput{Root: "workspace", Path: "work/x.txt", Data: "new", Overwrite: true}); e == nil {
		t.Fatal("overwrite without hash accepted")
	}
	if _, e = fsys.Delete(context.Background(), DeleteInput{Root: "workspace", Path: "work/x.txt"}); e == nil {
		t.Fatal("delete without hash accepted")
	}
	if _, e = fsys.Rename(context.Background(), RenameInput{SourceRoot: "workspace", SourcePath: "work/x.txt", DestinationRoot: "other", DestinationPath: "x.txt"}); e == nil || !strings.Contains(e.Error(), "cross-root") {
		t.Fatal("cross-root rename accepted")
	}
}
func TestExecDefaultsAndInterpreterRejection(t *testing.T) {
	cfg, _ := testConfig(t)
	if cfg.ExecEnabled {
		t.Fatal("exec enabled by default")
	}
	cfg.ExecEnabled = true
	cfg.ExecRules = []ExecRule{{Name: "shell", Executable: "/bin/sh", CWDs: []ExecCWD{{Root: "workspace", Path: "."}}}}
	if cfg.Validate() == nil {
		t.Fatal("shell executable accepted")
	}
	cfg.ExecRules = []ExecRule{{Name: "print", Executable: "/usr/bin/printf"}}
	if cfg.Validate() == nil {
		t.Fatal("enabled rule without cwd accepted")
	}
	cfg.ExecRules = []ExecRule{{Name: "print", Executable: "/usr/bin/printf", CWDs: []ExecCWD{{Root: "workspace", Path: "."}}}}
	if _, err := RunExec(context.Background(), cfg, Auditor{}, ExecInput{Rule: "print", Args: []string{"x"}}); err == nil || !strings.Contains(err.Error(), "cwd is required") {
		t.Fatalf("missing cwd accepted: %v", err)
	}
}
func TestNonTTYInitRequirementsAndDryRun(t *testing.T) {
	home := t.TempDir()
	var out, err bytes.Buffer
	c := CLI{Out: &out, Err: &err, In: strings.NewReader(""), Home: home, Args: []string{"init", "--dry-run"}, Detector: fixedDetector(ProfileWSL), IsTTY: func() bool { return false }}
	if c.Run() == 0 {
		t.Fatal("non-TTY dry run without profile accepted")
	}
	c.Args = []string{"init", "--profile", "wsl", "--dry-run"}
	if c.Run() != 0 {
		t.Fatal(err.String())
	}
	p, _ := ResolvePaths(home)
	if !isMissing(p.ConfigFile) {
		t.Fatal("dry-run wrote config")
	}
	c.Args = []string{"init", "--profile", "wsl"}
	if c.Run() == 0 {
		t.Fatal("non-TTY init without yes accepted")
	}
	c.Args = []string{"init", "--profile", "wsl", "--yes"}
	if c.Run() != 0 {
		t.Fatal(err.String())
	}
	cfg, e := LoadConfig(p.ConfigFile)
	if e != nil || cfg.Profile != ProfileWSL || len(cfg.Roots) != 0 {
		t.Fatalf("init result %+v %v", cfg, e)
	}
}
func TestInitUserExperience(t *testing.T) {
	t.Run("dry run summary", func(t *testing.T) {
		home := t.TempDir()
		var out, stderr bytes.Buffer
		cli := CLI{Out: &out, Err: &stderr, In: strings.NewReader(""), Home: home, Args: []string{"init", "--profile", "linux", "--dry-run"}, IsTTY: func() bool { return false }}
		if cli.Run() != 0 {
			t.Fatal(stderr.String())
		}
		text := out.String()
		for _, want := range []string{"host-mcp initialization", "Profile:        linux", "no roots are configured", "Authorized folders", "unrelated to Android root", "Trusted Shell", "Preview only", "no configuration or Bearer Token"} {
			if !strings.Contains(text, want) {
				t.Fatalf("summary missing %q:\n%s", want, text)
			}
		}
		if strings.Contains(text, `"action"`) {
			t.Fatalf("raw JSON preview remains:\n%s", text)
		}
	})

	t.Run("interactive default cancels", func(t *testing.T) {
		home := t.TempDir()
		var out, stderr bytes.Buffer
		cli := CLI{Out: &out, Err: &stderr, In: strings.NewReader("\n"), Home: home, Args: []string{"init", "--profile", "linux"}, IsTTY: func() bool { return true }}
		if cli.Run() == 0 {
			t.Fatal("empty confirmation accepted")
		}
		p, _ := ResolvePaths(home)
		if !isMissing(p.ConfigFile) || !isMissing(p.TokenFile) {
			t.Fatal("cancelled init wrote files")
		}
		for _, want := range []string{"generate a new Bearer Token? [y/N]", "Cancelled; no files were created"} {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("cancel output missing %q:\n%s", want, out.String())
			}
		}
	})

	t.Run("successful termux guidance", func(t *testing.T) {
		home := t.TempDir()
		prefix := filepath.Join(t.TempDir(), "usr")
		if err := os.MkdirAll(prefix, 0700); err != nil {
			t.Fatal(err)
		}
		oldPrefix := os.Getenv("PREFIX")
		t.Cleanup(func() { _ = os.Setenv("PREFIX", oldPrefix) })
		if err := os.Setenv("PREFIX", prefix); err != nil {
			t.Fatal(err)
		}
		var out, stderr bytes.Buffer
		cli := CLI{Out: &out, Err: &stderr, In: strings.NewReader(""), Home: home, Args: []string{"init", "--profile", "termux", "--yes"}, IsTTY: func() bool { return false }}
		if cli.Run() != 0 {
			t.Fatal(stderr.String())
		}
		p, _ := ResolvePaths(home)
		token, err := LoadToken(p.TokenFile)
		if err != nil {
			t.Fatal(err)
		}
		text := out.String()
		for _, want := range []string{token, "copy this into your MCP client", "Treat this Token like a password", "host-mcp token show", "host-mcp token rotate", "host-mcp commands setup", "host-mcp commands status", "host-mcp shell enable", "not a sandbox", "Choose exactly one startup mode", "host-mcp serve", "host-mcp service enable", "enable also starts the runit service"} {
			if !strings.Contains(text, want) {
				t.Fatalf("success output missing %q:\n%s", want, text)
			}
		}
	})

	t.Run("existing files fail before prompt", func(t *testing.T) {
		home := t.TempDir()
		p, _ := ResolvePaths(home)
		if err := SaveToken(p.TokenFile, "existing-token"); err != nil {
			t.Fatal(err)
		}
		var out, stderr bytes.Buffer
		cli := CLI{Out: &out, Err: &stderr, In: strings.NewReader("y\n"), Home: home, Args: []string{"init", "--profile", "linux"}, IsTTY: func() bool { return true }}
		if cli.Run() == 0 {
			t.Fatal("existing token accepted")
		}
		if strings.Contains(out.String(), "generate a new Bearer Token? [y/N]") {
			t.Fatalf("prompt shown despite existing files:\n%s", out.String())
		}
		if token, err := LoadToken(p.TokenFile); err != nil || token != "existing-token" {
			t.Fatalf("existing token changed: %q %v", token, err)
		}
	})
}
func TestTermuxEffectiveReadOnly(t *testing.T) {
	home := t.TempDir()
	old := os.Getenv("PREFIX")
	t.Cleanup(func() { os.Setenv("PREFIX", old) })
	os.Setenv("PREFIX", filepath.Join(t.TempDir(), "usr"))
	os.MkdirAll(os.Getenv("PREFIX"), 0700)
	cfg, e := DefaultConfig(ProfileTermux, home)
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range effectiveConfig(cfg).Roots {
		if (r.Kind == "termux-prefix" || r.Kind == "termux-storage") && !r.ReadOnly {
			t.Fatalf("root %s not read-only", r.ID)
		}
	}
	if !grantAllows(cfg, "termux-home", "read", ".") || grantAllows(cfg, "termux-home", "write", ".") {
		t.Fatal("termux home defaults are not read-only policy")
	}
}
func TestServeAddressInUseGuidance(t *testing.T) {
	cfg, p := testConfig(t)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	cfg.Listen = listener.Addr().String()
	rt, err := NewRuntime(cfg, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	err = rt.Serve(context.Background())
	if err == nil {
		t.Fatal("second listener unexpectedly succeeded")
	}
	for _, want := range []string{"address already in use", "host-mcp service status", "host-mcp status", "host-mcp service stop"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("occupied-port error missing %q: %v", want, err)
		}
	}
}
func TestHTTPAuthAndOrigin(t *testing.T) {
	cfg, p := testConfig(t)
	cfg.Origins = []string{"https://client.example"}
	rt, err := NewRuntime(cfg, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	req := httptest.NewRequest(http.MethodPost, "http://localhost"+cfg.Path, strings.NewReader(`{}`))
	res := httptest.NewRecorder()
	rt.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized || res.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("unauthenticated request: status=%d headers=%v", res.Code, res.Header())
	}

	req = httptest.NewRequest(http.MethodPost, "http://localhost"+cfg.Path, strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Origin", "https://evil.example")
	res = httptest.NewRecorder()
	rt.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("unexpected origin status %d", res.Code)
	}
}
func TestCommandToolsDisabledGuidance(t *testing.T) {
	cfg, _ := testConfig(t)
	status := commandCapabilityStatus(cfg)
	if status.ControlledEnabled || status.TrustedShell {
		t.Fatalf("unexpected default command status: %+v", status)
	}
	controlled, err := runControlledCommand(context.Background(), cfg, Auditor{}, ExecInput{})
	if err != nil || controlled.Status != "disabled" || !strings.Contains(controlled.Message, "host-mcp commands setup") {
		t.Fatalf("controlled guidance: %+v %v", controlled, err)
	}
	shell, err := RunTrustedShell(context.Background(), cfg, Auditor{}, ShellInput{})
	if err != nil || shell.Status != "disabled" || !strings.Contains(shell.Message, "host-mcp shell enable") {
		t.Fatalf("shell guidance: %+v %v", shell, err)
	}
}

func TestTrustedShellCLIRequiresExplicitRiskAcceptance(t *testing.T) {
	_, p := testConfig(t)
	var out, stderr bytes.Buffer
	cli := CLI{Out: &out, Err: &stderr, In: strings.NewReader(""), Home: p.Home, IsTTY: func() bool { return false }}
	cli.Args = []string{"shell", "enable", "--folder", "workspace:.", "--yes"}
	if cli.Run() == 0 {
		t.Fatal("trusted shell enabled without exact risk acceptance")
	}
	cfg, err := LoadConfig(p.ConfigFile)
	if err != nil || cfg.Shell.Enabled {
		t.Fatalf("trusted shell changed after rejection: %+v %v", cfg.Shell, err)
	}
	if !strings.Contains(out.String(), "not a sandbox") {
		t.Fatalf("risk warning missing: %s", out.String())
	}
}

func TestOfficialMCPHTTPRootPath(t *testing.T) {
	cfg, p := testConfig(t)
	if e := os.WriteFile(filepath.Join(cfg.Roots[0].Path, "work", "hello.txt"), []byte("hello"), 0600); e != nil {
		t.Fatal(e)
	}
	rt, e := NewRuntime(cfg, p, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer rt.Close()
	ts := httptest.NewServer(rt.Handler())
	defer ts.Close()
	hc := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		r.Header.Set("Authorization", "Bearer test-token")
		return http.DefaultTransport.RoundTrip(r)
	})}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	sess, e := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.URL + cfg.Path, HTTPClient: hc, DisableStandaloneSSE: true}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer sess.Close()
	tools, e := sess.ListTools(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	var names []string
	for _, x := range tools.Tools {
		names = append(names, x.Name)
	}
	for _, want := range []string{"fs_roots", "fs_stat", "fs_list", "fs_read", "fs_search", "fs_write", "fs_mkdir", "fs_rename", "fs_delete", "command_status", "exec_run", "shell_run"} {
		if !slices.Contains(names, want) {
			t.Fatalf("missing %s in %v", want, names)
		}
	}
	res, e := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "fs_read", Arguments: map[string]any{"root": "workspace", "path": "work/hello.txt"}})
	if e != nil || res.IsError {
		t.Fatalf("call %+v %v", res, e)
	}
	b, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(b), "hello") {
		t.Fatalf("result %s", b)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
