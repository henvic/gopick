package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompletion(t *testing.T) {
	dir := fixture(t, map[string]string{
		"example.go":       "package example\nvar broken = undefined\n",
		"example_test.go":  "package example\nimport \"testing\"\nfunc TestZebra(t *testing.T) {}\nfunc TestAlpha(t *testing.T) {}\n",
		"external_test.go": "package example_test\nimport \"testing\"\nfunc TestExternal(t *testing.T) {}\n",
		"sub/sub_test.go":  "package sub\nimport \"testing\"\nfunc TestAlpha(t *testing.T) {}\nfunc TestSubOnly(t *testing.T) {}\n",
		"tagged_test.go":   "//go:build special\n\npackage example\nimport \"testing\"\nfunc TestTagged(t *testing.T) {}\n",
	})
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-complete", "tests"}, "TestAlpha\nTestExternal\nTestSubOnly\nTestZebra\n"},
		{[]string{"-complete", "tests", "./sub"}, "TestAlpha\nTestSubOnly\n"},
		{[]string{"-complete", "tests", "-tags", "special", "Tagged"}, "TestTagged\n"},
		{[]string{"-complete", "tests", "absent"}, ""},
		{[]string{"-complete", "tests", "^TestAlpha$/child"}, "TestAlpha\n"},
		{[]string{"-complete", "packages"}, ".\n./...\n./sub\nexample.com/project\nexample.com/project/sub\n"},
		{[]string{"-complete", "arguments"}, ".\n./...\n./sub\nTestAlpha\nTestExternal\nTestSubOnly\nTestZebra\nexample.com/project\nexample.com/project/sub\n"},
		{[]string{"-complete", "go-tests"}, "TestAlpha\nTestExternal\nTestZebra\n"},
		{[]string{"-complete", "go-tests", "./sub"}, "TestAlpha\nTestSubOnly\n"},
		{[]string{"-complete", "go-tests", ".", "./sub"}, "TestAlpha\nTestExternal\nTestSubOnly\nTestZebra\n"},
		{[]string{"-complete", "go-tests", "example.com/project/sub"}, "TestAlpha\nTestSubOnly\n"},
		{[]string{"-complete", "go-tests", "-tags", "special"}, "TestAlpha\nTestExternal\nTestTagged\nTestZebra\n"},
		{[]string{"-complete", "go-tests", "--", "-mod=mod", "."}, "TestAlpha\nTestExternal\nTestZebra\n"},
		{[]string{"-complete", "go-tests", "example_test.go"}, "TestAlpha\nTestZebra\n"},
	} {
		var stdout, stderr bytes.Buffer
		err := run(context.Background(), append([]string{"-C", dir}, tc.args...), nil, &stdout, &stderr)
		if err != nil {
			t.Fatal(err)
		}
		if stdout.String() != tc.want || stderr.Len() != 0 {
			t.Errorf("run(%q): stdout = %q, stderr = %q; want %q", tc.args, &stdout, &stderr, tc.want)
		}
	}
	for _, args := range [][]string{
		{"-complete", "invalid"},
		{"-complete", "tests", "-list"},
		{"-complete", "tests", "-cmd"},
		{"-complete", "tests", "--format", "table"},
		{"-completion", "unsupported"},
	} {
		var stdout, stderr bytes.Buffer
		if err := run(context.Background(), args, nil, &stdout, &stderr); err == nil {
			t.Errorf("run(%q) succeeded; want an error", args)
		}
	}
}

func TestZshCompletion(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}
	var script, stderr bytes.Buffer
	// Generating the script must work outside a Go project.
	if err := run(context.Background(), []string{"-C", t.TempDir(), "-completion", "zsh"}, nil, &script, &stderr); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(zsh, "-n")
	cmd.Stdin = &script
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("invalid completion script: %v\n%s", err, output)
	}
	// Exercise argument forwarding without requiring an interactive terminal.
	for _, tc := range []struct {
		name   string
		line   string
		prefix string
		want   string
	}{
		{"first argument", "TestAl", "TestAl", "-complete\narguments\n-C\n/tmp/project space\n-tags\nspecial\n--\n"},
		{"package scope", "./sub Alpha", "Alpha", "-complete\ntests\n-C\n/tmp/project space\n-tags\nspecial\n--\n./sub\n\n"},
		{"unfinished regexp", "./sub 'Test['", "Test[", "-complete\ntests\n-C\n/tmp/project space\n-tags\nspecial\n--\n./sub\n\n"},
		{"anchored name", "'^TestAl'", "^TestAl", "-complete\ntests\n-C\n/tmp/project space\n-tags\nspecial\n--\n"},
		{"extra argument", "./sub TestAlpha extra", "extra", ""},
	} {
		cmd = exec.Command(zsh, "-f", "-c", `
compdef() { :; }
`+zshCompletion+`
words=(gopick --C "'/tmp/project space'" --tags special `+tc.line+`)
CURRENT=${#words}
_arguments() {
  state=arguments
  opt_args=(--C "'/tmp/project space'" --tags special)
  line=(`+tc.line+`)
  PREFIX='`+tc.prefix+`'
}

compset() { PREFIX=${PREFIX#\^}; }
gopick() { print -rl -- "$@"; }
compadd() { print -rl -- "${candidates[@]}"; }
_gopick
`)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("completion failed: %v\n%s", err, output)
		}
		if strings.TrimSpace(string(output)) != strings.TrimSpace(tc.want) {
			t.Errorf("%s: completion arguments = %q, want %q", tc.name, output, tc.want)
		}
	}
}

