package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func fixture(t *testing.T, files map[string]string) string {
	t.Helper()
	t.Setenv("GOWORK", "off")
	t.Setenv("GOENV", "off")
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOSUMDB", "off")
	dir := t.TempDir()
	files["go.mod"] = "module example.com/project\n\ngo 1.22\n"
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Keep shell source out of the Windows command line: Git Bash's argument
// decoding differs from Go's escaping, and large scripts exceed its size limit.
func shellScript(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "script")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	return filepath.ToSlash(path)
}

func TestDiscover(t *testing.T) {
	dir := fixture(t, map[string]string{
		// Discovery must work without resolving imports or compiling this code.
		"example.go": "package example\nimport _ \"example.com/unavailable\"\nvar broken = undefined\n",
		"example_test.go": `package example
import "testing"
func TestAlpha(t *testing.T) { t.Run("child", func(t *testing.T) {}) }
func TestZebra(t *testing.T) {}
func TestMain(m *testing.M) {}
func TestingHelper(t *testing.T) {}
func BenchmarkAlpha(b *testing.B) {}
`,
		"external_test.go":        "package example_test\nimport check \"testing\"\nfunc TestExternal(t *check.T) {}\n",
		"tagged_test.go":          "//go:build special\n\npackage example\nimport \"testing\"\nfunc TestTagged(t *testing.T) {}\n",
		"sub/sub_test.go":         "package sub\nimport . \"testing\"\nfunc TestAlpha(t *T) {}\n",
		"testdata/hidden_test.go": "package hidden\nimport \"testing\"\nfunc TestHidden(t *testing.T) {}\n",
	})

	for _, tags := range []string{"", "special"} {
		t.Run("tags="+tags, func(t *testing.T) {
			var override *string
			if tags != "" {
				override = &tags
			}
			tests, err := discover(context.Background(), dir, []string{"./..."}, override)
			if err != nil {
				t.Fatal(err)
			}
			want := []test{
				{Name: "TestAlpha", ImportPath: "example.com/project", Package: "example", File: filepath.Join(dir, "example_test.go"), Line: 3},
			}
			if tags != "" {
				want = append(want, test{Name: "TestTagged", ImportPath: "example.com/project", Package: "example", File: filepath.Join(dir, "tagged_test.go"), Line: 5})
			}
			want = append(want,
				test{Name: "TestZebra", ImportPath: "example.com/project", Package: "example", File: filepath.Join(dir, "example_test.go"), Line: 4},
				test{Name: "TestExternal", ImportPath: "example.com/project", Package: "example_test", File: filepath.Join(dir, "external_test.go"), Line: 3},
				test{Name: "TestAlpha", ImportPath: "example.com/project/sub", Package: "sub", File: filepath.Join(dir, "sub/sub_test.go"), Line: 3},
			)
			for i := range want {
				want[i].Dir = dir
				want[i].Command = []string{"go", "test"}
				if tags != "" {
					want[i].Command = append(want[i].Command, "-tags", tags)
				}
				want[i].Command = append(want[i].Command, want[i].ImportPath, "-run", "^"+want[i].Name+"$")
			}
			if !reflect.DeepEqual(tests, want) {
				t.Errorf("discover() = %#v, want %#v", tests, want)
			}
		})
	}
}

func TestDiscoveryLineDirectives(t *testing.T) {
	dir := fixture(t, map[string]string{
		"physical_test.go": `package example
import "testing"
//line generated_source.go:100
func TestRemapped(t *testing.T) {}
/*line other.go:200:3*/
func FuzzRemapped(f *testing.F) {}
//line :300
func Example() {
	// Output:
}
`,
	})
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"-C", dir, "-format", "json"}, nil, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"TestRemapped": 4, "FuzzRemapped": 6, "Example": 8}
	decoder := json.NewDecoder(&stdout)
	for range len(want) {
		var got test
		if err := decoder.Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got.File != filepath.Join(dir, "physical_test.go") || got.Line != want[got.Name] {
			t.Errorf("%s location = %s:%d, want physical_test.go:%d", got.Name, got.File, got.Line, want[got.Name])
		}
	}
}

