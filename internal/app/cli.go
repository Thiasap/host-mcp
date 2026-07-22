package app

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type CLI struct {
	Out, Err io.Writer
	In       io.Reader
	Home     string
	Args     []string
	Detector ProfileDetector
	IsTTY    func() bool
}

func Main(args []string) int {
	return CLI{Out: os.Stdout, Err: os.Stderr, In: os.Stdin, Home: os.Getenv("HOME"), Args: args, Detector: SystemProfileDetector{}, IsTTY: func() bool { st, e := os.Stdin.Stat(); return e == nil && st.Mode()&os.ModeCharDevice != 0 }}.Run()
}
func (c CLI) Run() int {
	if len(c.Args) == 0 {
		return c.usage(errors.New("command required"))
	}
	p, e := ResolvePaths(c.Home)
	if e != nil {
		return c.fail(e)
	}
	cmd, args := c.Args[0], c.Args[1:]
	switch cmd {
	case "version":
		fmt.Fprintln(c.Out, Version)
		return 0
	case "profile":
		return c.profileCmd(args)
	case "init":
		return c.initCmd(p, args)
	case "serve":
		return c.serveCmd(p, args)
	case "config":
		return c.configCmd(p, args)
	case "roots":
		return c.rootsCmd(p, args)
	case "permissions":
		return c.permissionsCmd(p, args)
	case "policy":
		return c.policyCmd(p, args)
	case "exec":
		return c.execCmd(p, args)
	case "commands":
		return c.commandsCmd(p, args)
	case "shell":
		return c.shellCmd(p, args)
	case "service":
		return c.serviceCmd(p, args)
	case "token":
		return c.tokenCmd(p, args)
	case "status":
		return c.statusCmd(p)
	case "doctor":
		return c.doctorCmd(p)
	default:
		return c.usage(fmt.Errorf("unknown command %q", cmd))
	}
}
func (c CLI) usage(e error) int {
	if e != nil {
		fmt.Fprintln(c.Err, "error:", e)
	}
	fmt.Fprintln(c.Err, "usage: host-mcp <serve|init|version|profile|config|roots|permissions|policy|commands|shell|exec|service|token|status|doctor> ...")
	return 2
}
func (c CLI) fail(e error) int { fmt.Fprintln(c.Err, "error:", e); return 1 }
func parse(name string) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	return f
}
func (c CLI) detector() ProfileDetector {
	if c.Detector != nil {
		return c.Detector
	}
	return SystemProfileDetector{}
}
func (c CLI) tty() bool {
	if c.IsTTY != nil {
		return c.IsTTY()
	}
	return false
}
func parseProfile(s string) (Profile, error) {
	p := Profile(s)
	if !p.Valid() {
		return "", errors.New("profile must be termux, linux, or wsl")
	}
	return p, nil
}
func (c CLI) profileCmd(args []string) int {
	if len(args) != 1 || args[0] != "detect" {
		return c.usage(errors.New("profile requires detect"))
	}
	p, e := c.detector().Detect()
	if e != nil {
		return c.fail(e)
	}
	fmt.Fprintln(c.Out, p)
	return 0
}
func (c CLI) initCmd(p Paths, args []string) int {
	f := parse("init")
	prof := f.String("profile", "", "termux, linux, or wsl")
	yes := f.Bool("yes", false, "confirm initialization")
	dry := f.Bool("dry-run", false, "preview without writes")
	if e := f.Parse(args); e != nil {
		return c.fail(e)
	}
	var profile Profile
	var e error
	if *prof != "" {
		profile, e = parseProfile(*prof)
	} else if c.tty() {
		profile, e = c.detector().Detect()
	} else {
		return c.fail(errors.New("non-TTY init requires explicit --profile"))
	}
	if e != nil {
		return c.fail(e)
	}
	cfg, e := DefaultConfig(profile, c.Home)
	if e != nil {
		return c.fail(e)
	}
	printInitSummary(c.Out, cfg, p, *dry)
	if *dry {
		return 0
	}
	if !isMissing(p.ConfigFile) || !isMissing(p.TokenFile) {
		return c.fail(errors.New("configuration or token already exists; existing files were not changed"))
	}
	if !*yes {
		if !c.tty() {
			return c.fail(errors.New("non-TTY init requires --yes"))
		}
		fmt.Fprint(c.Out, "Create these configuration files and generate a new Bearer Token? [y/N] ")
		line, _ := bufio.NewReader(c.In).ReadString('\n')
		if strings.ToLower(strings.TrimSpace(line)) != "y" {
			fmt.Fprintln(c.Out, "Cancelled; no files were created.")
			return 1
		}
	}
	tok, e := NewToken()
	if e == nil {
		e = SaveToken(p.TokenFile, tok)
	}
	if e == nil {
		e = SaveConfig(p.ConfigFile, cfg)
	}
	if e == nil {
		e = EnsurePrivateDir(p.StateDir)
	}
	if e != nil {
		return c.fail(e)
	}
	printInitSuccess(c.Out, cfg, p, tok)
	return 0
}
func printInitSummary(w io.Writer, cfg Config, p Paths, dryRun bool) {
	fmt.Fprintln(w, "host-mcp initialization")
	fmt.Fprintln(w, "-----------------------")
	fmt.Fprintf(w, "Profile:        %s\n", cfg.Profile)
	fmt.Fprintf(w, "MCP endpoint:   http://%s%s\n", cfg.Listen, cfg.Path)
	fmt.Fprintf(w, "Configuration:  %s\n", p.ConfigFile)
	fmt.Fprintf(w, "Token file:     %s\n", p.TokenFile)
	fmt.Fprintf(w, "Audit log:      %s\n", p.AuditFile)
	fmt.Fprintln(w, "Command access: not enabled yet")
	fmt.Fprintln(w, "  - Controlled commands (recommended): only device-owner-approved executables and arguments")
	fmt.Fprintln(w, "  - Trusted Shell (high risk): arbitrary shell commands; not confined by authorized folders")
	fmt.Fprintln(w, "Authorized folders (called Roots in the configuration) are folders exposed to the built-in file tools.")
	fmt.Fprintln(w, "They are unrelated to Android root/superuser access.")
	if cfg.Profile == ProfileTermux {
		storageRoots := 0
		for _, root := range cfg.Roots {
			if root.Kind == "termux-storage" {
				storageRoots++
			}
		}
		fmt.Fprintln(w, "Filesystem policy:")
		fmt.Fprintln(w, "  - termux-home is readable by default; write/delete still require explicit grants")
		fmt.Fprintln(w, "  - termux-prefix is permanently read-only")
		fmt.Fprintf(w, "  - %d verified shared-storage root(s) are permanently read-only\n", storageRoots)
	} else {
		fmt.Fprintln(w, "Filesystem policy: no roots are configured; file access is denied until you add one")
	}
	if dryRun {
		fmt.Fprintln(w, "\nPreview only: no configuration or Bearer Token will be created.")
		return
	}
	fmt.Fprintln(w, "\nInitialization will create private configuration files and a new Bearer Token.")
	fmt.Fprintln(w, "Enter y to continue. Press Enter or type anything else to cancel.")
}
func printInitSuccess(w io.Writer, cfg Config, p Paths, token string) {
	fmt.Fprintln(w, "\nInitialization completed.")
	fmt.Fprintf(w, "Configuration directory: %s\n", p.ConfigDir)
	fmt.Fprintln(w, "\nBearer Token (copy this into your MCP client):")
	fmt.Fprintln(w, token)
	fmt.Fprintln(w, "Treat this Token like a password. Do not commit, log, or share it.")
	fmt.Fprintln(w, "Show it later: host-mcp token show")
	fmt.Fprintln(w, "Rotate it:     host-mcp token rotate")
	fmt.Fprintln(w, "\nSet up command access for Claw:")
	fmt.Fprintln(w, "  Recommended controlled commands: host-mcp commands setup")
	fmt.Fprintln(w, "  Review command status:            host-mcp commands status")
	fmt.Fprintln(w, "  High-risk arbitrary shell:         host-mcp shell enable")
	fmt.Fprintln(w, "Trusted Shell can bypass authorized-folder and file-tool read-only policies; it is not a sandbox.")
	fmt.Fprintln(w, "After changing command access for a running service: host-mcp service restart")
	fmt.Fprintln(w, "\nChoose exactly one startup mode; do not run both on the same port.")
	fmt.Fprintln(w, "\nManual foreground mode (testing/troubleshooting):")
	fmt.Fprintln(w, "  host-mcp serve")
	fmt.Fprintln(w, "  Runs in this terminal until you press Ctrl+C.")
	fmt.Fprintln(w, "\nManaged background service (recommended for normal use):")
	if cfg.Profile == ProfileTermux {
		fmt.Fprintln(w, "  host-mcp service enable")
		fmt.Fprintln(w, "  host-mcp service status")
		fmt.Fprintln(w, "  On Termux, enable also starts the runit service.")
	} else {
		fmt.Fprintln(w, "  host-mcp service install")
		fmt.Fprintln(w, "  host-mcp service enable")
		fmt.Fprintln(w, "  host-mcp service start")
		fmt.Fprintln(w, "  host-mcp service status")
	}
	fmt.Fprintln(w, "\nBefore switching from the service to manual mode, run: host-mcp service stop")
	fmt.Fprintln(w, "MCP endpoint:", "http://"+cfg.Listen+cfg.Path)
}
func isMissing(path string) bool           { _, e := os.Lstat(path); return errors.Is(e, os.ErrNotExist) }
func (c CLI) load(p Paths) (Config, error) { return LoadConfig(p.ConfigFile) }
func (c CLI) serveCmd(p Paths, args []string) int {
	if len(args) != 0 {
		return c.usage(errors.New("serve takes no arguments"))
	}
	cfg, e := c.load(p)
	if e != nil {
		return c.fail(e)
	}
	rt, e := NewRuntime(cfg, p, nil)
	if e != nil {
		return c.fail(e)
	}
	defer rt.Close()
	ctx, stop := signalContext()
	defer stop()
	if e = rt.Serve(ctx); e != nil {
		return c.fail(e)
	}
	return 0
}
func (c CLI) configCmd(p Paths, args []string) int {
	if len(args) == 0 {
		return c.usage(errors.New("config requires check, show, show-effective, or set-listen"))
	}
	if args[0] == "set-listen" {
		return c.configSetListenCmd(p, args[1:])
	}
	if len(args) != 1 {
		return c.usage(errors.New("config requires check, show, show-effective, or set-listen"))
	}
	cfg, e := c.load(p)
	if e != nil {
		return c.fail(e)
	}
	switch args[0] {
	case "check":
		fmt.Fprintln(c.Out, "valid")
	case "show":
		b, e := os.ReadFile(p.ConfigFile)
		if e != nil {
			return c.fail(e)
		}
		fmt.Fprint(c.Out, string(b))
	case "show-effective":
		fmt.Fprintln(c.Out, JSON(effectiveConfig(cfg)))
	default:
		return c.usage(errors.New("config requires check, show, show-effective, or set-listen"))
	}
	return 0
}
func (c CLI) configSetListenCmd(p Paths, args []string) int {
	var listen string
	yes, dryRun := false, false
	for _, arg := range args {
		switch arg {
		case "--yes":
			yes = true
		case "--dry-run":
			dryRun = true
		default:
			if strings.HasPrefix(arg, "-") {
				return c.usage(fmt.Errorf("unknown set-listen option %q", arg))
			}
			if listen != "" {
				return c.usage(errors.New("set-listen requires exactly one address"))
			}
			listen = arg
		}
	}
	if listen == "" {
		return c.usage(errors.New("set-listen requires ADDRESS:PORT"))
	}
	cfg, err := c.load(p)
	if err != nil {
		return c.fail(err)
	}
	oldListen := cfg.Listen
	cfg.Listen = listen
	if err = cfg.Validate(); err != nil {
		return c.fail(err)
	}
	host, _, _ := net.SplitHostPort(listen)
	preview := map[string]any{"action": "set-listen", "old_listen": oldListen, "new_listen": listen, "non_loopback": host != "127.0.0.1", "dry_run": dryRun}
	fmt.Fprintln(c.Out, JSON(preview))
	if dryRun || oldListen == listen {
		return 0
	}
	if err = c.confirm("Change MCP listen address", yes); err != nil {
		return c.fail(err)
	}
	if err = SaveConfig(p.ConfigFile, cfg); err != nil {
		return c.fail(err)
	}
	fmt.Fprintln(c.Out, "saved; restart the service with: host-mcp service restart")
	return 0
}
func effectiveConfig(c Config) Config {
	for i := range c.Roots {
		if c.Profile == ProfileTermux && (c.Roots[i].Kind == "termux-prefix" || c.Roots[i].Kind == "termux-storage") {
			c.Roots[i].ReadOnly = true
		}
	}
	return c
}
func (c CLI) confirm(action string, yes bool) error {
	if yes {
		return nil
	}
	if !c.tty() {
		return errors.New("non-TTY operation requires --yes")
	}
	fmt.Fprintf(c.Out, "%s? [y/N] ", action)
	line, _ := bufio.NewReader(c.In).ReadString('\n')
	if strings.ToLower(strings.TrimSpace(line)) != "y" {
		return errors.New("cancelled")
	}
	return nil
}
func canonicalDirectory(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("path must be absolute")
	}
	clean := filepath.Clean(path)
	canonical, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", err
	}
	if canonical != clean {
		return "", errors.New("path must be canonical and contain no symlinks")
	}
	st, err := os.Stat(canonical)
	if err != nil || !st.IsDir() {
		return "", errors.New("path must be an existing directory")
	}
	return canonical, nil
}
func (c CLI) rootsCmd(p Paths, args []string) int {
	cfg, e := c.load(p)
	if e != nil {
		return c.fail(e)
	}
	if len(args) == 1 && args[0] == "list" {
		fmt.Fprintln(c.Out, JSON(cfg.Roots))
		return 0
	}
	if len(args) >= 1 && args[0] == "add" {
		f := parse("roots add")
		id := f.String("id", "", "root ID")
		path := f.String("path", "", "absolute path")
		description := f.String("description", "", "description")
		ro := f.Bool("read-only", false, "read-only")
		eligible := f.Bool("write-eligible", false, "allow later subpath grants")
		yes := f.Bool("yes", false, "confirm")
		if e = f.Parse(args[1:]); e != nil {
			return c.fail(e)
		}
		if *id == "" || *path == "" || *description == "" {
			return c.fail(errors.New("--id, --path, and --description are required"))
		}
		if _, ok := cfg.Root(*id); ok {
			return c.fail(fmt.Errorf("root %q already exists", *id))
		}
		canonical, e := canonicalDirectory(*path)
		if e != nil {
			return c.fail(e)
		}
		if cfg.Profile == ProfileTermux {
			home := filepath.Clean(c.Home)
			storage := filepath.Join(home, "storage")
			if canonical != home && !strings.HasPrefix(canonical, home+string(filepath.Separator)) {
				return c.fail(errors.New("Termux user roots must be inside HOME"))
			}
			if canonical == storage || strings.HasPrefix(canonical, storage+string(filepath.Separator)) {
				return c.fail(errors.New("Termux storage must use verified profile roots"))
			}
		}
		root := RootConfig{*id, canonical, *description, *ro, *eligible, "user"}
		cfg.Roots = append(cfg.Roots, root)
		if e = cfg.Validate(); e != nil {
			return c.fail(e)
		}
		fmt.Fprintln(c.Out, JSON(map[string]any{"action": "roots add", "root": root}))
		if e = c.confirm("Add root", *yes); e != nil {
			return c.fail(e)
		}
	} else if len(args) >= 1 && args[0] == "remove" {
		f := parse("roots remove")
		yes := f.Bool("yes", false, "confirm")
		if e = f.Parse(args[1:]); e != nil {
			return c.fail(e)
		}
		if f.NArg() != 1 {
			return c.usage(errors.New("roots remove [--yes] ID"))
		}
		id := f.Arg(0)
		if _, ok := cfg.Root(id); !ok {
			return c.fail(fmt.Errorf("unknown root %q", id))
		}
		for _, rule := range cfg.ExecRules {
			for _, cwd := range rule.CWDs {
				if cwd.Root == id {
					return c.fail(fmt.Errorf("root %q is referenced by exec rule %q", id, rule.Name))
				}
			}
		}
		fmt.Fprintln(c.Out, JSON(map[string]any{"action": "roots remove", "root": id}))
		if e = c.confirm("Remove root and its grants", *yes); e != nil {
			return c.fail(e)
		}
		cfg.Roots = slices.DeleteFunc(cfg.Roots, func(r RootConfig) bool { return r.ID == id })
		cfg.Permissions = slices.DeleteFunc(cfg.Permissions, func(g Grant) bool { return g.Root == id })
	} else {
		return c.usage(errors.New("roots: list | add --id ID --path ABS --description TEXT [--read-only] [--write-eligible] [--yes] | remove [--yes] ID"))
	}
	if e = SaveConfig(p.ConfigFile, cfg); e != nil {
		return c.fail(e)
	}
	fmt.Fprintln(c.Out, "ok")
	return 0
}
func (c CLI) permissionsCmd(p Paths, args []string) int {
	cfg, e := c.load(p)
	if e != nil {
		return c.fail(e)
	}
	if len(args) == 1 && args[0] == "list" {
		fmt.Fprintln(c.Out, JSON(cfg.Permissions))
		return 0
	}
	if len(args) < 1 || (args[0] != "grant" && args[0] != "revoke") {
		return c.usage(errors.New("permissions: list | grant|revoke --root ID --operation OP --path PATH"))
	}
	f := parse("permissions " + args[0])
	root := f.String("root", "", "root ID")
	operation := f.String("operation", "", "read, write, or delete")
	path := f.String("path", "", "relative path")
	if e = f.Parse(args[1:]); e != nil {
		return c.fail(e)
	}
	if *root == "" || *operation == "" || *path == "" {
		return c.fail(errors.New("--root, --operation, and --path are required"))
	}
	r, ok := cfg.Root(*root)
	if !ok {
		return c.fail(fmt.Errorf("unknown root %q", *root))
	}
	if *operation != "read" && *operation != "write" && *operation != "delete" {
		return c.fail(errors.New("operation must be read, write, or delete"))
	}
	clean := filepath.ToSlash(filepath.Clean(*path))
	if e = validateRel(clean, true); e != nil {
		return c.fail(e)
	}
	if *operation != "read" && (r.ReadOnly || !r.WriteEligible) {
		return c.fail(errors.New("root is effectively read-only"))
	}
	g := Grant{*root, *operation, clean}
	if args[0] == "grant" {
		if slices.Contains(cfg.Permissions, g) {
			return c.fail(errors.New("grant already exists"))
		}
		if *operation == "delete" && !grantAllows(cfg, *root, "write", clean) {
			return c.fail(errors.New("delete requires an existing matching write grant"))
		}
		cfg.Permissions = append(cfg.Permissions, g)
	} else {
		if !slices.Contains(cfg.Permissions, g) {
			return c.fail(errors.New("grant does not exist"))
		}
		if *operation == "write" {
			for _, x := range cfg.Permissions {
				if x.Root == *root && x.Operation == "delete" && pathWithin(x.Path, clean) {
					return c.fail(errors.New("cannot revoke write while dependent delete grant exists"))
				}
			}
		}
		cfg.Permissions = slices.DeleteFunc(cfg.Permissions, func(x Grant) bool { return x == g })
	}
	if e = SaveConfig(p.ConfigFile, cfg); e != nil {
		return c.fail(e)
	}
	fmt.Fprintln(c.Out, "ok")
	return 0
}
func (c CLI) policyCmd(p Paths, args []string) int {
	if len(args) != 4 || args[0] != "explain" {
		return c.usage(errors.New("policy explain <root> <operation> <path>"))
	}
	cfg, e := c.load(p)
	if e != nil {
		return c.fail(e)
	}
	r, ok := cfg.Root(args[1])
	allowed := ok && grantAllows(cfg, args[1], args[2], args[3])
	reason := "no matching grant"
	if !ok {
		reason = "unknown root"
	} else if args[2] != "read" && (r.ReadOnly || !r.WriteEligible) {
		allowed = false
		reason = "root is effectively read-only"
	} else if allowed {
		reason = "matching grant"
	}
	fmt.Fprintln(c.Out, JSON(map[string]any{"allowed": allowed, "reason": reason, "root": args[1], "operation": args[2], "path": args[3]}))
	if !allowed {
		return 1
	}
	return 0
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }
func (c CLI) execCmd(p Paths, args []string) int {
	cfg, e := c.load(p)
	if e != nil {
		return c.fail(e)
	}
	if len(args) == 1 && args[0] == "list" {
		fmt.Fprintln(c.Out, JSON(map[string]any{"enabled": cfg.ExecEnabled, "rules": cfg.ExecRules}))
		return 0
	}
	if len(args) == 2 && args[0] == "deny" {
		cfg.ExecRules = slices.DeleteFunc(cfg.ExecRules, func(r ExecRule) bool { return r.Name == args[1] })
		if len(cfg.ExecRules) == 0 {
			cfg.ExecEnabled = false
		}
	} else if len(args) > 0 && args[0] == "allow" {
		f := parse("exec allow")
		name := f.String("name", "", "rule name")
		exe := f.String("executable", "", "fixed absolute executable")
		pats, cwds := multiFlag{}, multiFlag{}
		f.Var(&pats, "arg-pattern", "full-match regex")
		f.Var(&cwds, "cwd", "ROOT:PATH")
		if e = f.Parse(args[1:]); e != nil {
			return c.fail(e)
		}
		if *name == "" || *exe == "" || len(cwds) == 0 {
			return c.fail(errors.New("--name, --executable, and at least one --cwd are required"))
		}
		canonicalExe, err := filepath.EvalSymlinks(filepath.Clean(*exe))
		if err != nil || canonicalExe != filepath.Clean(*exe) {
			return c.fail(errors.New("executable must be canonical and not symlinked"))
		}
		rule := ExecRule{Name: *name, Executable: canonicalExe, ArgPatterns: pats}
		for _, s := range cwds {
			x := strings.SplitN(s, ":", 2)
			if len(x) != 2 {
				return c.fail(errors.New("--cwd requires ROOT:PATH"))
			}
			rule.CWDs = append(rule.CWDs, ExecCWD{x[0], x[1]})
		}
		cfg.ExecRules = slices.DeleteFunc(cfg.ExecRules, func(r ExecRule) bool { return r.Name == *name })
		cfg.ExecRules = append(cfg.ExecRules, rule)
		cfg.ExecEnabled = true
	} else {
		return c.usage(errors.New("exec: list | allow --name N --executable ABS [--arg-pattern RE] [--cwd ROOT:PATH] | deny NAME"))
	}
	if e = SaveConfig(p.ConfigFile, cfg); e != nil {
		return c.fail(e)
	}
	fmt.Fprintln(c.Out, "ok")
	return 0
}
func (c CLI) commandsCmd(p Paths, args []string) int {
	cfg, err := c.load(p)
	if err != nil {
		return c.fail(err)
	}
	if len(args) == 0 || args[0] == "status" || args[0] == "list" {
		status := commandCapabilityStatus(cfg)
		fmt.Fprintln(c.Out, "Controlled commands")
		fmt.Fprintln(c.Out, "-------------------")
		fmt.Fprintf(c.Out, "Enabled: %t\n", status.ControlledEnabled)
		if len(status.ControlledRules) == 0 {
			fmt.Fprintln(c.Out, "Approved commands: none")
		} else {
			fmt.Fprintln(c.Out, "Approved commands:")
			for _, name := range status.ControlledRules {
				fmt.Fprintln(c.Out, "  -", name)
			}
		}
		fmt.Fprintln(c.Out, status.NextStep)
		return 0
	}
	if args[0] == "presets" {
		presets := availableCommandPresets(cfg)
		if len(presets) == 0 {
			fmt.Fprintln(c.Out, "No supported command presets are currently installed.")
			return 0
		}
		fmt.Fprintln(c.Out, "Available controlled-command presets:")
		for _, preset := range presets {
			fmt.Fprintf(c.Out, "  %-16s %s\n", preset.ID, preset.Description)
		}
		return 0
	}
	if args[0] == "setup" {
		fmt.Fprintln(c.Out, "Controlled commands run fixed executables directly, without a shell.")
		fmt.Fprintln(c.Out, "First list authorized folders with: host-mcp roots list")
		fmt.Fprintln(c.Out, "Then enable a preset, for example:")
		fmt.Fprintln(c.Out, "  host-mcp commands enable git-status --folder termux-home:. --yes")
		fmt.Fprintln(c.Out, "Available presets:")
		for _, preset := range availableCommandPresets(cfg) {
			fmt.Fprintf(c.Out, "  %-16s %s\n", preset.ID, preset.Description)
		}
		return 0
	}
	if args[0] == "enable" {
		f := parse("commands enable")
		folder := f.String("folder", "", "ROOT:PATH authorized starting folder")
		yes := f.Bool("yes", false, "confirm")
		dryRun := f.Bool("dry-run", false, "preview without saving")
		if err := f.Parse(args[1:]); err != nil {
			return c.fail(err)
		}
		if f.NArg() != 1 || *folder == "" {
			return c.usage(errors.New("commands enable PRESET --folder ROOT:PATH [--yes|--dry-run]"))
		}
		parts := strings.SplitN(*folder, ":", 2)
		if len(parts) != 2 {
			return c.fail(errors.New("--folder requires ROOT:PATH"))
		}
		rule, err := resolvePreset(cfg, f.Arg(0), ExecCWD{Root: parts[0], Path: parts[1]})
		if err != nil {
			return c.fail(err)
		}
		fmt.Fprintln(c.Out, JSON(map[string]any{"action": "enable controlled command", "rule": rule, "dry_run": *dryRun}))
		if *dryRun {
			return 0
		}
		if err := c.confirm("Enable this controlled command", *yes); err != nil {
			return c.fail(err)
		}
		cfg.ExecRules = slices.DeleteFunc(cfg.ExecRules, func(existing ExecRule) bool { return existing.Name == rule.Name })
		cfg.ExecRules = append(cfg.ExecRules, rule)
		cfg.ExecEnabled = true
		if err := SaveConfig(p.ConfigFile, cfg); err != nil {
			return c.fail(err)
		}
		fmt.Fprintln(c.Out, "saved; restart a running service with: host-mcp service restart")
		return 0
	}
	if args[0] == "disable" && len(args) == 2 {
		cfg.ExecRules = slices.DeleteFunc(cfg.ExecRules, func(rule ExecRule) bool { return rule.Name == args[1] })
		cfg.ExecEnabled = len(cfg.ExecRules) > 0
		if err := SaveConfig(p.ConfigFile, cfg); err != nil {
			return c.fail(err)
		}
		fmt.Fprintln(c.Out, "saved; restart a running service with: host-mcp service restart")
		return 0
	}
	return c.usage(errors.New("commands: status | list | setup | presets | enable PRESET --folder ROOT:PATH [--yes|--dry-run] | disable NAME"))
}

