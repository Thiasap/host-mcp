package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

type ExecInput struct {
	Rule string   `json:"rule"`
	Args []string `json:"args,omitempty"`
	CWD  ExecCWD  `json:"cwd"`
}
type ExecOutput struct {
	Rule            string `json:"rule"`
	ExitCode        int    `json:"exit_code"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	StdoutTruncated bool   `json:"stdout_truncated"`
	StderrTruncated bool   `json:"stderr_truncated"`
	DurationMS      int64  `json:"duration_ms"`
}
type cappedBuffer struct {
	b         bytes.Buffer
	n         int64
	truncated bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	orig := len(p)
	remain := c.n - int64(c.b.Len())
	if remain <= 0 {
		c.truncated = true
		return orig, nil
	}
	if int64(len(p)) > remain {
		p = p[:remain]
		c.truncated = true
	}
	_, _ = c.b.Write(p)
	return orig, nil
}
func RunExec(ctx context.Context, c Config, a Auditor, in ExecInput) (ExecOutput, error) {
	if !c.ExecEnabled {
		return ExecOutput{}, errors.New("exec is disabled")
	}
	var rule *ExecRule
	for i := range c.ExecRules {
		if c.ExecRules[i].Name == in.Rule {
			rule = &c.ExecRules[i]
			break
		}
	}
	if rule == nil {
		return ExecOutput{}, fmt.Errorf("unknown exec rule %q", in.Rule)
	}
	if forbiddenExecutable(rule.Executable) {
		return ExecOutput{}, errors.New("configured executable is a forbidden shell or interpreter")
	}
	canonicalExe, e := filepath.EvalSymlinks(rule.Executable)
	if e != nil || canonicalExe != filepath.Clean(rule.Executable) {
		return ExecOutput{}, errors.New("configured executable must be canonical and not symlinked")
	}
	st, e := os.Stat(canonicalExe)
	if e != nil {
		return ExecOutput{}, e
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0111 == 0 {
		return ExecOutput{}, errors.New("configured executable is not an executable regular file")
	}
	if len(in.Args) > c.Limits.MaxExecArgs {
		return ExecOutput{}, errors.New("too many arguments")
	}
	for i, arg := range in.Args {
		if len(arg) > c.Limits.MaxArgBytes || strings.IndexByte(arg, 0) >= 0 {
			return ExecOutput{}, fmt.Errorf("argument %d is invalid", i)
		}
		if len(rule.ArgPatterns) > 0 {
			if i >= len(rule.ArgPatterns) {
				return ExecOutput{}, fmt.Errorf("argument %d is not allowed", i)
			}
			ok, e := regexp.MatchString("^(?:"+rule.ArgPatterns[i]+")$", arg)
			if e != nil || !ok {
				return ExecOutput{}, fmt.Errorf("argument %d does not match rule", i)
			}
		}
	}
	if len(rule.ArgPatterns) > 0 && len(in.Args) != len(rule.ArgPatterns) {
		return ExecOutput{}, errors.New("argument count does not match rule")
	}
	if in.CWD.Root == "" {
		return ExecOutput{}, errors.New("cwd is required")
	}
	allowed := false
	for _, x := range rule.CWDs {
		if x.Root == in.CWD.Root && pathWithin(in.CWD.Path, x.Path) {
			allowed = true
			break
		}
	}
	if !allowed {
		return ExecOutput{}, errors.New("cwd is outside rule cwd roots")
	}
	r, ok := c.Root(in.CWD.Root)
	if !ok {
		return ExecOutput{}, errors.New("cwd root is unknown")
	}
	p, e := cleanPath(in.CWD.Path)
	if e != nil {
		return ExecOutput{}, e
	}
	or, e := os.OpenRoot(r.Path)
	if e != nil {
		return ExecOutput{}, e
	}
	cr, e := or.OpenRoot(p)
	if e != nil {
		or.Close()
		return ExecOutput{}, e
	}
	info, e := cr.Stat(".")
	cr.Close()
	or.Close()
	if e != nil || !info.IsDir() {
		return ExecOutput{}, errors.New("cwd must be an existing directory")
	}
	cwd := filepath.Join(r.Path, filepath.FromSlash(p))
	runCtx, cancel := context.WithTimeout(ctx, DurationSeconds(c.Limits.ExecTimeoutSec))
	defer cancel()
	cmd := exec.CommandContext(runCtx, rule.Executable, in.Args...)
	cmd.Dir = cwd
	env := []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8"}
	keys := make([]string, 0, len(rule.Environment))
	for k := range rule.Environment {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		env = append(env, k+"="+rule.Environment[k])
	}
	cmd.Env = env
	out, errout := &cappedBuffer{n: c.Limits.MaxOutputBytes}, &cappedBuffer{n: c.Limits.MaxOutputBytes}
	cmd.Stdout = out
	cmd.Stderr = errout
	start := time.Now()
	e = cmd.Run()
	res := ExecOutput{rule.Name, -1, out.b.String(), errout.b.String(), out.truncated, errout.truncated, time.Since(start).Milliseconds()}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	if runCtx.Err() == context.DeadlineExceeded {
		a.Log("exec_run", "timeout", rule.Name, fmt.Sprintf("argc=%d", len(in.Args)))
		return res, errors.New("execution timed out")
	}
	if e != nil {
		var ee *exec.ExitError
		if errors.As(e, &ee) {
			a.Log("exec_run", "exit", rule.Name, fmt.Sprintf("code=%d argc=%d", res.ExitCode, len(in.Args)))
			return res, nil
		}
		return res, e
	}
	a.Log("exec_run", "ok", rule.Name, fmt.Sprintf("argc=%d", len(in.Args)))
	return res, nil
}
