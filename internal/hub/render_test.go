package hub

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func boolPointer(value bool) *bool { return &value }

func renderStatusToString(t *testing.T, status Status) string {
	t.Helper()
	var out bytes.Buffer
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	if err := RenderStatus(&out, "https://hub.example.com", "grumpyorange", status, now); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// The whole point of this line: while the hub sends null, nobody checked the
// credential, and the panel must not imply that anybody did.
func TestStatusReportsAnUncheckedCredentialAsNotProbed(t *testing.T) {
	output := renderStatusToString(t, Status{
		Connections: []Connection{
			{Connection: "slack-smarta", Identity: "shreyans@example.com", AgeSeconds: 3600, UpstreamOK: nil},
		},
		ProbesImplemented: false,
	})

	if !strings.Contains(output, "not probed yet") {
		t.Fatalf("got %q, want \"not probed yet\" for a null upstream_ok", output)
	}
	if strings.Contains(output, "working") {
		t.Fatalf("got %q; a null upstream_ok must never read as working", output)
	}
	if !strings.Contains(output, "does not probe the upstream services yet") {
		t.Fatalf("got %q, want the note that this hub runs no probes", output)
	}
}

func TestStatusReportsAWorkingAndAFailingCredential(t *testing.T) {
	output := renderStatusToString(t, Status{
		Connections: []Connection{
			{Connection: "google-personal", Identity: "someone@example.com", AgeSeconds: 60, UpstreamOK: boolPointer(true)},
			{Connection: "slack-smarta", Identity: "shreyans@example.com", AgeSeconds: 60, UpstreamOK: boolPointer(false)},
		},
		ProbesImplemented: true,
	})

	if !strings.Contains(output, "working") {
		t.Fatalf("got %q, want \"working\" for upstream_ok true", output)
	}
	if !strings.Contains(output, "FAILING") {
		t.Fatalf("got %q, want \"FAILING\" for upstream_ok false", output)
	}
	if strings.Contains(output, "not probed yet") {
		t.Fatalf("got %q; a hub with probes must not print the unchecked note", output)
	}
	if strings.Contains(output, "does not probe the upstream services yet") {
		t.Fatalf("got %q; the note belongs to a hub without probes only", output)
	}
}

func TestStatusShowsTheHubAndTheDevice(t *testing.T) {
	output := renderStatusToString(t, Status{})
	if !strings.Contains(output, "https://hub.example.com") {
		t.Fatalf("got %q, want the hub address", output)
	}
	if !strings.Contains(output, "grumpyorange") {
		t.Fatalf("got %q, want this machine's device name", output)
	}
}

func TestStatusTellsThePersonHowToStoreTheFirstCredential(t *testing.T) {
	output := renderStatusToString(t, Status{Connections: nil})
	if !strings.Contains(output, "jf auth") {
		t.Fatalf("got %q, want the command that stores a credential", output)
	}
}

func TestStatusShowsTheCredentialAge(t *testing.T) {
	output := renderStatusToString(t, Status{
		Connections: []Connection{
			{Connection: "slack-smarta", Identity: "a@example.com", AgeSeconds: 30},
			{Connection: "google-personal", Identity: "b@example.com", AgeSeconds: 7200},
			{Connection: "aws-staging", Identity: "c@example.com", AgeSeconds: 3 * 24 * 3600},
		},
	})

	for _, want := range []string{"30s", "2h", "3d"} {
		if !strings.Contains(output, want) {
			t.Fatalf("got %q, want the age %q", output, want)
		}
	}
}

// A missing identity must read as unknown rather than as an empty column.
func TestStatusNamesAnAbsentIdentity(t *testing.T) {
	output := renderStatusToString(t, Status{
		Connections: []Connection{{Connection: "slack-smarta", Identity: "", AgeSeconds: 60}},
	})
	if !strings.Contains(output, "unknown") {
		t.Fatalf("got %q, want \"unknown\" for a credential with no identity", output)
	}
}

// The hub computes age against its own clock. Using its number avoids a
// negative age when this machine's clock runs behind the hub's.
func TestStatusPrefersTheHubsAgeOverTheLocalClock(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	err := RenderStatus(&out, "https://hub.example.com", "", Status{
		Connections: []Connection{{
			Connection: "slack-smarta",
			Identity:   "a@example.com",
			AgeSeconds: 120,
			// A timestamp in this machine's future, as a clock skew produces.
			UpdatedAt: now.Add(time.Hour).UnixMilli(),
		}},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "2m") {
		t.Fatalf("got %q, want the hub's own age of 2m", out.String())
	}
	// A subtraction against this machine's clock would give an age of -60m.
	if strings.Contains(out.String(), "-60m") {
		t.Fatalf("got %q, want no negative age from the clock skew", out.String())
	}
}

func TestRenderDevicesMarksThisMachine(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	lastUsed := now.Add(-5 * time.Minute).UnixMilli()

	var out bytes.Buffer
	err := RenderDevices(&out, []Device{
		{DeviceID: "aaa", Name: "macbook", CreatedAt: now.Add(-48 * time.Hour).UnixMilli(), LastUsedAt: &lastUsed, Current: true},
		{DeviceID: "bbb", Name: "grumpyorange", CreatedAt: now.Add(-time.Hour).UnixMilli(), LastUsedAt: nil},
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	output := out.String()
	if !strings.Contains(output, "this machine") {
		t.Fatalf("got %q, want the calling device marked", output)
	}
	if !strings.Contains(output, "never") {
		t.Fatalf("got %q, want \"never\" for a device that never used its token", output)
	}
	if !strings.Contains(output, "aaa") || !strings.Contains(output, "bbb") {
		t.Fatalf("got %q, want both device ids, because revoke accepts an id", output)
	}
}

func TestRenderDevicesTellsThePersonHowToSignIn(t *testing.T) {
	var out bytes.Buffer
	if err := RenderDevices(&out, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "jf login") {
		t.Fatalf("got %q, want the command that signs a machine in", out.String())
	}
}
