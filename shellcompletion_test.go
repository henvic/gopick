package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCompletionScripts(t *testing.T) {
	for _, shell := range []string{"zsh", "bash", "fish"} {
		for _, target := range []string{"", "go", "gopick", "both"} {
			t.Run(shell+"/"+target, func(t *testing.T) {
				var stdout, stderr bytes.Buffer
				args := []string{"-C", t.TempDir(), "-completion", shell}
				want := "both"
				if target != "" {
					args = append(args, "-completion-target", target)
					want = target
				}
				if err := run(context.Background(), args, nil, &stdout, &stderr); err != nil {
					t.Fatal(err)
				}
				if stderr.Len() != 0 || !strings.Contains(stdout.String(), want+"\n") {
					t.Fatalf("unexpected script: stdout=%s stderr=%s", &stdout, &stderr)
				}
				path, err := exec.LookPath(shell)
				if err != nil {
					t.Skip(shell + " is not installed")
				}
				cmd := exec.Command(path, "-n")
				cmd.Stdin = &stdout
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("invalid script: %v\n%s", err, output)
				}
			})
		}
	}
	for _, args := range [][]string{
		{"-completion", "bash", "-completion-target", "invalid"},
		{"-completion", "invalid"},
		{"-completion-target", "go"},
		{"-completion-target", "both"},
	} {
		var stdout, stderr bytes.Buffer
		if err := run(context.Background(), args, nil, &stdout, &stderr); err == nil || stdout.Len() != 0 {
			t.Errorf("run(%q) = %v, stdout=%q; want an error without output", args, err, &stdout)
		}
	}
}