func TestFuzzAndExamples(t *testing.T) {
	dir := fixture(t, map[string]string{
		"internal_test.go": `package example
import (
	"fmt"
	"testing"
)
func TestRegular(t *testing.T) { t.Fatal("must not run") }
func FuzzInternal(f *testing.F) {
	f.Add("seed")
	f.Fuzz(func(t *testing.T, input string) { t.Log(input) })
}
func Example_empty() {
	// Output:
}
func Example_unordered() {
	fmt.Println("b\na")
	// Unordered output:
	// a
	// b
}
func Example_documentation() { panic("examples without output must not run") }
type helper struct{}
func (helper) Example_empty() {
	panic("methods must not run")
	// Output:
}
`,
		"external_test.go": `package example_test
import (
	"fmt"
	check "testing"
)
func FuzzExternal(f *check.F) {
	f.Add(1)
	f.Fuzz(func(t *check.T, input int) { t.Log(input) })
}
func Example() {
	fmt.Println("example ran")
	// Output: example ran
}
`,
	})
	want := []string{"Example", "Example_empty", "Example_unordered", "FuzzExternal", "FuzzInternal", "TestRegular"}
	tests, err := discover(context.Background(), dir, []string{"."}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, test := range tests {
		names = append(names, test.Name)
		command := []string{"go", "test", "example.com/project", "-run", "^" + test.Name + "$"}
		if !reflect.DeepEqual(test.Command, command) {
			t.Errorf("%s command = %q, want %q", test.Name, test.Command, command)
		}
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("discovered names = %q, want %q", names, want)
	}
	// Compare with Go's actual runnable names, including empty-output examples.
	cmd := exec.Command("go", "test", "-list", ".", ".")
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go test -list: %v\n%s", err, output)
	}
	var listed []string
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "Test") || strings.HasPrefix(line, "Fuzz") || strings.HasPrefix(line, "Example") {
			listed = append(listed, line)
		}
	}
	sort.Strings(listed)
	if !reflect.DeepEqual(listed, want) {
		t.Errorf("Go's runnable names = %q, want %q", listed, want)
	}
	for _, args := range [][]string{nil, {"-complete", "tests"}, {"-complete", "go-tests"}} {
		var stdout, stderr bytes.Buffer
		if err := run(context.Background(), append([]string{"-C", dir}, args...), nil, &stdout, &stderr); err != nil {
			t.Fatal(err)
		}
		for _, name := range want {
			if !strings.Contains(stdout.String(), name) {
				t.Errorf("run(%q) omitted %s: %s", args, name, &stdout)
			}
		}
		if strings.Contains(stdout.String(), "Example_documentation") || stderr.Len() != 0 {
			t.Errorf("run(%q): stdout = %q, stderr = %q", args, &stdout, &stderr)
		}
	}
	for _, tc := range []struct {
		words []string
		want  string
	}{
		{[]string{"go", "-C", dir, "test", "-run=Fuzz"}, "exact\n-run=^FuzzExternal$\n-run=^FuzzInternal$\n"},
		{[]string{"go", "-C", dir, "test", "-run=Example"}, "exact\n-run=^Example$\n-run=^Example_empty$\n-run=^Example_unordered$\n"},
		{[]string{"gopick", "-C", dir, "Fuzz"}, "arguments\nFuzzExternal\nFuzzInternal\n"},
		{[]string{"gopick", "-C", dir, "Example"}, "arguments\nExample\nExample_empty\nExample_unordered\n"},
	} {
		var stdout bytes.Buffer
		if err := completeShell(context.Background(), &stdout, tc.words, len(tc.words)-1); err != nil {
			t.Fatal(err)
		}
		if stdout.String() != tc.want {
			t.Errorf("completion for %q = %q, want %q", tc.words, &stdout, tc.want)
		}
	}
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"-C", dir, "Fuzz|Example", "-v"}, nil, &stdout, &stderr); err != nil {
		t.Fatalf("run fuzz seeds and examples: %v\n%s\n%s", err, &stdout, &stderr)
	}
	for _, name := range want[:len(want)-1] {
		if !strings.Contains(stdout.String(), "=== RUN   "+name+"\n") {
			t.Errorf("%s did not run: %s", name, &stdout)
		}
	}
	if !strings.Contains(stdout.String(), "FuzzInternal/seed#0") || !strings.Contains(stdout.String(), "FuzzExternal/seed#0") {
		t.Errorf("fuzz seeds did not run: %s", &stdout)
	}
}

func TestTags(t *testing.T) {
	dir := fixture(t, map[string]string{
		"offline_test.go":     "//go:build !integration\n\npackage example\nimport \"testing\"\nfunc TestOffline(t *testing.T) {}\n",
		"integration_test.go": "//go:build integration\n\npackage example\nimport \"testing\"\nfunc TestIntegration(t *testing.T) {}\n",
	})
	t.Setenv("GOFLAGS", "-tags=integration -v")
	for _, tc := range []struct {
		name string
		args []string
		want string
		tags []string
	}{
		{"inherited", nil, "TestIntegration", nil},
		{"empty equals", []string{"-tags="}, "TestOffline", []string{"-tags", ""}},
		{"empty separate", []string{"--tags", ""}, "TestOffline", []string{"-tags", ""}},
		{"override", []string{"-tags", "other"}, "TestOffline", []string{"-tags", "other"}},
		{"last value wins", []string{"-tags", "integration", "-tags="}, "TestOffline", []string{"-tags", ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"-C", dir}, tc.args...)
			invoke := func(extra ...string) string {
				t.Helper()
				var stdout, stderr bytes.Buffer
				if err := run(context.Background(), append(append([]string{}, args...), extra...), nil, &stdout, &stderr); err != nil {
					t.Fatalf("run(%q): %v\n%s", extra, err, &stderr)
				}
				return stdout.String()
			}
			var listed test
			if err := json.Unmarshal([]byte(invoke("--format", "json")), &listed); err != nil {
				t.Fatal(err)
			}
			if listed.Name != tc.want {
				t.Fatalf("listed tests = %#v, want only %s", listed, tc.want)
			}
			wantCommand := append([]string{"go", "test"}, tc.tags...)
			wantCommand = append(wantCommand, "example.com/project", "-run", "^"+tc.want+"$")
			if !reflect.DeepEqual(listed.Command, wantCommand) {
				t.Errorf("listed command = %q, want %q", listed.Command, wantCommand)
			}
			for _, mode := range []string{"tests", "go-tests"} {
				if got := invoke("-complete", mode); got != tc.want+"\n" {
					t.Errorf("%s completion = %q, want %s", mode, got, tc.want)
				}
			}
			expression := "^" + tc.want + "$"
			wantShell := shellCommand(append([]string{"go", "-C", dir}, wantCommand[1:]...)) + "\n"
			if got := invoke("-cmd", expression); got != wantShell {
				t.Errorf("printed command = %q, want %q", got, wantShell)
			}
			if got := invoke(expression); !strings.Contains(got, "=== RUN   "+tc.want+"\n") {
				t.Errorf("test did not run: %s", got)
			}
		})
	}
}

