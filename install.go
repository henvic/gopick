package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const completionStart = "# gopick completion"
const completionEnd = "# end gopick completion"

type completionEdit struct {
	path, resolved string
	before, after  []byte
	mode           os.FileMode
	exists, remove bool
}

func installCompletion(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, shell, target, file string, yes bool) error {
	// Validate before inspecting or changing any configuration files.
	if err := printCompletionScript(io.Discard, shell, target); err != nil {
		return err
	}
	paths, err := completionPaths(shell, file)
	if err != nil {
		return err
	}
	edits, err := completionEdits(shell, target, paths)
	if err != nil {
		return err
	}
	if len(edits) == 0 {
		fmt.Fprintln(stdout, "Completion is already configured.")
		printCompletionActivation(stdout, shell, target, paths)
		return nil
	}
	var preview bytes.Buffer
	fmt.Fprintf(&preview, "Enable completion for %s in %s.\n", strings.ReplaceAll(target, "both", "go test + gopick"), shell)
	if shell == "zsh" {
		fmt.Fprintln(&preview, "Zsh completion will be initialized if needed.")
	}
	fmt.Fprintln(&preview)
	for _, edit := range edits {
		action := "Update"
		if !edit.exists {
			action = "Create"
		} else if edit.remove {
			action = "Remove"
		}
		fmt.Fprintf(&preview, "%s: %s\n", action, completionPath(edit.path))
	}
	if _, err := stderr.Write(preview.Bytes()); err != nil {
		return err
	}
	if !yes {
		terminal, err := completionTerminal(ctx, stdin)
		if err != nil {
			return err
		}
		if !terminal {
			return errors.New("completion installation requires a TTY for confirmation; use -yes to approve explicitly")
		}
		if _, err := fmt.Fprint(stderr, "\nApply these changes? [y/N] "); err != nil {
			return err
		}
		answer, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil || (strings.TrimSpace(strings.ToLower(answer)) != "y" && strings.TrimSpace(strings.ToLower(answer)) != "yes") {
			fmt.Fprintln(stdout, "No files changed.")
			return nil
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Recheck every file after confirmation, before writing any of them.
	for _, edit := range edits {
		current, err := readCompletionEdit(edit.path)
		if err != nil {
			return err
		}
		if current.resolved != edit.resolved || current.exists != edit.exists || current.mode != edit.mode || !bytes.Equal(current.before, edit.before) {
			return fmt.Errorf("%s changed since the preview; run the installer again", edit.path)
		}
	}
	for _, edit := range edits {
		if err := writeCompletionEdit(edit); err != nil {
			return fmt.Errorf("install completion in %s: %w", edit.path, err)
		}
		action := "Updated"
		if !edit.exists {
			action = "Created"
		} else if edit.remove {
			action = "Removed"
		}
		fmt.Fprintf(stdout, "%s: %s\n", action, completionPath(edit.path))
	}
	printCompletionActivation(stdout, shell, target, paths)
	return nil
}

// Ask a minimal POSIX shell to test the inherited input descriptor. This runs
// only during installation, needs no platform-specific Go code, and unlike
// ModeCharDevice correctly rejects /dev/null as well as files and pipes.
func completionTerminal(ctx context.Context, stdin io.Reader) (bool, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", "command test -t 0")
	cmd.Stdin = stdin
	cmd.Env = []string{}
	// Bash reads BASH_ENV even with --norc. Do not run startup hooks to test a TTY.
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if name != "BASH_ENV" && name != "ENV" {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("check terminal with sh (or pass -yes): %w", err)
	}
	return true, nil
}

func completionPaths(shell, file string) ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	var paths []string
	if file != "" {
		if shell == "fish" {
			return nil, errors.New("-completion-file is only for zsh or bash; use XDG_CONFIG_HOME for fish")
		}
		paths = []string{file}
	} else {
		switch shell {
		case "zsh":
			dir, set := os.LookupEnv("ZDOTDIR")
			if !set {
				dir = home
			} else if dir == "" {
				// Zsh concatenates ZDOTDIR + "/.zshrc", producing /.zshrc.
				return nil, errors.New("empty ZDOTDIR; unset it or use -completion-file to choose a startup file")
			}
			paths = []string{filepath.Join(dir, ".zshrc")}
		case "bash":
			paths = []string{filepath.Join(home, ".bashrc")}
			profile := filepath.Join(home, ".bash_profile")
			for _, name := range []string{".bash_profile", ".bash_login", ".profile"} {
				candidate := filepath.Join(home, name)
				if _, err := os.Lstat(candidate); err == nil {
					profile = candidate
					break
				} else if !errors.Is(err, os.ErrNotExist) {
					return nil, err
				}
			}
			paths = append(paths, profile)
		case "fish":
			dir := os.Getenv("XDG_CONFIG_HOME")
			if dir == "" {
				dir = filepath.Join(home, ".config")
			}
			paths = []string{filepath.Join(dir, "fish", "completions", "go.fish"), filepath.Join(dir, "fish", "completions", "gopick.fish")}
		}
	}
	for i, path := range paths {
		paths[i], err = filepath.Abs(path)
		if err != nil {
			return nil, err
		}
	}
	return paths, nil
}

func completionEdits(shell, target string, paths []string) ([]completionEdit, error) {
	var edits []completionEdit
	seen := make(map[string]bool)
	for _, path := range paths {
		edit, err := readCompletionEdit(path)
		if err != nil {
			return nil, err
		}
		if seen[edit.resolved] {
			if shell == "fish" {
				return nil, fmt.Errorf("fish completion files must not resolve to the same file: %s", edit.resolved)
			}
			continue
		}
		seen[edit.resolved] = true
		old, outside, err := completionBlock(edit.before)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		login := shell == "bash" && len(paths) > 1 && path == paths[1]
		body := completionSetup(shell, target, login)
		if shell == "fish" {
			command := strings.TrimSuffix(filepath.Base(path), ".fish")
			if target != "both" && target != command {
				if old == "" {
					continue
				}
				body = ""
			} else if len(bytes.TrimSpace(outside)) == 0 || strings.Contains(old, "__gopick_loading_"+command) {
				body = fishCompletionFallback(command) + body
			}
		}
		edit.after = replaceCompletionBlock(edit.before, old, body)
		if login && !edit.exists {
			// A new login profile should load the user's interactive settings.
			// Keep this outside our completion block so subsequent updates retain it.
			edit.after = append([]byte("case $- in\n  *i*) if [ -r \"$HOME/.bashrc\" ]; then . \"$HOME/.bashrc\"; fi ;;\nesac\n\n"), edit.after...)
		}
		// An empty Fish file would shadow the next provider's completions.
		edit.remove = shell == "fish" && body == "" && len(bytes.TrimSpace(edit.after)) == 0
		if !bytes.Equal(edit.before, edit.after) || edit.remove {
			edits = append(edits, edit)
		}
	}
	return edits, nil
}

func completionSetup(shell, target string, login bool) string {
	command := "gopick -completion " + shell
	if target != "both" {
		command += " -completion-target " + target
	}
	switch shell {
	case "zsh":
		return "if command -v gopick >/dev/null 2>&1; then\n" +
			"  source <(" + command + ")\nfi\n"
	case "bash":
		guard := "command -v gopick >/dev/null 2>&1"
		if login {
			// The profile may already have sourced .bashrc. Directly sourcing
			// .bashrc still refreshes completion after upgrading gopick.
			guard = "{ [ \"${_gopick_completion_target-}\" != " + target + " ] || ! command -v _gopick_complete >/dev/null 2>&1; } && " + guard
		}
		// This also works in .profile, which may be read by other shells.
		return "if [ -n \"${BASH_VERSION-}\" ]; then\n  case $- in\n    *i*)\n" +
			"      if " + guard + "; then\n" +
			"        eval \"$(" + command + ")\"\n      fi\n      ;;\n  esac\nfi\n"
	case "fish":
		return "if command -sq gopick\n    " + command + " | source\nend\n"
	}
	return ""
}

// A user completion file shadows all later providers. Load the next file
// first so installing our -run hook does not hide normal Go completion.
func fishCompletionFallback(command string) string {
	return "if not set -q __gopick_loading_" + command + "\n" +
		"    set -g __gopick_loading_" + command + " 1\n" +
		"    set -l current (status filename)\n" +
		"    set -l loaded 0\n" +
		"    for dir in $fish_complete_path\n" +
		"        set -l candidate \"$dir/" + command + ".fish\"\n" +
		"        if test -f \"$candidate\"; and not test \"$candidate\" -ef \"$current\"\n" +
		"            source \"$candidate\"\n            set loaded 1\n            break\n        end\n    end\n" +
		"    if test $loaded -eq 0\n" +
		"        # Recent Fish versions embed their bundled completions.\n" +
		"        status get-file completions/" + command + ".fish 2>/dev/null | source\n    end\n" +
		"    set -e __gopick_loading_" + command + "\nend\n"
}

func printCompletionActivation(w io.Writer, shell, target string, paths []string) {
	fmt.Fprintln(w, "\nOpen a new terminal, or run this in your current shell:")
	fmt.Fprintln(w)
	if shell != "fish" {
		paths = paths[:1]
	}
	for _, path := range paths {
		if shell == "fish" && target != "both" && filepath.Base(path) != target+".fish" {
			continue
		}
		fmt.Fprintf(w, "  source %s\n", completionPath(path))
	}
}

func completionPath(path string) string {
	home, _ := os.UserHomeDir()
	homes := []string{home}
	// HOME may itself contain symlinks.
	if resolved, err := filepath.EvalSymlinks(home); err == nil && resolved != home {
		homes = append(homes, resolved)
	}
	for _, dir := range homes {
		if rel, err := filepath.Rel(dir, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "~/" + shellCommand([]string{filepath.ToSlash(rel)})
		}
	}
	return shellCommand([]string{filepath.ToSlash(path)})
}

func readCompletionEdit(path string) (completionEdit, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return completionEdit{}, err
	}
	edit := completionEdit{path: path, resolved: path, mode: 0o644}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		dir, err := completionDirectory(filepath.Dir(path))
		if err != nil {
			return edit, err
		}
		edit.resolved = filepath.Join(dir, filepath.Base(path))
		return edit, nil
	}
	if err != nil {
		return edit, err
	}
	edit.exists = true
	edit.resolved, err = filepath.EvalSymlinks(path)
	if err != nil {
		if info.Mode()&os.ModeSymlink != 0 && errors.Is(err, os.ErrNotExist) {
			return edit, fmt.Errorf("%s is a dangling symlink; create its target before installing completion: %w", path, err)
		}
		return edit, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		info, err = os.Stat(edit.resolved)
		if err != nil {
			return edit, err
		}
	}
	if !info.Mode().IsRegular() {
		return edit, fmt.Errorf("%s is not a regular file", path)
	}
	edit.mode = info.Mode().Perm()
	edit.before, err = os.ReadFile(edit.resolved)
	return edit, err
}

