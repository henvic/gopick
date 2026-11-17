# gopick
[![Go Reference](https://pkg.go.dev/badge/github.com/henvic/gopick.svg)](https://pkg.go.dev/github.com/henvic/gopick)
[![Build Status](https://github.com/henvic/gopick/workflows/Tests/badge.svg)](https://github.com/henvic/gopick/actions?query=workflow%3ATests)
[![Coverage Status](https://coveralls.io/repos/henvic/gopick/badge.svg)](https://coveralls.io/r/henvic/gopick)

gopick adds autocomplete for Go tests in zsh, bash, and fish:

```text
go test -run=<Tab>
go test -run=Test<Tab>
go test ./... -run=Test<Tab>
```

After [setting up your shell](#shell-setup), type one of these commands inside a
Go project and press **Tab** where shown. Choose a test, such as `TestParse`,
and the completed command will be equivalent to:

```sh
go test -run='^TestParse$'
```

Press **Enter** to run it. The `^` and `$` anchors select that exact top-level
test and its subtests. Completion uses the current package by default; add
`./...` to look through the package tree. It reads test names from source
without compiling or running tests.

[![gopick in Zsh: completing and running Go tests](demo/zsh-autocomplete.gif)](demo/zsh-autocomplete.mp4)

Also watch the [Bash demo](demo/bash-autocomplete.mp4) or [Fish demo](demo/fish-autocomplete.mp4).

You can also use the [`gopick` command](#using-gopick) to find tests across a
project, run matches, or print a command to copy.

## Installation

```sh
go install github.com/henvic/gopick@latest
```

Make sure Go's binary directory (`$(go env GOPATH)/bin`, or your custom `GOBIN`)
is on your `PATH`, so `gopick -help` works in your terminal.

## Shell setup

Set this up once for your shell; it works in every Go project. The installer
enables `go test -run` and `gopick` completion by default. Run the command for
your shell:

```sh
gopick -install-completion zsh
gopick -install-completion bash
gopick -install-completion fish
```

It lists the files to create or update, then asks `Apply these changes? [y/N]`.
Nothing is written until you confirm. It preserves surrounding settings and updates
its own marked block on subsequent runs. Follow the printed `source` command to activate
completion in your current shell, or open a new terminal.

Zsh uses `$ZDOTDIR/.zshrc` (or `~/.zshrc`); the generated script initializes
completion if needed.
Bash configures `~/.bashrc` and its active login profile. Fish installs both
`go.fish` and `gopick.fish` under `$XDG_CONFIG_HOME/fish/completions` (normally
`~/.config/fish/completions`), preserving existing Go completion. Either command
can be completed first.

An explicitly empty `ZDOTDIR` is rejected; unset it to use `~/.zshrc`, or choose
a file with `-completion-file`.

To enable only one integration, include `-completion-target go` or
`-completion-target gopick`. Re-running the installer with another target updates
the setup. For a custom Zsh or Bash startup file, use `-completion-file path`.
Without a TTY, installation requires an explicit `-yes`; piped input cannot
approve changes. The interactive confirmation check uses `sh`.

Alternatively, configure completion manually using the instructions below.
Load gopick after your shell's completion framework and any Go completion plugins.

`source` loads the generated script into your shell. Adding the line to your
shell's startup file keeps completion enabled in new terminals; sourcing that
file activates it in the terminal you already have open.

### Zsh

Add this line to **`~/.zshrc`**, after your completion plugins:

```zsh
source <(gopick -completion zsh)
```

The script reuses your existing completion setup, or initializes it with
`compinit` if needed.

Then run this in your terminal to reload the file:

```zsh
source ~/.zshrc
```

You can also append the gopick line from the terminal. Run this once instead of
adding it by hand:

```zsh
printf '%s\n' 'source <(gopick -completion zsh)' >> ~/.zshrc
source ~/.zshrc
```

### Bash

Add this line to **`~/.bashrc`**, after `bash-completion` or any Go completion
plugin you use:

```bash
source <(gopick -completion bash)
```

Then reload it in your terminal:

```bash
source ~/.bashrc
```

For Bash login shells, make sure **`~/.bash_profile`** also loads `~/.bashrc`.
Add this if it does not:

```bash
if [ -f ~/.bashrc ]; then
  source ~/.bashrc
fi
```

### Fish

Add this line to **`~/.config/fish/config.fish`**:

```fish
gopick -completion fish | source
```

Then reload it in your terminal:

```fish
source ~/.config/fish/config.fish
```

### Choosing what to complete

To enable only one integration, add a `-completion-target` flag to the `gopick`
command in your startup file:

| Flag | Completion enabled |
| --- | --- |
| `-completion-target go` | Test names for `go test -run` only |
| `-completion-target gopick` | Flags, package paths, and test names for `gopick` only |

For example, to enable only `go test -run` completion in Zsh:

```zsh
source <(gopick -completion zsh -completion-target go)
```

In Bash:

```bash
source <(gopick -completion bash -completion-target go)
```

In Fish:

```fish
gopick -completion fish -completion-target go | source
```

Replace your existing gopick setup line, then source the startup file again.
Remove the flag to restore the default. After updating gopick, source the file
again to load the updated script.

To try a setup for the current terminal only, run its completion line directly
instead of adding it to a startup file.

## Using `go test -run`

With completion loaded, type a test name prefix and press **Tab** (`<Tab>`
below means the key, not literal text):

```text
go test -run=Test<Tab>
go test -run Test<Tab>
go test ./example -run=Test<Tab>
go test ./example -run=Fuzz<Tab>
go test ./example -run=Example<Tab>
go test ./... -run=Test<Tab>
go -C /path/to/project test -tags=integration -run=Test<Tab>
```

Both `-run=value` and `-run value` work, as do Go's `-test.run` spellings.
The shell adds quoting as needed. Completion respects package arguments, `-C`,
build tags, and build settings such as `-race`, `-mod`, `-buildmode`, and `-pkgdir`.
All shells use the same Go flag parser, including flags with separate values.
Zsh and Bash also consider package arguments after the cursor. Other Go completion
positions keep their existing behavior.

Go treats `-run` as a case-sensitive regexp. You can also write one yourself:

```sh
go test -run=TestParse                 # TestParse, TestParseError, ...
go test -run='^TestParse$'             # exact top-level test and its subtests
go test -run='TestParse|TestEncode'    # either group of tests
go test -run='(?i)parse'               # ignore case
go test -run='^TestParse$/^valid$'     # a particular subtest
```

Quote regexps containing shell metacharacters. Completion suggests top-level
tests, fuzz targets, and runnable examples. Enter subtest names and more complex
regexps manually. Go runs the parent test to discover its subtests, even when no
child matches the selector.

## Using gopick

Run `gopick` inside the Go project you want to search:

```sh
gopick                              # list tests in ./...
gopick TestParse                     # run matching tests
gopick '^TestParse$'                 # run an exact test name
gopick -list '(?i)parse'             # list matching names and source locations
gopick ./example                    # list tests in this package
gopick ./example Fuzz               # run matching fuzz targets' seed corpora
gopick ./example '^Example$'        # run an example and check its output
gopick ./example/... External       # run matches within a package tree
gopick -cmd '^TestParse$'            # print the command without running it
gopick -C /path/to/project TestParse
gopick -tags integration Database
```

Put gopick flags before positional arguments and `go test` flags after them.
An optional first positional path selects
the packages to search: `.`, `./example`, `./...`, `../other/...`, or a
domain-qualified import path such as `example.com/project`. Without one, the
scope is `./...`. Other import paths containing `/` can be used with an explicit
regexp, for example `gopick net/http '^TestRead$'`. Use `-C` to select the search
directory.

Provide one regexp using the same rules as `go test -run`, including `/` for
subtests. It matches test names; package and file names are not part of the match.

Without a regexp, tests in the selected packages are listed, even if only one
test exists. An explicit empty regexp (`gopick ''`) runs all discovered
tests. Use `-list` to display matches without running them.

For custom listings, `--format` accepts a Go template for each matching test.
Prefix it with `table ` for aligned columns and headers. Discover the available
fields, types, and descriptions with `-format-fields` (JSON Lines):

```sh
gopick -format-fields
gopick --format '{{.Test}}' ./example
gopick --format 'table {{.Test}}\t{{.Location}}' ./example
```

`-format-fields` works without a Go project or the Go toolchain. `--format` always
lists, even with a regexp; it never runs tests. An empty format or `table` selects
the default table. Literal `\t` and `\n` are supported, along with `json`, `upper`,
`lower`, `split`, `join`, and Go template built-ins such as `printf` and `println`.

Execution passes the regexp unchanged to `go test -run`, selecting each package
with a matching top-level test once. Multiple matches run together. The command
includes build tags and forwarded arguments, and uses Go's normal test caching
behavior unless overridden with a flag such as `-count=1`.
The process runs in the search directory
(the current directory by default, or the directory selected with `-C`). A selected
test's subtests and the package's normal setup (`init` and `TestMain`) still run.
Test output is streamed to the terminal and failures retain their exit status.

Listing a subtest selector shows matching top-level parents; it does not
confirm which children exist.

Fuzz targets run their seed corpus by default, following
[Go's `-run` behavior](https://pkg.go.dev/cmd/go#hdr-Testing_flags). To start
active fuzzing, forward `-fuzz` and optionally limit its duration:

```sh
gopick ./example FuzzBase64RoundTrip -fuzz=FuzzBase64RoundTrip -fuzztime=10s
```

Examples are runnable when they have an `Output:` or `Unordered output:` comment,
including an explicitly empty output comment. Examples without an output
comment are documentation only and are omitted from listings and completion.

With gopick completion enabled, press **Tab** for flags, packages, and test names:

```text
gopick Test<Tab>
gopick -list Test<Tab>
gopick ./<Tab>
gopick ./example Test<Tab>
```

The `gopick` command completes literal names: choosing `TestParse` retains its
regexp meaning and also matches `TestParseError`. A leading `^` is preserved;
add `$` for an exact match. Suggestions use the selected package scope, `-C`,
and `-tags`. Zsh delegates forwarded Go flags to your existing Go completer.

### Filtering by kind

Use the regexp to filter by Go's function-name prefixes. `^` anchors the match
at the beginning of the name, and `|` combines alternatives:

```sh
gopick -list '^Test'             # regular tests only
gopick -list '^Example'          # runnable examples only
gopick -list '^Fuzz'             # fuzz targets only
gopick -list '^(Test|Example)'    # regular tests and runnable examples
```

Keep the regexp quoted so your shell passes `|` to gopick instead of treating
it as a pipeline. `-list` displays matches without running them; omit it to run
the matches. Without a regexp, gopick lists all supported kinds. Benchmark
functions are not currently indexed.

### Printing a command

`-cmd` prints the full invocation to stdout, ready to copy into your shell,
including all selected packages and the unchanged regexp. It does not compile
or execute tests. No matching top-level tests returns an error and leaves
stdout empty. Commands normally start with `go test`. When
`-C` selects another directory, displayed commands use
`go -C /path/to/project test ...` so they work in Bash, Zsh, and Fish without
changing the shell's directory.

```sh
gopick -cmd ./internal/parser '^TestParse$'
gopick -cmd TestRoundTrip
```

`-list` can be combined with `--format`, but not with `-cmd`.

## Forwarding Go flags

After the package path and regexp, the first flag starts the arguments passed
to `go test`. An optional `--` separator makes the boundary explicit. The
arguments are appended unchanged to the generated command; both `-flag=value`
and `-flag value` work, and forwarding does not depend on a list of known flags.

```sh
gopick TestParse -race -short -count=1 -v
gopick TestParse -- -coverprofile 'coverage file.out'
gopick -cmd ./example External -timeout 30s -json
gopick TestParse -- -args -custom 'a value with spaces'
```

`-args` passes the remaining arguments to the test binary using Go's normal
rules. Only the initial gopick `--` separator is removed; subsequent separators
are preserved. Flags before the selector belong to gopick, so `gopick -list Test`
lists source locations, while `gopick Test -list .` invokes Go's own listing.
Use `gopick '' -v` to run all discovered tests with forwarded flags. Without a
regexp, gopick still lists tests.

Forwarded `-tags`, `-race`, `-msan`, `-asan`, `-compiler`, `-buildmode`,
`-installsuffix`, `-pkgdir`, `-linkshared`, `-mod`, `-modcacherw`, `-modfile`, and
`-overlay` also apply to discovery.
Later flags override earlier settings using Go's rules, including `-tags=` to
clear inherited tags. Keep `-C` before the selector so discovery and execution
use the same directory.

`-cmd`, template `.Command`, and JSON `command` include the forwarded arguments.
An explicit forwarded `-run` or `-test.run` overrides the generated runtime
filter; it does not change which packages gopick selected. Benchmarks are not
indexed, even when benchmark flags are forwarded.

## For AI agents

Discover tests and their source locations without compiling or running them:

```sh
gopick --format json ./...
gopick --format json ./... '(?i)parse'
```

Output is JSON Lines, with `name`, `importPath`, `package`, `file`, `line`, `dir`,
and `command` in each record. Inspect the absolute `file` and its `line` to choose
relevant tests, then execute the `command` argument array with the working
directory set to `dir`. Each command selects that exact top-level test and its
subtests. No shell parsing is needed. Narrow the package scope and regexp to
keep results focused. Subtests are not enumerated; no matches produce no output
and exit successfully.

`--format json` and `--format '{{json .}}'` emit the same raw records. Template
fields listed by `-format-fields` use display values instead, such as relative
filenames and shell-quoted commands.

## Discovery

gopick reads test source files selected by `go list -find`, without compiling
test binaries or resolving package imports. It respects the Go environment,
build constraints, `-tags`, and Go file overlays supplied through `GOFLAGS` or
forwarded `-overlay` arguments. Omitting `-tags` inherits Go's settings; an
explicit `-tags=` clears inherited tags for discovery, completion, and execution.

Discovery includes internal and external tests (`package foo` and
`package foo_test`). It indexes top-level `Test…` and `Fuzz…` functions, plus
runnable `Example…` functions. Subtests and benchmarks are not indexed.

Listings show the package declaration from the test's source file, so internal
and external tests can show `example` and `example_test`. Try `gopick ./example`
in this checkout to see both. Import paths are available through custom
formatting and JSON. Commands normally pass those paths to `go test`; local
packages outside GOPATH use runnable paths such as `.` or `./sub` instead of
Go's synthetic `_/...` identifiers.

A file scope such as `gopick ./example/internal_test.go TestInternal` preserves
that file in generated commands. Like `go test file.go`, it includes only the
named files, not the rest of their directory.

## Development

```sh
go test ./...
go vet ./...
```

The shell scripts use `-complete tests` and `-complete packages` for sorted,
deduplicated candidates. `-complete arguments` combines both for the first
positional argument. `-complete go-tests` accepts Go package arguments and
defaults to the current package. All three shells use the internal
`-complete shell -cursor N -- command words...` adapter, where `N` is the
zero-based index of the word being completed, for Go command lines.

`TestGoFlags` checks the shared flag metadata against the installed toolchain's
build/test help and registered test-binary flags. New flags or changes to which
flags take values fail this check, so Go upgrades expose compatibility gaps.

> [!WARNING]
> Most of the code here is AI generated. It has been verified to work
> correctly, but it is not human handcrafted.
