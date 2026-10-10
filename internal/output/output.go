package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
)

type Formatter struct {
	stdout  io.Writer
	stderr  io.Writer
	asJSON  bool
	plain   bool
	quiet   bool
	noColor bool
}

func New(stdout, stderr io.Writer, asJSON, plain, quiet, noColor bool) *Formatter {
	return &Formatter{
		stdout:  stdout,
		stderr:  stderr,
		asJSON:  asJSON,
		plain:   plain,
		quiet:   quiet,
		noColor: noColor || os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb",
	}
}

func (f *Formatter) IsJSON() bool {
	return f.asJSON
}

func (f *Formatter) IsPlain() bool {
	return f.plain
}

func (f *Formatter) JSON(data any) error {
	encoder := json.NewEncoder(f.stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(data)
}

func (f *Formatter) Printf(format string, args ...any) {
	if f.quiet {
		return
	}
	_, _ = fmt.Fprintf(f.stdout, format, args...)
}

func (f *Formatter) Println(args ...any) {
	if f.quiet {
		return
	}
	_, _ = fmt.Fprintln(f.stdout, args...)
}

func (f *Formatter) Errorf(format string, args ...any) {
	_, _ = fmt.Fprintf(f.stderr, format, args...)
}

func (f *Formatter) Table(headers []string, rows [][]string) {
	if f.quiet {
		return
	}
	writer := tabwriter.NewWriter(f.stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, strings.Join(headers, "\t"))
	for _, row := range rows {
		_, _ = fmt.Fprintln(writer, strings.Join(row, "\t"))
	}
	_ = writer.Flush()
}

func (f *Formatter) Success(message string) {
	if f.noColor {
		f.Println("[ok]", message)
		return
	}
	f.Println("\033[32m[ok]\033[0m", message)
}
