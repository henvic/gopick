# gopick
[![Go Reference](https://pkg.go.dev/badge/github.com/henvic/gopick.svg)](https://pkg.go.dev/github.com/henvic/gopick)
[![Build Status](https://github.com/henvic/gopick/workflows/Tests/badge.svg)](https://github.com/henvic/gopick/actions?query=workflow%3ATests)
[![Coverage Status](https://coveralls.io/repos/henvic/gopick/badge.svg)](https://coveralls.io/r/henvic/gopick)

gopick adds test-name autocomplete to `go test -run` in Zsh, Bash, and Fish:

```text
go test -run=<Tab>
go test -run=Test<Tab>
go test ./... -run=Test<Tab>
```

After [setting up your shell](#shell-setup-for-autocompletion),
press **Tab** to choose a test,
then **Enter** to run it. Completion reads names from source without compiling
or running tests.

https://github.com/user-attachments/assets/489fcd09-3e80-440d-b8dc-332acd17ae18

Watch on asciinema: [Zsh](https://asciinema.org/a/1272748),
[Bash](https://asciinema.org/a/1272750), or [Fish](https://asciinema.org/a/1272751).

The [`gopick` command](#using-gopick) also finds tests across a project,
runs matches, and lists their source locations.

## Installation

```sh
go install github.com/henvic/gopick@latest
```

## Shell setup for autocompletion

Choose **your shell** below. The installer enables completion for both
`go test -run` and `gopick`. It shows which files it will change and asks for
confirmation before writing.

After confirming, run the `source` command to activate completion in your current
terminal. New terminals load it automatically.

### Zsh

```zsh
gopick -install-completion zsh
source ~/.zshrc
```

Uses `$ZDOTDIR/.zshrc` when `ZDOTDIR` is set, and initializes completion if needed.

### Bash

```bash
gopick -install-completion bash
source ~/.bashrc
```

Configures `~/.bashrc` and your login profile so completion works in both
login and non-login shells. It creates `~/.bash_profile` if no login profile exists.

### Fish

```fish
gopick -install-completion fish
source ~/.config/fish/completions/go.fish
source ~/.config/fish/completions/gopick.fish
```

Uses `$XDG_CONFIG_HOME/fish/completions` when configured, and preserves existing
Go completion.

### Setup options

Add `-completion-target go` to enable only `go test -run` completion, or
`-completion-target gopick` to enable only the `gopick` command's completion:

```sh
gopick -install-completion zsh -completion-target go
```

Run the installer again to change your selection, then source the files it prints.
Use `-completion-file path` for a custom Zsh or Bash startup file, or `-yes`
to approve installation without a prompt (required without a TTY).

<details>
<summary>Manual setup</summary>

Run the command for your shell to enable completion in the current terminal.
To make it permanent, add that same line to the indicated startup file, after
your completion framework and Go completion plugins.

Zsh — `~/.zshrc` (or `$ZDOTDIR/.zshrc`):

```zsh
source <(gopick -completion zsh)
```

Bash — `~/.bashrc`:

```bash
source <(gopick -completion bash)
```

For Bash login shells, make sure your login profile also sources `~/.bashrc`.

Fish — `~/.config/fish/config.fish` (or `$XDG_CONFIG_HOME/fish/config.fish`):

```fish
gopick -completion fish | source
```

These commands also accept `-completion-target go` or `-completion-target gopick`.
After editing a startup file, source it to apply the change to your current shell.

</details>

## Using `go test -run`

Type a prefix and press **Tab**. `<Tab>` means the key, not literal text:

```text
go test -run=Test<Tab>
go test ./example -run=Fuzz<Tab>
go test ./example -run=Example<Tab>
go test ./... -run=Test<Tab>
```

Completion searches the current package by default. Give it a package path or
`./...` to search more packages. Both `-run=value` and `-run value` work;
completion respects `-C`, build tags, and other Go build flags.

Choosing `TestInternal` completes an exact top-level match. You can then add
ordinary Go test flags:

```sh
go test ./example -run='^TestInternal$' -v -race -count=2
```

Completion suggests top-level tests, runnable examples, and fuzz targets.
Subtest names must be entered manually; Go discovers them when the parent test runs.

## Using gopick

Run these from a Go project. Without a regexp, `gopick` lists tests; with one,
it runs matching tests. `-list` always lists without running.

```sh
gopick                              # list tests in ./...
gopick ./example                    # list tests in one package
gopick TestInternal                 # run matching tests across ./...
gopick -list TestInternal           # show matching names and source locations
gopick -list '^Example'             # list runnable examples
gopick -list '^Fuzz'                # list fuzz tests
gopick -cmd '^TestInternal$'        # print the "go test" command without running it
gopick ./example FuzzBase64RoundTrip -fuzz=FuzzBase64RoundTrip -fuzztime=10s # fuzz for 10s
gopick -C /path/to/mod              # list tests in another module
```

The optional package path limits which packages are searched. It accepts local
paths such as `./example` and import paths such as `github.com/henvic/gopick/example`.
The regexp matches test names using the same rules as `go test -run`.

Press **Tab** after `gopick` to complete flags, packages, and test names.
Selecting a test inserts an exact regexp such as `'^TestInternal$'`, running
that top-level test and its subtests. Manually typed regexps keep their usual
behavior: `gopick TestInternal` also matches names such as `TestInternalError`.

### Passing Go test flags

Put gopick flags **before** the package path and regexp, and Go test flags **after**:

```sh
gopick TestExternal -v -race -count=2
gopick -tags integration Database -v
gopick -cmd ./example TestInternal -timeout 30s
gopick TestInternal -- -args -custom 'a value with spaces'
```

Go test flags are forwarded unchanged, including flags gopick does not recognize.
The optional `--` marks where forwarding starts.

Build flags such as `-tags`, `-race`, and `-overlay` also apply to discovery.
Keep `-C` before the package path and regexp.
Test output is streamed, and test failures retain their exit status.

## For scripts and AI agents

Get test names, source locations, and runnable commands without executing tests:

```sh
gopick -format json ./...
gopick -format json ./... '(?i)parse'
```

Output is JSON Lines. Use `file` and `line` to inspect a test, then execute its
`command` argument array with `dir` as the working directory. Each command
selects that exact top-level test and its subtests; no shell parsing is needed.
No matches produce no output and exit successfully.

For custom listings, use a Go template. `-format-fields` lists the available
fields, types, and descriptions as JSON Lines:

```sh
gopick -format-fields
gopick -format '{{.Test}}' ./example
gopick -format 'table {{.Test}}\t{{.Location}}' ./example
```

`-format` always lists without running tests, even when its value is empty.

## What gopick finds

gopick reads test source selected by `go list`, respecting build constraints,
build tags, and file overlays. It finds internal and external tests, fuzz
targets, and examples. Subtests and benchmarks are not indexed.

Run `gopick -help` for flags.

> [!WARNING]
> Most of the code here is AI generated. It has been verified to work
> correctly, but it is not human handcrafted.