func TestZshCompletionInitialization(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip(err)
	}
	home := completionHome(t)
	var script bytes.Buffer
	if err := printCompletionScript(&script, "zsh", "both"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "completion.zsh")
	if err := os.WriteFile(path, script.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	// Simulate initialization failing or being declined, without changing the
	// system completion directories or disabling compinit's normal checks.
	if err := os.WriteFile(filepath.Join(home, "compinit"), []byte("return 42\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, setup, check string }{
		{"fresh", "", `
source "$1" || exit 1
(( $+functions[compdef] )) || exit 2
[[ ${_comps[gopick]} == _gopick && ${_patcomps[go]} == _gopick_go ]] || exit 3
compinit() { print -u2 'unexpected reinitialization'; return 1; }
source "$1" || exit 4
[[ ${_comps[gopick]} == _gopick && ${_patcomps[go]} == _gopick_go ]]
`},
		{"initialized", `
autoload -Uz compinit && compinit || exit 1
compdef _existing_go go
compinit() { print -u2 'unexpected reinitialization'; return 1; }
`, `
source "$1" || exit 2
[[ ${_comps[go]} == _existing_go && ${_comps[gopick]} == _gopick && ${_patcomps[go]} == _gopick_go ]]
`},
		{"failed", `fpath=("$HOME")`, `
source "$1"
[[ $? == 42 ]] || exit 1
(( ! $+functions[compdef] && ! $+functions[_gopick] ))
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, zsh, "-df", shellScript(t, tc.setup+"\n"+tc.check), path)
			if output, err := cmd.CombinedOutput(); err != nil || len(output) != 0 {
				t.Fatalf("Zsh initialization: %v\n%s", err, output)
			}
		})
	}
}

func TestShellCompletion(t *testing.T) {
	dir := fixture(t, map[string]string{
		"example.go":      "package example\nvar broken = undefined\n",
		"example_test.go": "package example\nimport \"testing\"\nfunc TestAlpha(t *testing.T) { panic(\"must not run\") }\nfunc TestZebra(t *testing.T) {}\n",
		"tagged_test.go":  "//go:build special\n\npackage example\nimport \"testing\"\nfunc TestTagged(t *testing.T) {}\n",
		"sub/sub_test.go": "package sub\nimport \"testing\"\nfunc TestSub(t *testing.T) {}\n",
	})
	for _, tc := range []struct {
		words  []string
		cursor int
		want   string
	}{
		{[]string{"go", "-C", dir, "test", "-run="}, 4, "exact\n-run=^TestAlpha$\n-run=^TestZebra$\n"},
		{[]string{"go", "-C", dir, "test", "-run", "^TestAl"}, 5, "exact\n^TestAlpha$\n"},
		{[]string{"go", "-C", dir, "test", "-test.run=TestS", "./sub"}, 4, "exact\n-test.run=^TestSub$\n"},
		{[]string{"go", "-C", dir, "test", "-run=Test", "-tags", "special"}, 4, "exact\n-run=^TestAlpha$\n-run=^TestTagged$\n-run=^TestZebra$\n"},
		{[]string{"go", "-C", dir, "test", "-tags=special", "-tags=", "-run=TestT"}, 6, "exact\n"},
		{[]string{"go", "test", "-C=" + dir, "-count", "2", "-run", "TestA"}, 6, "exact\n^TestAlpha$\n"},
		{[]string{"go", "-C", dir, "test", "./...", "-run=TestS", "-args", "custom"}, 5, "exact\n-run=^TestSub$\n"},
		{[]string{"go", "test", "-run=Test["}, 2, "none\n"},
		{[]string{"go", "test", "-run=Test/child"}, 2, "none\n"},
		{[]string{"go", "test", "-args", "-run=Test"}, 3, "fallback\n"},
		{[]string{"go", "test", "-unknown", "-run=Test"}, 3, "fallback\n"},
		{[]string{"go", "test", "-tags", "-run=Test"}, 3, "fallback\n"},
		{[]string{"go", "build", "-run=Test"}, 2, "fallback\n"},
		{[]string{"go", "test", "-r"}, 2, "fallback\n"},
		{[]string{"gopick", "-C", dir, "TestA"}, 3, "arguments\n^TestAlpha$\n"},
		{[]string{"gopick", "-C", dir, "./sub", "Test"}, 4, "tests\n^TestSub$\n"},
		{[]string{"gopick", "-C", dir, "-tags", "special", "^TestT"}, 5, "tests\n^TestTagged$\n"},
		{[]string{"gopick", "-C", dir, "TestAlpha|TestZebra"}, 3, "arguments\n"},
		{[]string{"gopick", "-C", dir, "^TestAlpha$/child"}, 3, "tests\n"},
		{[]string{"gopick", "-C", dir, "TestAlpha", "extra"}, 4, "none\n"},
		{[]string{"gopick", "-C", dir, "./sub", "TestAlpha", "extra"}, 5, "none\n"},
		{[]string{"gopick", "-completion", "b"}, 2, "arguments\nbash\n"},
		{[]string{"gopick", "-completion-target", "go"}, 2, "arguments\ngo\ngopick\n"},
		{[]string{"gopick", "--completion-target=b"}, 1, "arguments\n--completion-target=both\n"},
		{[]string{"gopick", "--comp"}, 1, "arguments\n--completion\n--completion-target\n--completion-file\n"},
		{[]string{"gopick", "-install-completion", "f"}, 2, "arguments\nfish\n"},
		{[]string{"gopick", "--install-completion=z"}, 1, "arguments\n--install-completion=zsh\n"},
		{[]string{"gopick", "-install-completion", "zsh", ""}, 3, "none\n"},
		{[]string{"gopick", "-completion-file", ""}, 2, "files\n"},
		{[]string{"gopick", "--completion-file="}, 1, "files\n"},
		{[]string{"gopick", "-install-completion", "zsh", "-"}, 3, "arguments\n-completion-target\n-completion-file\n-yes\n-help\n"},
		{[]string{"gopick", "--install-completion=zsh", "--"}, 2, "arguments\n--completion-target\n--completion-file\n--yes\n--help\n"},
		{[]string{"gopick", "--yes", "--"}, 2, "arguments\n--completion-target\n--completion-file\n--install-completion\n--yes\n--help\n"},
		{[]string{"gopick", "--yes=false", ""}, 2, "none\n"},
		{[]string{"gopick", "--completion-file", "config", "--"}, 3, "arguments\n--completion-target\n--completion-file\n--install-completion\n--yes\n--help\n"},
		{[]string{"gopick", "--completion-file=config", ""}, 2, "none\n"},
		{[]string{"gopick", "--yes", "--install-completion", "z"}, 3, "arguments\nzsh\n"},
		{[]string{"gopick", "--list", "--"}, 2, "arguments\n--C\n--tags\n--list\n--cmd\n--format\n--format-fields\n--completion\n--completion-target\n--help\n"},
		{[]string{"gopick", "-C", dir, "--inst"}, 3, "arguments\n"},
		{[]string{"gopick", "-tags=special", "--yes"}, 2, "arguments\n"},
		{[]string{"gopick", "-completion", "bash", "--completion-fi"}, 3, "arguments\n"},
		{[]string{"gopick", "--completion-target=go", "--inst"}, 2, "arguments\n--install-completion\n"},
		{[]string{"gopick", "--yes", "--list=false"}, 2, "none\n"},
		{[]string{"gopick", "--list", "--completion-file="}, 2, "none\n"},
		{[]string{"gopick", "-install-completion", "zsh", "extra", "-r"}, 4, "none\n"},
		{[]string{"gopick", "-yes", "extra", "-r"}, 3, "none\n"},
		{[]string{"gopick", "-completion-file=config", "extra", "-r"}, 3, "none\n"},
		{[]string{"gopick", "-yes="}, 1, "arguments\n-yes=true\n-yes=false\n"},
		{[]string{"gopick", "-list=f"}, 1, "arguments\n-list=false\n"},
		{[]string{"gopick", "--cmd=t"}, 1, "arguments\n--cmd=true\n"},
		{[]string{"gopick", "-format-fields=invalid"}, 1, "arguments\n"},
		{[]string{"gopick", "-format-"}, 1, "arguments\n-format-fields\n"},
		{[]string{"gopick", "--format-"}, 1, "arguments\n--format-fields\n"},
		{[]string{"gopick", "Test", "-r"}, 2, "fallback\n"},
		{[]string{"gopick", "Test", "--", "-r"}, 3, "fallback\n"},
		{[]string{"gopick", "-C", dir + "/s"}, 2, "directories\n" + dir + "/sub/\n"},
	} {
		var output bytes.Buffer
		if err := completeShell(context.Background(), &output, tc.words, tc.cursor); err != nil {
			t.Fatalf("completeShell(%q, %d): %v", tc.words, tc.cursor, err)
		}
		if output.String() != tc.want {
			t.Errorf("completeShell(%q, %d) = %q, want %q", tc.words, tc.cursor, &output, tc.want)
		}
	}
	for _, cursor := range []int{-1, 0, 2} {
		if err := completeShell(context.Background(), &bytes.Buffer{}, []string{"go", "test"}, cursor); err == nil {
			t.Errorf("cursor %d: want an error", cursor)
		}
	}
}

func TestCompletedTest(t *testing.T) {
	dir := fixture(t, map[string]string{
		"example_test.go": `package example
import "testing"
func TestAlpha(t *testing.T) {
    t.Run("child", func(t *testing.T) { t.Log("selected child ran") })
}
func TestAlphaError(t *testing.T) { t.Fatal("sibling ran") }
`,
	})
	var candidates bytes.Buffer
	words := []string{"gopick", "-C", dir, "TestAl"}
	if err := completeShell(context.Background(), &candidates, words, len(words)-1); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(candidates.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected two completion candidates, got %q", &candidates)
	}
	for _, tc := range []struct {
		name       string
		expression string
		wantError  bool
	}{
		{"completed name", lines[1], false},
		{"typed regexp", "TestAlpha", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := run(context.Background(), []string{"-C", dir, tc.expression, "-v", "-count=1"}, nil, &stdout, &stderr)
			if (err != nil) != tc.wantError {
				t.Fatalf("run(%q) = %v; stdout=%s stderr=%s", tc.expression, err, &stdout, &stderr)
			}
			if !strings.Contains(stdout.String(), "selected child ran") || strings.Contains(stdout.String(), "sibling ran") != tc.wantError {
				t.Fatalf("run(%q) selected the wrong tests: %s", tc.expression, &stdout)
			}
		})
	}
}

// Exercise real shell adapters against the actual CLI without building another
// executable. The subprocess exits before the testing package prints anything.
func TestCompletionHelper(t *testing.T) {
	if os.Getenv("GOPICK_COMPLETION_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			if err := run(context.Background(), os.Args[i+1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
	os.Exit(1)
}

func completionExecutable(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nexec " + shellCommand([]string{filepath.ToSlash(executable), "-test.run=^TestCompletionHelper$", "--"}) + " \"$@\"\n"
	path := filepath.Join(dir, "gopick")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOPICK_COMPLETION_HELPER", "1")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return path
}

func terminalPython(t *testing.T) string {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is needed for the interactive PTY test")
	}
	if output, err := exec.Command(python, "-c", "import pty, termios").CombinedOutput(); err != nil {
		t.Skipf("Python needs Unix PTY support: %v\n%s", err, output)
	}
	return python
}

func TestCompletionOrder(t *testing.T) {
	dir := fixture(t, map[string]string{
		"root_test.go":    "package example\nimport \"testing\"\nfunc TestAlpha(t *testing.T) {}\nfunc TestAlphaError(t *testing.T) {}\nfunc FuzzSample(f *testing.F) {}\nfunc Example() {\n// Output:\n}\n",
		"sub/sub_test.go": "package sub\nimport \"testing\"\nfunc TestSub(t *testing.T) {}\n",
	})
	want := []string{"./...", "example.com/project", "example.com/project/sub", "^Example$", "^FuzzSample$", "^TestAlpha$", "^TestAlphaError$", "^TestSub$"}
	var output bytes.Buffer
	if err := completeShell(context.Background(), &output, []string{"gopick", "-C", dir, ""}, 3); err != nil {
		t.Fatal(err)
	}
	if expected := "arguments\n" + strings.Join(want, "\n") + "\n"; output.String() != expected {
		t.Fatalf("backend completion = %q, want %q", &output, expected)
	}
	completionExecutable(t)
	t.Run("fish", func(t *testing.T) {
		fish, err := exec.LookPath("fish")
		if err != nil {
			t.Skip("fish is not installed")
		}
		for _, legacy := range []string{"", "complete -c gopick -n __gopick_command_context -f -a '(__gopick_candidates)'\n"} {
			cmd := exec.Command(fish, "--no-config", shellScript(t, legacy+fishCompletion+fishCompletion+"\ncomplete -C 'gopick '\n"))
			cmd.Dir = dir
			output, err := cmd.CombinedOutput()
			if expected := strings.Join(want, "\n") + "\n"; err != nil || string(output) != expected {
				t.Fatalf("completion (legacy=%v) = %q, %v; want %q", legacy != "", output, err, expected)
			}
		}
	})
	seen := make(map[string]bool)
	for _, name := range []string{"zsh", "bash", "/bin/bash"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		path, err = filepath.EvalSymlinks(path)
		if err != nil || seen[path] {
			continue
		}
		seen[path] = true
		t.Run(name, func(t *testing.T) {
			python := terminalPython(t)
			rc := filepath.Join(t.TempDir(), ".zshrc")
			script := bashCompletion + "\nbind '\"\\t\": menu-complete'\n"
			if name == "zsh" {
				script = "autoload -Uz compinit\ncompinit -D -i\n" + zshCompletion + "\nbindkey '^I' menu-complete\n"
			}
			script += `
gopick() {
  if [[ $1 == -complete ]]; then
    command gopick "$@"
  else
    printf '%s\0' "$@" > "$GOPICK_CAPTURE"
  fi
}
PS1='GOPICK_READY> '
`
			if err := os.WriteFile(rc, []byte(script), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(python, append([]string{"-c", `
import os, pathlib, pty, select, shlex, signal, subprocess, sys, time
shell, root, rc = sys.argv[1:4]
want = sys.argv[4:]
capture = pathlib.Path(rc).parent / 'captured'
env = dict(os.environ, INPUTRC='/dev/null', TERM='xterm-256color', LC_ALL='C',
           GOPICK_CAPTURE=str(capture), ZDOTDIR=str(pathlib.Path(rc).parent))
if pathlib.Path(shell).name == 'zsh':
    args = [shell, '-d', '-i']
else:
    args = [shell, '--noprofile', '--rcfile', rc, '-i']
    probe = subprocess.run([shell, '-c', 'complete -o nosort -W x _probe'], capture_output=True)
    if probe.returncode:
        want.sort()  # Older Bash has no option to preserve completion order.
child, master = pty.fork()
if child == 0:
    os.chdir(root)
    os.execve(shell, args, env)
def until(done):
    output = b''
    end = time.monotonic() + 10
    while time.monotonic() < end:
        if select.select([master], [], [], .05)[0]:
            output += os.read(master, 65536)
        if done(output):
            return
    raise AssertionError('interactive completion timed out: ' + repr(output))
try:
    until(lambda output: b'GOPICK_READY>' in output)
    for i, expected in enumerate(want, 1):
        capture.unlink(missing_ok=True)
        os.write(master, ('gopick ' + '\t' * i + '\r').encode())
        until(lambda output: capture.exists() and b'GOPICK_READY>' in output)
        actual = capture.read_bytes().decode().split('\0')[:-1]
        if actual != [expected]:
            raise AssertionError(f'candidate {i}: got {actual!r}, want {expected!r}')
    for line, expected in [
        ('gopick TestAl', ['^TestAlpha$']),
        ('gopick ^TestAl', ['^TestAlpha$']),
        ("gopick '^TestAl'", ['^TestAlpha$']),
        ('gopick ./sub TestS', ['./sub', '^TestSub$']),
        ('gopick -list TestAl', ['-list', '^TestAlpha$']),
        ("gopick 'TestAlpha|TestSub'", ['TestAlpha|TestSub']),
        ("gopick '^TestAlpha$/child'", ['^TestAlpha$/child']),
    ]:
        capture.unlink(missing_ok=True)
        os.write(master, (line + '\t\r').encode())
        until(lambda output: capture.exists() and b'GOPICK_READY>' in output)
        actual = capture.read_bytes().decode().split('\0')[:-1]
        if actual != expected:
            raise AssertionError(f'{line}: got {actual!r}, want {expected!r}')
    if pathlib.Path(shell).name == 'zsh':
        # An ambiguous prefix must stay editable until a candidate is selected.
        os.write(master, b"bindkey '^I' expand-or-complete\r")
        until(lambda output: b'GOPICK_READY>' in output)
        capture.unlink(missing_ok=True)
        os.write(master, b'gopick TestAl\t\r')
        until(lambda output: capture.exists() and b'GOPICK_READY>' in output)
        actual = capture.read_bytes().decode().split('\0')[:-1]
        if actual != ['TestAl']:
            raise AssertionError(f'ambiguous prefix was changed: {actual!r}')
        # Exercise real _arguments exclusions, not a stub of its parser.
        for line, expected in [
            ('gopick -install-completion zsh --li', '-install-completion zsh --li'),
            ('gopick --install-completion=zsh --tag', '--install-completion=zsh --tag'),
            ('gopick --completion-file config --cm', '--completion-file config --cm'),
            ('gopick --yes --format-f', '--yes --format-f'),
            ('gopick --list --inst', '--list --inst'),
            ('gopick --list --ye', '--list --ye'),
            ('gopick --completion zsh --completion-fi', '--completion zsh --completion-fi'),
            ('gopick -install-completion zsh --ye', '-install-completion zsh --yes'),
            ('gopick --yes --install-completion zs', '--yes --install-completion zsh'),
        ]:
            capture.unlink(missing_ok=True)
            os.write(master, (line + '\t\r').encode())
            until(lambda output: capture.exists() and b'GOPICK_READY>' in output)
            actual = capture.read_bytes().decode().split('\0')[:-1]
            if actual != shlex.split(expected):
                raise AssertionError(f'{line}: got {actual!r}, want {expected!r}')
finally:
    os.kill(child, signal.SIGKILL)
    # Closing the PTY releases a shell that may still be flushing terminal I/O.
    os.close(master)
    os.waitpid(child, 0)
`, path, dir, rc}, want...)...)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("interactive completion order: %v\n%s", err, output)
			}
		})
	}
}

func TestBashCompletion(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	dir := fixture(t, map[string]string{
		"example_test.go": "package example\nimport \"testing\"\nfunc TestAlpha(t *testing.T) { panic(\"must not run\") }\nfunc TestAlphaError(t *testing.T) {}\n",
		"sub/sub_test.go": "package sub\nimport \"testing\"\nfunc TestSub(t *testing.T) {}\n",
	})
	dir = filepath.ToSlash(dir)
	completionExecutable(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	home = filepath.ToSlash(home)
	relative, err := filepath.Rel(home, dir)
	if err != nil {
		t.Fatal(err)
	}
	homePath := "~/" + filepath.ToSlash(relative)
	expanded := home + "/" + filepath.ToSlash(relative)
	for _, tc := range []struct {
		words    string
		line     string
		function string
		want     string
	}{
		{"go test -run = TestAl", "go test -run=TestAl", "_gopick_go", "^TestAlpha$\n^TestAlphaError$\n"},
		{"go test -run =", "go test -run=", "_gopick_go", "^TestAlpha$\n^TestAlphaError$\n"},
		{"go test -run TestAl", "go test -run TestAl", "_gopick_go", "^TestAlpha$\n^TestAlphaError$\n"},
		{"go test '-run=TestAl'", "go test -run=TestAl", "_gopick_go", "^TestAlpha$\n^TestAlphaError$\n"},
		{`go test "-run='^TestAl"`, "go test -run='^TestAl", "_gopick_go", "^TestAlpha$\n^TestAlphaError$\n"},
		{`go test "'-run=^TestAl"`, "go test '-run=^TestAl", "_gopick_go", "-run=^TestAlpha$\n-run=^TestAlphaError$\n"},
		{"go -C \"'" + dir + "'\" test -run = TestAl", "go -C '" + dir + "' test -run=TestAl", "_gopick_go", "^TestAlpha$\n^TestAlphaError$\n"},
		{"go test ./sub -test.run = Test", "go test ./sub -test.run=Test", "_gopick_go", "^TestSub$\n"},
		{"go test -run 'Test['", "go test -run Test[", "_gopick_go", ""},
		{"go test -run Missing", "go test -run Missing", "_gopick_go", ""},
		{"go build ''", "go build ", "_gopick_go", "existing\n"},
		{"go test -args -run=Test", "go test -args -run=Test", "_gopick_go", "existing\n"},
		{"gopick TestAl", "gopick TestAl", "_gopick", "^TestAlpha$\n^TestAlphaError$\n"},
		{"gopick '^TestAl'", "gopick ^TestAl", "_gopick", "^TestAlpha$\n^TestAlphaError$\n"},
		{`gopick "'^TestAl"`, "gopick '^TestAl", "_gopick", "^TestAlpha$\n^TestAlphaError$\n"},
		{"gopick ./sub '^TestS'", "gopick ./sub ^TestS", "_gopick", "^TestSub$\n"},
		{"gopick -completion-target = go", "gopick -completion-target=go", "_gopick", "go\ngopick\n"},
		{"gopick --yes --", "gopick --yes --", "_gopick", "--completion-target\n--completion-file\n--install-completion\n--yes\n--help\n"},
		{"gopick --completion-file config --li", "gopick --completion-file config --li", "_gopick", ""},
		{"gopick --yes ''", "gopick --yes ", "_gopick", ""},
		{"gopick --list --inst", "gopick --list --inst", "_gopick", ""},
		{"go -C " + shellCommand([]string{homePath}) + " test -run=TestAl", "go -C " + homePath + " test -run=TestAl", "_gopick_go", "^TestAlpha$\n^TestAlphaError$\n"},
		{"go -C " + shellCommand([]string{shellCommand([]string{homePath})}) + " test -run=TestAl", "go -C " + shellCommand([]string{homePath}) + " test -run=TestAl", "_gopick_go", ""},
		{`go -C '~+' test -run=TestAl`, "go -C ~+ test -run=TestAl", "_gopick_go", "^TestAlpha$\n^TestAlphaError$\n"},
		{"gopick -C " + shellCommand([]string{homePath + "/s"}), "gopick -C " + homePath + "/s", "_gopick", expanded + "/sub\n"},
		{`gopick -C '~+/s'`, "gopick -C ~+/s", "_gopick", dir + "/sub\n"},
		{"go test -run ''", "go test -run ", "_gopick_go", "^TestAlpha$\n^TestAlphaError$\n"},
		{"go test -run = ''", "go test -run= ", "_gopick_go", "existing\n"},
		{"go test -run = TestAl", "go test -run = TestAl", "_gopick_go", "existing\n"},
		{"go -C . : test -run = TestAl", "go -C . : test -run=TestAl", "_gopick_go", "existing\n"},
	} {
		cmd := exec.Command(bash, "--noprofile", "--norc", shellScript(t, `
_existing_go() { COMPREPLY=(existing); }
complete -o nospace -F _existing_go go
`+bashCompletion+bashCompletion+`
COMP_WORDBREAKS=' =:'
COMP_LINE=`+shellCommand([]string{tc.line})+`
COMP_WORDS=(`+tc.words+`)
COMP_CWORD=$((${#COMP_WORDS[@]}-1))
`+tc.function+` "${COMP_WORDS[0]}" "${COMP_WORDS[COMP_CWORD]}" "${COMP_WORDS[COMP_CWORD-1]}"
for result in "${COMPREPLY[@]}"; do printf '%s\n' "$result"; done
`))
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		if err != nil || string(output) != tc.want {
			t.Errorf("words %s: output=%q error=%v, want %q", tc.words, output, err, tc.want)
		}
	}
}

func TestBashPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires Unix filenames and terminal behavior")
	}
	python := terminalPython(t)
	completionExecutable(t)
	dir := fixture(t, map[string]string{
		"project:colon/go.mod":         "module example.com/colon\n\ngo 1.26\n",
		"project:colon/main_test.go":   "package example\nimport \"testing\"\nfunc TestColon(t *testing.T) {}\n",
		"project::double/go.mod":       "module example.com/double\n\ngo 1.26\n",
		"project::double/main_test.go": "package example\nimport \"testing\"\nfunc TestDouble(t *testing.T) {}\n",
		"home space/configrc":          "",
		"home space/settings file":     "",
		"~/literalrc":                  "",
	})
	for _, name := range []string{"space dir", "apostrophe's dir", "back\\slash", "dollar$(printf injected)", "colon:space dir"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"configrc", "settings file", "quote'config", "path:config"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rc := filepath.Join(dir, "bashrc")
	if err := os.WriteFile(rc, []byte(`
_previous_gopick() { COMPREPLY=(wrong); }
complete -F _previous_gopick gopick
`+bashCompletion+`
gopick() {
  if [[ $1 == -complete ]]; then
    command gopick "$@" 2> "$GOPICK_CAPTURE.error"
  else
    printf '%s\0' "$@" > "$GOPICK_CAPTURE"
  fi
}
go() { printf '%s\0' "$@" > "$GOPICK_CAPTURE"; }
PS1='GOPICK_READY> '
PS2='GOPICK_MORE> '
bind 'set editing-mode emacs'
bind '"\t": complete'
`), 0o600); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	// On macOS these exercise the current Bash and the bundled Bash 3.2.
	for _, name := range []string{"bash", "/bin/bash"} {
		bash, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		bash, err = filepath.EvalSymlinks(bash)
		if err != nil || seen[bash] {
			continue
		}
		seen[bash] = true
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command(python, "-c", `
import os, pathlib, pty, select, shlex, subprocess, sys, time
bash, root, rc = sys.argv[1:]
capture = pathlib.Path(root) / 'captured'
master, slave = pty.openpty()
env = dict(os.environ, INPUTRC='/dev/null', TERM='xterm-256color', GOPICK_CAPTURE=str(capture))
child = subprocess.Popen([bash, '--noprofile', '--rcfile', rc, '-i'], cwd=root, env=env,
                         stdin=slave, stdout=slave, stderr=slave)
os.close(slave)
def until(done):
    output = b''
    end = time.monotonic() + 10
    while time.monotonic() < end:
        ready, _, _ = select.select([master], [], [], .05)
        if ready:
            output += os.read(master, 65536)
        if done(output):
            return output
    raise AssertionError('interactive completion timed out: ' + repr(output))
try:
    until(lambda output: b'GOPICK_READY>' in output)
    for prefix, expected in [
        ('./spa', './space dir/'),
        ("'./spa", './space dir/'),
        ('"./spa', './space dir/'),
        ('./apo', "./apostrophe's dir/"),
        ('./back', './back\\slash/'),
        ('./dollar', './dollar$(printf injected)/'),
        ('./project:co', './project:colon/'),
        ('./project::do', './project::double/'),
        ('./colon:', './colon:space dir/'),
        ("'./project:co", './project:colon/'),
        ('"./project:co', './project:colon/'),
        ('./project\\:co', './project:colon/'),
        ('./colon:spa', './colon:space dir/'),
    ]:
        for flag in ['-C ', '-C=']:
            capture.unlink(missing_ok=True)
            # Input is processed in order: Enter runs only after Tab completes.
            os.write(master, ('gopick ' + flag + prefix + '\t\r').encode())
            until(lambda output: capture.exists() and b'GOPICK_READY>' in output)
            actual = capture.read_bytes().decode().split('\0')[:-1]
            want = ['-C', expected] if flag == '-C ' else ['-C=' + expected]
            if actual != want:
                raise AssertionError(f'{flag}{prefix}: got {actual!r}, want {want!r}')
    for path, test in [
        ('./project:colon', 'TestColon'),
        ('./project::double', 'TestDouble'),
        ("'./project:colon'", 'TestColon'),
        ('./project\\:colon', 'TestColon'),
    ]:
        for flag in ['-C ', '-C=']:
            capture.unlink(missing_ok=True)
            os.write(master, ('go ' + flag + path + ' test -run=Test\t\r').encode())
            until(lambda output: capture.exists() and b'GOPICK_READY>' in output)
            actual = capture.read_bytes().decode().split('\0')[:-1]
            if actual[-1] != '-run=^' + test + '$':
                error = pathlib.Path(str(capture) + '.error').read_text()
                raise AssertionError(f'{flag}{path}: got {actual!r}; {error}')
    home = pathlib.Path(root) / 'home space'
    os.write(master, ('export HOME=' + shlex.quote(str(home)) + '\r').encode())
    until(lambda output: b'GOPICK_READY>' in output)
    for prefix, expected in [
        ('./con', './configrc'),
        ('./set', './settings file'),
        ("'./set", './settings file'),
        ('./quo', "./quote'config"),
        ('./path:co', './path:config'),
        ('./colon:spa', './colon:space dir/'),
        ('~/con', str(home / 'configrc')),
        ('~/set', str(home / 'settings file')),
        ("'~/lit", './~/literalrc'),
        ('"~/lit', './~/literalrc'),
        ('\\~/lit', './~/literalrc'),
    ]:
        for flag in ['-completion-file ', '--completion-file=']:
            capture.unlink(missing_ok=True)
            os.write(master, ('gopick ' + flag + prefix + '\t\r').encode())
            until(lambda output: capture.exists() and b'GOPICK_READY>' in output)
            actual = capture.read_bytes().decode().split('\0')[:-1]
            want = ['-completion-file', expected] if flag.endswith(' ') else ['--completion-file=' + expected]
            if actual != want:
                raise AssertionError(f'{flag}{prefix}: got {actual!r}, want {want!r}')
finally:
    child.terminate()
    try:
        child.wait(timeout=1)
    except subprocess.TimeoutExpired:
        child.kill()
        child.wait()
    os.close(master)
`, bash, dir, rc)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("interactive path completion: %v\n%s", err, output)
			}
		})
	}
}

func TestCompletionSelection(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		path, err := exec.LookPath(shell)
		if err != nil {
			t.Skip(shell + " is not installed")
		}
		t.Run(shell, func(t *testing.T) {
			completionHome(t)
			var script strings.Builder
			check := "complete -p go gopick\n"
			if shell == "zsh" {
				script.WriteString("autoload -Uz compinit\ncompinit -D -i\ncompdef _existing_go go\ncompdef _existing_gopick gopick\n")
				check = "print -r -- \"${_comps[go]} ${_comps[gopick]} ${_patcomps[go]}\"\n"
			} else {
				script.WriteString("complete -F _existing_go go\ncomplete -F _existing_gopick gopick\n")
			}
			for _, mode := range []string{"go", "both", "gopick", "both", "go"} {
				if err := printCompletionScript(&script, shell, mode); err != nil {
					t.Fatal(err)
				}
				script.WriteString(check)
			}
			cmd := exec.Command(path, "-f", shellScript(t, script.String()))
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("selection failed: %v\n%s", err, output)
			}
			want := "_existing_go _existing_gopick _gopick_go\n_existing_go _gopick _gopick_go\n_existing_go _gopick \n_existing_go _gopick _gopick_go\n_existing_go _existing_gopick _gopick_go\n"
			if shell == "bash" {
				want = "complete -F _gopick_go go\ncomplete -F _existing_gopick gopick\ncomplete -F _gopick_go go\ncomplete -F _gopick gopick\ncomplete -F _existing_go go\ncomplete -F _gopick gopick\ncomplete -F _gopick_go go\ncomplete -F _gopick gopick\ncomplete -F _gopick_go go\ncomplete -F _existing_gopick gopick\n"
			}
			if string(output) != want {
				t.Errorf("registrations=%q, want %q", output, want)
			}
		})
	}
}

func TestFishCompletion(t *testing.T) {
	fish, err := exec.LookPath("fish")
	if err != nil {
		t.Skip("fish is not installed")
	}
	dir := fixture(t, map[string]string{
		"example_test.go": "package example\nimport \"testing\"\nfunc TestAlpha(t *testing.T) { panic(\"must not run\") }\nfunc TestAlphaError(t *testing.T) {}\n",
		"sub/sub_test.go": "package sub\nimport \"testing\"\nfunc TestSub(t *testing.T) {}\n",
		"tagged_test.go":  "//go:build special\n\npackage example\nimport \"testing\"\nfunc TestTagged(t *testing.T) {}\n",
		"~/literal/keep":  "",
		"settings":        "",
	})
	completionExecutable(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(home, dir)
	if err != nil {
		t.Fatal(err)
	}
	homePath := "~/" + filepath.ToSlash(relative)
	for _, tc := range []struct {
		line string
		want string
	}{
		{"go test -run=TestAl", "-run=^TestAlpha$\n-run=^TestAlphaError$\n"},
		{"go test -run=", "-run=^TestAlpha$\n-run=^TestAlphaError$\n"},
		{"go test -run TestAl", "^TestAlpha$\n^TestAlphaError$\n"},
		{"go test ./sub -test.run=Test", "-test.run=^TestSub$\n"},
		{"go test -tags=special -run=TestT", "-run=^TestTagged$\n"},
		{"go test -run='^TestAl", "-run=^TestAlpha$\n-run=^TestAlphaError$\n"},
		{"go test -run=Missing", ""},
		{"gopick TestAl", "^TestAlpha$\n^TestAlphaError$\n"},
		{"gopick ^TestAl", "^TestAlpha$\n^TestAlphaError$\n"},
		{"gopick '^TestAl", "^TestAlpha$\n^TestAlphaError$\n"},
		{"gopick 'TestAlpha|TestSub'", ""},
		{"gopick '^TestAlpha$/child'", ""},
		{"gopick ./sub Test", "^TestSub$\n"},
		{"gopick -completion-target go", "go\ngopick\n"},
		{"gopick -completion-target=b", "-completion-target=both\n"},
		{"gopick --completion-file=set", "--completion-file=settings\n"},
		{"gopick -completion-file set", "settings\n"},
		{"gopick -install-completion fish --", "--completion-target\n--completion-file\n--yes\n--help\n"},
		{"gopick --yes --", "--completion-target\n--completion-file\n--install-completion\n--yes\n--help\n"},
		{"gopick --completion-file config --list", ""},
		{"gopick --yes ", ""},
		{"gopick --list --install-", ""},
		{"gopick --completion fish --yes", ""},
		{"gopick -yes=", "-yes=false\n-yes=true\n"},
		{"gopick TestAlpha -comp", ""},
		{"gopick -C " + homePath + "/s", homePath + "/sub/\n"},
		{"gopick -C " + homePath + "/'s", homePath + "/sub/\n"},
		{"gopick -C '~/l", "~/literal/\n"},
		{`gopick -C "~/l`, "~/literal/\n"},
		{`gopick -C \~/l`, "~/literal/\n"},
		{"gopick -C=~/l", "-C=~/literal/\n"},
		{"go -C " + homePath + " test -run=TestAl", "-run=^TestAlpha$\n-run=^TestAlphaError$\n"},
	} {
		cmd := exec.Command(fish, "--no-config", shellScript(t, fishCompletion+"\ncomplete -C "+shellCommand([]string{tc.line})+"\n"))
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		if err != nil || string(output) != tc.want {
			t.Errorf("line %q: output=%q error=%v, want %q", tc.line, output, err, tc.want)
		}
	}
	t.Run("selection and reload", func(t *testing.T) {
		cmd := exec.Command(fish, "--no-config", shellScript(t, `
complete -C 'go ' >/dev/null
complete -e -c go
complete -c go -f -a existing
for target in go both gopick both go
    gopick -completion fish -completion-target $target | source
    complete -C 'go test -run=TestAl'
    complete -C 'gopick TestAl'
    complete -C 'go ex'
end
set before (complete -c go)
gopick -completion fish -completion-target go | source
test (count $before) -eq (count (complete -c go)); or exit 1
`))
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("selection failed: %v\n%s", err, output)
		}
		goNames := "-run=^TestAlpha$\n-run=^TestAlphaError$\n"
		gopickNames := "^TestAlpha$\n^TestAlphaError$\n"
		want := goNames + "existing\n" + goNames + gopickNames + "existing\n" + gopickNames + "existing\n" + goNames + gopickNames + "existing\n" + goNames + "existing\n"
		if string(output) != want {
			t.Errorf("completion=%q, want %q", output, want)
		}
	})
	t.Run("upgrade registrations", func(t *testing.T) {
		cmd := exec.Command(fish, "--no-config", shellScript(t, `
complete -c gopick -n __gopick_command_context -o list
complete -c gopick -n __gopick_command_context -f -a '(__gopick_candidates)'
complete -c gopick -l custom
gopick -completion fish | source
set before (complete -c gopick)
gopick -completion fish | source
test (count $before) -eq (count (complete -c gopick)); or exit 1
complete -C 'gopick --install-'
complete -C 'gopick --completion-file=set'
complete -C 'gopick --custom'
`))
		cmd.Dir = dir
		want := "--install-completion\n--completion-file=settings\n--custom\n"
		if output, err := cmd.CombinedOutput(); err != nil || string(output) != want {
			t.Fatalf("upgrading Fish registrations: %v\n%s", err, output)
		}
	})
}
