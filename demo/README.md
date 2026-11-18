# Shell demos

Watch on asciinema: [Zsh](https://asciinema.org/a/1272748),
[Bash](https://asciinema.org/a/1272750), or [Fish](https://asciinema.org/a/1272751).

The README embeds the [Zsh MP4](https://github.com/user-attachments/assets/489fcd09-3e80-440d-b8dc-332acd17ae18),
hosted in [issue #1](https://github.com/henvic/gopick/issues/1). It opens with a
one-second preview of the completion menu at 16 seconds, then plays the full demo
from the beginning. Recordings (`.cast`) and MP4s are ignored by Git; only the Zsh MP4
is uploaded as a GitHub attachment.

Replay a terminal recording with asciinema:

```sh
asciinema play demo/zsh-autocomplete.cast
```

Each demo installs gopick, runs `gopick -install-completion` for its shell, pauses
on the file summary, confirms with `y`, and runs the printed `source` command. It then lists
the project and example package tests, chooses a `go test -run` target with Tab,
and recalls the command with `-v -race -count=2`. After selecting the example
package with Tab, it runs `TestInternal` with `gopick`, locates it with `-list`, and runs
`TestExternal` with `-v -race -count=2`. Typing and command output play at 2.39×
speed; Tab-selection pauses use a gentler 2.05× pace. The shorter
recordings (`go-test-completion.cast`, `bash-go-test-completion.cast`, and
`fish-go-test-completion.cast`) stop after the two `go test` runs.

The terminal is 100 columns by 26 rows, keeping commands on one line in a frame
close to 16:9. Three screen clears separate the longer sections; related commands
stay together on screen.

To regenerate the asciinema recordings from the repository root:

```sh
python3 demo/record.py
python3 demo/record.py --shell bash
python3 demo/record.py --shell fish
```

Requires Go and the selected shell on `PATH`.
The Bash recording uses Bash 5's Readline `menu-complete` binding to
cycle candidates with Tab. Zsh and Fish use their highlighted completion menus.
The recorder really runs `go install github.com/henvic/gopick@latest` and normally
uses that published binary throughout. To preview an unpublished feature,
add `--local` to any command above: it visibly runs `go install .` after the published
installation and uses the checkout for the remaining commands.
The current recordings use the published binary throughout.

It opens an isolated shell session, sends real Tab keystrokes, and checks the
installer's confirmation, the sourced file, and the
selected test and package. All command output is real.
Installation uses a temporary `GOBIN` and home directory, with `ZDOTDIR` and XDG
directories inside that home. This shows the usual `~/.zshrc`, `~/.bashrc`, and
Fish completion paths. Your shell configuration and installed gopick stay untouched.
The recorder produces `.cast` files only. The existing MP4s are kept locally;
regenerating the recordings does not update them.

## Preparing the GitHub video

For the existing Zsh MP4, use FFmpeg to prepend its frame at 16 seconds as a
one-second preview:

```sh
ffmpeg -y -v error -ss 16 -i demo/zsh-autocomplete.mp4 \
  -frames:v 1 /tmp/gopick-zsh-poster.png
ffmpeg -y -v error -loop 1 -framerate 24 -i /tmp/gopick-zsh-poster.png \
  -i demo/zsh-autocomplete.mp4 \
  -filter_complex '[0:v]trim=duration=1,setpts=PTS-STARTPTS[preview];[1:v]setpts=PTS-STARTPTS[demo];[preview][demo]concat=n=2:v=1:a=0[v]' \
  -map '[v]' -r 24 -c:v libx264 -crf 18 -preset slow \
  -pix_fmt yuv420p -movflags +faststart demo/zsh-autocomplete-github.mp4
```

Upload `zsh-autocomplete-github.mp4` as a GitHub issue attachment and use its
`github.com/user-attachments/assets/…` URL on its own line in the README.
