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
		{[]string{"gopick", "-C", dir, "TestA"}, 3, "arguments\nTestAlpha\n"},
		{[]string{"gopick", "-C", dir, "./sub", "Test"}, 4, "tests\nTestSub\n"},
		{[]string{"gopick", "-C", dir, "-tags", "special", "^TestT"}, 5, "tests\n^TestTagged\n"},
		{[]string{"gopick", "-C", dir, "TestAlpha", "extra"}, 4, "none\n"},
		{[]string{"gopick", "-C", dir, "./sub", "TestAlpha", "extra"}, 5, "none\n"},
		{[]string{"gopick", "-completion", "b"}, 2, "arguments\nbash\n"},
		{[]string{"gopick", "-completion-target", "go"}, 2, "arguments\ngo\ngopick\n"},
		{[]string{"gopick", "--completion-target=b"}, 1, "arguments\n--completion-target=both\n"},
		{[]string{"gopick", "--comp"}, 1, "arguments\n--completion\n--completion-target\n"},
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

func completionExecutable(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nexec " + shellCommand([]string{executable, "-test.run=^TestCompletionHelper$", "--"}) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "gopick"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOPICK_COMPLETION_HELPER", "1")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestCompletionOrder(t *testing.T) {
	dir := fixture(t, map[string]string{
		"root_test.go":    "package example\nimport \"testing\"\nfunc TestAlpha(t *testing.T) {}\nfunc FuzzSample(f *testing.F) {}\nfunc Example() {\n// Output:\n}\n",
		"sub/sub_test.go": "package sub\nimport \"testing\"\nfunc TestSub(t *testing.T) {}\n",
	})
	want := []string{"./...", "example.com/project", "example.com/project/sub", "Example", "FuzzSample", "TestAlpha", "TestSub"}
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
			cmd := exec.Command(fish, "--no-config", "-c", legacy+fishCompletion+fishCompletion+"\ncomplete -C 'gopick '\n")
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
			python, err := exec.LookPath("python3")
			if err != nil {
				t.Skip("python3 is needed for the interactive PTY test")
			}
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
import os, pathlib, pty, select, signal, subprocess, sys, time
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
		{"gopick TestAl", "gopick TestAl", "_gopick", "TestAlpha\nTestAlphaError\n"},
		{"gopick ./sub '^TestS'", "gopick ./sub ^TestS", "_gopick", "^TestSub\n"},
		{"gopick -completion-target = go", "gopick -completion-target=go", "_gopick", "go\ngopick\n"},
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
		cmd := exec.Command(bash, "--noprofile", "--norc", "-c", `
_existing_go() { COMPREPLY=(existing); }
complete -o nospace -F _existing_go go
`+bashCompletion+bashCompletion+`
COMP_WORDBREAKS=' =:'
COMP_LINE=`+shellCommand([]string{tc.line})+`
COMP_WORDS=(`+tc.words+`)
COMP_CWORD=$((${#COMP_WORDS[@]}-1))
`+tc.function+` "${COMP_WORDS[0]}" "${COMP_WORDS[COMP_CWORD]}" "${COMP_WORDS[COMP_CWORD-1]}"
for result in "${COMPREPLY[@]}"; do printf '%s\n' "$result"; done
`)
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		if err != nil || string(output) != tc.want {
			t.Errorf("words %s: output=%q error=%v, want %q", tc.words, output, err, tc.want)
		}
	}
}

func TestBashPaths(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is needed for the interactive PTY test")
	}
	completionExecutable(t)
	dir := fixture(t, map[string]string{
		"project:colon/go.mod":         "module example.com/colon\n\ngo 1.26\n",
		"project:colon/main_test.go":   "package example\nimport \"testing\"\nfunc TestColon(t *testing.T) {}\n",
		"project::double/go.mod":       "module example.com/double\n\ngo 1.26\n",
		"project::double/main_test.go": "package example\nimport \"testing\"\nfunc TestDouble(t *testing.T) {}\n",
	})
	for _, name := range []string{"space dir", "apostrophe's dir", "back\\slash", "dollar$(printf injected)", "colon:space dir"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	rc := filepath.Join(dir, "bashrc")
	if err := os.WriteFile(rc, []byte(bashCompletion+`
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
import os, pathlib, pty, select, subprocess, sys, time
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
			cmd := exec.Command(path, "-f", "-c", script.String())
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
		{"gopick TestAl", "TestAlpha\nTestAlphaError\n"},
		{"gopick ./sub Test", "TestSub\n"},
		{"gopick -completion-target go", "go\ngopick\n"},
		{"gopick -completion-target=b", "-completion-target=both\n"},
		{"gopick TestAlpha -comp", ""},
		{"gopick -C " + homePath + "/s", homePath + "/sub/\n"},
		{"gopick -C " + homePath + "/'s", homePath + "/sub/\n"},
		{"gopick -C '~/l", "~/literal/\n"},
		{`gopick -C "~/l`, "~/literal/\n"},
		{`gopick -C \~/l`, "~/literal/\n"},
		{"gopick -C=~/l", "-C=~/literal/\n"},
		{"go -C " + homePath + " test -run=TestAl", "-run=^TestAlpha$\n-run=^TestAlphaError$\n"},
	} {
		cmd := exec.Command(fish, "--no-config", "-c", fishCompletion+"\ncomplete -C "+shellCommand([]string{tc.line})+"\n")
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		if err != nil || string(output) != tc.want {
			t.Errorf("line %q: output=%q error=%v, want %q", tc.line, output, err, tc.want)
		}
	}
	t.Run("selection and reload", func(t *testing.T) {
		cmd := exec.Command(fish, "--no-config", "-c", `
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
`)
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("selection failed: %v\n%s", err, output)
		}
		goNames := "-run=^TestAlpha$\n-run=^TestAlphaError$\n"
		gopickNames := "TestAlpha\nTestAlphaError\n"
		want := goNames + "existing\n" + goNames + gopickNames + "existing\n" + gopickNames + "existing\n" + goNames + gopickNames + "existing\n" + goNames + "existing\n"
		if string(output) != want {
			t.Errorf("completion=%q, want %q", output, want)
		}
	})
}
