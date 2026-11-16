package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestOverlay(t *testing.T) {
	dir := fixture(t, map[string]string{
		"root_test.go":         "package example\nimport \"testing\"\nfunc TestStale(t *testing.T) { t.Fatal(\"stale source ran\") }\n",
		"deleted_test.go":      "package example\nimport \"testing\"\nfunc TestDeleted(t *testing.T) { t.Fatal(\"deleted source ran\") }\n",
		"backing/root.txt":     "package example\nimport \"testing\"\nfunc TestReplaced(t *testing.T) { t.Log(\"replacement ran\") }\n",
		"backing/new.txt":      "package generated\nimport \"testing\"\nfunc TestAdded(t *testing.T) { t.Log(\"addition ran\") }\n",
		"generated/doc.go":     "package generated\n",
		"backing/external.txt": "package example_test\nimport \"testing\"\nfunc TestExternal(t *testing.T) {}\n",
		"overlays/overlay file.json": `{"Replace":{
"root_test.go":"backing/root.txt",
"deleted_test.go":"",
"generated/new_test.go":"backing/new.txt",
"external_test.go":"backing/external.txt"
}}`,
	})
	for _, mode := range []string{"environment", "persisted", "explicit"} {
		t.Run(mode, func(t *testing.T) {
			var forwarded []string
			setting := "'-overlay=overlays/overlay file.json'"
			switch mode {
			case "environment":
				t.Setenv("GOFLAGS", setting)
			case "persisted":
				path := filepath.Join(t.TempDir(), "goenv")
				if err := os.WriteFile(path, []byte("GOFLAGS="+setting+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("GOENV", path)
			case "explicit":
				forwarded = []string{"-overlay", "overlays/overlay file.json"}
			}
			var stdout, stderr bytes.Buffer
			args := append([]string{"-C", dir, "--format", "json", "Test"}, forwarded...)
			if err := run(context.Background(), args, nil, &stdout, &stderr); err != nil {
				t.Fatalf("listing: %v\n%s", err, &stderr)
			}
			decoder := json.NewDecoder(&stdout)
			for _, tc := range []struct{ name, file string }{
				{"TestReplaced", "root_test.go"}, {"TestExternal", "external_test.go"}, {"TestAdded", "generated/new_test.go"},
			} {
				var got test
				if err := decoder.Decode(&got); err != nil {
					t.Fatal(err)
				}
				if got.Name != tc.name || got.File != filepath.Join(dir, tc.file) || got.Line != 3 {
					t.Errorf("listed = %#v; want %s at %s:3", got, tc.name, tc.file)
				}
				cmd := exec.Command(got.Command[0], got.Command[1:]...)
				cmd.Dir = got.Dir
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("JSON command: %v\n%s", err, output)
				}
			}
			for _, kind := range []string{"tests", "go-tests"} {
				stdout.Reset()
				args = []string{"-C", dir, "-complete", kind}
				if kind == "go-tests" {
					args = append(args, "--")
					args = append(args, forwarded...)
					args = append(args, "./...")
				} else {
					args = append(args, "Test")
					args = append(args, forwarded...)
				}
				if err := run(context.Background(), args, nil, &stdout, &stderr); err != nil {
					t.Fatal(err)
				}
				if got, want := stdout.String(), "TestAdded\nTestExternal\nTestReplaced\n"; got != want {
					t.Errorf("%s completion = %q, want %q", kind, got, want)
				}
			}
			stdout.Reset()
			args = append([]string{"-C", dir, "Test", "-v"}, forwarded...)
			if err := run(context.Background(), args, nil, &stdout, &stderr); err != nil {
				t.Fatalf("execution: %v\n%s\n%s", err, &stdout, &stderr)
			}
			for _, message := range []string{"replacement ran", "addition ran"} {
				if !strings.Contains(stdout.String(), message) {
					t.Errorf("missing %q in output: %s", message, &stdout)
				}
			}
			stdout.Reset()
			args = []string{"-C", dir, "-complete", "tests", "Test", "-overlay="}
			if err := run(context.Background(), args, nil, &stdout, &stderr); err != nil {
				t.Fatal(err)
			}
			if got, want := stdout.String(), "TestDeleted\nTestStale\n"; got != want {
				t.Errorf("cleared overlay = %q, want %q", got, want)
			}
		})
	}
}

func TestOverlayFlags(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
		set  bool
	}{
		{nil, "", false},
		{[]string{"-overlay=one", "--overlay", "two", "."}, "two", true},
		{[]string{"-overlay=one", "-overlay="}, "", true},
		{[]string{"-tags", "-overlay=fake", "-ldflags", "-overlay=fake", "."}, "", false},
		{[]string{".", "-overlay=ignored"}, "", false},
	} {
		value, supplied := overlayFlag(tc.args)
		if value != tc.want || supplied != tc.set {
			t.Errorf("overlayFlag(%q) = %q, %v; want %q, %v", tc.args, value, supplied, tc.want, tc.set)
		}
	}
	for _, tc := range []struct {
		value string
		want  []string
	}{
		{"", nil},
		{" -v\t'-overlay=path with spaces'\n", []string{"-v", "-overlay=path with spaces"}},
		{`"-overlay=C:\path with spaces" -tags=special`, []string{`-overlay=C:\path with spaces`, "-tags=special"}},
		{`-overlay=repo's/file`, []string{`-overlay=repo's/file`}},
	} {
		got, err := splitGoFlags(tc.value)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("splitGoFlags(%q) = %q, %v; want %q", tc.value, got, err, tc.want)
		}
	}
}
