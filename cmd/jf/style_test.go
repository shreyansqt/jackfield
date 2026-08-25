package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/shreyansqt/jackfield/internal/hub"
)

// fakeEnv returns a getenv function backed by a map.
func fakeEnv(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

// A buffer is not a terminal, so it must never receive colour. Every redirect,
// every pipe, and every test reaches this path.
func TestColorIsOffForANonTerminal(t *testing.T) {
	if colorIsWanted(&bytes.Buffer{}, fakeEnv(nil)) {
		t.Fatal("a buffer is not a terminal and must get plain text")
	}
}

func TestNoColorTurnsColorOff(t *testing.T) {
	// Even with the force flag set, NO_COLOR wins.
	environment := fakeEnv(map[string]string{"NO_COLOR": "1", "CLICOLOR_FORCE": "1"})
	if colorIsWanted(&bytes.Buffer{}, environment) {
		t.Fatal("NO_COLOR must turn colour off, even against CLICOLOR_FORCE")
	}
}

// A script that renders jf output for a person needs colour through a pipe.
func TestClicolorForceTurnsColorOn(t *testing.T) {
	if !colorIsWanted(&bytes.Buffer{}, fakeEnv(map[string]string{"CLICOLOR_FORCE": "1"})) {
		t.Fatal("CLICOLOR_FORCE must turn colour on for a pipe")
	}
	if colorIsWanted(&bytes.Buffer{}, fakeEnv(map[string]string{"CLICOLOR_FORCE": "0"})) {
		t.Fatal("CLICOLOR_FORCE=0 must not turn colour on")
	}
}

func TestDumbTerminalGetsNoColor(t *testing.T) {
	if colorIsWanted(&bytes.Buffer{}, fakeEnv(map[string]string{"TERM": "dumb"})) {
		t.Fatal("TERM=dumb renders escape codes as text, so colour must be off")
	}
}

// The plain theme must write its input unchanged. That is what makes the same
// rendering code produce parseable output.
func TestPlainThemeAddsNothing(t *testing.T) {
	style := plainTheme()
	for _, render := range []func(...string) string{
		style.Alive.Render, style.Dead.Render, style.Unknown.Render,
		style.Accent.Render, style.Dim.Render, style.Marker.Render,
		style.TableHeader.Render, style.Label.Render, style.Value.Render,
	} {
		if got := render("plain text"); got != "plain text" {
			t.Fatalf("got %q, want the input unchanged", got)
		}
	}
}

/* ------------------------------------------------------------------ */
/* the status panel                                                    */
/* ------------------------------------------------------------------ */

func alive(value bool) *bool { return &value }

func sampleStatus() hub.Status {
	return hub.Status{
		Connections: []hub.Connection{
			{Connection: "slack-smarta", Identity: "shreyans@example.com", AgeSeconds: 120, UpstreamOK: alive(true)},
			{Connection: "google-personal", Identity: "you@example.com", AgeSeconds: 7200, UpstreamOK: alive(false)},
			{Connection: "cloudflare", AgeSeconds: 300},
		},
	}
}

// The plain panel must carry no escape codes, so a script parses it.
func TestStatusPanelIsPlainWithoutATerminal(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	if err := renderStatus(&out, plainTheme(), "https://hub.example.dev", "macbook", sampleStatus(), now); err != nil {
		t.Fatal(err)
	}

	text := out.String()
	if strings.Contains(text, "\x1b[") {
		t.Fatalf("the plain panel carries escape codes:\n%s", text)
	}
	for _, want := range []string{"slack-smarta", "working", "FAILING", "not probed yet", "unknown", "macbook"} {
		if !strings.Contains(text, want) {
			t.Errorf("the panel does not contain %q", want)
		}
	}
	// No line may end in spaces, or a diff of two panels reads as changed when
	// only a column width moved.
	for _, line := range strings.Split(text, "\n") {
		if line != strings.TrimRight(line, " ") {
			t.Errorf("the line %q ends in spaces", line)
		}
	}
}

// The three upstream states get three colours, and they must differ. A panel
// that painted "working" and "FAILING" alike would be worse than a plain one.
func TestUpstreamStatesGetDifferentColors(t *testing.T) {
	style := colorTheme()
	working := renderUpstream(style, alive(true))
	failing := renderUpstream(style, alive(false))
	unknown := renderUpstream(style, nil)

	for _, rendered := range []string{working, failing, unknown} {
		if !strings.Contains(rendered, "\x1b[") {
			t.Fatalf("the coloured theme produced no escape code for %q", rendered)
		}
	}
	if working == failing || working == unknown || failing == unknown {
		t.Fatal("the three upstream states must not render alike")
	}
	// The words must still differ, so the colour is not the only signal.
	if !strings.Contains(working, "working") || !strings.Contains(failing, "FAILING") || !strings.Contains(unknown, "not probed yet") {
		t.Fatal("each upstream state must carry its own word as well as its colour")
	}
}

func TestStatusPanelSaysWhenTheHubIsEmpty(t *testing.T) {
	var out bytes.Buffer
	err := renderStatus(&out, plainTheme(), "https://hub.example.dev", "macbook", hub.Status{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// The message must name the command that fixes the state, under its new
	// name rather than the old `jf auth`.
	if !strings.Contains(out.String(), "jf cred set") {
		t.Fatalf("got %q, want it to point at `jf cred set`", out.String())
	}
}

/* ------------------------------------------------------------------ */
/* the device table                                                    */
/* ------------------------------------------------------------------ */

func TestDeviceTableAlignsAndMarksThisMachine(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	lastUsed := now.Add(-30 * time.Minute).UnixMilli()
	devices := []hub.Device{
		{DeviceID: "aaa", Name: "macbook", CreatedAt: now.Add(-72 * time.Hour).UnixMilli(), LastUsedAt: &lastUsed, Current: true},
		{DeviceID: "bbb", Name: "grumpyorange", CreatedAt: now.Add(-time.Hour).UnixMilli()},
	}

	var out bytes.Buffer
	if err := renderDevices(&out, plainTheme(), devices, now); err != nil {
		t.Fatal(err)
	}
	text := out.String()

	for _, want := range []string{"macbook", "grumpyorange", "this machine", "never", "NAME", "DEVICE ID"} {
		if !strings.Contains(text, want) {
			t.Errorf("the device table does not contain %q", want)
		}
	}

	// The name column must line up, so the eye reads down it.
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want a header and two machines", len(lines))
	}
	firstColumn := strings.Index(lines[1], "  ")
	secondColumn := strings.Index(lines[2], "  ")
	if firstColumn <= 0 || secondColumn <= 0 {
		t.Fatal("the table has no column separator")
	}
}

func TestDeviceTableSaysWhenNoMachineIsSignedIn(t *testing.T) {
	var out bytes.Buffer
	if err := renderDevices(&out, plainTheme(), nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "jf login") {
		t.Fatalf("got %q, want it to point at `jf login`", out.String())
	}
}

// A styled cell carries escape codes that take no screen column. Measuring the
// raw string would push every later column out by the length of those codes.
func TestVisibleWidthIgnoresEscapeCodes(t *testing.T) {
	style := colorTheme()
	painted := style.Alive.Render("working")
	if visibleWidth(painted) != len("working") {
		t.Fatalf("got width %d for %q, want %d", visibleWidth(painted), painted, len("working"))
	}
	if visibleWidth("plain") != 5 {
		t.Fatalf("got width %d, want 5", visibleWidth("plain"))
	}
}

// The coloured table must line up as well as the plain one.
func TestColoredTableKeepsItsColumnsAligned(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	var plain, painted bytes.Buffer
	if err := renderStatus(&plain, plainTheme(), "https://hub.example.dev", "macbook", sampleStatus(), now); err != nil {
		t.Fatal(err)
	}
	if err := renderStatus(&painted, colorTheme(), "https://hub.example.dev", "macbook", sampleStatus(), now); err != nil {
		t.Fatal(err)
	}

	// Stripping the escape codes from the painted panel must give the plain
	// one, which proves the colour changed no layout.
	if stripEscapes(painted.String()) != plain.String() {
		t.Fatalf("the coloured panel does not lay out like the plain one:\n%q\n%q",
			stripEscapes(painted.String()), plain.String())
	}
}

// stripEscapes removes every ANSI escape sequence from a string.
func stripEscapes(text string) string {
	var out strings.Builder
	inEscape := false
	for _, symbol := range text {
		switch {
		case inEscape:
			if (symbol >= 'a' && symbol <= 'z') || (symbol >= 'A' && symbol <= 'Z') {
				inEscape = false
			}
		case symbol == 0x1b:
			inEscape = true
		default:
			out.WriteRune(symbol)
		}
	}
	return out.String()
}

/* ------------------------------------------------------------------ */
/* the spinner                                                         */
/* ------------------------------------------------------------------ */

// A spinner would fill a log file with carriage returns, so on a pipe it prints
// its message once instead.
func TestSpinnerPrintsOnceWithoutATerminal(t *testing.T) {
	var out bytes.Buffer
	wait := newSpinner(&out, plainTheme(), "Waiting for the approval.")
	wait.Start()
	wait.Stop()

	if out.String() != "Waiting for the approval.\n" {
		t.Fatalf("got %q, want the message once and no animation", out.String())
	}
}

func TestSpinnerStopIsSafeToCallTwice(t *testing.T) {
	var out bytes.Buffer
	wait := newSpinner(&out, plainTheme(), "Waiting.")
	wait.Start()
	wait.Stop()
	wait.Stop()
}
