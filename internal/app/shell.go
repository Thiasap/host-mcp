package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const trustedShellConfirmation = "I UNDERSTAND TRUSTED SHELL IS NOT SANDBOXED"

type ShellInput struct {
	Command string  `json:"command"`
	CWD     ExecCWD `json:"cwd"`
}

type ShellOutput struct {
	Status          string `json:"status"`
	Message         string `json:"message,omitempty"`
	ExitCode        int    `json:"exit_code,omitempty"`
	Stdout          string `json:"stdout,omitempty"`
	Stderr          string `json:"stderr,omitempty"`
	StdoutTruncated bool   `json:"stdout_truncated,omitempty"`
	StderrTruncated bool   `json:"stderr_truncated,omitempty"`
	DurationMS      int64  `json:"duration_ms,omitempty"`
}

func trustedShellEnvironment(shell string) []string {
	env := []string{"HOME=" + os.Getenv("HOME"), "LANG=C.UTF-8"}
	if prefix := os.Getenv("PREFIX"); prefix != "" {
		env = append(env, "PREFIX="+prefix, "PATH="+filepath.Join(prefix, "bin")+":"+filepath.Join(prefix, "bin", "applets"))
	} else {
		env = append(env, "PATH=/usr/local/bin:/usr/bin:/bin")
	}
	if tmp := os.Getenv("TMPDIR"); tmp != "" {
		env = append(env, "TMPDIR="+tmp)
	}
	if shell != "" {
		env = append(env, "SHELL="+shell)
	}
	return env
}

func RunTrustedShell(ctx context.Context, c Config, a Auditor, in ShellInput) (ShellOutput, error) {
	if !c.Shell.Enabled {
		return ShellOutput{Status: "disabled", Message: "Trusted Shell is disabled. The device owner can review the risk and run: host-mcp shell enable"}, nil
	}
	if strings.TrimSpace(in.Command) == "" || strings.IndexByte(in.Command, 0) >= 0 {
		return ShellOutput{}, errors.New("shell command is empty or invalid")
	}
	if int64(len(in.Command)) > c.Limits.MaxBodyBytes {
		return ShellOutput{}, errors.New("shell command is too large")
	}
	if !allowedShellExecutable(c.Shell.Executable) {
		return ShellOutput{}, errors.New("configured trusted shell is not supported")
	}
	canonical, err := filepath.EvalSymlinks(c.Shell.Executable)
	if err != nil || canonical != filepath.Clean(c.Shell.Executable) {
		return ShellOutput{}, errors.New("configured trusted shell must remain at its canonical target")
	}
	allowed := false
	for _, cwd := range c.Shell.CWDs {
		if cwd.Root == in.CWD.Root && pathWithin(in.CWD.Path, cwd.Path) {
			allowed = true
			break
		}
	}
	if !allowed {
		return ShellOutput{}, errors.New("starting directory is not authorized")
	}
	root, ok := c.Root(in.CWD.Root)
	if !ok {
		return ShellOutput{}, errors.New("starting directory references an unknown authorized folder")
	}
	rel, err := cleanPath(in.CWD.Path)
	if err != nil {
		return ShellOutput{}, err
	}
	or, err := os.OpenRoot(root.Path)
	if err != nil {
		return ShellOutput{}, err
	}
	cr, err := or.OpenRoot(rel)
	if err != nil {
		or.Close()
		return ShellOutput{}, err
	}
	info, err := cr.Stat(".")
	cr.Close()
	or.Close()
	if err != nil || !info.IsDir() {
		return ShellOutput{}, errors.New("starting directory must be an existing directory")
	}

	runCtx, cancel := context.WithTimeout(ctx, DurationSeconds(c.Limits.ExecTimeoutSec))
	defer cancel()
	cmd := exec.CommandContext(runCtx, canonical, "-lc", in.Command)
	cmd.Dir = filepath.Join(root.Path, filepath.FromSlash(rel))
	cmd.Env = trustedShellEnvironment(canonical)
	stdout, stderr := &cappedBuffer{n: c.Limits.MaxOutputBytes}, &cappedBuffer{n: c.Limits.MaxOutputBytes}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	start := time.Now()
	err = cmd.Run()
	result := ShellOutput{Status: "ok", ExitCode: -1, Stdout: stdout.b.String(), Stderr: stderr.b.String(), StdoutTruncated: stdout.truncated, StderrTruncated: stderr.truncated, DurationMS: time.Since(start).Milliseconds()}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	hash := fmt.Sprintf("sha256=%x", sha256.Sum256([]byte(in.Command)))
	if runCtx.Err() == context.DeadlineExceeded {
		a.Log("shell_run", "timeout", in.CWD.Root, hash)
		return result, errors.New("execution timed out")
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			a.Log("shell_run", "exit", in.CWD.Root, fmt.Sprintf("code=%d %s", result.ExitCode, hash))
			return result, nil
		}
		return result, err
	}
	a.Log("shell_run", "ok", in.CWD.Root, hash)
	return result, nil
}
