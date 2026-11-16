package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFormatFields(t *testing.T) {
	// Metadata must work without Go, without a project, and even when a selector
	// is supplied: this mode must never discover or execute tests.
	t.Setenv("PATH", "")
	dir := filepath.Join(t.TempDir(), "missing")
	want := make(map[string]string)
	for _, field := range reflect.VisibleFields(reflect.TypeFor[formatRow]()) {
		if field.IsExported() {
			want[field.Name] = field.Type.String()
		}
	}
	for _, args := range [][]string{
		{"-format-fields"},
		{"--format-fields", "Test"},
		{"-format-fields=true", "-cmd", "Test"},
		{"-format-fields", "-format=", "Test"},
	} {
		var stdout, stderr bytes.Buffer
		if err := run(context.Background(), append([]string{"-C", dir}, args...), nil, &stdout, &stderr); err != nil {
			t.Fatal(err)
		}
		if stderr.Len() != 0 {
			t.Fatalf("unexpected stderr: %s", &stderr)
		}
		got := make(map[string]string)
		for _, line := range bytes.Split(bytes.TrimSuffix(stdout.Bytes(), []byte("\n")), []byte("\n")) {
			var field formatField
			if err := json.Unmarshal(line, &field); err != nil {
				t.Fatal(err)
			}
			if field.Description == "" {
				t.Errorf("field %q has no description", field.Name)
			}
			if _, exists := got[field.Name]; exists {
				t.Errorf("duplicate field %q", field.Name)
			}
			got[field.Name] = field.Type
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("documented fields = %v, template fields = %v", got, want)
		}
	}
}

func TestFormat(t *testing.T) {
	dir := t.TempDir()
	tests := []test{{
		Name: "TestRead", ImportPath: "example.com/project/sub", Package: "sub_test",
		File: filepath.Join(dir, "sub", "read_test.go"), Line: 12, Dir: dir,
		Command: []string{"go", "test", "example.com/project/sub", "-run", "^TestRead$"},
	}}
	location := filepath.Join("sub", "read_test.go") + ":12"
	for _, tc := range []struct {
		name   string
		format string
		want   string
	}{
		{"default", "", "Test      Package   Location\nTestRead  sub_test  " + location + "\n"},
		{"plain", "{{.Test}}", "TestRead\n"},
		{"name alias", "{{.Name}}", "TestRead\n"},
		{"custom table", `table {{.ImportPath}}\t{{.Name}}`, "Import path              Test\nexample.com/project/sub  TestRead\n"},
		{"numeric header", `table {{printf "%03d" .Line}}\t{{upper .Package}}`, "Line  Package\n012   SUB_TEST\n"},
		{"numeric value", `{{printf "%03d" .Line}}`, "012\n"},
		{"separators", `{{.Test}}\t{{.Package}}\n{{.Location}}`, "TestRead\tsub_test\n" + location + "\n"},
		{"escaped template string", `{{printf "%s\n" .Test}}`, "TestRead\n"},
		{"conditional separators", `{{if .Package}}{{.Name}}\t{{.Package}}{{end}}`, "TestRead\tsub_test\n"},
		{"functions", `{{join (split .ImportPath "/") ":"}} {{lower .Test}}`, "example.com:project:sub testread\n"},
		{"command", `{{.Command}}`, "go test example.com/project/sub -run '^TestRead$'\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := printTests(&output, tests, dir, dir, tc.format); err != nil {
				t.Fatal(err)
			}
			if output.String() != tc.want {
				t.Errorf("output = %q, want %q", &output, tc.want)
			}
		})
	}
	var output bytes.Buffer
	if err := printTests(&output, tests, dir, dir, "{{json .File}}"); err != nil {
		t.Fatal(err)
	}
	var file string
	if err := json.Unmarshal(output.Bytes(), &file); err != nil {
		t.Fatal(err)
	}
	if file != filepath.Join("sub", "read_test.go") {
		t.Errorf("individual JSON field = %q, want relative filename", file)
	}
	for _, format := range []string{
		"{{", "prefix {{.Missing}}", "table {{.Missing}}", "{{unknown .Test}}", "{{join .Test \" \"}}",
	} {
		var output bytes.Buffer
		if err := printTests(&output, tests, dir, dir, format); err == nil {
			t.Errorf("format %q succeeded; want an error", format)
		}
		if output.Len() != 0 {
			t.Errorf("invalid format %q printed partial output: %q", format, &output)
		}
	}
}

