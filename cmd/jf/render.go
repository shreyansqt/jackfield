package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/shreyansqt/jackfield/internal/hub"
)

// renderStatus writes the status panel of `jf status`.
//
// The panel answers three questions: which hub this machine talks to, which
// connections the hub holds, and how old each credential is. It answers the
// fourth question — does the upstream service still accept each credential —
// only when the hub has probes. Until then the row reads "not probed yet" rather
// than a tick, so the panel never claims a check that nobody made.
func renderStatus(out io.Writer, style *theme, baseURL string, deviceName string, status hub.Status, now time.Time) error {
	fmt.Fprintf(out, "%s  %s\n", style.Label.Render("hub   "), style.Value.Render(baseURL))
	if deviceName != "" {
		fmt.Fprintf(out, "%s  %s\n", style.Label.Render("device"), style.Value.Render(deviceName))
	}
	fmt.Fprintln(out)

	if len(status.Connections) == 0 {
		fmt.Fprintln(out, "The hub holds no credentials yet. Run `jf cred set NAME` to store one.")
		return nil
	}

	rows := make([][]string, 0, len(status.Connections))
	for _, connection := range status.Connections {
		identity := strings.TrimSpace(connection.Identity)
		if identity == "" {
			identity = "unknown"
		}
		rows = append(rows, []string{
			connection.Connection,
			identity,
			formatAge(connection, now),
			renderUpstream(style, connection.UpstreamOK),
		})
	}
	writeTable(out, style, []string{"CONNECTION", "IDENTITY", "AGE", "UPSTREAM"}, rows)

	if !status.ProbesImplemented {
		fmt.Fprintln(out)
		fmt.Fprintln(out, style.Dim.Render("This hub does not probe the upstream services yet. A credential shown here"))
		fmt.Fprintln(out, style.Dim.Render("can still be one that Slack or Google already refused."))
	}
	return nil
}

// renderUpstream renders the three states of upstream_ok.
//
// The null case is the one that matters. The hub sends null while it has no
// probes, and that means "nobody checked", which is not the same as "the check
// failed". The three states get three colours and three different words, so the
// panel reads correctly with the colour off as well.
func renderUpstream(style *theme, ok *bool) string {
	switch {
	case ok == nil:
		return style.Unknown.Render("not probed yet")
	case *ok:
		return style.Alive.Render("working")
	default:
		return style.Dead.Render("FAILING")
	}
}

// renderDevices writes the machine list of `jf device list`.
func renderDevices(out io.Writer, style *theme, devices []hub.Device, now time.Time) error {
	if len(devices) == 0 {
		fmt.Fprintln(out, "The hub has issued no device tokens. Run `jf login` on a machine.")
		return nil
	}

	rows := make([][]string, 0, len(devices))
	for _, device := range devices {
		marker := ""
		if device.Current {
			marker = style.Marker.Render("this machine")
		}
		rows = append(rows, []string{
			device.Name,
			style.Dim.Render(device.DeviceID),
			formatSince(device.CreatedAt, now),
			formatLastUsed(device.LastUsedAt, now),
			marker,
		})
	}
	writeTable(out, style, []string{"NAME", "DEVICE ID", "CREATED", "LAST USED", ""}, rows)
	return nil
}

// writeTable writes a table with the columns aligned.
//
// The width of a column is measured on the visible text, not on the rendered
// string, because a style adds escape codes that occupy no screen column. The
// last column is never padded, so a plain-theme table has no trailing spaces and
// a tool such as `cut` reads it cleanly.
func writeTable(out io.Writer, style *theme, headers []string, rows [][]string) {
	widths := make([]int, len(headers))
	for index, header := range headers {
		widths[index] = len(header)
	}
	for _, row := range rows {
		for index, cell := range row {
			if index >= len(widths) {
				continue
			}
			if width := visibleWidth(cell); width > widths[index] {
				widths[index] = width
			}
		}
	}

	writeRow(out, widths, headers, func(index int, cell string) string {
		// An empty header, such as the marker column of the device table, stays
		// empty. Styling it would emit escape codes around nothing.
		if cell == "" {
			return ""
		}
		return style.TableHeader.Render(cell)
	})
	for _, row := range rows {
		writeRow(out, widths, row, func(index int, cell string) string { return cell })
	}
}

// writeRow writes one table row, padded to the column widths.
func writeRow(out io.Writer, widths []int, cells []string, render func(int, string) string) {
	var line strings.Builder
	for index, cell := range cells {
		rendered := render(index, cell)
		last := index == len(cells)-1
		if last {
			// The final cell carries no padding, so no line ends in spaces.
			line.WriteString(rendered)
			break
		}
		line.WriteString(rendered)
		padding := widths[index] - visibleWidth(cell) + 2
		line.WriteString(strings.Repeat(" ", padding))
	}
	fmt.Fprintln(out, strings.TrimRight(line.String(), " "))
}

// visibleWidth returns the number of screen columns that a cell occupies.
//
// A styled cell carries ANSI escape codes, which take no column. Measuring the
// raw string would push every later column out by the length of those codes, so
// the codes are skipped here.
func visibleWidth(text string) int {
	width := 0
	inEscape := false
	for _, symbol := range text {
		switch {
		case inEscape:
			// A CSI sequence ends at the first letter.
			if (symbol >= 'a' && symbol <= 'z') || (symbol >= 'A' && symbol <= 'Z') {
				inEscape = false
			}
		case symbol == 0x1b:
			inEscape = true
		default:
			width++
		}
	}
	return width
}

// formatAge prefers the hub's own age over a subtraction of timestamps.
//
// The hub computes age_seconds against its own clock. Using it avoids reporting
// a negative age when this machine's clock runs behind the hub's.
func formatAge(connection hub.Connection, now time.Time) string {
	seconds := connection.AgeSeconds
	if seconds == 0 && connection.UpdatedAt > 0 {
		seconds = int64(now.Sub(time.UnixMilli(connection.UpdatedAt)).Seconds())
	}
	if seconds < 0 {
		seconds = 0
	}
	return formatDuration(time.Duration(seconds) * time.Second)
}

func formatDuration(duration time.Duration) string {
	switch {
	case duration < time.Minute:
		return fmt.Sprintf("%ds", int(duration.Seconds()))
	case duration < time.Hour:
		return fmt.Sprintf("%dm", int(duration.Minutes()))
	case duration < 48*time.Hour:
		return fmt.Sprintf("%dh", int(duration.Hours()))
	default:
		return fmt.Sprintf("%dd", int(duration.Hours()/24))
	}
}

func formatSince(milliseconds int64, now time.Time) string {
	if milliseconds <= 0 {
		return "unknown"
	}
	elapsed := now.Sub(time.UnixMilli(milliseconds))
	if elapsed < 0 {
		elapsed = 0
	}
	return formatDuration(elapsed) + " ago"
}

func formatLastUsed(milliseconds *int64, now time.Time) string {
	if milliseconds == nil || *milliseconds <= 0 {
		return "never"
	}
	return formatSince(*milliseconds, now)
}
