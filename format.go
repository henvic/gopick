package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"text/template"
	"text/template/parse"
)

const defaultFormat = `table {{.Test}}\t{{.Package}}\t{{.Location}}`

type formatField struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	heading     string
}

// Keep field documentation and table headings together. TestFormatFields checks
// the names and types against the fields available to templates.
var formatFields = []formatField{
	{"Test", "string", "Test function name", "Test"},
	{"Name", "string", "Test function name (alias of Test)", "Test"},
	{"Package", "string", "Declared package name, including _test for external tests", "Package"},
	{"ImportPath", "string", "Package import path reported by Go", "Import path"},
	{"Location", "string", "Source filename relative to the search directory, followed by :line", "Location"},
	{"File", "string", "Source filename relative to the search directory", "File"},
	{"Line", "int", "Source line number, starting at 1", "Line"},
	{"Dir", "string", "Absolute working directory for the command", "Directory"},
	{"Command", "string", "Shell-quoted command for this exact top-level test and its subtests, including go -C when needed", "Command"},
}

func printFormatFields(w io.Writer) error {
	encoder := json.NewEncoder(w)
	for _, field := range formatFields {
		if err := encoder.Encode(field); err != nil {
			return err
		}
	}
	return nil
}

// Templates use display values, while JSON serialization preserves the raw
// record so programs can use absolute paths and execute command arguments.
type formatRow struct {
	test
	Test     string
	Location string
	File     string
	Command  string
}

func (row formatRow) MarshalJSON() ([]byte, error) {
	return json.Marshal(row.test)
}

func printTests(w io.Writer, tests []test, dir, cwd, format string) error {
	if format == "" || format == "table" {
		format = defaultFormat
	}
	if format == "json" {
		format = "{{json .}}"
	}
	format, table := strings.CutPrefix(format, "table ")
	// Expand separators in literal text without changing escapes inside Go
	// template expressions, such as {{printf "%s\n" .Name}}.
	tmpl, err := template.New("format").Option("missingkey=error").Funcs(template.FuncMap{
		"json": func(value any) (string, error) {
			data, err := json.Marshal(value)
			return string(data), err
		},
		"join":  strings.Join,
		"split": strings.Split,
		"lower": strings.ToLower,
		"upper": strings.ToUpper,
	}).Parse(format)
	if err != nil {
		return fmt.Errorf("parse format: %w", err)
	}
	for _, part := range tmpl.Templates() {
		expandSeparators(part.Tree.Root)
	}
	var output bytes.Buffer
	if table {
		headers, err := tmpl.Clone()
		if err != nil {
			return err
		}
		identity := func(value string) string { return value }
		headers.Funcs(template.FuncMap{
			"json":  func(value any) string { return fmt.Sprint(value) },
			"join":  func(value, _ string) string { return value },
			"split": func(value, _ string) string { return value },
			"lower": identity,
			"upper": identity,
			"printf": func(format string, values ...any) string {
				for i, value := range values {
					values[i] = heading(fmt.Sprint(value))
				}
				return fmt.Sprintf(format, values...)
			},
		})
		headings := make(map[string]any, len(formatFields))
		for _, field := range formatFields {
			headings[field.Name] = field.heading
		}
		if err := renderRow(&output, headers, headings); err != nil {
			return err
		}
	}
	for _, test := range tests {
		file := test.File
		if relative, err := filepath.Rel(dir, file); err == nil {
			file = relative
		}
		if err := renderRow(&output, tmpl, formatRow{
			test: test, Test: test.Name, File: file,
			Location: fmt.Sprintf("%s:%d", file, test.Line),
			Command:  test.shellCommand(cwd),
		}); err != nil {
			return err
		}
	}
	// Render everything first so a bad template never prints a partial result.
	if table {
		writer := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		if _, err := output.WriteTo(writer); err != nil {
			return err
		}
		return writer.Flush()
	}
	_, err = output.WriteTo(w)
	return err
}

func renderRow(w io.Writer, tmpl *template.Template, values any) error {
	var row bytes.Buffer
	if err := tmpl.Execute(&row, values); err != nil {
		return fmt.Errorf("render format: %w", err)
	}
	if !bytes.HasSuffix(row.Bytes(), []byte("\n")) {
		row.WriteByte('\n')
	}
	_, err := row.WriteTo(w)
	return err
}

// Keep column names readable even with numeric printf verbs such as %.3d.
type heading string

func (h heading) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, string(h))
}

func expandSeparators(node parse.Node) {
	switch node := node.(type) {
	case *parse.ListNode:
		if node != nil {
			for _, child := range node.Nodes {
				expandSeparators(child)
			}
		}
	case *parse.TextNode:
		node.Text = []byte(strings.NewReplacer(`\t`, "\t", `\n`, "\n").Replace(string(node.Text)))
	case *parse.IfNode:
		expandSeparators(node.List)
		expandSeparators(node.ElseList)
	case *parse.RangeNode:
		expandSeparators(node.List)
		expandSeparators(node.ElseList)
	case *parse.WithNode:
		expandSeparators(node.List)
		expandSeparators(node.ElseList)
	}
}
