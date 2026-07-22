package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	AppName          = "host-mcp"
	Version          = "2.0.0"
	DefaultListen    = "127.0.0.1:8765"
	DefaultPath      = "/mcp"
	DefaultConfigDir = ".config/host-mcp"
	DefaultStateDir  = ".local/state/host-mcp"
)

type Profile string

const (
	ProfileTermux Profile = "termux"
	ProfileLinux  Profile = "linux"
	ProfileWSL    Profile = "wsl"
)

func (p Profile) Valid() bool { return p == ProfileTermux || p == ProfileLinux || p == ProfileWSL }

type ProfileDetector interface{ Detect() (Profile, error) }
type SystemProfileDetector struct{}

func (SystemProfileDetector) Detect() (Profile, error) {
	if runtime.GOOS != "linux" && runtime.GOOS != "android" {
		return "", fmt.Errorf("unsupported operating system %s", runtime.GOOS)
	}
	prefix, home := os.Getenv("PREFIX"), os.Getenv("HOME")
	termuxPrefix := "/data/data/com.termux/files/usr"
	termuxHome := "/data/data/com.termux/files/home"
	androidEvidence := os.Getenv("TERMUX_VERSION") != ""
	if !androidEvidence {
		_, err := os.Stat("/system/build.prop")
		androidEvidence = err == nil
	}
	if prefix == termuxPrefix && filepath.Clean(home) == termuxHome && androidEvidence {
		return ProfileTermux, nil
	}
	if runtime.GOOS == "android" {
		return "", errors.New("Android is supported only through a verified Termux environment")
	}
	b, _ := os.ReadFile("/proc/version")
	if os.Getenv("WSL_DISTRO_NAME") != "" || strings.Contains(strings.ToLower(string(b)), "microsoft") {
		return ProfileWSL, nil
	}
	return ProfileLinux, nil
}

type Limits struct {
	MaxBodyBytes, MaxReadBytes, MaxWriteBytes, MaxSearchFileSize, MaxOutputBytes int64
	MaxListEntries, MaxSearchResults, MaxExecArgs, MaxArgBytes                   int
	ExecTimeoutSec, RequestTimeoutSec, SessionTimeoutSec                         int
}

func (l Limits) MarshalJSON() ([]byte, error) {
	type limitsJSON struct {
		MaxBodyBytes      int64 `json:"max_body_bytes"`
		MaxReadBytes      int64 `json:"max_read_bytes"`
		MaxWriteBytes     int64 `json:"max_write_bytes"`
		MaxListEntries    int   `json:"max_list_entries"`
		MaxSearchResults  int   `json:"max_search_results"`
		MaxSearchFileSize int64 `json:"max_search_file_size"`
		MaxExecArgs       int   `json:"max_exec_args"`
		MaxArgBytes       int   `json:"max_arg_bytes"`
		MaxOutputBytes    int64 `json:"max_output_bytes"`
		ExecTimeoutSec    int   `json:"exec_timeout_seconds"`
		RequestTimeoutSec int   `json:"request_timeout_seconds"`
		SessionTimeoutSec int   `json:"session_timeout_seconds"`
	}
	return json.Marshal(limitsJSON{l.MaxBodyBytes, l.MaxReadBytes, l.MaxWriteBytes, l.MaxListEntries, l.MaxSearchResults, l.MaxSearchFileSize, l.MaxExecArgs, l.MaxArgBytes, l.MaxOutputBytes, l.ExecTimeoutSec, l.RequestTimeoutSec, l.SessionTimeoutSec})
}