func TestTestFunctions(t *testing.T) {
	for _, tc := range []struct {
		declaration string
		want        bool
	}{
		{"func Test(t *testing.T) {}", true},
		{"func TestThing(t *testing.T) {}", true},
		{"func TestÉclair(t *check.T) {}", true},
		{"func Test1(*T) {}", true},
		{"func TestMain(t *testing.T) {}", true},
		{"func Testing(t *testing.T) {}", false},
		{"func Testéclair(t *testing.T) {}", false},
		{"func TestMain(m *testing.M) {}", false},
		{"func TestValue(t testing.T) {}", false},
		{"func TestPair(t, other *testing.T) {}", false},
		{"func TestPair(t *testing.T, other int) {}", false},
		{"func TestMissing() {}", false},
		{"func TestReturn(t *testing.T) int { return 0 }", false},
		{"func TestGeneric[T any](t *testing.T) {}", false},
		{"func (s suite) TestMethod(t *testing.T) {}", false},
		{"func Fuzz(f *testing.F) {}", true},
		{"func FuzzInput(f *testing.F) {}", true},
		{"func FuzzÉclair(f *check.F) {}", true},
		{"func Fuzz1(*F) {}", true},
		{"func Fuzzing(f *testing.F) {}", false},
		{"func Fuzzéclair(f *testing.F) {}", false},
		{"func FuzzInput(t *testing.T) {}", false},
		{"func FuzzValue(f testing.F) {}", false},
		{"func FuzzPair(f, other *testing.F) {}", false},
		{"func FuzzMissing() {}", false},
		{"func FuzzReturn(f *testing.F) int { return 0 }", false},
		{"func FuzzGeneric[T any](f *testing.F) {}", false},
		{"func (s suite) FuzzMethod(f *testing.F) {}", false},
	} {
		t.Run(tc.declaration, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "test.go", "package example\n"+tc.declaration, 0)
			if err != nil {
				t.Fatal(err)
			}
			if got := isTest(file.Decls[0].(*ast.FuncDecl)); got != tc.want {
				t.Errorf("isTest() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSearch(t *testing.T) {
	tests := []test{
		{Name: "TestRead", ImportPath: "example.com/parser", Package: "syntax", File: "/src/parser/reader_test.go"},
		{Name: "TestRead", ImportPath: "example.com/server", Package: "server", File: "/src/server/server_test.go"},
		{Name: "TestWrite", ImportPath: "example.com/parser", Package: "syntax", File: "/src/parser/writer_test.go"},
	}
	for _, tc := range []struct {
		expression string
		want       []test
	}{
		{"", tests},
		{"Read", tests[:2]},
		{"^TestRead$", tests[:2]},
		{"READ", []test{}},
		{"(?i)READ", tests[:2]},
		{"parser|syntax|reader_test.go", []test{}},
		{"Read|Write", tests},
		{"^TestRead$/child|^TestWrite$/other", tests},
		{"Test(Read|Write)/child", tests},
		{"TestRead[/]child", []test{}},
		{`TestRead\/child`, []test{}},
		{"TestRead/(a/b)", tests[:2]},
		{"TestRead /child", []test{}},
		{"TestRead|", tests},
		{"missing", []test{}},
	} {
		filter, err := testFilter(tc.expression)
		if err != nil {
			t.Fatal(err)
		}
		if got := search(tests, filter); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("search(%q) = %#v, want %#v", tc.expression, got, tc.want)
		}
	}
	for _, expression := range []string{"[", "TestRead/[", "TestRead/(bad", "TestRead/ok|["} {
		if _, err := testFilter(expression); err == nil {
			t.Errorf("testFilter(%q) succeeded; want invalid regexp", expression)
		}
	}
}

func TestRun(t *testing.T) {
	dir := fixture(t, map[string]string{
		"example_test.go": `package example
import "testing"
func TestChosen(t *testing.T) { t.Log("selected test ran") }
func TestChosenSibling(t *testing.T) { t.Fatal("sibling must not run") }
`,
		"external_test.go": "package example_test\nimport \"testing\"\nfunc TestExternal(t *testing.T) {}\n",
		"sub/sub_test.go":  "package sub\nimport \"testing\"\nfunc TestChosen(t *testing.T) { t.Fatal(\"wrong package\") }\n",
		"tagged_test.go":   "//go:build special\n\npackage example\nimport \"testing\"\nfunc TestTagged(t *testing.T) {}\n",
	})
	for _, tc := range []struct {
		name    string
		args    []string
		wantErr string
		wantOut string
	}{
		{"no arguments", nil, "", "TestChosenSibling"},
		{"package only", []string{"./sub"}, "", "TestChosen"},
		{"list", []string{"-list", "Chosen"}, "", "example_test.go:4"},
		{"list single match", []string{"-list", "Sibling"}, "", "TestChosenSibling"},
		{"list exact name", []string{"-list", "./sub", "^TestChosen$"}, "", "TestChosen"},
		{"list custom format", []string{"-list", "--format", "{{.Test}}", "Sibling"}, "", "TestChosenSibling\n"},
		{"multiple matches run", []string{"Chosen"}, "exit status 1", "sibling must not run"},
		{"duplicate names run", []string{"^TestChosen$"}, "exit status 1", "wrong package"},
		{"exact", []string{".", "^TestChosen$"}, "", "ok"},
		{"external", []string{"TestExternal"}, "", "ok"},
		{"scoped search", []string{".", "External"}, "", "ok"},
		{"removed run flag", []string{"-run", "TestChosen"}, "flag provided but not defined", ""},
		{"removed name flag", []string{"-name", "TestChosen"}, "flag provided but not defined", ""},
		{"tags", []string{"-tags", "special", "Tagged"}, "", "ok"},
		{"failure", []string{"Sibling"}, "exit status 1", "sibling must not run"},
		{"missing", []string{"absent"}, "no tests match", ""},
		{"case sensitive", []string{"external"}, "no tests match", ""},
		{"invalid regexp", []string{"["}, "invalid test regexp", ""},
		{"multiple expressions", []string{"Chosen", "Sibling"}, "expected one regexp", ""},
		{"empty JSON", []string{"--format", "json", "absent"}, "", ""},
		{"removed JSON flag", []string{"-json"}, "flag provided but not defined", ""},
		{"removed package flag", []string{"-pkg", "."}, "flag provided but not defined", ""},
		{"invalid package", []string{"./missing"}, "discover packages", ""},
		{"positional package", []string{"--format", "{{.ImportPath}}", "./sub"}, "", "example.com/project/sub"},
		{"package and search", []string{"--format", "{{.Test}}", "./sub", "Chosen"}, "", "TestChosen\n"},
		{"custom format", []string{"--format", "{{.Test}} {{.Package}}", "External"}, "", "TestExternal example_test\n"},
		{"invalid format", []string{"--format", "{{.Unknown}}", "External"}, "render format", ""},
		{"invalid template", []string{"--format", "{{", "External"}, "parse format", ""},
		{"format with command", []string{"--format", "{{.Test}}", "-cmd"}, "cannot be used with", ""},
		{"list with command", []string{"-list", "-cmd"}, "cannot be used with", ""},
		{"help", []string{"-h"}, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args := append([]string{"-C", dir}, tc.args...)
			err := run(context.Background(), args, strings.NewReader(""), &stdout, &stderr)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("run() = %v; stderr: %s", err, &stderr)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("run() = %v, want error containing %q", err, tc.wantErr)
			}
			if !strings.Contains(stdout.String(), tc.wantOut) {
				t.Errorf("stdout = %q, want %q", &stdout, tc.wantOut)
			}
			if tc.name == "exact" {
				want := shellCommand([]string{"go", "-C", dir, "test", "example.com/project", "-run", "^TestChosen$"}) + "\n"
				if stderr.String() != want {
					t.Errorf("run command = %q, want %q", &stderr, want)
				}
			}
			if tc.name == "empty JSON" && (stdout.Len() != 0 || stderr.Len() != 0) {
				t.Errorf("empty JSON results printed output: stdout %q, stderr %q", &stdout, &stderr)
			}
			if tc.name == "failure" {
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 1 {
					t.Errorf("test failure did not preserve exit status: %v", err)
				}
			}
		})
	}

	t.Run("JSON commands specify their working directory", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := run(context.Background(), []string{"-C", dir, "--format", "json", ".", "^TestChosen$"}, nil, &stdout, &stderr)
		if err != nil {
			t.Fatal(err)
		}
		var selected test
		if err := json.Unmarshal(stdout.Bytes(), &selected); err != nil {
			t.Fatal(err)
		}
		if selected.Name != "TestChosen" {
			t.Fatalf("unexpected result: %#v", selected)
		}
		if selected.Dir != dir {
			t.Fatalf("working directory = %q, want %q", selected.Dir, dir)
		}
		if selected.ImportPath != "example.com/project" || selected.Package != "example" {
			t.Fatalf("unexpected import path or package name: %#v", selected)
		}
		cmd := exec.Command(selected.Command[0], selected.Command[1:]...)
		cmd.Dir = selected.Dir
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("generated command failed: %v\n%s", err, output)
		}
	})
}

