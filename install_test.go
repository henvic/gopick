package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func completionHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", filepath.ToSlash(home))
	t.Setenv("USERPROFILE", home)
	t.Setenv("ZDOTDIR", "")
	if err := os.Unsetenv("ZDOTDIR"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

func installForTest(t *testing.T, args ...string) string {
	t.Helper()
	var output bytes.Buffer
	if err := run(context.Background(), args, nil, &output, &output); err != nil {
		t.Fatalf("run(%q): %v\n%s", args, err, &output)
	}
	return output.String()
}

func readInstalled(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestInstallZsh(t *testing.T) {
	home := completionHome(t)
	dir := filepath.Join(home, "dotfiles")
	t.Setenv("ZDOTDIR", dir)
	args := []string{"-install-completion", "zsh", "-yes"}
	output := installForTest(t, args...)
	if !strings.HasSuffix(output, "  source ~/dotfiles/.zshrc\n") {
		t.Fatalf("activation did not use ZDOTDIR: %s", output)
	}
	rc := filepath.Join(dir, ".zshrc")
	first := readInstalled(t, rc)
	if strings.Contains(first, "compinit") || !strings.Contains(first, "source <(gopick -completion zsh)") || !strings.Contains(output, "Created: ~/dotfiles/.zshrc\n") {
		t.Fatalf("unexpected installation:\n%s\n%s", output, first)
	}
	if strings.Contains(output, completionStart) || strings.Contains(output, "source <(") || !strings.Contains(output, "initialized if needed") {
		t.Fatalf("expected a file summary and initialization notice: %s", output)
	}
	if _, err := os.Stat(filepath.Join(home, ".zshrc")); !os.IsNotExist(err) {
		t.Fatal("ignored ZDOTDIR")
	}
	output = installForTest(t, args...)
	if !strings.Contains(output, "already configured") || !strings.HasSuffix(output, "  source ~/dotfiles/.zshrc\n") || readInstalled(t, rc) != first {
		t.Fatal("repeat installation changed the file")
	}
	before := "# user settings\n" + first + "# after gopick\n"
	if err := os.WriteFile(rc, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(rc, 0o600); err != nil {
		t.Fatal(err)
	}
	beforeInfo, err := os.Stat(rc)
	if err != nil {
		t.Fatal(err)
	}
	installForTest(t, append(args, "-completion-target", "go")...)
	after := readInstalled(t, rc)
	if strings.Count(after, completionStart) != 1 || !strings.HasPrefix(after, "# user settings\n") || !strings.HasSuffix(after, "# after gopick\n") || !strings.Contains(after, "-completion-target go") {
		t.Fatalf("target update lost settings or duplicated a block:\n%s", after)
	}
	info, err := os.Stat(rc)
	if err != nil || info.Mode().Perm() != beforeInfo.Mode().Perm() {
		t.Fatalf("permissions changed: %v, %v", info, err)
	}
}

func TestInstallLegacyMarkers(t *testing.T) {
	for _, shell := range []string{"zsh", "bash", "fish"} {
		t.Run(shell, func(t *testing.T) {
			completionHome(t)
			installForTest(t, "-install-completion", shell, "-yes")
			paths, err := completionPaths(shell, "")
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range paths {
				content := readInstalled(t, path)
				content = strings.ReplaceAll(content, completionStart, "# >>> gopick completion >>>")
				content = strings.ReplaceAll(content, completionEnd, "# <<< gopick completion <<<")
				content = "# user settings\n" + content + "# more settings\n"
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			installForTest(t, "-install-completion", shell, "-yes")
			for _, path := range paths {
				content := readInstalled(t, path)
				if strings.Count(content, completionStart+"\n") != 1 || strings.Count(content, completionEnd+"\n") != 1 || strings.Contains(content, ">>>") || strings.Contains(content, "<<<") || !strings.HasPrefix(content, "# user settings\n") || !strings.HasSuffix(content, "# more settings\n") {
					t.Fatalf("legacy block was not replaced in place: %s", content)
				}
			}
			if output := installForTest(t, "-install-completion", shell, "-yes"); !strings.Contains(output, "already configured") {
				t.Fatalf("upgraded installation is not idempotent: %s", output)
			}
		})
	}
}

func TestInstallEmptyZdotdir(t *testing.T) {
	home := completionHome(t)
	t.Setenv("ZDOTDIR", "")
	t.Chdir(t.TempDir())
	var output bytes.Buffer
	if err := run(context.Background(), []string{"-install-completion", "zsh", "-yes"}, nil, &output, &output); err == nil || !strings.Contains(err.Error(), "empty ZDOTDIR") {
		t.Fatalf("expected an explicit path request: %v\n%s", err, &output)
	}
	if _, err := os.Stat(".zshrc"); !os.IsNotExist(err) {
		t.Fatal("empty ZDOTDIR must not use the working directory")
	}
	if _, err := os.Stat(filepath.Join(home, ".zshrc")); !os.IsNotExist(err) {
		t.Fatal("empty ZDOTDIR must not use HOME")
	}
	installForTest(t, "-install-completion", "zsh", "-completion-file", filepath.Join(home, ".zshrc"), "-yes")
}

func TestInstallBash(t *testing.T) {
	home := completionHome(t)
	profile := filepath.Join(home, ".profile")
	if err := os.WriteFile(profile, []byte("# shared profile\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if output := installForTest(t, "-install-completion", "bash", "-yes"); !strings.HasSuffix(output, "  source ~/.bashrc\n") {
		t.Fatalf("unexpected activation command: %s", output)
	}
	for _, path := range []string{profile, filepath.Join(home, ".bashrc")} {
		if !strings.Contains(readInstalled(t, path), "gopick -completion bash") {
			t.Fatalf("missing loader in %s", path)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".bash_profile")); !os.IsNotExist(err) {
		t.Fatal("created a profile that would shadow the existing .profile")
	}
	if sh, err := exec.LookPath("sh"); err == nil {
		// A shared .profile must remain parseable by POSIX shells.
		if output, err := exec.Command(sh, "-n", profile).CombinedOutput(); err != nil {
			t.Fatalf("invalid POSIX profile: %v\n%s", err, output)
		}
	}
}

func TestInstallStartup(t *testing.T) {
	completionExecutable(t)
	for _, shell := range []string{"zsh", "bash"} {
		t.Run(shell, func(t *testing.T) {
			path, err := exec.LookPath(shell)
			if err != nil {
				t.Skip(err)
			}
			completionHome(t)
			installForTest(t, "-install-completion", shell, "-yes")
			args := []string{"-d", "-i", shellScript(t, `[[ ${_comps[gopick]} == _gopick && ${_patcomps[go]} == _gopick_go ]]`)}
			if shell == "bash" {
				args = []string{"--noprofile", "-i", shellScript(t, `complete -p go gopick`)}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if output, err := exec.CommandContext(ctx, path, args...).CombinedOutput(); err != nil {
				t.Fatalf("installed startup failed: %v\n%s", err, output)
			}
		})
	}
}

func TestInstallBashProfile(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip(err)
	}
	for _, profile := range []string{"", ". \"$HOME/.bashrc\"\n", "# independent login settings\n"} {
		t.Run(profile, func(t *testing.T) {
			home := completionHome(t)
			binary := completionExecutable(t)
			wrapper := strings.Replace(readInstalled(t, binary), "#!/bin/sh\n", "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$HOME/calls\"\n", 1)
			if err := os.WriteFile(binary, []byte(wrapper), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte("GOPICK_BASHRC_LOADED=1\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if profile != "" {
				if err := os.WriteFile(filepath.Join(home, ".bash_profile"), []byte(profile), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			installForTest(t, "-install-completion", "bash", "-yes")
			// Reinstalling must retain the .bashrc loader in a newly created profile.
			installForTest(t, "-install-completion", "bash", "-yes")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			script := `. "$HOME/.bash_profile"
complete -p go gopick >/dev/null || exit 1
[[ $(wc -l < "$HOME/calls") -eq 1 ]] || exit 2
`
			if profile == "" || strings.Contains(profile, ".bashrc") {
				script += `[[ $GOPICK_BASHRC_LOADED == 1 ]] || exit 3` + "\n"
			}
			script += `. "$HOME/.bashrc"
[[ $(wc -l < "$HOME/calls") -eq 2 ]]`
			cmd := exec.CommandContext(ctx, bash, "--noprofile", "--norc", "-i", shellScript(t, script))
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("Bash profile: %v\n%s", err, output)
			}
		})
	}
}

func TestInstallWriteFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires Unix file size limits and SIGXFSZ")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip(err)
	}
	home := completionHome(t)
	completionExecutable(t)
	rc := filepath.Join(home, ".zshrc")
	before := strings.Repeat("# existing settings\n", 1024)
	if err := os.WriteFile(rc, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, sh, shellScript(t, "trap '' XFSZ; ulimit -f 1 || exit 1; exec gopick -install-completion zsh -yes"))
	if output, err := cmd.CombinedOutput(); err == nil || !bytes.Contains(output, []byte("install completion in")) {
		t.Fatalf("expected installation failure: %v\n%s", err, output)
	}
	if readInstalled(t, rc) != before {
		t.Fatal("failed write changed the original file")
	}
	if files, err := filepath.Glob(filepath.Join(home, ".gopick-completion-*")); err != nil || len(files) != 0 {
		t.Fatalf("failed write left temporary files: %v, %v", files, err)
	}
}

func TestCompletionTerminalStartup(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "startup-ran")
	script := filepath.Join(dir, "startup")
	if err := os.WriteFile(script, []byte("touch "+shellCommand([]string{marker})+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BASH_ENV", script)
	t.Setenv("ENV", script)
	if terminal, err := completionTerminal(context.Background(), strings.NewReader("yes\n")); terminal || err != nil {
		t.Fatalf("piped input accepted: %v, %v", terminal, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("terminal detection ran a startup hook")
	}
}

func TestInstallOverrideAndSymlink(t *testing.T) {
	home := completionHome(t)
	real := filepath.Join(home, "real config with 'quotes'")
	link := filepath.Join(home, "customrc")
	if err := os.WriteFile(real, []byte("# existing settings without a final newline"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if output := installForTest(t, "-install-completion", "zsh", "-completion-file", link, "-yes"); !strings.HasSuffix(output, "  source ~/customrc\n") {
		t.Fatalf("activation ignored the custom startup file: %s", output)
	}
	if target, err := os.Readlink(link); err != nil || target != real {
		t.Fatalf("symlink replaced: %q, %v", target, err)
	}
	if !strings.HasPrefix(readInstalled(t, real), "# existing settings without a final newline\n"+completionStart) {
		t.Fatal("existing text changed")
	}
	if _, err := os.Stat(filepath.Join(home, ".zshrc")); !os.IsNotExist(err) {
		t.Fatal("ignored explicit startup file")
	}
	dangling := filepath.Join(home, "danglingrc")
	missing := filepath.Join("missing", "config")
	if err := os.Symlink(missing, dangling); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := run(context.Background(), []string{"-install-completion", "zsh", "-completion-file", dangling, "-yes"}, nil, &output, &output); err == nil || !strings.Contains(err.Error(), "dangling symlink") {
		t.Fatalf("expected a clear dangling symlink error: %v\n%s", err, &output)
	}
	if target, err := os.Readlink(dangling); err != nil || target != missing {
		t.Fatalf("dangling symlink changed: %q, %v", target, err)
	}
}

func TestInstallValidation(t *testing.T) {
	home := completionHome(t)
	for _, args := range [][]string{
		{"-install-completion", ""},
		{"-install-completion", "sh", "-yes"},
		{"-install-completion", "zsh", "-completion-target", "invalid", "-yes"},
		{"-install-completion", "zsh", "TestAlpha", "-yes"},
		{"-install-completion", "zsh", "-completion", "zsh", "-yes"},
		{"-install-completion", "zsh", "-format", "", "-yes"},
		{"-install-completion", "fish", "-completion-file", "config", "-yes"},
		{"-yes"},
		{"-yes=false"},
		{"-completion-file", "config"},
	} {
		var output bytes.Buffer
		if err := run(context.Background(), args, nil, &output, &output); err == nil {
			t.Errorf("run(%q) succeeded", args)
		}
	}
	for _, shell := range []string{"zsh", "bash", "fish"} {
		for _, option := range [][]string{{"-completion-file", ""}, {"--completion-file="}} {
			args := append([]string{"-install-completion", shell, "-yes"}, option...)
			var output bytes.Buffer
			if err := run(context.Background(), args, nil, &output, &output); err == nil || !strings.Contains(err.Error(), "-completion-file must not be empty") || output.Len() != 0 {
				t.Errorf("run(%q) = %v, output=%q; want an empty-file error before installation", args, err, &output)
			}
		}
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("invalid invocation changed files: %v, %v", entries, err)
	}
	rc := filepath.Join(home, ".zshrc")
	for _, content := range []string{
		completionStart + "\nbroken\n",
		completionEnd + "\n",
		completionStart + "\n" + completionStart + "\n" + completionEnd + "\n",
		"# >>> gopick completion >>>\nbroken\n",
		completionStart + "\n# <<< gopick completion <<<\n",
		"# >>> gopick completion >>>\n" + completionEnd + "\n",
		completionStart + "\n" + completionEnd + "\n# >>> gopick completion >>>\n# <<< gopick completion <<<\n",
	} {
		if err := os.WriteFile(rc, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		if err := run(context.Background(), []string{"-install-completion", "zsh", "-yes"}, nil, &output, &output); err == nil || readInstalled(t, rc) != content {
			t.Fatalf("malformed block was modified: %q, %v", content, err)
		}
	}
}

func TestInstallNonterminal(t *testing.T) {
	home := completionHome(t)
	for _, shell := range []string{"zsh", "bash", "fish"} {
		t.Run(shell, func(t *testing.T) {
			if _, err := exec.LookPath(shell); err != nil {
				t.Skip(err)
			}
			var output bytes.Buffer
			if err := run(context.Background(), []string{"-install-completion", shell}, strings.NewReader("yes\n"), &output, &output); err == nil || !strings.Contains(err.Error(), "TTY") {
				t.Fatalf("piped approval accepted: %v\n%s", err, &output)
			}
			file, err := os.Open(os.DevNull)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			terminal, err := completionTerminal(context.Background(), file)
			if terminal || err != nil {
				t.Fatalf("/dev/null considered terminal: %v, %v", terminal, err)
			}
		})
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("noninteractive installer changed files: %v, %v", entries, err)
	}
}

func TestInstallConfirmation(t *testing.T) {
	python := terminalPython(t)
	completionHome(t)
	completionExecutable(t)
	for _, shell := range []string{"zsh", "bash", "fish"} {
		if _, err := exec.LookPath(shell); err != nil {
			continue
		}
		for _, answer := range []string{"y", "no", "", "eof", "interrupt", "changed", "retargeted"} {
			t.Run(shell+"/"+answer, func(t *testing.T) {
				home := completionHome(t)
				if answer == "retargeted" {
					for _, name := range []string{"first", "second"} {
						if err := os.Mkdir(filepath.Join(home, name), 0o700); err != nil {
							t.Fatal(err)
						}
					}
					link := filepath.Join(home, "config")
					if err := os.Symlink(filepath.Join(home, "first"), link); err != nil {
						t.Skipf("symlinks unavailable: %v", err)
					}
					home = link
					t.Setenv("HOME", home)
					t.Setenv("USERPROFILE", home)
					t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
				}
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, python, "-c", `
import os, pty, select, signal, subprocess, sys, time
shell, answer = sys.argv[1:]
master, slave = pty.openpty()
p = subprocess.Popen(['gopick', '-install-completion', shell], stdin=slave, stdout=slave, stderr=slave)
os.close(slave)
data = b''
try:
    deadline = time.monotonic() + 12
    while b'Apply these changes? [y/N]' not in data:
        assert time.monotonic() < deadline, data
        if select.select([master], [], [], .1)[0]:
            data += os.read(master, 65536)
    path = {'zsh': '.zshrc', 'bash': '.bashrc', 'fish': '.config/fish/completions/go.fish'}[shell]
    assert ('Create: ~/' + path).encode() in data, data
    assert b'gopick completion' not in data and b'gopick -completion' not in data, data
    assert not os.path.exists(os.path.join(os.environ['HOME'], path)), data
    if answer == 'changed':
        path = os.path.join(os.environ['HOME'], path)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, 'w') as f:
            f.write('# changed during confirmation\n')
    if answer == 'retargeted':
        home = os.environ['HOME']
        os.unlink(home)
        os.symlink(os.path.join(os.path.dirname(home), 'second'), home)
    if answer == 'interrupt':
        p.send_signal(signal.SIGINT)
    else:
        os.write(master, b'\x04' if answer == 'eof' else (('y' if answer in ('changed', 'retargeted') else answer) + '\n').encode())
    while p.poll() is None:
        assert time.monotonic() < deadline, data
        if select.select([master], [], [], .1)[0]:
            try:
                data += os.read(master, 65536)
            except OSError:
                break
    code = p.wait(timeout=2)
    if answer == 'interrupt':
        assert code != 0, (code, data)
    else:
        assert code == (1 if answer in ('changed', 'retargeted') else 0), (code, data)
finally:
    if p.poll() is None:
        p.kill()
    os.close(master)
    p.wait()
`, shell, answer)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("confirmation failed: %v\n%s", err, output)
				}
				path := filepath.Join(home, "."+shell+"rc")
				if shell == "fish" {
					path = filepath.Join(home, ".config", "fish", "completions", "go.fish")
				}
				switch answer {
				case "y":
					if !strings.Contains(readInstalled(t, path), completionStart) {
						t.Fatal("confirmation did not install")
					}
				case "changed":
					if readInstalled(t, path) != "# changed during confirmation\n" {
						t.Fatal("overwrote a concurrent edit")
					}
				case "retargeted":
					for _, name := range []string{"first", "second"} {
						entries, err := os.ReadDir(filepath.Join(filepath.Dir(home), name))
						if err != nil || len(entries) != 0 {
							t.Fatalf("retargeted parent allowed a write in %s: %v, %v", name, entries, err)
						}
					}
				default:
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatal("declining confirmation changed a file")
					}
				}
			})
		}
	}
}

func TestInstallFish(t *testing.T) {
	fish, err := exec.LookPath("fish")
	if err != nil {
		t.Skip(err)
	}
	dir := fixture(t, map[string]string{"example_test.go": "package example\nimport \"testing\"\nfunc TestAlpha(t *testing.T) {}\n"})
	home := completionHome(t)
	completionExecutable(t)
	completionDir := filepath.Join(home, ".config", "fish", "completions")
	for _, target := range []string{"both", "go", "gopick", "both"} {
		output := installForTest(t, "-install-completion", "fish", "-completion-target", target, "-yes")
		for _, command := range []string{"go", "gopick"} {
			if enabled := target == "both" || target == command; strings.Contains(output, "source ~/.config/fish/completions/"+command+".fish") != enabled {
				t.Fatalf("target %s: incorrect activation command: %s", target, output)
			}
			_, err := os.Stat(filepath.Join(completionDir, command+".fish"))
			if enabled := target == "both" || target == command; (err == nil) != enabled {
				t.Fatalf("target %s: %s file presence: %v", target, command, err)
			}
		}
		for _, first := range []string{"go test -run=TestAl", "gopick TestAl"} {
			cmd := exec.Command(fish, "--no-config", shellScript(t, "set -p fish_complete_path "+shellCommand([]string{completionDir})+"\n"+
				"complete -C "+shellCommand([]string{first})+" >/dev/null\n"+
				"echo GO\ncomplete -C 'go test -run=TestAl'\necho GOPICK\ncomplete -C 'gopick TestAl'\necho NATIVE\ncomplete -C 'go buil'\n"))
			cmd.Dir = dir
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("Fish loading failed: %v\n%s", err, output)
			}
			text := string(output)
			if strings.Contains(text, "-run=^TestAlpha$") != (target != "gopick") || strings.Contains(text, "GOPICK\n^TestAlpha$\n") != (target != "go") || !strings.Contains(text, "NATIVE\nbuild\t") {
				t.Fatalf("target %s, first %s: missing completion or lost native Go completion:\n%s", target, first, text)
			}
		}
	}
	// Also preserve completions supplied on disk (older Fish and plugins).
	native := t.TempDir()
	if err := os.WriteFile(filepath.Join(native, "go.fish"), []byte("complete -c go -f -a native\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(fish, "--no-config", shellScript(t, "set fish_complete_path "+shellCommand([]string{completionDir, native})+" $fish_complete_path\ncomplete -C 'go nat'\n"))
	if output, err := cmd.CombinedOutput(); err != nil || string(output) != "native\n" {
		t.Fatalf("lost next provider's completion: %v\n%s", err, output)
	}
	// Existing custom completion files must survive installation and deselection.
	custom := filepath.Join(completionDir, "go.fish")
	if err := os.WriteFile(custom, []byte("complete -c go -f -a custom\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		installForTest(t, "-install-completion", "fish", "-yes")
		if !strings.HasPrefix(readInstalled(t, custom), "complete -c go -f -a custom\n") {
			t.Fatal("custom Go completion lost")
		}
		installForTest(t, "-install-completion", "fish", "-completion-target", "gopick", "-yes")
		if readInstalled(t, custom) != "complete -c go -f -a custom\n" {
			t.Fatal("deselecting Go changed custom completion")
		}
	}
}