func (l *Limits) UnmarshalJSON(b []byte) error {
	type limitsJSON struct {
		MaxBodyBytes      int64 `json:"max_body_bytes"`
		MaxReadBytes      int64 `json:"max_read_bytes"`
		MaxWriteBytes     int64 `json:"max_write_bytes"`
		MaxListEntries    int   `json:"max_list_entries"`
		MaxSearchResults  int   `json:"max_search_results"`
		MaxSearchFileSize int64 `json:"max_search_file_size"`
		MaxExecArgs       int   `json:"max_exec_args"`
		MaxArgBytes       int   `json:"max_arg_bytes"`
		MaxOutputBytes    int64 `json:"max_output_bytes"`
		ExecTimeoutSec    int   `json:"exec_timeout_seconds"`
		RequestTimeoutSec int   `json:"request_timeout_seconds"`
		SessionTimeoutSec int   `json:"session_timeout_seconds"`
	}
	var v limitsJSON
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&v); err != nil {
		return err
	}
	*l = Limits{v.MaxBodyBytes, v.MaxReadBytes, v.MaxWriteBytes, v.MaxSearchFileSize, v.MaxOutputBytes, v.MaxListEntries, v.MaxSearchResults, v.MaxExecArgs, v.MaxArgBytes, v.ExecTimeoutSec, v.RequestTimeoutSec, v.SessionTimeoutSec}
	return nil
}

