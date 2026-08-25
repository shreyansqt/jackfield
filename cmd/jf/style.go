package main

import (
	"io"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
)

// theme holds every style that jf output uses.
//
// A theme is built once per command run, from one decision: does this output go
// to a terminal that accepts colour? When the answer is no, every style in the
// theme is the identity style, so the same rendering code writes plain text. No
// call site has to ask whether colour is on.
type theme struct {
	// Enabled reports whether this theme paints colour. The tables read it to
	// decide between a coloured mark and a plain word.
	Enabled bool

	// Heading styles a section title, such as the "hub" label of the status
	// panel.
	Heading lipgloss.Style
	// Label styles the left column of a key-and-value line.
	Label lipgloss.Style
	// Value styles the right column of a key-and-value line.
	Value lipgloss.Style
	// Dim styles text that carries less weight than the text around it.
	Dim lipgloss.Style
	// Alive styles a connection that the hub probed and accepted.
	Alive lipgloss.Style
	// Dead styles a connection that the hub probed and that failed.
	Dead lipgloss.Style
	// Unknown styles a connection that nobody probed.
	Unknown lipgloss.Style
	// Accent styles a value that the reader looks for first, such as the login
	// code.
	Accent lipgloss.Style
	// Marker styles the note that points at the current machine.
	Marker lipgloss.Style
	// TableHeader styles the header row of a table.
	TableHeader lipgloss.Style
}

// plainTheme returns a theme in which every style writes its input unchanged.
func plainTheme() *theme {
	plain := lipgloss.NewStyle()
	return &theme{
		Enabled:     false,
		Heading:     plain,
		Label:       plain,
		Value:       plain,
		Dim:         plain,
		Alive:       plain,
		Dead:        plain,
		Unknown:     plain,
		Accent:      plain,
		Marker:      plain,
		TableHeader: plain,
	}
}

// colorTheme returns the painted theme.
//
// The colours are the ANSI 16, not 24-bit values. A terminal maps the ANSI
// colours to the palette that the person chose, so jf follows the terminal's own
// scheme rather than fighting it.
func colorTheme() *theme {
	return &theme{
		Enabled:     true,
		Heading:     lipgloss.NewStyle().Bold(true),
		Label:       lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		Value:       lipgloss.NewStyle(),
		Dim:         lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		Alive:       lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		Dead:        lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true),
		Unknown:     lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		Accent:      lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true),
		Marker:      lipgloss.NewStyle().Foreground(lipgloss.Color("4")),
		TableHeader: lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Bold(true),
	}
}

// newTheme returns the theme for one output stream.
//
// Colour is on only when the stream is a terminal and no environment variable
// asks for plain text. A redirect to a file, a pipe into grep, and a run inside
// a script all reach the plain theme, so the output stays parseable.
func newTheme(out io.Writer) *theme {
	if colorIsWanted(out, os.Getenv) {
		return colorTheme()
	}
	return plainTheme()
}

// colorIsWanted decides whether output to this writer carries colour.
//
// The rules, in order:
//   - NO_COLOR with a value turns colour off. That is the no-color.org rule, and
//     it wins over every other setting, because a person who sets it has said
//     what they want for every tool.
//   - CLICOLOR_FORCE with a value other than "0" turns colour on, even when the
//     output is a pipe. A script that renders jf output for a person needs this.
//   - TERM=dumb turns colour off. That terminal renders escape codes as text.
//   - Otherwise colour follows the terminal test.
//
// getenv is a parameter so a test can supply an environment.
func colorIsWanted(out io.Writer, getenv func(string) string) bool {
	if getenv("NO_COLOR") != "" {
		return false
	}
	if force := getenv("CLICOLOR_FORCE"); force != "" && force != "0" {
		return true
	}
	if strings.EqualFold(getenv("TERM"), "dumb") {
		return false
	}
	return isTerminal(out)
}

// isTerminal reports whether a writer is a terminal.
func isTerminal(out io.Writer) bool {
	file, ok := out.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(file.Fd())
}