// Resolve the existing parents of a directory that may not exist yet. Keep
// missing components so confirmation can detect a parent symlink changing.
func completionDirectory(path string) (string, error) {
	suffix := ""
	for {
		_, err := os.Lstat(path)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				return "", fmt.Errorf("resolve completion directory %s: %w", path, err)
			}
			return filepath.Join(resolved, suffix), nil
		}
		if !errors.Is(err, os.ErrNotExist) || filepath.Dir(path) == path {
			return "", err
		}
		suffix = filepath.Join(filepath.Base(path), suffix)
		path = filepath.Dir(path)
	}
}

// Return the unique managed block and the untouched surrounding content.
func completionBlock(content []byte) (string, []byte, error) {
	start, end, offset := -1, -1, 0
	closing := ""
	for _, line := range strings.SplitAfter(string(content), "\n") {
		marker := strings.TrimRight(line, "\r\n")
		switch marker {
		case completionStart, "# >>> gopick completion >>>":
			if start != -1 || end != -1 {
				return "", nil, errors.New("duplicate or malformed gopick completion markers")
			}
			start = offset
			closing = completionEnd
			if marker != completionStart {
				// Upgrade the earlier markers in place on the next installation.
				closing = "# <<< gopick completion <<<"
			}
		case completionEnd, "# <<< gopick completion <<<":
			if start == -1 || end != -1 || marker != closing {
				return "", nil, errors.New("duplicate or malformed gopick completion markers")
			}
			end = offset + len(line)
		}
		offset += len(line)
	}
	if start == -1 && end == -1 {
		return "", content, nil
	}
	if end == -1 {
		return "", nil, errors.New("unterminated gopick completion block")
	}
	return string(content[start:end]), append(bytes.Clone(content[:start]), content[end:]...), nil
}

func replaceCompletionBlock(content []byte, old, body string) []byte {
	block := ""
	if body != "" {
		block = completionStart + "\n" + body + completionEnd + "\n"
	}
	if old != "" {
		return []byte(strings.Replace(string(content), old, block, 1))
	}
	if len(content) == 0 {
		return []byte(block)
	}
	separator := ""
	if content[len(content)-1] != '\n' {
		separator = "\n"
	}
	return []byte(string(content) + separator + block)
}

func writeCompletionEdit(edit completionEdit) error {
	if edit.remove {
		// Remove the loader, not the target of a user-owned symlink.
		return os.Remove(edit.path)
	}
	if err := os.MkdirAll(filepath.Dir(edit.resolved), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(edit.resolved), ".gopick-completion-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	_, writeErr := temp.Write(edit.after)
	modeErr := temp.Chmod(edit.mode)
	closeErr := temp.Close()
	if err := errors.Join(writeErr, modeErr, closeErr); err != nil {
		return err
	}
	return os.Rename(temp.Name(), edit.resolved)
}
