package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// All shells share Go command-line interpretation here. Shell scripts only
// adapt their token/cursor conventions and register with the existing completer.
type completionRequest struct {
	kind     string
	dir      string
	tags     *string
	patterns []string
	build    []string
	prefix   string
	prepend  string
	values   []string
}

// The first line tells the shell whether we handled this position. Subsequent
// lines are whole, unquoted replacement words. No shell input is ever evaluated.
func completeShell(ctx context.Context, w io.Writer, words []string, cursor int) error {
	if cursor < 1 || cursor >= len(words) {
		return errors.New("-cursor must identify a word after the command for -complete shell")
	}
	var request completionRequest
	if filepath.Base(words[0]) == "go" {
		request = goCompletionRequest(words, cursor)
	} else {
		request = gopickCompletionRequest(words, cursor)
	}
	kind := request.kind
	if kind == "" {
		kind = "fallback"
	}
	if _, err := fmt.Fprintln(w, kind); err != nil {
		return err
	}
	if kind == "fallback" || kind == "none" || kind == "files" {
		return nil
	}
	values := request.values
	names := make(map[string]bool)
	if request.patterns != nil {
		dir := request.dir
		if dir == "" {
			dir = "."
		}
		absolute, err := filepath.Abs(dir)
		if err != nil {
			return err
		}
		tests, err := discover(ctx, absolute, request.patterns, request.tags, request.build...)
		if err != nil {
			return err
		}
		for _, test := range tests {
			names[test.Name] = true
		}
		var output bytes.Buffer
		candidateKind := "tests"
		if kind == "arguments" {
			candidateKind = "arguments"
		}
		if err := printCompletions(&output, tests, candidateKind, absolute); err != nil {
			return err
		}
		values = strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
		if output.Len() == 0 {
			values = nil
		}
	}
	for _, value := range values {
		if !strings.HasPrefix(value, request.prefix) {
			continue
		}
		if names[value] {
			value = "^" + value + "$"
		}
		if _, err := fmt.Fprintln(w, request.prepend+value); err != nil {
			return err
		}
	}
	return nil
}

func goCompletionRequest(words []string, cursor int) completionRequest {
	r := completionRequest{kind: "exact"}
	first := 1
	if name, value, equals := completionFlag(words[first]); name == "C" {
		if !equals {
			first++
			if first >= cursor {
				return completionRequest{}
			}
			value = words[first]
		}
		r.dir, first = value, first+1
	}
	if first >= cursor || words[first] != "test" {
		return completionRequest{}
	}
	active, packagesDone := false, false
	for i := first + 1; i < len(words); i++ {
		word := words[i]
		if word == "--" || word == "-args" || word == "--args" {
			if i <= cursor {
				return completionRequest{}
			}
			break
		}
		name, value, equals := completionFlag(word)
		if i == cursor {
			if (name != "run" && name != "test.run") || !equals {
				return completionRequest{}
			}
			r.prepend = word[:len(word)-len(value)]
			r.prefix, active = value, true
			packagesDone = packagesDone || len(r.patterns) > 0
			continue
		}
		if name == "" {
			// Go accepts one contiguous package list. After a flag closes
			// that list, a bare word starts literal test-binary arguments.
			if packagesDone {
				if i < cursor {
					return completionRequest{}
				}
				break
			}
			r.patterns = append(r.patterns, word)
			continue
		}
		info, known := goFlag(name)
		if !known {
			return completionRequest{}
		}
		packagesDone = packagesDone || len(r.patterns) > 0 || info.testOnly
		takesValue := info.takesValue
		if info.testOnly {
			// Go doesn't know the test binary's flag arity. It only consumes
			// a following non-flag word; build flags remain Go arguments.
			takesValue = i+1 < len(words) && (words[i+1] == "-" || !strings.HasPrefix(words[i+1], "-"))
		}
		if takesValue && !equals {
			i++
			if i >= len(words) {
				return completionRequest{}
			}
			value = words[i]
			if i == cursor {
				if name != "run" && name != "test.run" {
					return completionRequest{}
				}
				r.prefix, active = value, true
				continue
			}
		}
		switch {
		case name == "C":
			r.dir = value
		case name == "tags":
			r.tags = &value
		case info.discover:
			if info.takesValue || equals {
				r.build = append(r.build, "-"+name+"="+value)
			} else {
				r.build = append(r.build, "-"+name)
			}
		}
	}
	if !active {
		return completionRequest{}
	}
	r.prefix = strings.TrimPrefix(r.prefix, "^")
	if !literalTestPrefix(r.prefix) {
		return completionRequest{kind: "none"}
	}
	if len(r.patterns) == 0 {
		r.patterns = []string{"."}
	}
	return r
}

