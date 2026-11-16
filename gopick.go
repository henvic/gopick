// gopick finds Go tests from source and runs matching tests.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type test struct {
	Name       string   `json:"name"`
	ImportPath string   `json:"importPath"`
	Package    string   `json:"package"`
	File       string   `json:"file"`
	Line       int      `json:"line"`
	Dir        string   `json:"dir"`
	Command    []string `json:"command"`
	files      []string // Original file arguments for a command-line-arguments package.
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "gopick:", err)
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() > 0 {
			os.Exit(exit.ExitCode())
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("gopick", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dir := flags.String("C", ".", "search from this directory")
	// nil inherits Go's defaults; an explicit empty value clears inherited tags.
	var tags *string
	flags.Func("tags", "Go build tags", func(value string) error {
		tags = &value
		return nil
	})
	format := flags.String("format", "", "list as json or a Go template; prefix with table for headers")
	fields := flags.Bool("format-fields", false, "print template field names, types, and descriptions as JSON Lines")
	list := flags.Bool("list", false, "list matching tests without running them")
	printCommand := flags.Bool("cmd", false, "print the selected go test command without running it")
	completion := flags.String("completion", "", "print a shell completion script (zsh, bash, or fish)")
	completionTarget := flags.String("completion-target", "both", "enable completion for go, gopick, or both (with -completion)")
	complete := flags.String("complete", "", "print completion candidates (tests, packages, arguments, go-tests, or shell)")
	cursor := flags.Int("cursor", -1, "current word index for -complete shell")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: gopick [flags] [package-pattern] [regexp] [--] [go-test-args...]")
		fmt.Fprintln(stderr, "\nAn optional leading path such as ./example or ./... selects packages (default ./...).")
		fmt.Fprintln(stderr, "\nUse the same case-sensitive regexp as go test -run, including / for subtests.")
		fmt.Fprintln(stderr, "A regexp runs all matching tests. Use -list to list matching top-level tests instead.")
		fmt.Fprintln(stderr)
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *fields {
		return printFormatFields(stdout)
	}
	if *completion != "" {
		return printCompletionScript(stdout, *completion, *completionTarget)
	}
	var completionTargetSet, formatSet bool
	flags.Visit(func(f *flag.Flag) {
		completionTargetSet = completionTargetSet || f.Name == "completion-target"
		formatSet = formatSet || f.Name == "format"
	})
	if completionTargetSet {
		return errors.New("-completion-target requires -completion")
	}
	if formatSet && (*printCommand || *complete != "") {
		return errors.New("-format cannot be used with -cmd or -complete")
	}
	if *list && (*printCommand || *complete != "") {
		return errors.New("-list cannot be used with -cmd or -complete")
	}
	if *complete != "" {
		if *complete != "tests" && *complete != "packages" && *complete != "arguments" && *complete != "go-tests" && *complete != "shell" {
			return errors.New("-complete must be tests, packages, arguments, go-tests, or shell")
		}
		if *printCommand {
			return errors.New("-complete cannot be used with -cmd")
		}
	}
	if *complete == "shell" {
		return completeShell(ctx, stdout, flags.Args(), *cursor)
	}
	if *complete == "go-tests" {
		patterns := flags.Args()
		if len(patterns) == 0 {
			patterns = []string{"."}
		}
		absolute, err := filepath.Abs(*dir)
		if err != nil {
			return err
		}
		tests, err := discover(ctx, absolute, patterns, tags)
		if err != nil {
			return err
		}
		return printCompletions(stdout, tests, "tests", absolute)
	}
	positional, testArgs := splitTestArgs(flags.Args())
	pattern, expression, supplied, err := searchArgs(positional)
	if err != nil {
		return err
	}
	build, err := discoveryFlags(testArgs)
	if err != nil {
		return err
	}
	filter, err := testFilter(expression)
	if err != nil {
		return err
	}
	absolute, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	tests, err := discover(ctx, absolute, []string{pattern}, tags, build...)
	if err != nil {
		return err
	}
	matches := search(tests, filter)
	for i := range matches {
		matches[i].Command = append(matches[i].Command, testArgs...)
	}
	if *complete != "" {
		return printCompletions(stdout, matches, *complete, absolute)
	}
	if formatSet {
		return printTests(stdout, matches, absolute, cwd, *format)
	}
	if len(matches) == 0 {
		return errors.New("no tests match")
	}
	shouldRun := !*list && supplied
	if !shouldRun && !*printCommand {
		return printTests(stdout, matches, absolute, cwd, *format)
	}
	selected := matches[0]
	selected.Command = append(testCommand(matches, expression, tags), testArgs...)
	if *printCommand {
		_, err := fmt.Fprintln(stdout, selected.shellCommand(cwd))
		return err
	}
	fmt.Fprintln(stderr, selected.shellCommand(cwd))
	command := selected.Command
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = selected.Dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	return cmd.Run()
}

// flag.Parse stops at the first positional argument. The next flag starts the
// Go arguments, whose spelling, values, and order are left to go test. A leading
// -- still works with flag.Parse to escape a regexp beginning with a dash.
func splitTestArgs(args []string) (positional, forwarded []string) {
	for i, arg := range args {
		if i > 0 && strings.HasPrefix(arg, "-") && arg != "-" {
			positional, forwarded = args[:i], args[i:]
			if arg == "--" {
				forwarded = forwarded[1:]
			}
			return positional, forwarded
		}
	}
	return args, nil
}

// Only discovery needs to understand flags. Forwarding itself has no allowlist,
// so new Go flags and custom test flags work without updating gopick. Consume
// known values even when they look like flags, and stop where go test stops
// interpreting its own options (-args, --, or a literal test argument).
func discoveryFlags(args []string) ([]string, error) {
	var build []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" || arg == "-args" || arg == "--args" || arg == "-" || !strings.HasPrefix(arg, "-") {
			break
		}
		name, _, equals := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-"), "=")
		if name == "C" {
			return nil, errors.New("put -C before the package path and regexp so discovery and go test use the same directory")
		}
		info, known := goFlag(name)
		value := info.takesValue
		if !known || info.testOnly {
			// Like go test, an unknown flag may consume the next non-flag
			// word. This includes flags known only to the test binary:
			// flag-shaped words are still interpreted by the Go command.
			value = i+1 < len(args) && (args[i+1] == "-" || !strings.HasPrefix(args[i+1], "-"))
		}
		if info.discover {
			build = append(build, arg)
		}
		if value && !equals {
			i++
			if i == len(args) {
				return nil, fmt.Errorf("flag needs an argument: %s", arg)
			}
			if info.discover {
				build = append(build, args[i])
			}
		}
	}
	return build, nil
}

