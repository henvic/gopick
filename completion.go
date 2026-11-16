package main

import (
	_ "embed"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

func printCompletionScript(w io.Writer, shell, target string) error {
	if target != "go" && target != "gopick" && target != "both" {
		return fmt.Errorf("invalid -completion-target %q; use go, gopick, or both", target)
	}
	var script, setting string
	switch shell {
	case "zsh":
		script, setting = zshCompletion, "_gopick_completion_target="+target+"\n"
	case "bash":
		script, setting = bashCompletion, "_gopick_completion_target="+target+"\n"
	case "fish":
		script, setting = fishCompletion, "set -g __gopick_completion_target "+target+"\n"
	default:
		return fmt.Errorf("unsupported shell %q; use zsh, bash, or fish", shell)
	}
	_, err := io.WriteString(w, setting+script)
	return err
}

func printCompletions(w io.Writer, tests []test, kind, dir string) error {
	packages, names := make(map[string]bool), make(map[string]bool)
	for _, test := range tests {
		if kind != "packages" {
			names[test.Name] = true
		}
		if kind == "tests" {
			continue
		}
		// Prefer an argument that selects the package without a test regexp.
		// Ambiguous imports such as "foo/sub" need a relative-path fallback.
		found := false
		for _, path := range test.packageArgs() {
			if packagePattern(path) {
				packages[path], found = true, true
			}
		}
		if found {
			continue
		}
		rel, err := filepath.Rel(dir, filepath.Dir(test.File))
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			if rel != "." {
				rel = "./" + filepath.ToSlash(rel)
			}
			packages[rel] = true
		}
	}
	if kind != "tests" {
		packages["./..."] = true
	}
	// Sort each group independently so package scopes precede test names.
	for _, group := range []map[string]bool{packages, names} {
		candidates := make([]string, 0, len(group))
		for candidate := range group {
			candidates = append(candidates, candidate)
		}
		sort.Strings(candidates)
		for _, candidate := range candidates {
			if _, err := fmt.Fprintln(w, candidate); err != nil {
				return err
			}
		}
	}
	return nil
}

//go:embed completions/gopick.zsh
var zshCompletion string

//go:embed completions/gopick.bash
var bashCompletion string

//go:embed completions/gopick.fish
var fishCompletion string