func TestZshPaths(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}
	dir := filepath.Join(t.TempDir(), "path with 'quotes' and \\backslash")
	if err := os.MkdirAll(filepath.Join(dir, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(dir, "child")
	for _, tc := range []struct {
		word  string
		magic bool
		want  string
	}{
		{"~/sub", false, home + "/sub"},
		{"~+", false, current},
		{"~+/sub", false, current + "/sub"},
		{"~-/sub", false, dir + "/sub"},
		{"~+/'path space'", false, current + "/path space"},
		{"'~+/sub'", false, "~+/sub"},
		{`"~+/sub"`, false, "~+/sub"},
		{`\~+/sub`, false, "~+/sub"},
		{"-C=~+/sub", false, "-C=~+/sub"},
		{"-C=~+/sub", true, "-C=" + current + "/sub"},
		{"-C='~+/sub'", true, "-C=~+/sub"},
		{"~+/$(printf unexpected)", false, current + "/$(printf unexpected)"},
	} {
		option := "unsetopt"
		if tc.magic {
			option = "setopt"
		}
		cmd := exec.Command(zsh, "-f", "-c", `
compdef() { :; }
`+zshCompletion+`
cd child
`+option+` magicequalsubst
_gopick_unquote `+shellCommand([]string{tc.word})+`
print -r -- "$REPLY"
`)
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		if err != nil || string(output) != tc.want+"\n" {
			t.Errorf("decode %q (magicequalsubst=%v) = %q, %v; want %q", tc.word, tc.magic, output, err, tc.want)
		}
	}
}

func TestZshCompletionTags(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}
	for _, options := range []string{"", "-tags ''", `-tags "''"`, "--tags ''", `--tags "''"`} {
		cmd := exec.Command(zsh, "-f", "-c", `
compdef() { :; }
`+zshCompletion+`
_arguments() {
  state=arguments
  opt_args=(`+options+`)
  line=(Test)
  PREFIX=Test
}
gopick() { print -rl -- "$@"; }
compadd() { print -rl -- "${candidates[@]}"; }
_gopick
`)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("completion failed: %v\n%s", err, output)
		}
		want := "-complete\narguments\n--\n"
		if options != "" {
			want = "-complete\narguments\n-tags\n\n--\n"
		}
		if string(output) != want {
			t.Errorf("options %q: completion arguments = %q, want %q", options, output, want)
		}
	}
}

func TestZshForwardedCompletion(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}
	for _, tc := range []struct {
		words string
		want  string
	}{
		{"gopick Test -r", "4\ngo\ntest\n./...\n-r\n"},
		{"gopick ./sub Test -- -count ''", "5\ngo\ntest\n./sub\n-count\n\n"},
		{"gopick -C '/tmp/project space' -tags special -cmd Test -timeout ''", "9\ngo\n-C\n/tmp/project space\ntest\n-tags\nspecial\n./...\n-timeout\n\n"},
		{"gopick -- -Test -v", "4\ngo\ntest\n./...\n-v\n"},
		{"gopick Test -args -custom ''", "6\ngo\ntest\n./...\n-args\n-custom\n\n"},
		{"gopick -C=/tmp/project -tags= ./sub Test -run=Test", "6\ngo\n-C=/tmp/project\ntest\n-tags=\n./sub\n-run=Test\n"},
		{"gopick net/http Test -v", "4\ngo\ntest\nnet/http\n-v\n"},
		{"gopick example.com/project -v", "4\ngo\ntest\nexample.com/project\n-v\n"},
		{"gopick 'Test.*/child' -v", "4\ngo\ntest\n./...\n-v\n"},
		{"gopick '.*/child' -v", "4\ngo\ntest\n./...\n-v\n"},
	} {
		cmd := exec.Command(zsh, "-f", "-c", `
compdef() { :; }
`+zshCompletion+`
words=(`+tc.words+`)
CURRENT=${#words}
_normal() { print -r -- "$CURRENT"; print -rl -- "${words[@]}"; }
_arguments() { print 'unexpected gopick completion'; }
_gopick
`)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("completion failed: %v\n%s", err, output)
		}
		if string(output) != tc.want {
			t.Errorf("words %q: completion = %q, want %q", tc.words, output, tc.want)
		}
	}
}