func (c CLI) shellCmd(p Paths, args []string) int {
	cfg, err := c.load(p)
	if err != nil {
		return c.fail(err)
	}
	if len(args) == 0 || (len(args) == 1 && args[0] == "status") {
		fmt.Fprintln(c.Out, "Trusted Shell")
		fmt.Fprintln(c.Out, "-------------")
		fmt.Fprintf(c.Out, "Enabled: %t\n", cfg.Shell.Enabled)
		fmt.Fprintln(c.Out, "Risk: arbitrary shell commands are not confined by authorized folders and are not sandboxed.")
		return 0
	}
	if len(args) == 1 && args[0] == "disable" {
		cfg.Shell = ShellConfig{}
		if err := SaveConfig(p.ConfigFile, cfg); err != nil {
			return c.fail(err)
		}
		fmt.Fprintln(c.Out, "Trusted Shell disabled; restart a running service with: host-mcp service restart")
		return 0
	}
	if len(args) > 0 && args[0] == "enable" {
		f := parse("shell enable")
		executable := f.String("executable", "", "canonical shell executable")
		folder := f.String("folder", "", "ROOT:PATH starting folder")
		yes := f.Bool("yes", false, "non-interactive confirmation")
		acceptRisk := f.String("accept-risk", "", "required exact risk confirmation")
		if err := f.Parse(args[1:]); err != nil {
			return c.fail(err)
		}
		if *executable == "" {
			if cfg.Profile == ProfileTermux && os.Getenv("PREFIX") != "" {
				*executable = filepath.Join(os.Getenv("PREFIX"), "bin", "sh")
			} else {
				*executable = "/bin/sh"
			}
		}
		if *folder == "" {
			if cfg.Profile == ProfileTermux {
				*folder = "termux-home:."
			} else {
				return c.fail(errors.New("--folder ROOT:PATH is required"))
			}
		}
		parts := strings.SplitN(*folder, ":", 2)
		if len(parts) != 2 {
			return c.fail(errors.New("--folder requires ROOT:PATH"))
		}
		fmt.Fprintln(c.Out, "WARNING: Trusted Shell is high risk.")
		fmt.Fprintln(c.Out, "It can read or write anything available to the host-mcp OS account, access the network, and start child processes.")
		fmt.Fprintln(c.Out, "Authorized folders and Termux file-tool read-only policies do not confine shell commands. This is not a sandbox.")
		host, _, _ := net.SplitHostPort(cfg.Listen)
		if host != "127.0.0.1" {
			fmt.Fprintf(c.Out, "WARNING: MCP currently listens on %s; network clients with the Token could use this shell.\n", cfg.Listen)
		}
		if c.tty() && !*yes {
			fmt.Fprintf(c.Out, "Type exactly %q to enable: ", trustedShellConfirmation)
			line, _ := bufio.NewReader(c.In).ReadString('\n')
			*acceptRisk = strings.TrimSpace(line)
		}
		if *acceptRisk != trustedShellConfirmation || (!c.tty() && !*yes) {
			return c.fail(errors.New("Trusted Shell was not enabled; exact risk confirmation is required"))
		}
		canonical, err := filepath.EvalSymlinks(filepath.Clean(*executable))
		if err != nil || !allowedShellExecutable(canonical) {
			return c.fail(errors.New("shell executable must resolve to a supported executable shell"))
		}
		cfg.Shell = ShellConfig{Enabled: true, Executable: canonical, CWDs: []ExecCWD{{Root: parts[0], Path: parts[1]}}}
		if err := cfg.Validate(); err != nil {
			return c.fail(err)
		}
		if err := SaveConfig(p.ConfigFile, cfg); err != nil {
			return c.fail(err)
		}
		fmt.Fprintln(c.Out, "Trusted Shell enabled; restart a running service with: host-mcp service restart")
		return 0
	}
	return c.usage(errors.New("shell: status | enable [--executable ABS] [--folder ROOT:PATH] [--yes --accept-risk TEXT] | disable"))
}

