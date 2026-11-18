#!/usr/bin/env python3
"""Record real shell completion as asciinema recordings.

Requires Go and the selected shell. Uses temporary configuration;
the user's shell configuration and installed gopick are left untouched.
"""

import argparse
import errno
import fcntl
import json
import os
from pathlib import Path
import pty
import select
import shlex
import shutil
import struct
import subprocess
import tempfile
import termios
import time


REPO = Path(__file__).resolve().parent.parent
# Keep commands on one line and the rendered frame close to 16:9.
COLS, ROWS = 100, 26
PLAYBACK_SPEED = 1.55 * 1.4 * 1.1
TAB_PLAYBACK_SPEED = 1.55 * 1.2 * 1.1


def prepare_shell(work, env, name):
    ready, executed = work / "ready", work / "executed"
    buffer = work / "buffer"
    resolved = work / "resolved"
    home = Path(env["HOME"])
    executable = shutil.which(name, path=env["PATH"])
    env = dict(env, TERM="xterm-256color", SHELL=executable, HISTFILE="/dev/null", INPUTRC="/dev/null")
    install = f"gopick -install-completion {name}"
    if name == "bash":
        rc = home / ".bashrc"
        rc.write_text(
            "PS1='\\[\\e[36m\\]gopick\\[\\e[0m\\] \\[\\e[90m\\]$\\[\\e[0m\\] '\n"
            "HISTFILE=/dev/null\nHISTCONTROL=\n"
            "demo_prompt=${demo_prompt:-0}\n"
            "_demo_prompt() { local result=$?; hash -r; "
            f"type -P gopick > {shlex.quote(str(resolved))}; "
            f"fc -ln -1 > {shlex.quote(str(executed))}; "
            f"printf '%s %s\\n' \"$((++demo_prompt))\" \"$result\" > {shlex.quote(str(ready))}; }}\n"
            "PROMPT_COMMAND=_demo_prompt\n"
            "bind 'set bell-style none'\n"
            "bind 'set show-all-if-ambiguous on'\n"
            "bind 'set menu-complete-display-prefix on'\n"
            "bind 'set page-completions off'\n"
            "bind 'set completion-query-items 0'\n"
            "bind '\"\\t\": menu-complete'\n"
            f"bind -x '\"\\C-x\\C-g\": printf \"%s\" \"$READLINE_LINE\" > {buffer}'\n"
        )
        return env, [executable, "--noprofile", "--rcfile", str(rc), "-i"], [rc], install
    if name == "fish":
        rc = work / "init.fish"
        rc.write_text(
            "set -g fish_greeting\nset -g fish_history ''\nset -g fish_autosuggestion_enabled 0\n"
            "set -g fish_color_command normal\nset -g fish_color_param normal\n"
            "set -g fish_color_valid_path normal\nset -g fish_color_comment brblack\n"
            "set -g fish_pager_color_selected_background --background=184070\n"
            "set -g fish_pager_color_selected_completion white --bold\n"
            "function fish_prompt\n    set_color cyan\n    printf gopick\n    set_color brblack\n    printf ' $ '\n    set_color normal\nend\n"
            "set -g demo_prompt 1\n"
            f"printf '1 0\\n' > {shlex.quote(str(ready))}\n"
            "function _demo_preexec --on-event fish_preexec\n"
            f"    printf '%s' \"$argv[1]\" > {shlex.quote(str(executed))}\nend\n"
            "function _demo_postexec --on-event fish_postexec\n    set -l result $status\n"
            f"    command -s gopick > {shlex.quote(str(resolved))}\n"
            "    set -g demo_prompt (math $demo_prompt + 1)\n"
            f"    printf '%s %s\\n' $demo_prompt $result > {shlex.quote(str(ready))}\nend\n"
            f"bind ctrl-x,ctrl-g 'commandline -b > {buffer}'\n"
        )
        files = [home / ".config" / "fish" / "completions" / f"{command}.fish" for command in ["go", "gopick"]]
        return env, [executable, "--no-config", "-i", "--init-command", "source " + shlex.quote(str(rc))], files, install
    rc = home / ".zshrc"
    rc.write_text(
        "bindkey -e\n"
        "setopt AUTO_MENU INTERACTIVE_COMMENTS\n"
        "unsetopt BEEP PROMPT_SP\n"
        "autoload -Uz compinit && compinit -D -i\n"
        "zmodload zsh/complist\n"
        "zstyle ':completion:*' menu select\n"
        "zstyle ':completion:*' list-colors 'ma=1;38;2;255;255;255;48;2;24;64;112'\n"
        "bindkey -M menuselect '^M' .accept-line\n"
        "LISTMAX=1000\n"
        "HISTFILE=/dev/null\n"
        "PROMPT='%F{cyan}gopick%f %F{244}$%f '\n"
        "RPROMPT=''\n"
        "integer demo_prompt=${demo_prompt:-0}\n"
        "precmd() { local result=$?; rehash; "
        f"whence -p gopick > {shlex.quote(str(resolved))}; "
        f"print -r -- \"$((++demo_prompt)) $result\" > {shlex.quote(str(ready))}; }}\n"
        f"preexec() {{ print -r -- \"$1\" > {shlex.quote(str(executed))}; }}\n"
        f"zle-line-pre-redraw() {{ print -rn -- \"$BUFFER\" > {shlex.quote(str(buffer))}; }}\n"
        "zle -N zle-line-pre-redraw\n"
    )
    return env, [executable, "-d", "-i"], [rc], install


