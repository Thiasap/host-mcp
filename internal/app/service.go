package app

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type ServiceBackend interface {
	Install() error
	Enable() error
	Disable() error
	Start() error
	Stop() error
	Restart() error
	Status() (map[string]any, error)
}

func (c CLI) serviceCmd(p Paths, args []string) int {
	if len(args) != 1 {
		return c.usage(errors.New("service requires install, enable, disable, start, stop, restart, or status"))
	}
	cfg, err := c.load(p)
	if err != nil {
		return c.fail(err)
	}
	var backend ServiceBackend
	if cfg.Profile == ProfileTermux {
		backend = &runitBackend{p: p}
	} else {
		backend = &systemdBackend{p: p}
	}
	switch args[0] {
	case "install":
		err = backend.Install()
	case "enable":
		err = backend.Enable()
	case "disable":
		err = backend.Disable()
	case "start":
		err = backend.Start()
	case "stop":
		err = backend.Stop()
	case "restart":
		err = backend.Restart()
	case "status":
		v, e := backend.Status()
		if e != nil {
			return c.fail(e)
		}
		fmt.Fprintln(c.Out, JSON(v))
		return 0
	default:
		return c.usage(errors.New("unknown service operation"))
	}
	if err != nil {
		return c.fail(err)
	}
	fmt.Fprintln(c.Out, "ok")
	return 0
}

type runitBackend struct{ p Paths }

func (r *runitBackend) dir() string {
	if prefix := os.Getenv("PREFIX"); prefix != "" {
		d := filepath.Join(prefix, "var", "service", AppName)
		if isDir(d) || isDir(filepath.Join(prefix, "var", "service")) {
			return d
		}
	}
	return r.p.ServiceDir
}
func (r *runitBackend) Install() error {
	d := r.dir()
	if err := os.MkdirAll(d, 0700); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	run := []byte("#!/data/data/com.termux/files/usr/bin/sh\nset -eu\nexec \"" + exe + "\" serve\n")
	if err = AtomicWrite(filepath.Join(d, "run"), run, 0700); err != nil {
		return err
	}
	return AtomicWrite(filepath.Join(d, "down"), nil, 0600)
}
func (r *runitBackend) Enable() error {
	if !isDir(r.dir()) {
		if err := r.Install(); err != nil {
			return err
		}
	}
	if err := os.Remove(filepath.Join(r.dir(), "down")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return commandRequired("sv", "up", r.dir())
}
func (r *runitBackend) Disable() error {
	if err := AtomicWrite(filepath.Join(r.dir(), "down"), nil, 0600); err != nil {
		return err
	}
	return r.Stop()
}
func (r *runitBackend) Start() error   { return commandRequired("sv", "up", r.dir()) }
func (r *runitBackend) Stop() error    { return commandRequired("sv", "down", r.dir()) }
func (r *runitBackend) Restart() error { return commandRequired("sv", "restart", r.dir()) }
func (r *runitBackend) Status() (map[string]any, error) {
	return map[string]any{"backend": "runit", "service_dir": r.dir(), "installed": isDir(r.dir()), "enabled": isMissing(filepath.Join(r.dir(), "down")), "running": serviceRunning(r.dir())}, nil
}

type systemdBackend struct{ p Paths }

func (s *systemdBackend) unit() string { return filepath.Join(s.p.SystemdDir, AppName+".service") }
func (s *systemdBackend) Install() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if strings.ContainsAny(exe, "\r\n") {
		return errors.New("executable path contains invalid characters")
	}
	unit := "[Unit]\nDescription=host-mcp MCP server\nAfter=network.target\n\n[Service]\nType=simple\nExecStart=" + systemdQuote(exe) + " serve\nRestart=on-failure\nNoNewPrivileges=true\nPrivateTmp=true\n\n[Install]\nWantedBy=default.target\n"
	if err = AtomicWrite(s.unit(), []byte(unit), 0600); err != nil {
		return err
	}
	return commandRequired("systemctl", "--user", "daemon-reload")
}
func (s *systemdBackend) Enable() error {
	return commandRequired("systemctl", "--user", "enable", AppName+".service")
}
func (s *systemdBackend) Disable() error {
	return commandRequired("systemctl", "--user", "disable", AppName+".service")
}
func (s *systemdBackend) Start() error {
	return commandRequired("systemctl", "--user", "start", AppName+".service")
}
func (s *systemdBackend) Stop() error {
	return commandRequired("systemctl", "--user", "stop", AppName+".service")
}
func (s *systemdBackend) Restart() error {
	return commandRequired("systemctl", "--user", "restart", AppName+".service")
}
func (s *systemdBackend) Status() (map[string]any, error) {
	running := commandRequired("systemctl", "--user", "is-active", "--quiet", AppName+".service") == nil
	enabled := commandRequired("systemctl", "--user", "is-enabled", "--quiet", AppName+".service") == nil
	return map[string]any{"backend": "systemd-user", "installed": !isMissing(s.unit()), "enabled": enabled, "running": running}, nil
}
func systemdQuote(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}
func commandRequired(name string, args ...string) error {
	p, err := exec.LookPath(name)
	if err != nil {
		return fmt.Errorf("%s is required: %w", name, err)
	}
	if out, err := exec.Command(p, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s %v: %w: %s", name, args, err, string(out))
	}
	return nil
}
func serviceRunning(dir string) bool {
	status, err := os.ReadFile(filepath.Join(dir, "supervise", "status"))
	return err == nil && len(status) >= 20 && status[19] == 1 && status[18] == 0
}