func (c CLI) tokenCmd(p Paths, args []string) int {
	if len(args) != 1 {
		return c.usage(errors.New("token requires show or rotate"))
	}
	if args[0] == "show" {
		t, e := LoadToken(p.TokenFile)
		if e != nil {
			return c.fail(e)
		}
		fmt.Fprintln(c.Out, t)
		return 0
	}
	if args[0] == "rotate" {
		t, e := NewToken()
		if e == nil {
			e = SaveToken(p.TokenFile, t)
		}
		if e != nil {
			return c.fail(e)
		}
		fmt.Fprintln(c.Out, t)
		return 0
	}
	return c.usage(errors.New("token requires show or rotate"))
}
func (c CLI) statusCmd(p Paths) int {
	cfg, e := c.load(p)
	if e != nil {
		return c.fail(e)
	}
	v, e := HTTPStatus(cfg, p)
	if e != nil {
		return c.fail(e)
	}
	fmt.Fprintln(c.Out, JSON(v))
	return 0
}
func (c CLI) doctorCmd(p Paths) int {
	cfg, e := c.load(p)
	if e != nil {
		return c.fail(e)
	}
	roots := map[string]bool{}
	ok := true
	for _, r := range cfg.Roots {
		roots[r.ID] = isDir(r.Path)
		ok = ok && roots[r.ID]
	}
	checks := map[string]any{"profile": cfg.Profile, "config_validation": "ok", "roots_exist": roots, "config_secure": CheckPrivate(p.ConfigFile, false) == nil, "token_secure": CheckPrivate(p.TokenFile, false) == nil, "commands": commandCapabilityStatus(cfg)}
	fmt.Fprintln(c.Out, JSON(checks))
	if !ok {
		return 1
	}
	return 0
}
func isDir(p string) bool { s, e := os.Stat(p); return e == nil && s.IsDir() }
func SaveJSON(path string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return AtomicWrite(path, b, 0600)
}