def record(work, env, local, name):
    ready, executed = work / "ready", work / "executed"
    buffer = work / "buffer"
    env, argv, files, install = prepare_shell(work, env, name)
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", ROWS, COLS, 0, 0))

    def attach():
        os.setsid()
        fcntl.ioctl(0, termios.TIOCSCTTY, 0)

    start = time.monotonic()
    events, transcript = [], bytearray()
    shell = subprocess.Popen(
        argv, cwd=REPO, env=env,
        stdin=slave, stdout=slave, stderr=slave, preexec_fn=attach,
    )
    os.close(slave)

    def drain(seconds):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            readable, _, _ = select.select([master], [], [], max(0, deadline - time.monotonic()))
            if not readable:
                break
            try:
                data = os.read(master, 65536)
            except OSError as error:
                if error.errno == errno.EIO:
                    break
                raise
            if not data:
                break
            if name == "fish":
                # Fish probes terminal capabilities before accepting input.
                # These are device replies, not simulated user keystrokes.
                if b"\x1b[0c" in data:
                    os.write(master, b"\x1b[?1;2c")
                if b"\x1b[6n" in data:
                    os.write(master, b"\x1b[1;1R")
            transcript.extend(data)
            events.append([round(time.monotonic() - start, 6), "o", data.decode("utf-8", "replace")])

    def state():
        if ready.exists():
            fields = ready.read_text().split()
            if len(fields) == 2:
                return tuple(map(int, fields))
        return 0, 0

    def prompt(previous):
        deadline = time.monotonic() + 120
        while time.monotonic() < deadline:
            drain(0.03)
            number, status = state()
            if number > previous:
                if status:
                    raise RuntimeError(f"Command exited {status}: {executed.read_text()}")
                return
        raise RuntimeError(f"{name} prompt timed out:\n" + transcript.decode("utf-8", "replace"))

    def send(value, delay=0.06):
        for char in value:
            os.write(master, char.encode())
            events.append([round(time.monotonic() - start, 6), "i", char])
            drain(delay)

    def enter(hold=2.5):
        previous = state()[0]
        send("\r", 0)
        prompt(previous)
        drain(hold)

    def command(value, hold=2.5):
        send(value)
        drain(0.8)
        enter(hold)

    def scene(value, clear=False):
        print(f"{time.monotonic() - start:5.1f}s  {value}", flush=True)
        if clear:
            send("\x0c")
            drain(0.5)

    def tab_pause(seconds):
        # Keep menu pauses slower than the rest of the exported recording.
        drain(seconds * PLAYBACK_SPEED / TAB_PLAYBACK_SPEED)

    def tab(hold=1.8):
        send("\t", 0)
        tab_pause(hold)

    def choose(candidate):
        if name != "zsh":
            # Readline/Fish keep their own menu state. Inspect the final buffer
            # only after cycling, so instrumentation cannot reset that state.
            steps = 5 if candidate == "-run=^TestInternal$" else 3
            for step in range(steps):
                tab(2.8 if step == 0 else 1.8)
            send("\x18\x07", 0)
            drain(0.1)
            words = shlex.split(buffer.read_text())
            if not words or words[-1] != candidate:
                raise RuntimeError(f"Tab did not select {candidate!r}: {buffer.read_text()}")
            tab_pause(1.2)
            if name == "fish":
                # Fish accepts the pager selection before executing the line.
                send("\r", 0)
                drain(0.1)
            return
        # Cycle the real completion menu; never type the selected result.
        for step in range(10):
            tab(2.8 if step == 0 else 1.8)
            if buffer.exists():
                try:
                    words = shlex.split(buffer.read_text())
                except ValueError:
                    continue
                if words and words[-1] == candidate:
                    tab_pause(1.2)
                    return
        raise RuntimeError(f"Tab did not select {candidate!r}: {buffer.read_text()}")

    try:
        prompt(0)
        drain(0.8)
        command("go install github.com/henvic/gopick@latest", hold=1.5)
        binary = Path(env["GOBIN"]) / "gopick"
        if not binary.is_file():
            raise RuntimeError("go install did not produce the demo binary")
        if local:
            command("go install . # preview unreleased changes", hold=1.5)
        if Path((work / "resolved").read_text().strip()).resolve() != binary.resolve():
            raise RuntimeError("The shell is not using the freshly installed demo binary")
        help_output = subprocess.check_output([str(binary), "-help"], stderr=subprocess.STDOUT, env=env)
        if b"-install-completion" not in help_output:
            raise RuntimeError("The published version has no installer yet; use --local for an explicit checkout preview")

        scene("Install completion")
        send(install)
        drain(0.8)
        previous, begin = state()[0], len(transcript)
        send("\r", 0)
        deadline = time.monotonic() + 20
        while b"Apply these changes? [y/N] " not in transcript[begin:]:
            drain(0.03)
            if time.monotonic() > deadline or state()[0] > previous:
                raise RuntimeError("Installer did not ask for confirmation:\n" + transcript[begin:].decode())
        if any(path.exists() and "# gopick completion" in path.read_text() for path in files):
            raise RuntimeError("Installer changed the startup file before confirmation")
        if b"source <(" in transcript[begin:] or b"# gopick completion" in transcript[begin:]:
            raise RuntimeError("Installer printed shell code instead of a file summary:\n" + transcript[begin:].decode())
        drain(5)
        send("y")
        drain(0.6)
        send("\r", 0)
        prompt(previous)
        drain(3)
        for path in files:
            if path.read_text().count("# gopick completion") != 1:
                raise RuntimeError("Installer did not add exactly one completion block")
            activation = "source ~/" + shlex.quote(str(path.relative_to(env["HOME"])))
            if activation.encode() not in transcript[begin:]:
                raise RuntimeError("Installer did not print the expected source command")
            command(activation, hold=3)

        scene("List tests")
        command("gopick", hold=3.5)

        scene("Scope the listing", clear=True)
        command("gopick ./example", hold=3)

        scene("Choose a test with Tab")
        send("go test ./example -run=")
        choose("-run=^TestInternal$")
        enter()
        selected = shlex.split(executed.read_text())

        # Recall the actual completed command, then append ordinary Go flags.
        send("\x1b[A", 0)
        drain(1)
        send(" -v -race -count=2")
        drain(0.8)
        enter(3.5)
        if shlex.split(executed.read_text()) != selected + ["-v", "-race", "-count=2"]:
            raise RuntimeError("History did not preserve the selected command")
        if transcript.count(b"=== RUN   TestInternal") != 2:
            raise RuntimeError("The verbose command did not run the selected test twice")
        short_events = list(events)

        scene("Choose a package with Tab", clear=True)
        send("gopick ")
        # Show the full menu, then narrow it before cycling package choices.
        tab(3)
        send("github.com/henvic/")
        choose("github.com/henvic/gopick/example")
        enter(3)
        if shlex.split(executed.read_text()) != ["gopick", "github.com/henvic/gopick/example"]:
            raise RuntimeError("Package completion failed")

        scene("Run and locate tests", clear=True)
        command("gopick TestInternal -v", hold=3)
        command("gopick -list TestInternal # locate without running", hold=4)
        command("gopick TestExternal -v -race -count=2", hold=2.5)
        if transcript.count(b"=== RUN   TestExternal") != 2:
            raise RuntimeError("The verbose external test command did not run twice")

        # Stop at the final prompt, without recording cleanup commands.
        return events, short_events
    finally:
        shell.terminate()
        try:
            shell.wait(timeout=2)
        except subprocess.TimeoutExpired:
            shell.kill()
            os.close(master)
            master = None
            shell.wait()
        if master is not None:
            os.close(master)