func TestRegexSelection(t *testing.T) {
	const source = `package example
import (
    "os"
    "strings"
    "testing"
)
func record(t *testing.T) {
    t.Helper()
    f, err := os.OpenFile(os.Getenv("GOPICK_RECORD"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
    if err != nil { t.Fatal(err) }
    defer f.Close()
    if _, err := f.WriteString("PACKAGE " + t.Name() + "\n"); err != nil { t.Fatal(err) }
}
func TestAlpha(t *testing.T) {
    record(t)
    t.Run(strings.Join([]string{"child", "one"}, " "), record)
    t.Run("nested", func(t *testing.T) {
        record(t)
        t.Run("leaf", record)
    })
}
func TestAlphabet(t *testing.T) { record(t) }
func TestBeta(t *testing.T) { record(t) }
`
	dir := fixture(t, map[string]string{
		"example_test.go": strings.ReplaceAll(source, "PACKAGE", "root"),
		"sub/sub_test.go": strings.ReplaceAll(source, "PACKAGE", "sub"),
	})
	path := filepath.Join(t.TempDir(), "executed")
	t.Setenv("GOPICK_RECORD", path)
	read := func(t *testing.T) []string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		// Keep package and test names paired while ignoring package scheduling.
		names := strings.Split(strings.TrimSpace(string(data)), "\n")
		sort.Strings(names)
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		return names
	}
	for _, expression := range []string{
		"Alpha", "^TestAlpha$", "(?i)alpha", "",
		"^TestAlpha$/^child_one$", "TestAlpha/child one",
		"^TestAlpha$/child_one|^TestBeta$",
		"Test(Alpha|Beta)/(child_one|nested/leaf)",
		"TestAlpha/[/]|TestBeta", "TestAlpha[/]child|TestBeta", "/leaf",
	} {
		t.Run(expression, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if err := run(context.Background(), []string{"-C", dir, expression}, nil, &stdout, &stderr); err != nil {
				t.Fatalf("gopick: %v\n%s\n%s", err, &stdout, &stderr)
			}
			got := read(t)
			// Compare actual execution with Go, including dynamic subtest names,
			// parent execution, alternatives, and duplicate names across packages.
			cmd := exec.Command("go", "test", ".", "./sub", "-run", expression, "-count=1")
			cmd.Dir = dir
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("go test: %v\n%s", err, output)
			}
			if want := read(t); !reflect.DeepEqual(got, want) {
				t.Errorf("executed tests = %q, want Go's selection %q", got, want)
			}
		})
	}
}

