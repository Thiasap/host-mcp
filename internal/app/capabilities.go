package app

import (
	"context"
	"slices"
)

type CommandCapabilityStatus struct {
	ControlledEnabled bool     `json:"controlled_enabled"`
	ControlledRules   []string `json:"controlled_rules"`
	TrustedShell      bool     `json:"trusted_shell_enabled"`
	ToolsVisible      []string `json:"tools_visible"`
	Risk              string   `json:"risk"`
	NextStep          string   `json:"next_step,omitempty"`
	RestartHint       string   `json:"restart_hint"`
}

func commandCapabilityStatus(c Config) CommandCapabilityStatus {
	rules := make([]string, 0, len(c.ExecRules))
	for _, rule := range c.ExecRules {
		rules = append(rules, rule.Name)
	}
	slices.Sort(rules)
	status := CommandCapabilityStatus{
		ControlledEnabled: c.ExecEnabled && len(rules) > 0,
		ControlledRules:   rules,
		TrustedShell:      c.Shell.Enabled,
		ToolsVisible:      []string{"command_status", "exec_run", "shell_run"},
		Risk:              "controlled commands run approved executables; trusted shell is not confined by authorized folders",
		RestartHint:       "After changing command settings, run: host-mcp service restart",
	}
	switch {
	case c.Shell.Enabled:
		status.NextStep = "Trusted Shell is enabled. Disable it with: host-mcp shell disable"
	case status.ControlledEnabled:
		status.NextStep = "Controlled commands are ready. Review them with: host-mcp commands list"
	default:
		status.NextStep = "Set up controlled commands with: host-mcp commands setup; or explicitly enable high-risk shell with: host-mcp shell enable"
	}
	return status
}

type CommandStatusInput struct{}

type CommandRunOutput struct {
	Status  string      `json:"status"`
	Message string      `json:"message,omitempty"`
	Result  *ExecOutput `json:"result,omitempty"`
}

func runControlledCommand(ctx context.Context, c Config, a Auditor, in ExecInput) (CommandRunOutput, error) {
	if !c.ExecEnabled || len(c.ExecRules) == 0 {
		return CommandRunOutput{Status: "disabled", Message: "No controlled commands are configured. The device owner can run: host-mcp commands setup"}, nil
	}
	out, err := RunExec(ctx, c, a, in)
	if err != nil {
		return CommandRunOutput{}, err
	}
	return CommandRunOutput{Status: "ok", Result: &out}, nil
}
