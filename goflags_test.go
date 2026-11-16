package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os/exec"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// Use the installed toolchain as an independent inventory. A new public build
// or test flag (or a change to whether it consumes a value) must be reviewed
// here, rather than silently breaking completion after a Go upgrade.
func TestGoFlags(t *testing.T) {
	check := func(t *testing.T, name string, takesValue bool) {
		t.Helper()
		info, known := goFlag(name)
		if !known {
			t.Errorf("Go supports -%s; add its metadata to goFlag", name)
		} else if info.takesValue != takesValue {
			t.Errorf("-%s takesValue = %v, want %v", name, info.takesValue, takesValue)
		}
	}
	for _, topic := range []string{"build", "test", "testflag"} {
		t.Run(topic, func(t *testing.T) {
			output, err := exec.Command("go", "help", topic).Output()
			if err != nil {
				t.Fatal(err)
			}
			definitions := regexp.MustCompile(`(?m)^\t-([a-zA-Z][a-zA-Z0-9.-]*)([^\n]*)$`).FindAllStringSubmatch(string(output), -1)
			if len(definitions) == 0 {
				t.Fatalf("no flag definitions found in go help %s", topic)
			}
			for _, definition := range definitions {
				if definition[1] == "args" {
					continue // -args is a terminator, handled before flag lookup.
				}
				check(t, definition[1], strings.TrimSpace(definition[2]) != "")
			}
		})
	}
	t.Run("test binary", func(t *testing.T) {
		flag.CommandLine.VisitAll(func(f *flag.Flag) {
			if strings.HasPrefix(f.Name, "test.") {
				boolean, ok := f.Value.(interface{ IsBoolFlag() bool })
				check(t, f.Name, !ok || !boolean.IsBoolFlag())
			}
		})
	})
}

func TestGoCompletionFlags(t *testing.T) {
	// These include flags previously missing from Zsh, compiler/linker values
	// that look like options, Go diagnostics, and test-binary-only flags.
	for _, name := range []string{
		"asmflags", "buildmode", "compiler", "covermode", "coverpkg", "gcflags", "gccgoflags",
		"installsuffix", "ldflags", "mod", "modfile", "overlay", "pgo", "pkgdir", "toolexec",
		"p", "o", "exec", "vet", "debug-actiongraph", "debug-runtime-trace", "debug-trace",
		"bench", "benchtime", "count", "cpu", "fuzz", "fuzztime", "fuzzminimizetime", "list",
		"outputdir", "parallel", "run", "skip", "shuffle", "timeout", "blockprofile",
		"blockprofilerate", "coverprofile", "cpuprofile", "memprofile", "memprofilerate",
		"mutexprofile", "mutexprofilefraction", "trace", "test.run", "test.timeout",
	} {
		for _, prefix := range []string{"-", "--"} {
			for _, equals := range []bool{false, true} {
				args := []string{prefix + name, "-tags=not-a-build-setting"}
				if equals {
					args = []string{prefix + name + "=-tags=not-a-build-setting"}
				}
				words := append([]string{"go", "test", "./sub"}, args...)
				words = append(words, "-run=Test", "-tags=real")
				r := goCompletionRequest(words, len(words)-2)
				if r.kind != "exact" || r.prefix != "Test" || !reflect.DeepEqual(r.patterns, []string{"./sub"}) || r.tags == nil || *r.tags != "real" {
					t.Errorf("completion for %q = %#v", words, r)
				}
			}
		}
	}
	for _, name := range []string{
		"a", "n", "x", "race", "msan", "asan", "linkshared", "modcacherw", "work",
		"trimpath", "buildvcs", "c", "json", "cover", "v", "short", "failfast", "fullpath",
		"benchmem", "artifacts", "test.v", "test.short", "test.paniconexit0", "test.fuzzworker",
	} {
		for _, suffix := range []string{"", "=false"} {
			words := []string{"go", "test", "./sub", "-" + name + suffix, "-run=Test"}
			r := goCompletionRequest(words, len(words)-1)
			if r.kind != "exact" || !reflect.DeepEqual(r.patterns, []string{"./sub"}) {
				t.Errorf("completion for %q = %#v", words, r)
			}
		}
	}
}