func TestList(t *testing.T) {
	dir := fixture(t, map[string]string{
		// Listing a single match must not compile or execute the test.
		"example.go":      "package example\nvar broken = undefined\n",
		"example_test.go": "package example\nimport \"testing\"\nfunc TestChosen(t *testing.T) { panic(\"must not run\") }\n",
	})
	for _, args := range [][]string{
		nil,
		{"."},
		{"-list", "Chosen"},
		{"-list", "^TestChosen$"},
		{"--format", "{{.Test}}", "Chosen"},
		{"--format", "", "Chosen"},
		{"-format=", "Chosen"},
		{"--format=json", "--format=", "Chosen"},
		{"-list", "--format", "json", "Chosen"},
	} {
		var stdout, stderr bytes.Buffer
		if err := run(context.Background(), append([]string{"-C", dir}, args...), nil, &stdout, &stderr); err != nil {
			t.Fatalf("run(%q) = %v; stderr: %s", args, err, &stderr)
		}
		if !strings.Contains(stdout.String(), "TestChosen") || stderr.Len() != 0 {
			t.Errorf("run(%q): stdout = %q, stderr = %q; want a listing only", args, &stdout, &stderr)
		}
	}
	for _, args := range [][]string{
		{"--format=", "-cmd", "Chosen"},
		{"--format", "", "-complete", "tests"},
	} {
		var stdout, stderr bytes.Buffer
		err := run(context.Background(), append([]string{"-C", dir}, args...), nil, &stdout, &stderr)
		if err == nil || !strings.Contains(err.Error(), "-format cannot be used") || stdout.Len() != 0 {
			t.Errorf("run(%q) = %v, stdout=%q; want incompatible format error", args, err, &stdout)
		}
	}
}

func TestFileScopes(t *testing.T) {
	dir := fixture(t, map[string]string{
		"scope/broken.go":          "package example\nvar broken = undefined\n",
		"scope/standalone_test.go": "package example\nimport \"testing\"\nfunc TestStandalone(t *testing.T) { t.Log(\"file scope ran\") }\n",
		"scope/helper.go":          "package example\nconst answer = 42\n",
		"scope/pair_test.go":       "package example\nimport \"testing\"\nfunc TestPair(t *testing.T) { if answer != 42 { t.Fatal(answer) } }\n",
		"scope/other_test.go":      "package example\nimport \"testing\"\nfunc TestOther(t *testing.T) {}\n",
	})
	for _, files := range [][]string{
		{"./scope/standalone_test.go"},
		{"./scope/pair_test.go", "./scope/helper.go", "./scope/other_test.go"},
	} {
		tests, err := discover(context.Background(), dir, files, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(tests) == 0 {
			t.Fatal("no tests discovered in named files")
		}
		commands := [][]string{testCommand(tests, "Test", nil)}
		for _, entry := range tests {
			commands = append(commands, entry.Command)
		}
		for _, command := range commands {
			if !reflect.DeepEqual(command[2:len(command)-2], files) {
				t.Fatalf("command %q did not preserve file arguments %q", command, files)
			}
			cmd := exec.Command(command[0], command[1:]...)
			cmd.Dir = dir
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("file command: %v\n%s", err, output)
			}
		}
	}
	for _, mode := range []string{"run", "json", "cmd", "template"} {
		var stdout, stderr bytes.Buffer
		args := []string{"-C", dir}
		switch mode {
		case "json":
			args = append(args, "-format", "json")
		case "cmd":
			args = append(args, "-cmd")
		case "template":
			args = append(args, "-format", "{{.Command}}")
		}
		args = append(args, "./scope/standalone_test.go", "TestStandalone", "-v")
		if err := run(context.Background(), args, nil, &stdout, &stderr); err != nil {
			t.Fatalf("%s: %v\n%s", mode, err, &stderr)
		}
		var cmd *exec.Cmd
		switch mode {
		case "run":
			if !strings.Contains(stdout.String(), "file scope ran") {
				t.Errorf("test did not run: %s", &stdout)
			}
			continue
		case "json":
			var entry test
			if err := json.Unmarshal(stdout.Bytes(), &entry); err != nil {
				t.Fatal(err)
			}
			cmd = exec.Command(entry.Command[0], entry.Command[1:]...)
			cmd.Dir = entry.Dir
		default:
			cmd = exec.Command("sh", shellScript(t, stdout.String()))
			cmd.Dir = t.TempDir()
		}
		if output, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(output), "file scope ran") {
			t.Fatalf("%s generated command: %v\n%s", mode, err, output)
		}
	}
}