type goFlagInfo struct {
	takesValue bool // Whether the flag's consumer expects a value.
	discover   bool // Also pass the flag to go list when selecting source files.
	testOnly   bool // Unknown to the Go command: closes its package list and uses unknown-flag parsing.
}

// One flag inventory for discovery, overlays, and all three shell adapters.
// TestGoFlags checks it against the installed Go toolchain. This is parsing
// metadata, not a forwarding allowlist: all Go arguments are passed unchanged.
// Completion falls back to the existing Go completer if a flag is unfamiliar.
func goFlag(name string) (goFlagInfo, bool) {
	switch name {
	case "race", "msan", "asan", "linkshared", "modcacherw":
		return goFlagInfo{discover: true}, true
	case "tags", "mod", "modfile", "compiler", "buildmode", "installsuffix", "pkgdir", "overlay":
		return goFlagInfo{takesValue: true, discover: true}, true
	case "C", "asmflags", "gcflags", "gccgoflags", "ldflags", "pgo", "toolexec", "p", "o", "exec", "vet", "covermode", "coverpkg":
		return goFlagInfo{takesValue: true}, true
	case "a", "n", "x", "work", "trimpath", "buildvcs", "c", "json", "cover", "h", "help":
		return goFlagInfo{}, true
	// Undocumented Go command diagnostics still consume their arguments.
	case "debug-actiongraph", "debug-runtime-trace", "debug-trace":
		return goFlagInfo{takesValue: true}, true
	// Test-binary flags without an unprefixed alias in the Go command.
	case "test.gocoverdir", "test.testlogfile", "test.fuzzcachedir":
		return goFlagInfo{takesValue: true, testOnly: true}, true
	case "test.paniconexit0", "test.fuzzworker":
		return goFlagInfo{testOnly: true}, true
	}
	switch strings.TrimPrefix(name, "test.") {
	case "bench", "benchtime", "count", "cpu", "fuzz", "fuzztime", "fuzzminimizetime", "list", "outputdir", "parallel", "run", "skip", "shuffle", "timeout", "blockprofile", "blockprofilerate", "coverprofile", "cpuprofile", "memprofile", "memprofilerate", "mutexprofile", "mutexprofilefraction", "trace":
		return goFlagInfo{takesValue: true}, true
	case "v", "short", "failfast", "fullpath", "benchmem", "artifacts":
		return goFlagInfo{}, true
	}
	return goFlagInfo{}, false
}