func TestTestBinaryFlags(t *testing.T) {
	dir := fixture(t, map[string]string{
		"default_test.go": "//go:build !special\n\npackage example\nimport \"testing\"\nfunc TestDefault(t *testing.T) { t.Fatal(\"must not run\") }\n",
		"tagged_test.go":  "//go:build special\n\npackage example\nimport \"testing\"\nfunc TestTagged(t *testing.T) { t.Fatal(\"must not run\") }\n",
	})
	for _, flags := range [][]string{
		{"-test.testlogfile", "-tags=special", "testlog"},
		{"-test.testlogfile", "testlog", "-tags=special"},
		{"-test.testlogfile=testlog", "-tags=special"},
		{"-test.testlogfile=testlog", "literal", "-tags=special"},
		{"-test.paniconexit0", "false", "-tags=special"},
		{"-test.paniconexit0=false", "-tags=special"},
	} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			cmd := exec.Command("go", append([]string{"test", "-list", "Test", "."}, flags...)...)
			cmd.Dir = dir
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("go test: %v\n%s", err, output)
			}
			var want []string
			for _, line := range strings.Split(string(output), "\n") {
				if strings.HasPrefix(line, "Test") {
					want = append(want, line)
				}
			}
			if len(want) != 1 {
				t.Fatalf("expected one runnable test, got %q", output)
			}
			var stdout, stderr bytes.Buffer
			args := append([]string{"-C", dir, "-format", "json", ".", "Test"}, flags...)
			if err := run(context.Background(), args, nil, &stdout, &stderr); err != nil {
				t.Fatal(err)
			}
			var got test
			if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Name != want[0] {
				t.Errorf("discovery = %s, go test = %s", got.Name, want[0])
			}
			stdout.Reset()
			words := append([]string{"go", "-C", dir, "test", ".", "-run=Test"}, flags...)
			if err := completeShell(context.Background(), &stdout, words, 5); err != nil {
				t.Fatal(err)
			}
			if expected := "exact\n-run=^" + want[0] + "$\n"; stdout.String() != expected {
				t.Errorf("completion = %q, want %q", &stdout, expected)
			}
		})
	}
}

func TestGoCompletionPackageList(t *testing.T) {
	for _, tc := range []struct {
		words    []string
		cursor   int
		patterns []string
	}{
		{[]string{"go", "test", "-v", "-run=Test", "./one", "./two"}, 3, []string{"./one", "./two"}},
		{[]string{"go", "test", "./one", "./two", "-run=Test"}, 4, []string{"./one", "./two"}},
		{[]string{"go", "test", "./one", "-run=Test", "./two"}, 3, []string{"./one"}},
		{[]string{"go", "test", "./one", "-v", "literal", "-run=Test"}, 5, nil},
		{[]string{"go", "test", "-test.testlogfile=log", "-run=Test", "literal"}, 3, []string{"."}},
		{[]string{"go", "test", "-test.testlogfile", "log", "literal", "-run=Test"}, 5, nil},
		{[]string{"go", "test", "-test.testlogfile", "-run=Test", "log"}, 3, []string{"."}},
		{[]string{"go", "test", "-test.paniconexit0", "false", "-run=Test"}, 4, []string{"."}},
		{[]string{"go", "test", "-test.paniconexit0=false", "literal", "-run=Test"}, 4, nil},
		{[]string{"go", "test", "-run=Test", "-args", "./ignored"}, 2, []string{"."}},
		{[]string{"go", "test", "-args", "-run=Test"}, 3, nil},
		{[]string{"go", "test", "--", "-run=Test"}, 3, nil},
		{[]string{"go", "test", "-custom", "value", "-run=Test"}, 4, nil},
	} {
		r := goCompletionRequest(tc.words, tc.cursor)
		if !reflect.DeepEqual(r.patterns, tc.patterns) {
			t.Errorf("completion for %q: packages = %q, want %q", tc.words, r.patterns, tc.patterns)
		}
		if tc.patterns == nil && r.kind != "" {
			t.Errorf("completion for %q: kind = %q, want fallback", tc.words, r.kind)
		}
	}
}