func TestZshDelegatedContext(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}
	wrong := fixture(t, map[string]string{
		"wrong_test.go": "package wrong\nimport \"testing\"\nfunc TestWrong(t *testing.T) {}\n",
	})
	project := fixture(t, map[string]string{
		"root_test.go":       "package root\nimport \"testing\"\nfunc TestRoot(t *testing.T) {}\n",
		"sub/hidden_test.go": "//go:build special\n\npackage sub\nimport \"testing\"\nfunc TestInScope(t *testing.T) {}\n",
	})
	completionExecutable(t)
	cmd := exec.Command(zsh, "-f", "-c", `
compdef() { :; }
`+zshCompletion+`
words=(`+shellCommand([]string{"gopick", "-C", project, "-tags", "special", "./sub", "Test", "-run=Test"})+`)
CURRENT=${#words}
PREFIX=-run=Test
typeset -A compstate
_normal() { _gopick_go; }
compset() { PREFIX=${PREFIX#${~2}}; }
compadd() {
  if [[ $1 == -O ]]; then matched=("${candidates[@]}"); else print -rl -- "${anchored[@]}"; fi
}
_gopick
`)
	cmd.Dir = wrong
	output, err := cmd.CombinedOutput()
	if err != nil || string(output) != "^TestInScope$\n" {
		t.Fatalf("delegated completion = %q, %v; want only the tagged test in the selected package", output, err)
	}
}

func TestCompletionPackagePaths(t *testing.T) {
	dir := fixture(t, map[string]string{
		"root_test.go":    "package root\nimport \"testing\"\nfunc TestModule(t *testing.T) {}\n",
		"sub/sub_test.go": "package sub\nimport \"testing\"\nfunc TestSub(t *testing.T) {}\n",
	})
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module foo\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"packages", "arguments"} {
		var stdout, stderr bytes.Buffer
		if err := run(context.Background(), []string{"-C", dir, "-complete", kind}, nil, &stdout, &stderr); err != nil {
			t.Fatal(err)
		}
		want := ".\n./...\n./sub\nfoo/sub\n"
		if kind == "arguments" {
			want = ".\n./...\n./sub\nTestModule\nTestSub\nfoo/sub\n"
		}
		if stdout.String() != want {
			t.Errorf("%s completion = %q, want %q", kind, &stdout, want)
		}
		if kind == "packages" {
			for _, path := range strings.Fields(stdout.String()) {
				var command bytes.Buffer
				if err := run(context.Background(), []string{"-C", dir, "-cmd", path, "Test"}, nil, &command, &stderr); err != nil {
					t.Errorf("advertised package %q cannot be selected: %v", path, err)
				}
			}
		}
	}
}