func TestLocalPackages(t *testing.T) {
	dir := fixture(t, map[string]string{
		"root_test.go":    "package root\nimport \"testing\"\nfunc TestLocal(t *testing.T) { t.Log(\"root ran\") }\n",
		"sub/sub_test.go": "package sub\nimport \"testing\"\nfunc TestLocal(t *testing.T) { t.Log(\"sub ran\") }\n",
	})
	t.Setenv("GO111MODULE", "off")
	t.Setenv("GOPATH", t.TempDir())
	var stdout, stderr bytes.Buffer
	args := []string{"-C", dir, "--format", "json", "Local"}
	if err := run(context.Background(), args, nil, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&stdout)
	for _, path := range []string{".", "./sub"} {
		var got test
		if err := decoder.Decode(&got); err != nil {
			t.Fatal(err)
		}
		want := []string{"go", "test", path, "-run", "^TestLocal$"}
		if !strings.HasPrefix(got.ImportPath, "_/") || !reflect.DeepEqual(got.Command, want) {
			t.Errorf("local package = %#v, want synthetic identity and command %q", got, want)
		}
		cmd := exec.Command(got.Command[0], got.Command[1:]...)
		cmd.Dir = got.Dir
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("JSON command: %v\n%s", err, output)
		}
	}
	stdout.Reset()
	if err := run(context.Background(), []string{"-C", dir, "-cmd", "Local"}, nil, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "test . ./sub -run Local") {
		t.Errorf("command = %q; want both local package paths", &stdout)
	}
	cmd := exec.Command("sh", shellScript(t, stdout.String()))
	cmd.Dir = t.TempDir()
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("copied command: %v\n%s", err, output)
	}
	stdout.Reset()
	if err := run(context.Background(), []string{"-C", dir, "Local", "-v"}, nil, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	for _, message := range []string{"root ran", "sub ran"} {
		if !strings.Contains(stdout.String(), message) {
			t.Errorf("missing %q in test output: %s", message, &stdout)
		}
	}
}

func TestShellCommand(t *testing.T) {
	got := shellCommand([]string{"cd", "/tmp/my repo's path"})
	want := `cd '/tmp/my repo'"'"'s path'`
	if got != want {
		t.Errorf("shellCommand() = %q, want %q", got, want)
	}
	for _, shell := range []string{"sh", "bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			path, err := exec.LookPath(shell)
			if err != nil {
				t.Skip(shell + " is not installed")
			}
			args := []string{"", "space dir", "quote'd", `one\slash`, `two\\slashes`, "trailing\\", "slash\\'quote", "$HOME", "$(printf injected)", "`printf injected`", "line\nbreak"}
			command := shellCommand(append([]string{"printf", "%s\\000"}, args...))
			output, err := exec.Command(path, shellScript(t, command)).CombinedOutput()
			if err != nil {
				t.Fatalf("shell command: %v\n%s", err, output)
			}
			got := strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00")
			if !reflect.DeepEqual(got, args) {
				t.Errorf("shell arguments = %q, want %q", got, args)
			}
		})
	}
}

func TestCopiedCommands(t *testing.T) {
	dir := fixture(t, map[string]string{
		"marker": "expected working directory",
		"shell_test.go": `package example
import (
    "os"
    "testing"
)
func TestShell(t *testing.T) {
    if data, err := os.ReadFile("marker"); err != nil || string(data) != "expected working directory" {
        t.Fatalf("wrong working directory: %q, %v", data, err)
    }
    t.Log("copied command ran")
}
`,
	})
	name := "project with 'quotes'"
	if runtime.GOOS != "windows" {
		// A literal backslash is a filename character only on Unix.
		name += " and \\\\slashes"
	}
	project := filepath.Join(t.TempDir(), name)
	if err := os.Rename(dir, project); err != nil {
		t.Fatal(err)
	}
	for _, shell := range []string{"sh", "bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			path, err := exec.LookPath(shell)
			if err != nil {
				t.Skip(shell + " is not installed")
			}
			for _, mode := range [][]string{{"-cmd"}, {"-format", "{{.Command}}"}} {
				var stdout, stderr bytes.Buffer
				args := append([]string{"-C", project}, mode...)
				args = append(args, "TestShell", "-v", "-count=1")
				if err := run(context.Background(), args, nil, &stdout, &stderr); err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command(path, shellScript(t, stdout.String()))
				cmd.Dir = t.TempDir()
				if output, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(output), "copied command ran") {
					t.Fatalf("copied command %q: %v\n%s", &stdout, err, output)
				}
			}
		})
	}
}

