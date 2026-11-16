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
	unique := make(map[string]bool)
	for _, test := range tests {
		if kind != "packages" {
			unique[test.Name] = true
		}
		if kind == "tests" {
			continue
		}
		// Offer import paths only when the selector parser can use them as
		// package scopes. A module named "foo" is otherwise a test regexp.
		for _, path := range test.packageArgs() {
			if pattern, _, _, err := searchArgs([]string{path, ""}); err == nil && pattern == path {
				unique[path] = true
			}
		}
		rel, err := filepath.Rel(dir, filepath.Dir(test.File))
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			if rel != "." {
				rel = "./" + filepath.ToSlash(rel)
			}
			unique[rel] = true
		}
	}
	if kind != "tests" {
		unique["./..."] = true
	}
	candidates := make([]string, 0, len(unique))
	for candidate := range unique {
		candidates = append(candidates, candidate)
	}
	sort.Strings(candidates)
	for _, candidate := range candidates {
		if _, err := fmt.Fprintln(w, candidate); err != nil {
			return err
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