func TestZshGoCompletion(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}
	dir := fixture(t, map[string]string{
		"root_test.go":    "package root\nimport \"testing\"\nfunc TestAlpha(t *testing.T) {}\nfunc TestAlphaError(t *testing.T) {}\n",
		"tagged_test.go":  "//go:build special\n\npackage root\nimport \"testing\"\nfunc TestTagged(t *testing.T) {}\n",
		"race_test.go":    "//go:build race\n\npackage root\nimport \"testing\"\nfunc TestRace(t *testing.T) {}\n",
		"one/one_test.go": "package one\nimport \"testing\"\nfunc TestOne(t *testing.T) {}\n",
		"two/two_test.go": "package two\nimport \"testing\"\nfunc TestTwo(t *testing.T) {}\n",
	})
	project := filepath.Join(t.TempDir(), "project space")
	if err := os.Rename(dir, project); err != nil {
		t.Fatal(err)
	}
	dir = project
	completionExecutable(t)
	const root = "^TestAlpha$\n^TestAlphaError$\n"
	for _, tc := range []struct {
		name    string
		words   []string
		current int
		prefix  string
		want    string
	}{
		{"equals", []string{"go", "test", "-run=Test"}, 3, "-run=Test", root + "skip:all\n"},
		{"separate value", []string{"go", "test", "-run", "Test"}, 4, "Test", root + "skip:all\n"},
		{"test prefix", []string{"go", "test", "-test.run=Test"}, 3, "-test.run=Test", root + "skip:all\n"},
		{"packages", []string{"go", "test", "./one", "./two", "-run=Test"}, 5, "-run=Test", "^TestOne$\n^TestTwo$\nskip:all\n"},
		{"packages after cursor", []string{"go", "test", "-run=Test", "./one", "./two"}, 3, "-run=Test", "^TestOne$\n^TestTwo$\nskip:all\n"},
		{"quoted context", []string{"go", "-C", "'" + dir + "'", "test", "-tags", "'special extra'", "-run=Test"}, 7, "-run=Test", root + "^TestTagged$\nskip:all\n"},
		{"inline context", []string{"go", "test", "-C='" + dir + "'", "-tags=special", "-run=Test"}, 5, "-run=Test", root + "^TestTagged$\nskip:all\n"},
		{"current directory tilde", []string{"go", "-C", "~+/one", "test", "-run=Test"}, 5, "-run=Test", "^TestOne$\nskip:all\n"},
		{"quoted tilde is literal", []string{"go", "-C", "'~+/one'", "test", "-run=Test"}, 5, "-run=Test", "skip:all\n"},
		{"empty tags equals", []string{"go", "test", "-tags=", "-run=Test"}, 4, "-run=Test", root + "skip:all\n"},
		{"empty tags separate", []string{"go", "test", "-tags", "''", "-run=Test"}, 5, "-run=Test", root + "skip:all\n"},
		{"empty tags after cursor", []string{"go", "test", "-run=Test", "-tags="}, 3, "-run=Test", root + "skip:all\n"},
		{"last tags value wins", []string{"go", "test", "-tags=special", "-tags=", "-run=Test"}, 5, "-run=Test", root + "skip:all\n"},
		{"other flags", []string{"go", "test", "-timeout", "1m", "-count=2", "-v", "./one", "-run=Test"}, 8, "-run=Test", "^TestOne$\nskip:all\n"},
		{"build context", []string{"go", "test", "-race", "-mod=mod", "-run=Test"}, 5, "-run=Test", root + "^TestRace$\nskip:all\n"},
		{"buildmode and pkgdir", []string{"go", "test", "-buildmode=default", "-pkgdir", dir, "./one", "-run=Test"}, 7, "-run=Test", "^TestOne$\nskip:all\n"},
		{"pgo and toolexec", []string{"go", "test", "-pgo=off", "-toolexec", "tool with spaces", "./one", "-run=Test"}, 7, "-run=Test", "^TestOne$\nskip:all\n"},
		{"flag shaped value", []string{"go", "test", "-ldflags", "-tags=special", "-run=Test"}, 5, "-run=Test", root + "skip:all\n"},
		{"diagnostics", []string{"go", "test", "-debug-trace", "trace.out", "./one", "-run=Test"}, 6, "-run=Test", "^TestOne$\nskip:all\n"},
		{"test binary flag", []string{"go", "test", "-test.testlogfile", "log.out", "-run=Test"}, 5, "-run=Test", root + "skip:all\n"},
		{"build flag after test binary flag", []string{"go", "test", "-test.testlogfile", "-tags=special", "-run=Test", "log.out"}, 5, "-run=Test", root + "^TestTagged$\nskip:all\n"},
		{"double dash flags", []string{"go", "test", "--test.timeout=1m", "--test.short", "--run=Test"}, 5, "--run=Test", root + "skip:all\n"},
		{"unfinished regexp", []string{"go", "test", "-run=Test["}, 3, "-run=Test[", "skip:all\n"},
		{"handwritten regexp", []string{"go", "test", "-run=TestAlpha|TestBeta"}, 3, "-run=TestAlpha|TestBeta", "skip:all\n"},
		{"test arguments after cursor", []string{"go", "test", "-run=Test", "-args", "value"}, 3, "-run=Test", root + "skip:all\n"},
		{"closed package list", []string{"go", "test", "./one", "-run=Test", "./two"}, 4, "-run=Test", "^TestOne$\nskip:all\n"},
		{"another command", []string{"go", "build", "-run=Test"}, 3, "-run=Test", "skip:\n"},
		{"flag completion", []string{"go", "test", "-r"}, 3, "-r", "skip:\n"},
		{"package completion", []string{"go", "test", "./s"}, 3, "./s", "skip:\n"},
		{"value of another flag", []string{"go", "test", "-tags", "-run=Test"}, 4, "-run=Test", "skip:\n"},
		{"custom flag", []string{"go", "test", "-custom", "value", "-run=Test"}, 5, "-run=Test", "skip:\n"},
		{"test arguments", []string{"go", "test", "-args", "-run=Test"}, 4, "-run=Test", "skip:\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(zsh, "-f", "-c", `
compdef() { :; }
`+zshCompletion+`
words=(`+shellCommand(tc.words)+`)
CURRENT=`+fmt.Sprint(tc.current)+`
PREFIX=`+shellCommand([]string{tc.prefix})+`
typeset -A compstate
compset() { PREFIX=${PREFIX#${~2}}; }
compadd() {
  if [[ $1 == -O ]]; then matched=("${candidates[@]}"); else print -rl -- "${anchored[@]}"; fi
}
_gopick_go
print -r -- "skip:$_compskip"
`)
			cmd.Dir = dir
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("completion failed: %v\n%s", err, output)
			}
			if string(output) != tc.want {
				t.Errorf("completion = %q, want %q", output, tc.want)
			}
		})
	}

	t.Run("quoted caret retains regexp meaning", func(t *testing.T) {
		for _, prefix := range []string{"-run=^TestAl", `-run=\^TestAl`} {
			cmd := exec.Command(zsh, "-f", "-c", `
compdef() { :; }
`+zshCompletion+`
words=(go test "-run='^TestAl")
CURRENT=3
PREFIX='`+prefix+`'
typeset -A compstate
compset() {
  local before=$PREFIX
  PREFIX=${PREFIX#${~2}}
  local removed=$((${#before} - ${#PREFIX}))
  IPREFIX+=${before[1,removed]}
}
gopick() { print exact; print -r -- "-run=^TestAlpha$"; }
compadd() {
  if [[ $1 == -O ]]; then matched=("${candidates[@]}"); else print -r -- "$IPREFIX${anchored[1]}"; fi
}
_gopick_go
`)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("completion failed: %v\n%s", err, output)
			}
			if want := "-run=^TestAlpha$\n"; string(output) != want {
				t.Errorf("prefix %q completed to %q, want %q", prefix, output, want)
			}
		}
	})

	t.Run("ambiguous names wait for selection", func(t *testing.T) {
		for _, tc := range []struct {
			mode  string
			names string
			want  string
		}{
			{"unambiguous", "TestAlpha", "insert:unambiguous list:\n^TestAlpha$\n"},
			{"unambiguous", "TestAlpha TestAlphaError", "insert: list:list\n^TestAlpha$\n^TestAlphaError$\n"},
			{"menu", "TestAlpha TestAlphaError", "insert:menu list:\n^TestAlpha$\n^TestAlphaError$\n"},
		} {
			cmd := exec.Command(zsh, "-f", "-c", `
compdef() { :; }
`+zshCompletion+`
words=(go test -run=TestAl)
CURRENT=3
PREFIX=-run=TestAl
typeset -A compstate
compstate[insert]=`+tc.mode+`
typeset -a selected
compset() { PREFIX=${PREFIX#${~2}}; }
gopick() { print exact; for name in `+tc.names+`; do print -r -- "-run=^${name}$"; done; }
compadd() {
  if [[ $1 == -O ]]; then
    matched=("${candidates[@]}")
  else
    compstate[nmatches]=${#anchored}
    selected=("${anchored[@]}")
  fi
}
_gopick_go
print -r -- "insert:$compstate[insert] list:$compstate[list]"
print -rl -- "${selected[@]}"
`)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("completion failed: %v\n%s", err, output)
			}
			if string(output) != tc.want {
				t.Errorf("completion state = %q, want %q", output, tc.want)
			}
		}
	})

	t.Run("preserve existing completion on reload", func(t *testing.T) {
		cmd := exec.Command(zsh, "-f", "-c", `
autoload -Uz compinit
compinit -D -i
_existing_go() { print existing; }
compdef _existing_go go
`+zshCompletion+zshCompletion+`
print -r -- "${_comps[go]} ${_patcomps[go]}"
words=(go build '')
CURRENT=3
PREFIX=''
_dispatch command go
`)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("registration failed: %v\n%s", err, output)
		}
		if want := "_existing_go _gopick_go\nexisting\n"; string(output) != want {
			t.Errorf("registration = %q, want %q", output, want)
		}
	})
}