func TestForwardedArgs(t *testing.T) {
	for _, tc := range []struct {
		args       []string
		positional []string
		forwarded  []string
	}{
		{nil, nil, nil},
		{[]string{"Test"}, []string{"Test"}, nil},
		{[]string{".", "Test", "-v", "-count", "2"}, []string{".", "Test"}, []string{"-v", "-count", "2"}},
		{[]string{"Test", "--", "-args", "hello world", "--"}, []string{"Test"}, []string{"-args", "hello world", "--"}},
		{[]string{"Test", "--"}, []string{"Test"}, []string{}},
		{[]string{"-Test", "-v"}, []string{"-Test"}, []string{"-v"}},
		{[]string{".", "-", "-v"}, []string{".", "-"}, []string{"-v"}},
	} {
		positional, forwarded := splitTestArgs(tc.args)
		if !reflect.DeepEqual(positional, tc.positional) || !reflect.DeepEqual(forwarded, tc.forwarded) {
			t.Errorf("splitTestArgs(%q) = (%q, %q), want (%q, %q)", tc.args, positional, forwarded, tc.positional, tc.forwarded)
		}
	}
}

func TestDiscoveryFlags(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"-v", "-count", "2", "-race", "-tags=special", "--mod", "mod"}, []string{"-race", "-tags=special", "--mod", "mod"}},
		{[]string{"--tags", "", "-race=false", "-compiler=gc", "-modfile", "other.mod"}, []string{"--tags", "", "-race=false", "-compiler=gc", "-modfile", "other.mod"}},
		{[]string{"-ldflags", "-tags=fake", "-test.run", "-race", "-tags=real"}, []string{"-tags=real"}},
		{[]string{"-tags=before", "-args", "-tags=after", "-C", "/ignored"}, []string{"-tags=before"}},
		{[]string{"--args", "-tags=after"}, nil},
		{[]string{"--", "-tags=after"}, nil},
		{[]string{"-custom", "value", "-tags=real"}, []string{"-tags=real"}},
		{[]string{"-custom=true", "positional", "-tags=after"}, nil},
		{[]string{"-v", "positional", "-tags=after"}, nil},
		{[]string{"-futureflag=anything", "-tags=real"}, []string{"-tags=real"}},
		{[]string{"-linkshared=false", "-modcacherw", "-pkgdir", "cache", "-buildmode=default"}, []string{"-linkshared=false", "-modcacherw", "-pkgdir", "cache", "-buildmode=default"}},
		{[]string{"-debug-trace", "-tags=fake", "-test.testlogfile=log", "-tags=real"}, []string{"-tags=real"}},
		{[]string{"-test.testlogfile", "-tags=real", "log"}, []string{"-tags=real"}},
		{[]string{"-test.testlogfile", "-", "-tags=real"}, []string{"-tags=real"}},
		{[]string{"-test.paniconexit0", "false", "-tags=real"}, []string{"-tags=real"}},
		{[]string{"-test.paniconexit0=false", "literal", "-tags=ignored"}, nil},
		{[]string{"-test.testlogfile"}, nil},
	} {
		got, err := discoveryFlags(tc.args)
		if err != nil {
			t.Fatalf("discoveryFlags(%q): %v", tc.args, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("discoveryFlags(%q) = %q, want %q", tc.args, got, tc.want)
		}
	}
	for _, args := range [][]string{{"-tags"}, {"-count"}, {"-C", "elsewhere"}, {"--C=elsewhere"}} {
		if _, err := discoveryFlags(args); err == nil {
			t.Errorf("discoveryFlags(%q) succeeded; want an error", args)
		}
	}
}

func TestForwardedRun(t *testing.T) {
	dir := fixture(t, map[string]string{
		"example_test.go": `package example
import (
    "flag"
    "testing"
)
var message = flag.String("message", "", "custom test flag")
func TestChosen(t *testing.T) {
    t.Run("child", func(t *testing.T) {
        if !testing.Short() { t.Fatal("-short was not forwarded") }
        if *message != "space and 'quotes'" { t.Fatalf("message = %q", *message) }
        t.Log("forwarded flags reached the test")
    })
}
func TestChosenSibling(t *testing.T) { t.Fatal("sibling must not run") }
`,
	})
	for _, separator := range [][]string{nil, {"--"}} {
		args := []string{"-C", dir, ".", "^TestChosen$/child"}
		args = append(args, separator...)
		args = append(args, "-v", "-short", "-count", "2", "-timeout=30s", "-args", "-message", "space and 'quotes'")
		var stdout, stderr bytes.Buffer
		if err := run(context.Background(), args, nil, &stdout, &stderr); err != nil {
			t.Fatalf("run(%q): %v\n%s\n%s", args, err, &stdout, &stderr)
		}
		if got := strings.Count(stdout.String(), "forwarded flags reached the test"); got != 2 {
			t.Errorf("test ran %d times, want 2; output: %s", got, &stdout)
		}
	}
}