func completionFlag(word string) (name, value string, equals bool) {
	if !strings.HasPrefix(word, "-") || word == "-" {
		return "", "", false
	}
	return strings.Cut(strings.TrimPrefix(strings.TrimPrefix(word, "-"), "-"), "=")
}

func literalTestPrefix(prefix string) bool {
	for _, char := range prefix {
		if char != '_' && !unicode.IsLetter(char) && !unicode.IsDigit(char) {
			return false
		}
	}
	return true
}

func gopickCompletionRequest(words []string, cursor int) completionRequest {
	r := completionRequest{kind: "arguments", prefix: words[cursor]}
	var positional []string
	options := true
	var installing, searching, installSet bool
	for i := 1; i <= cursor; i++ {
		word := words[i]
		name, value, equals := completionFlag(word)
		if len(positional) > 0 && name != "" {
			return completionRequest{}
		}
		if options && len(positional) == 0 && word == "--" && i < cursor {
			options = false
			continue
		}
		if options && len(positional) == 0 && (name != "" || i == cursor && strings.HasPrefix(word, "-")) {
			if i == cursor && !equals {
				var flags []string
				if !installing {
					flags = append(flags, "C", "tags", "list", "cmd", "format", "format-fields", "completion")
				}
				flags = append(flags, "completion-target")
				if !searching {
					flags = append(flags, "completion-file")
					if !installSet {
						flags = append(flags, "install-completion")
					}
					flags = append(flags, "yes")
				}
				flags = append(flags, "help")
				prefix := "-"
				if strings.HasPrefix(word, "--") {
					prefix = "--"
				}
				for _, flag := range flags {
					r.values = append(r.values, prefix+flag)
				}
				return r
			}
			switch name {
			case "install-completion", "completion-file", "yes":
				if searching {
					return completionRequest{kind: "none"}
				}
				installing = true
				installSet = installSet || name == "install-completion"
			case "completion-target":
				// Shared by script output and installation.
			default:
				if installing {
					return completionRequest{kind: "none"}
				}
				searching = true
			}
			switch name {
			case "C", "tags", "format", "completion", "completion-target", "completion-file", "install-completion", "complete", "cursor":
				if !equals {
					i++
					value = words[i]
				}
				if i == cursor {
					r.prefix = value
					if equals {
						r.prepend = word[:len(word)-len(value)]
					}
					switch name {
					case "completion", "install-completion":
						r.values = []string{"zsh", "bash", "fish"}
					case "completion-file":
						r.kind = "files"
					case "completion-target":
						r.values = []string{"go", "gopick", "both"}
					case "format":
						r.values = []string{"table", "json"}
					case "C":
						r.kind = "directories"
						r.values = completionDirectories(value)
					}
					return r
				}
				switch name {
				case "C":
					r.dir = value
				case "tags":
					r.tags = &value
				}
			case "list", "cmd", "format-fields", "yes":
				if i == cursor && equals {
					r.prefix = value
					r.prepend = word[:len(word)-len(value)]
					r.values = []string{"true", "false"}
					return r
				}
			default:
				return completionRequest{kind: "none"}
			}
			continue
		}
		if installing {
			return completionRequest{kind: "none"}
		}
		if i < cursor {
			positional = append(positional, word)
		}
	}
	if len(positional) > 1 {
		return completionRequest{kind: "none"}
	}
	if len(positional) == 1 {
		pattern, _, _, err := searchArgs([]string{positional[0], ""})
		if err != nil {
			return completionRequest{kind: "none"}
		}
		r.kind, r.patterns = "tests", []string{pattern}
	} else {
		r.patterns = []string{"./..."}
	}
	if strings.HasPrefix(r.prefix, "^") {
		r.kind, r.prefix = "tests", strings.TrimPrefix(r.prefix, "^")
	}
	return r
}

func completionDirectories(prefix string) []string {
	dir, base := filepath.Split(prefix)
	readDir := dir
	if readDir == "" {
		readDir = "."
	}
	entries, err := os.ReadDir(readDir)
	if err != nil {
		return nil
	}
	var values []string
	for _, entry := range entries {
		info, err := os.Stat(filepath.Join(readDir, entry.Name()))
		if err == nil && info.IsDir() && strings.HasPrefix(entry.Name(), base) {
			// Candidates are inserted into POSIX shells, including Git Bash.
			values = append(values, dir+entry.Name()+"/")
		}
	}
	return values
}