// Local paths and domain-qualified import paths are unambiguous package scopes.
// Other import paths, such as net/http, need a second argument for the regexp.
// In particular, TestParse/valid is a test selector, not a package scope.
func searchArgs(args []string) (pattern, expression string, supplied bool, err error) {
	pattern = "./..."
	if len(args) > 0 && packagePattern(args[0]) {
		pattern, args = args[0], args[1:]
	} else if len(args) == 2 && strings.Contains(args[0], "/") && !strings.ContainsAny(args[0], `^$*+?()[]{}|\`) && !strings.HasPrefix(args[0], "Test") {
		pattern, args = args[0], args[1:]
	}
	if len(args) > 1 {
		return "", "", false, errors.New("expected one regexp, optionally preceded by a package path")
	}
	if len(args) == 1 {
		return pattern, args[0], true, nil
	}
	return pattern, "", false, nil
}

func packagePattern(value string) bool {
	if value == "." || value == ".." || value == "..." || strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../") {
		return true
	}
	host, _, slash := strings.Cut(value, "/")
	return slash && strings.Contains(host, ".") && !strings.HasPrefix(value, "Test") &&
		!strings.ContainsAny(value, `^$*+?()[]{}|\`)
}

func testCommand(tests []test, expression string, tags *string) []string {
	command := []string{"go", "test"}
	if tags != nil {
		command = append(command, "-tags", *tags)
	}
	seen := make(map[string]bool)
	for _, test := range tests {
		if !seen[test.ImportPath] {
			command = append(command, test.packageArgs()...)
			seen[test.ImportPath] = true
		}
	}
	return append(command, "-run", expression)
}

// Outside GOPATH, Go reports a synthetic _/absolute/path import path. Keep
// that identity in listings, but use a local path that go test can resolve.
func (t test) packageArgs() []string {
	if len(t.files) > 0 {
		return t.files
	}
	if !strings.HasPrefix(t.ImportPath, "_/") {
		return []string{t.ImportPath}
	}
	dir := filepath.Dir(t.File)
	relative, err := filepath.Rel(t.Dir, dir)
	if err != nil {
		return []string{dir}
	}
	if relative == "." {
		return []string{"."}
	}
	return []string{"./" + filepath.ToSlash(relative)}
}

// go list -find respects build constraints without resolving dependencies.
// Reading the selected test files avoids building a test binary for discovery.
func discover(ctx context.Context, dir string, patterns []string, tags *string, build ...string) ([]test, error) {
	args := []string{"list", "-find", "-json=Dir,ImportPath,Match,TestGoFiles,XTestGoFiles"}
	if tags != nil {
		args = append(args, "-tags", *tags)
	}
	args = append(args, build...)
	args = append(args, patterns...)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("discover packages: %w\n%s", err, strings.TrimSpace(stderr.String()))
	}
	overlay, err := readOverlay(ctx, dir, args[1:])
	if err != nil {
		return nil, err
	}
	tests := make([]test, 0)
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var pkg struct {
			Dir          string
			ImportPath   string
			Match        []string
			TestGoFiles  []string
			XTestGoFiles []string
		}
		if err := decoder.Decode(&pkg); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode packages: %w", err)
		}
		var files []string
		if pkg.ImportPath == "command-line-arguments" && len(pkg.Match) > 0 && strings.HasSuffix(pkg.Match[0], ".go") {
			// Go's synthetic import path cannot be passed back to go test.
			// Preserve every named file, including helpers without tests.
			files = pkg.Match
		}
		for _, name := range append(pkg.TestGoFiles, pkg.XTestGoFiles...) {
			filename := filepath.Join(pkg.Dir, name)
			fset := token.NewFileSet()
			var source any
			if backing, ok := overlay[filename]; ok {
				contents, err := os.ReadFile(backing)
				if err != nil {
					return nil, fmt.Errorf("read overlaid test %s: %w", filename, err)
				}
				source = contents
			}
			file, err := parser.ParseFile(fset, filename, source, parser.ParseComments|parser.SkipObjectResolution)
			if err != nil {
				return nil, err
			}
			// Use Go's own example parser so empty and unordered output
			// comments have the same meaning as they do for go test.
			examples := make(map[string]bool)
			for _, example := range doc.Examples(file) {
				if example.Output != "" || example.EmptyOutput {
					examples["Example"+example.Name] = true
				}
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil || fn.Type.TypeParams != nil {
					continue
				}
				if !isTest(fn) && !examples[fn.Name.Name] {
					continue
				}
				entry := test{
					Name: fn.Name.Name, ImportPath: pkg.ImportPath, Package: file.Name.Name,
					// Keep the physical filename and line together, ignoring //line directives.
					File: filename, Line: fset.PositionFor(fn.Pos(), false).Line,
					Dir: dir, files: files,
				}
				entry.Command = testCommand([]test{entry}, "^"+regexp.QuoteMeta(fn.Name.Name)+"$", tags)
				tests = append(tests, entry)
			}
		}
	}
	sort.Slice(tests, func(i, j int) bool {
		if tests[i].ImportPath != tests[j].ImportPath {
			return tests[i].ImportPath < tests[j].ImportPath
		}
		if tests[i].Package != tests[j].Package {
			return tests[i].Package < tests[j].Package
		}
		return tests[i].Name < tests[j].Name
	})
	return tests, nil
}

// Match the test and fuzz signatures used by go test, including aliased and
// dot imports of testing. Type checking is left to go test when a test is run.
func isTest(fn *ast.FuncDecl) bool {
	name := fn.Name.Name
	prefix, parameter := "Test", "T"
	if strings.HasPrefix(name, "Fuzz") {
		prefix, parameter = "Fuzz", "F"
	}
	if fn.Recv != nil || !strings.HasPrefix(name, prefix) {
		return false
	}
	if suffix := strings.TrimPrefix(name, prefix); suffix != "" {
		r, _ := utf8.DecodeRuneInString(suffix)
		if unicode.IsLower(r) {
			return false
		}
	}
	if fn.Type.TypeParams != nil ||
		(fn.Type.Results != nil && len(fn.Type.Results.List) != 0) ||
		len(fn.Type.Params.List) != 1 || len(fn.Type.Params.List[0].Names) > 1 {
		return false
	}
	ptr, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	switch typ := ptr.X.(type) {
	case *ast.Ident:
		return typ.Name == parameter
	case *ast.SelectorExpr:
		return typ.Sel.Name == parameter
	}
	return false
}

func search(tests []test, filter []*regexp.Regexp) []test {
	matches := make([]test, 0)
	for _, test := range tests {
		for _, pattern := range filter {
			if pattern.MatchString(test.Name) {
				matches = append(matches, test)
				break
			}
		}
	}
	return matches
}

// Match top-level tests using Go's testing package rules: slash separates
// subtests and | separates alternatives outside brackets and parentheses.
// Validate every component, but let go test discover and filter subtests.
func testFilter(expression string) ([]*regexp.Regexp, error) {
	var roots []*regexp.Regexp
	start, brackets, groups := 0, 0, 0
	top := true
	compile := func(end int) error {
		var sanitized strings.Builder
		for _, r := range expression[start:end] {
			switch {
			case unicode.IsSpace(r) || r == '\u200b':
				sanitized.WriteByte('_')
			case !strconv.IsPrint(r):
				quoted := strconv.QuoteRune(r)
				sanitized.WriteString(quoted[1 : len(quoted)-1])
			default:
				sanitized.WriteRune(r)
			}
		}
		pattern, err := regexp.Compile(sanitized.String())
		if err != nil {
			return fmt.Errorf("invalid test regexp %q: %w", expression, err)
		}
		if top {
			roots = append(roots, pattern)
		}
		return nil
	}
	for i := 0; i < len(expression); i++ {
		switch expression[i] {
		case '\\':
			i++
		case '[':
			brackets++
		case ']':
			if brackets > 0 {
				brackets--
			}
		case '(':
			if brackets == 0 {
				groups++
			}
		case ')':
			if brackets == 0 {
				groups--
			}
		case '/', '|':
			if brackets == 0 && groups == 0 {
				if err := compile(i); err != nil {
					return nil, err
				}
				start, top = i+1, expression[i] == '|'
			}
		}
	}
	if err := compile(len(expression)); err != nil {
		return nil, err
	}
	return roots, nil
}

// Keep copied commands usable when searching another directory, without
// changing the caller's shell directory. Execution itself uses exec.Cmd.Dir.
func (t test) shellCommand(cwd string) string {
	command := t.Command
	if len(command) > 0 && t.Dir != cwd {
		command = append([]string{command[0], "-C", t.Dir}, command[1:]...)
	}
	return shellCommand(command)
}

func shellCommand(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		if arg != "" && strings.IndexFunc(arg, func(r rune) bool {
			return !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_./:=-", r)
		}) == -1 {
			quoted[i] = arg
		} else {
			// Fish interprets backslashes even inside single quotes. Emit
			// them outside the quoted segments so all supported shells agree.
			quoted[i] = "'" + strings.NewReplacer("\\", "'\\\\'", "'", "'\"'\"'").Replace(arg) + "'"
		}
	}
	return strings.Join(quoted, " ")
}