func TestForwardedDiscovery(t *testing.T) {
	dir := fixture(t, map[string]string{
		"offline_test.go":    "//go:build !special\n\npackage example\nimport \"testing\"\nfunc TestOffline(t *testing.T) {}\n",
		"special_test.go":    "//go:build special\n\npackage example\nimport \"testing\"\nfunc TestSpecial(t *testing.T) {}\n",
		"alternate file.mod": "module example.com/alternate\n\ngo 1.22\n",
	})
	for _, tc := range []struct {
		args []string
		name string
		path string
	}{
		{[]string{"Test", "-tags=special"}, "TestSpecial", "example.com/project"},
		{[]string{"-tags=special", "Test", "--", "-tags", ""}, "TestOffline", "example.com/project"},
		{[]string{"Test", "-modfile", "alternate file.mod", "-tags", "special"}, "TestSpecial", "example.com/alternate"},
		{[]string{"Test", "-args", "-tags=special"}, "TestOffline", "example.com/project"},
		{[]string{"Test", "--", "--", "-tags=special"}, "TestOffline", "example.com/project"},
	} {
		var stdout, stderr bytes.Buffer
		args := append([]string{"-C", dir, "--format", "json"}, tc.args...)
		if err := run(context.Background(), args, nil, &stdout, &stderr); err != nil {
			t.Fatalf("run(%q): %v\n%s", args, err, &stderr)
		}
		var got test
		if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Name != tc.name || got.ImportPath != tc.path {
			t.Errorf("run(%q): got %s in %s, want %s in %s", args, got.Name, got.ImportPath, tc.name, tc.path)
		}
		// JSON commands must preserve the same arguments as direct execution.
		tail := tc.args
		for tail[0] != "Test" {
			tail = tail[1:]
		}
		tail = tail[1:]
		if tail[0] == "--" {
			tail = tail[1:]
		}
		if !reflect.DeepEqual(got.Command[len(got.Command)-len(tail):], tail) {
			t.Errorf("JSON command = %q, want trailing arguments %q", got.Command, tail)
		}
	}
}

func TestCommand(t *testing.T) {
	dir := fixture(t, map[string]string{
		// Printing the command must not try to compile this package.
		"example.go": "package example\nvar broken = undefined\n",
		"example_test.go": `package example
import "testing"
func TestChosen(t *testing.T) { panic("must not run") }
func TestChosenSibling(t *testing.T) {}
`,
		"tagged_test.go": "//go:build special\n\npackage example\nimport \"testing\"\nfunc TestTagged(t *testing.T) {}\n",
	})
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"standalone", []string{"-cmd", "^TestChosen$"}, []string{"example.com/project", "-run", "^TestChosen$"}},
		{"multiple matches", []string{"-cmd", "Chosen"}, []string{"example.com/project", "-run", "Chosen"}},
		{"subtests", []string{"-cmd", "TestChosen/child"}, []string{"example.com/project", "-run", "TestChosen/child"}},
		{"tags", []string{"-cmd", "-tags", "special", "Tagged"}, []string{"-tags", "special", "example.com/project", "-run", "Tagged"}},
		{"Go flags", []string{"-cmd", ".", "Chosen", "-v", "-count", "2", "-json"}, []string{"example.com/project", "-run", "Chosen", "-v", "-count", "2", "-json"}},
		{"separator", []string{"-cmd", "Chosen", "--", "-list", "."}, []string{"example.com/project", "-run", "Chosen", "-list", "."}},
		{"unknown flags", []string{"-cmd", "Chosen", "--", "-futureflag=value", "-args", "-custom", "a value's spaces", "--", "literal"}, []string{"example.com/project", "-run", "Chosen", "-futureflag=value", "-args", "-custom", "a value's spaces", "--", "literal"}},
		{"run override", []string{"-cmd", "Chosen", "-test.run=Sibling"}, []string{"example.com/project", "-run", "Chosen", "-test.run=Sibling"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if err := run(context.Background(), append([]string{"-C", dir}, tc.args...), nil, &stdout, &stderr); err != nil {
				t.Fatal(err)
			}
			want := shellCommand(append([]string{"go", "-C", dir, "test"}, tc.want...)) + "\n"
			if stdout.String() != want || stderr.Len() != 0 {
				t.Errorf("stdout = %q, stderr = %q; want only %q on stdout", &stdout, &stderr, want)
			}
		})
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-cmd", "absent"}, "no tests match"},
		{[]string{"-cmd", "--format", "json"}, "cannot be used with"},
	} {
		var stdout, stderr bytes.Buffer
		err := run(context.Background(), append([]string{"-C", dir}, tc.args...), nil, &stdout, &stderr)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("run(%q) = %v; want %q", tc.args, err, tc.want)
		}
		if stdout.Len() != 0 {
			t.Errorf("failed command selection printed to stdout: %q", &stdout)
		}
	}
}

func TestWorkingDirectory(t *testing.T) {
	dir := fixture(t, map[string]string{
		"example_test.go": "package example\nimport \"testing\"\nfunc TestChosen(t *testing.T) {}\n",
	})
	quotedDir := filepath.Join(t.TempDir(), "repo's directory")
	if err := os.Rename(dir, quotedDir); err != nil {
		t.Fatal(err)
	}
	dir = quotedDir

	t.Run("copied command from another directory", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		if err := run(context.Background(), []string{"-C", dir, "-cmd", "^TestChosen$"}, nil, &stdout, &stderr); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("sh", shellScript(t, stdout.String()))
		cmd.Dir = t.TempDir()
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("copied command failed: %v\n%s", err, output)
		}
	})

	t.Run("current directory", func(t *testing.T) {
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.Chdir(cwd); err != nil {
				t.Fatal(err)
			}
		})
		for _, args := range [][]string{
			{"-cmd", "^TestChosen$"},
			{"-C", ".", "-cmd", "^TestChosen$"},
		} {
			var stdout, stderr bytes.Buffer
			if err := run(context.Background(), args, nil, &stdout, &stderr); err != nil {
				t.Fatal(err)
			}
			want := "go test example.com/project -run '^TestChosen$'\n"
			if stdout.String() != want {
				t.Errorf("command = %q, want %q", &stdout, want)
			}
		}
	})
}