type RootConfig struct {
	ID            string `json:"id"`
	Path          string `json:"path"`
	Description   string `json:"description"`
	ReadOnly      bool   `json:"read_only"`
	WriteEligible bool   `json:"write_eligible,omitempty"`
	Kind          string `json:"kind,omitempty"`
}
type Grant struct {
	Root      string `json:"root"`
	Operation string `json:"operation"`
	Path      string `json:"path"`
}
type ExecCWD struct {
	Root string `json:"root"`
	Path string `json:"path"`
}
type ExecRule struct {
	Name        string            `json:"name"`
	Executable  string            `json:"executable"`
	ArgPatterns []string          `json:"arg_patterns,omitempty"`
	CWDs        []ExecCWD         `json:"cwds,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
}
type ShellConfig struct {
	Enabled    bool      `json:"enabled"`
	Executable string    `json:"executable,omitempty"`
	CWDs       []ExecCWD `json:"cwds,omitempty"`
}
type Config struct {
	Version     int          `json:"version"`
	Profile     Profile      `json:"profile"`
	Listen      string       `json:"listen"`
	Path        string       `json:"path"`
	Origins     []string     `json:"origins,omitempty"`
	Roots       []RootConfig `json:"roots"`
	Permissions []Grant      `json:"permissions"`
	ExecEnabled bool         `json:"exec_enabled"`
	ExecRules   []ExecRule   `json:"exec_rules,omitempty"`
	Shell       ShellConfig  `json:"trusted_shell"`
	Limits      Limits       `json:"limits"`
}
type Paths struct{ Home, ConfigDir, ConfigFile, TokenFile, StateDir, AuditFile, ServiceDir, SystemdDir string }

func ResolvePaths(home string) (Paths, error) {
	if home == "" {
		return Paths{}, errors.New("HOME is not set")
	}
	a, err := filepath.Abs(home)
	if err != nil {
		return Paths{}, err
	}
	cd, sd := filepath.Join(a, DefaultConfigDir), filepath.Join(a, DefaultStateDir)
	return Paths{a, cd, filepath.Join(cd, "config.json"), filepath.Join(cd, "token"), sd, filepath.Join(sd, "audit.jsonl"), filepath.Join(a, ".termux", "service", AppName), filepath.Join(a, ".config", "systemd", "user")}, nil
}
func defaultLimits() Limits {
	return Limits{1 << 20, 1 << 20, 1 << 20, 2 << 20, 1 << 20, 1000, 200, 32, 4096, 30, 60, 900}
}
func DefaultConfig(profile Profile, home string) (Config, error) {
	if !profile.Valid() {
		return Config{}, fmt.Errorf("invalid profile %q", profile)
	}
	c := Config{Version: 2, Profile: profile, Listen: DefaultListen, Path: DefaultPath, Roots: []RootConfig{}, Permissions: []Grant{}, Limits: defaultLimits()}
	if profile != ProfileTermux {
		return c, nil
	}
	if home == "" {
		return Config{}, errors.New("HOME is not set")
	}
	h, err := filepath.Abs(home)
	if err != nil {
		return Config{}, err
	}
	prefix := os.Getenv("PREFIX")
	if prefix == "" {
		prefix = "/data/data/com.termux/files/usr"
	}
	c.Roots = append(c.Roots, RootConfig{"termux-home", h, "Termux application home", false, true, "termux-home"}, RootConfig{"termux-prefix", prefix, "Termux installation prefix", true, false, "termux-prefix"})
	c.Permissions = append(c.Permissions, Grant{"termux-home", "read", "."}, Grant{"termux-prefix", "read", "."})
	entries, _ := os.ReadDir(filepath.Join(h, "storage"))
	for _, e := range entries {
		p := filepath.Join(h, "storage", e.Name())
		target, er := filepath.EvalSymlinks(p)
		if er != nil {
			continue
		}
		st, er := os.Stat(target)
		if er != nil || !st.IsDir() {
			continue
		}
		id := "termux-storage-" + sanitizeID(e.Name())
		if id != "termux-storage-" {
			c.Roots = append(c.Roots, RootConfig{id, target, "Verified Termux storage target " + e.Name(), true, true, "termux-storage"})
			c.Permissions = append(c.Permissions, Grant{id, "read", "."})
		}
	}
	return c, nil
}
func sanitizeID(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func LoadConfig(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	var c Config
	if err = d.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return Config{}, errors.New("decode config: trailing JSON")
	}
	if err = c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

var idRE = regexp.MustCompile(`^[a-z](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

func (c Config) Validate() error {
	if c.Version != 2 {
		return fmt.Errorf("unsupported config version %d", c.Version)
	}
	if !c.Profile.Valid() {
		return fmt.Errorf("invalid profile %q", c.Profile)
	}
	host, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	addr, err := netip.ParseAddr(host)
	if err != nil || !addr.Is4() || addr.Is4In6() || addr.IsMulticast() || port == "" {
		return errors.New("listen must use a literal unicast IPv4 address or 0.0.0.0 with a port")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return errors.New("listen port must be a number from 1 to 65535")
	}
	if c.Path == "" || !strings.HasPrefix(c.Path, "/") || strings.Contains(c.Path, "?") {
		return errors.New("path must be an absolute URL path")
	}
	roots := map[string]RootConfig{}
	for _, r := range c.Roots {
		if !idRE.MatchString(r.ID) {
			return fmt.Errorf("invalid root id %q", r.ID)
		}
		if _, ok := roots[r.ID]; ok {
			return fmt.Errorf("duplicate root id %q", r.ID)
		}
		if r.Description == "" {
			return fmt.Errorf("root %q description is required", r.ID)
		}
		if !filepath.IsAbs(r.Path) || filepath.Clean(r.Path) != r.Path {
			return fmt.Errorf("root %q path must be clean and absolute", r.ID)
		}
		if err := profileRootAllowed(c.Profile, r.Path); err != nil {
			return fmt.Errorf("root %q: %w", r.ID, err)
		}
		if c.Profile == ProfileTermux && r.Kind == "termux-home" && r.ReadOnly {
			return fmt.Errorf("root %q must remain write-eligible; default read-only is enforced by grants", r.ID)
		}
		if c.Profile == ProfileTermux && (r.Kind == "termux-prefix" || r.Kind == "termux-storage") && !r.ReadOnly {
			return fmt.Errorf("root %q is profile-enforced read-only", r.ID)
		}
		roots[r.ID] = r
	}
	for _, g := range c.Permissions {
		r, ok := roots[g.Root]
		if !ok {
			return fmt.Errorf("grant references unknown root %q", g.Root)
		}
		if g.Operation != "read" && g.Operation != "write" && g.Operation != "delete" {
			return fmt.Errorf("invalid operation %q", g.Operation)
		}
		if err := validateRel(g.Path, true); err != nil {
			return fmt.Errorf("grant path: %w", err)
		}
		if g.Operation == "delete" && !grantAllows(c, g.Root, "write", g.Path) {
			return fmt.Errorf("delete grant for root %q path %q requires a matching write grant", g.Root, g.Path)
		}
		if g.Operation != "read" && (r.ReadOnly || !r.WriteEligible) {
			return fmt.Errorf("root %q is not eligible for %s", r.ID, g.Operation)
		}
	}
	for _, o := range c.Origins {
		u, err := url.Parse(o)
		if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("invalid origin %q", o)
		}
	}
	if c.ExecEnabled && len(c.ExecRules) == 0 {
		return errors.New("exec_enabled requires a rule")
	}
	names := map[string]bool{}
	for _, r := range c.ExecRules {
		if !idRE.MatchString(r.Name) || names[r.Name] {
			return fmt.Errorf("invalid or duplicate exec rule %q", r.Name)
		}
		names[r.Name] = true
		if !filepath.IsAbs(r.Executable) {
			return fmt.Errorf("exec rule %q executable must be absolute", r.Name)
		}
		if forbiddenExecutable(r.Executable) {
			return fmt.Errorf("exec rule %q executable is a forbidden shell or interpreter", r.Name)
		}
		if c.ExecEnabled && len(r.CWDs) == 0 {
			return fmt.Errorf("exec rule %q requires at least one cwd", r.Name)
		}
		canonicalExe, err := filepath.EvalSymlinks(r.Executable)
		if err != nil {
			return fmt.Errorf("exec rule %q executable: %w", r.Name, err)
		}
		if canonicalExe != filepath.Clean(r.Executable) {
			return fmt.Errorf("exec rule %q executable must be canonical and not symlinked", r.Name)
		}
		st, err := os.Stat(canonicalExe)
		if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0111 == 0 {
			return fmt.Errorf("exec rule %q executable must be an executable regular file", r.Name)
		}
		for _, p := range r.ArgPatterns {
			if _, err := regexp.Compile(p); err != nil {
				return fmt.Errorf("exec rule %q invalid argument pattern", r.Name)
			}
		}
		for _, cwd := range r.CWDs {
			if _, ok := roots[cwd.Root]; !ok {
				return fmt.Errorf("exec rule %q cwd root unknown", r.Name)
			}
			if err := validateRel(cwd.Path, true); err != nil {
				return err
			}
		}
		for k, v := range r.Environment {
			if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(k) || strings.IndexByte(v, 0) >= 0 {
				return fmt.Errorf("exec rule %q invalid environment", r.Name)
			}
		}
	}
	if c.Shell.Enabled {
		if c.Shell.Executable == "" {
			return errors.New("trusted shell requires an executable")
		}
		if !allowedShellExecutable(c.Shell.Executable) {
			return errors.New("trusted shell executable is not supported")
		}
		if len(c.Shell.CWDs) == 0 {
			return errors.New("trusted shell requires at least one starting directory")
		}
		canonicalShell, err := filepath.EvalSymlinks(c.Shell.Executable)
		if err != nil {
			return fmt.Errorf("trusted shell executable: %w", err)
		}
		if canonicalShell != filepath.Clean(c.Shell.Executable) {
			return errors.New("trusted shell executable must be stored as its canonical target")
		}
		st, err := os.Stat(canonicalShell)
		if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0111 == 0 {
			return errors.New("trusted shell executable must be an executable regular file")
		}
		for _, cwd := range c.Shell.CWDs {
			if _, ok := roots[cwd.Root]; !ok {
				return errors.New("trusted shell starting directory references an unknown authorized folder")
			}
			if err := validateRel(cwd.Path, true); err != nil {
				return fmt.Errorf("trusted shell starting directory: %w", err)
			}
		}
	} else if c.Shell.Executable != "" || len(c.Shell.CWDs) != 0 {
		return errors.New("trusted shell settings must be empty while disabled")
	}
	l := c.Limits
	if l.MaxBodyBytes <= 0 || l.MaxReadBytes <= 0 || l.MaxWriteBytes <= 0 || l.MaxListEntries <= 0 || l.MaxSearchResults <= 0 || l.MaxSearchFileSize <= 0 || l.MaxExecArgs <= 0 || l.MaxArgBytes <= 0 || l.MaxOutputBytes <= 0 || l.ExecTimeoutSec <= 0 || l.RequestTimeoutSec <= 0 || l.SessionTimeoutSec <= 0 {
		return errors.New("all limits must be positive")
	}
	if l.RequestTimeoutSec > 600 || l.SessionTimeoutSec > 86400 || l.ExecTimeoutSec > 300 {
		return errors.New("timeout limit is too large")
	}
	return nil
}
func profileRootAllowed(p Profile, path string) error {
	denies := []string{"/", "/proc", "/sys", "/dev", "/run", "/boot"}
	if p == ProfileWSL {
		denies = append(denies, "/mnt")
	}
	clean := filepath.Clean(path)
	if p != ProfileTermux {
		for _, d := range denies {
			if clean == d || (d != "/" && strings.HasPrefix(clean, d+string(filepath.Separator))) {
				return fmt.Errorf("profile %s hard-denies %s", p, d)
			}
		}
	}
	return nil
}
func validateRel(s string, allowDot bool) error {
	if s == "" || strings.IndexByte(s, 0) >= 0 || filepath.IsAbs(s) {
		return errors.New("path must be relative")
	}
	c := filepath.ToSlash(filepath.Clean(filepath.FromSlash(s)))
	if c == "." && allowDot {
		return nil
	}
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return errors.New("path must name a confined subtree")
	}
	return nil
}
func forbiddenExecutable(p string) bool {
	n := strings.ToLower(filepath.Base(p))
	return map[string]bool{"sh": true, "bash": true, "dash": true, "zsh": true, "fish": true, "ksh": true, "python": true, "python3": true, "perl": true, "ruby": true, "node": true, "php": true, "lua": true, "pwsh": true, "powershell": true, "cmd.exe": true}[n]
}
func allowedShellExecutable(p string) bool {
	n := strings.ToLower(filepath.Base(p))
	return n == "sh" || n == "bash" || n == "dash" || n == "zsh" || n == "fish" || n == "ksh"
}
func (c *Config) Normalize() {
	slices.SortFunc(c.Roots, func(a, b RootConfig) int { return strings.Compare(a.ID, b.ID) })
	slices.SortFunc(c.Permissions, func(a, b Grant) int {
		if a.Root != b.Root {
			return strings.Compare(a.Root, b.Root)
		}
		if a.Operation != b.Operation {
			return strings.Compare(a.Operation, b.Operation)
		}
		return strings.Compare(a.Path, b.Path)
	})
	c.Permissions = slices.Compact(c.Permissions)
	slices.Sort(c.Origins)
	c.Origins = slices.Compact(c.Origins)
	slices.SortFunc(c.ExecRules, func(a, b ExecRule) int { return strings.Compare(a.Name, b.Name) })
}
func DurationSeconds(v int) time.Duration { return time.Duration(v) * time.Second }
func (c Config) Root(id string) (RootConfig, bool) {
	for _, r := range c.Roots {
		if r.ID == id {
			return r, true
		}
	}
	return RootConfig{}, false
}
func grantAllows(c Config, root, op, path string) bool {
	for _, g := range c.Permissions {
		if g.Root == root && g.Operation == op && pathWithin(path, g.Path) {
			return true
		}
	}
	return false
}
func pathWithin(path, base string) bool {
	path = filepath.ToSlash(filepath.Clean(path))
	base = filepath.ToSlash(filepath.Clean(base))
	return base == "." || path == base || strings.HasPrefix(path, strings.TrimSuffix(base, "/")+"/")
}
