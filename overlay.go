package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Go applies explicit flags after GOFLAGS, including an empty -overlay=.
// Query persisted Go settings only when the process environment doesn't supply
// them. Paths in the overlay are relative to Go's working directory, not the
// directory containing the overlay JSON file.
func readOverlay(ctx context.Context, dir string, args []string) (map[string]string, error) {
	filename, explicit := overlayFlag(args)
	if !explicit {
		settings := os.Getenv("GOFLAGS")
		if settings == "" && os.Getenv("GOENV") != "off" {
			cmd := exec.CommandContext(ctx, "go", "env", "-json", "GOFLAGS")
			cmd.Dir = dir
			output, err := cmd.Output()
			if err != nil {
				return nil, fmt.Errorf("read Go settings: %w", err)
			}
			var environment struct{ GOFLAGS string }
			if err := json.Unmarshal(output, &environment); err != nil {
				return nil, fmt.Errorf("decode Go settings: %w", err)
			}
			settings = environment.GOFLAGS
		}
		flags, err := splitGoFlags(settings)
		if err != nil {
			return nil, err
		}
		filename, _ = overlayFlag(flags)
	}
	if filename == "" {
		return nil, nil
	}
	absolute := func(path string) string {
		if path == "" {
			return ""
		}
		if filepath.IsAbs(path) {
			return filepath.Clean(path)
		}
		return filepath.Join(dir, path)
	}
	contents, err := os.ReadFile(absolute(filename))
	if err != nil {
		return nil, fmt.Errorf("read Go overlay: %w", err)
	}
	var overlay struct {
		Replace map[string]string
	}
	if err := json.Unmarshal(contents, &overlay); err != nil {
		return nil, fmt.Errorf("decode Go overlay: %w", err)
	}
	paths := make(map[string]string, len(overlay.Replace))
	for from, to := range overlay.Replace {
		paths[absolute(from)] = absolute(to)
	}
	return paths, nil
}

func overlayFlag(args []string) (value string, supplied bool) {
	for i := 0; i < len(args); i++ {
		name, argument, equals := completionFlag(args[i])
		if name == "" || args[i] == "--" {
			break
		}
		info, _ := goFlag(name)
		if info.takesValue && !equals {
			i++
			if i >= len(args) {
				break // go list reports malformed flags before we read the overlay.
			}
			argument = args[i]
		}
		if name == "overlay" {
			value, supplied = argument, true
		}
	}
	return value, supplied
}

// Match Go's GOFLAGS splitting: quotes can wrap a whole field, with no shell
// expansion or backslash unescaping. Quotes inside an unquoted field are literal.
func splitGoFlags(value string) ([]string, error) {
	var fields []string
	for value = strings.TrimLeft(value, " \t\r\n"); value != ""; value = strings.TrimLeft(value, " \t\r\n") {
		if quote := value[0]; quote == '\'' || quote == '"' {
			end := strings.IndexByte(value[1:], quote)
			if end == -1 {
				return nil, fmt.Errorf("unterminated quote in GOFLAGS")
			}
			fields = append(fields, value[1:end+1])
			value = value[end+2:]
			continue
		}
		end := strings.IndexAny(value, " \t\r\n")
		if end == -1 {
			end = len(value)
		}
		fields = append(fields, value[:end])
		value = value[end:]
	}
	return fields, nil
}
