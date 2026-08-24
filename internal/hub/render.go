package hub

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
)

// RenderStatus writes the status panel.
//
// The panel answers three questions: which hub this machine talks to, which
// connections the hub holds, and how old each credential is. It answers the
// fourth question — is each credential still accepted upstream — only when the
// hub has probes. Until then it prints "not probed yet" rather than a tick, so
// the panel never claims a check that nobody made.
func RenderStatus(out io.Writer, baseURL string, deviceName string, status Status, now time.Time) error {
	fmt.Fprintf(out, "hub      %s\n", baseURL)
	if deviceName != "" {
		fmt.Fprintf(out, "device   %s\n", deviceName)
	}
	fmt.Fprintln(out)

	if len(status.Connections) == 0 {
		fmt.Fprintln(out, "The hub holds no credentials yet. Run `jf auth <connection>` to store one.")
		return nil
	}

	table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "CONNECTION\tIDENTITY\tAGE\tUPSTREAM")
	for _, connection := range status.Connections {
		identity := connection.Identity
		if strings.TrimSpace(identity) == "" {
			identity = "unknown"
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\n",
			connection.Connection,
			identity,
			formatAge(connection, now),
			formatUpstream(connection.UpstreamOK),
		)
	}
	if err := table.Flush(); err != nil {
		return err
	}

	if !status.ProbesImplemented {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "This hub does not probe the upstream services yet. A credential shown here")
		fmt.Fprintln(out, "can still be one that Slack or Google already refused.")
	}
	return nil
}

// formatAge prefers the hub's own age over a subtraction of timestamps.
//
// The hub computes age_seconds against its own clock. Using it avoids reporting
// a negative age when this machine's clock runs behind the hub's.
func formatAge(connection Connection, now time.Time) string {
	seconds := connection.AgeSeconds
	if seconds == 0 && connection.UpdatedAt > 0 {
		seconds = int64(now.Sub(time.UnixMilli(connection.UpdatedAt)).Seconds())
	}
	if seconds < 0 {
		seconds = 0
	}
	return formatDuration(time.Duration(seconds) * time.Second)
}

// formatUpstream renders the three states of upstream_ok.
//
// The null case is the one that matters. The hub sends null while it has no
// probes, and that means "nobody checked", which is not the same as "the check
// failed". Printing "not probed yet" keeps those apart.
func formatUpstream(ok *bool) string {
	switch {
	case ok == nil:
		return "not probed yet"
	case *ok:
		return "working"
	default:
		return "FAILING"
	}
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

// RenderDevices writes the machine list behind `jf devices`.
func RenderDevices(out io.Writer, devices []Device, now time.Time) error {
	if len(devices) == 0 {
		fmt.Fprintln(out, "The hub has issued no device tokens. Run `jf login` on a machine.")
		return nil
	}

	table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "NAME\tDEVICE ID\tCREATED\tLAST USED\t")
	for _, device := range devices {
		marker := ""
		if device.Current {
			marker = "this machine"
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n",
			device.Name,
			device.DeviceID,
			formatSince(device.CreatedAt, now),
			formatLastUsed(device.LastUsedAt, now),
			marker,
		)
	}
	return table.Flush()
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