func TestJSONFormat(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "project with spaces and 'quotes'")
	tests := []test{
		{
			Name: "TestInternal", ImportPath: "example.com/project", Package: "example",
			File: filepath.Join(dir, "internal_test.go"), Line: 5, Dir: dir,
			Command: []string{"go", "test", "example.com/project", "-run", "^TestInternal$"},
		},
		{
			Name: "TestExternal", ImportPath: "example.com/project", Package: "example_test",
			File: filepath.Join(dir, "external_test.go"), Line: 9, Dir: dir,
			Command: []string{"go", "test", "example.com/project", "-run", "^TestExternal$"},
		},
	}
	for _, format := range []string{"json", "{{json .}}"} {
		t.Run(format, func(t *testing.T) {
			var output bytes.Buffer
			if err := printTests(&output, tests, dir, t.TempDir(), format); err != nil {
				t.Fatal(err)
			}
			lines := bytes.Split(bytes.TrimSuffix(output.Bytes(), []byte("\n")), []byte("\n"))
			if len(lines) != len(tests) || !bytes.HasSuffix(output.Bytes(), []byte("\n")) {
				t.Fatalf("expected one newline-terminated record per test, got %q", &output)
			}
			for i, line := range lines {
				var got test
				if err := json.Unmarshal(line, &got); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, tests[i]) {
					t.Errorf("record = %#v, want %#v", got, tests[i])
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(line, &fields); err != nil {
					t.Fatal(err)
				}
				keys := []string{"name", "importPath", "package", "file", "line", "dir", "command"}
				if len(fields) != len(keys) {
					t.Errorf("unexpected JSON fields: %s", line)
				}
				for _, key := range keys {
					if _, ok := fields[key]; !ok {
						t.Errorf("missing JSON field %q: %s", key, line)
					}
				}
			}
			output.Reset()
			if err := printTests(&output, nil, dir, dir, format); err != nil {
				t.Fatal(err)
			}
			if output.Len() != 0 {
				t.Errorf("empty results printed %q", &output)
			}
		})
	}
}

func TestSearchArgs(t *testing.T) {
	for _, tc := range []struct {
		args       []string
		pattern    string
		expression string
		supplied   bool
	}{
		{nil, "./...", "", false},
		{[]string{""}, "./...", "", true},
		{[]string{"."}, ".", "", false},
		{[]string{"./sub", "Read"}, "./sub", "Read", true},
		{[]string{"../other/...", "Read"}, "../other/...", "Read", true},
		{[]string{"example.com/project/sub", "Read"}, "example.com/project/sub", "Read", true},
		{[]string{"example.com/project/sub"}, "example.com/project/sub", "", false},
		{[]string{"net/http", "Read"}, "net/http", "Read", true},
		{[]string{"TestRead/child"}, "./...", "TestRead/child", true},
		{[]string{"^TestRead$/child|TestWrite/other"}, "./...", "^TestRead$/child|TestWrite/other", true},
		{[]string{"Test.Read/child"}, "./...", "Test.Read/child", true},
		{[]string{".*/child"}, "./...", ".*/child", true},
		{[]string{"/child"}, "./...", "/child", true},
		{[]string{"./...", "example.com/project"}, "./...", "example.com/project", true},
	} {
		pattern, expression, supplied, err := searchArgs(tc.args)
		if err != nil {
			t.Fatal(err)
		}
		if pattern != tc.pattern || expression != tc.expression || supplied != tc.supplied {
			t.Errorf("searchArgs(%q) = (%q, %q, %v), want (%q, %q, %v)", tc.args, pattern, expression, supplied, tc.pattern, tc.expression, tc.supplied)
		}
	}
	for _, args := range [][]string{{"Read", "Write"}, {"./sub", "Read", "Write"}, {"TestRead/child", "Write"}} {
		if _, _, _, err := searchArgs(args); err == nil {
			t.Errorf("searchArgs(%q) succeeded; want one regexp", args)
		}
	}
}