def write_cast(path, events, title, shell):
    header = {
        "version": 2, "width": COLS, "height": ROWS,
        "timestamp": int(time.time()), "title": title,
        "command": shell,
        "env": {"SHELL": shutil.which(shell), "TERM": "xterm-256color"},
    }
    with path.open("w") as output:
        output.write(json.dumps(header, ensure_ascii=False) + "\n")
        for timestamp, kind, data in events:
            entry = [round(timestamp / PLAYBACK_SPEED, 6), kind, data]
            output.write(json.dumps(entry, ensure_ascii=False) + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--shell", choices=["zsh", "bash", "fish"], default="zsh")
    parser.add_argument("--local", action="store_true", help="visibly install this checkout after @latest to preview unreleased changes")
    args = parser.parse_args()
    for tool in ["go", args.shell]:
        if not shutil.which(tool):
            raise SystemExit(f"Missing required tool: {tool}")
    # Isolate shell configuration while keeping Go's existing download/build
    # caches, so the real installation does not start from an empty cache.
    with tempfile.TemporaryDirectory(prefix="gopick-demo-", dir="/tmp") as directory:
        work = Path(directory)
        binary = work / "bin" / "gopick"
        binary.parent.mkdir()
        env = dict(os.environ, GOENV="off", GOFLAGS="", GOWORK="off", GOBIN=str(binary.parent))
        cache = json.loads(subprocess.check_output(["go", "env", "-json", "GOCACHE", "GOMODCACHE"], env=env))
        home = (work / "home").resolve()
        home.mkdir()
        env.update(cache, HOME=str(home), ZDOTDIR=str(home),
                   XDG_CONFIG_HOME=str(home / ".config"), XDG_DATA_HOME=str(home / ".local" / "share"),
                   XDG_CACHE_HOME=str(home / ".cache"))
        print("Preparing the demo and warming the example test cache...", flush=True)
        subprocess.run(["go", "test", "./example", "-run", "^$"], cwd=REPO, env=env, check=True)
        subprocess.run(["go", "test", "-race", "./example", "-run", "^$"], cwd=REPO, env=env, check=True)
        env["PATH"] = str(binary.parent) + os.pathsep + env["PATH"]
        events, short_events = record(work, env, args.local, args.shell)
        full = work / f"{args.shell}-autocomplete.cast"
        short = work / ("go-test-completion.cast" if args.shell == "zsh" else f"{args.shell}-go-test-completion.cast")
        write_cast(full, events, f"gopick in {args.shell}: complete, discover, and run Go tests", args.shell)
        write_cast(short, short_events, f"go test -run completion in {args.shell}", args.shell)

        for artifact in [full, short]:
            destination = REPO / "demo" / artifact.name
            shutil.copyfile(artifact, destination)
            print(f"Saved {destination.relative_to(REPO)} ({artifact.stat().st_size:,} bytes)", flush=True)


if __name__ == "__main__":
    main()
