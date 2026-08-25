package main

import (
	"io"
	"os"
	"time"

	"github.com/shreyansqt/jackfield/internal/hub"
)

// hubEnvironment holds what every command needs from the machine around it.
//
// The fields are function values so a test can replace the terminal, the clock,
// and the browser without a running hub or a real home directory.
type hubEnvironment struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	// ManifestPath may be empty. `jf login` runs before any manifest exists.
	ManifestPath string

	// OpenBrowser shows a URL to the person.
	OpenBrowser func(string) error
	// HasDisplay reports whether this machine can show a browser.
	HasDisplay func() bool
	// ReadSecret reads a secret without echoing it to the screen.
	ReadSecret func(prompt string) (string, error)
	// Sleep delays the device-code poll.
	Sleep func(time.Duration)
	// Now is the clock behind every age in the output.
	Now func() time.Time
	// Hostname names this machine when `jf login` asks for a device name.
	Hostname func() (string, error)

	// Theme paints the output. It is resolved once, on first use, from the
	// output stream and the environment.
	Theme *theme
}

func defaultHubEnvironment(manifestPath string) *hubEnvironment {
	return &hubEnvironment{
		Stdin:        os.Stdin,
		Stdout:       os.Stdout,
		Stderr:       os.Stderr,
		ManifestPath: manifestPath,
		OpenBrowser:  hub.OpenBrowser,
		HasDisplay:   hub.HasDisplay,
		ReadSecret:   readSecretFromTerminal,
		Sleep:        time.Sleep,
		Now:          time.Now,
		Hostname:     os.Hostname,
	}
}

func (environment *hubEnvironment) now() time.Time {
	if environment.Now != nil {
		return environment.Now()
	}
	return time.Now()
}

// theme returns the styles for this run's output.
//
// The decision is made once and kept, so every part of one panel is painted the
// same way even when a caller swaps the writer between calls.
func (environment *hubEnvironment) theme() *theme {
	if environment.Theme == nil {
		environment.Theme = newTheme(environment.Stdout)
	}
	return environment.Theme
}

// client returns a hub client that carries this machine's device token.
func (environment *hubEnvironment) client() (*hub.Client, error) {
	baseURL, err := hub.BaseURL(environment.ManifestPath)
	if err != nil {
		return nil, err
	}
	tokenPath, err := hub.TokenPath()
	if err != nil {
		return nil, err
	}
	token, err := hub.LoadToken(tokenPath)
	if err != nil {
		return nil, err
	}
	return hub.New(baseURL, token), nil
}
