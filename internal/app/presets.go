package app

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
)

type CommandPreset struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Candidates  []string `json:"-"`
	Arguments   []string `json:"arguments"`
}

func commandPresets(c Config) []CommandPreset {
	prefix := os.Getenv("PREFIX")
	bin := "/usr/bin"
	if c.Profile == ProfileTermux && prefix != "" {
		bin = filepath.Join(prefix, "bin")
	}
	return []CommandPreset{
		{ID: "git-status", Description: "Show concise Git working-tree status", Candidates: []string{filepath.Join(bin, "git")}, Arguments: []string{"status", "--short"}},
		{ID: "git-diff-stat", Description: "Show a summary of uncommitted Git changes", Candidates: []string{filepath.Join(bin, "git")}, Arguments: []string{"diff", "--stat"}},
		{ID: "git-log", Description: "Show the latest 20 Git commits", Candidates: []string{filepath.Join(bin, "git")}, Arguments: []string{"log", "-20", "--oneline", "--decorate"}},
	}
}

func availableCommandPresets(c Config) []CommandPreset {
	var available []CommandPreset
	for _, preset := range commandPresets(c) {
		if _, err := resolvePresetExecutable(preset); err == nil {
			available = append(available, preset)
		}
	}
	return available
}

func resolvePreset(c Config, id string, cwd ExecCWD) (ExecRule, error) {
	for _, preset := range commandPresets(c) {
		if preset.ID != id {
			continue
		}
		executable, err := resolvePresetExecutable(preset)
		if err != nil {
			return ExecRule{}, err
		}
		if _, ok := c.Root(cwd.Root); !ok {
			return ExecRule{}, errors.New("authorized folder is unknown")
		}
		if err := validateRel(cwd.Path, true); err != nil {
			return ExecRule{}, err
		}
		patterns := make([]string, len(preset.Arguments))
		for i, arg := range preset.Arguments {
			patterns[i] = regexp.QuoteMeta(arg)
		}
		return ExecRule{Name: preset.ID, Executable: executable, ArgPatterns: patterns, CWDs: []ExecCWD{cwd}}, nil
	}
	return ExecRule{}, errors.New("unknown command preset")
}

func resolvePresetExecutable(preset CommandPreset) (string, error) {
	for _, candidate := range preset.Candidates {
		canonical, err := filepath.EvalSymlinks(candidate)
		if err != nil || canonical != filepath.Clean(candidate) || forbiddenExecutable(canonical) {
			continue
		}
		st, err := os.Stat(canonical)
		if err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0111 != 0 {
			return canonical, nil
		}
	}
	return "", errors.New("preset executable is not installed as a canonical regular file")
}
