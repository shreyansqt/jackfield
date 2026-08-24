package hub

import (
	"os"
	"os/exec"
	"runtime"
)

// OpenBrowser tries to show a URL in the person's browser.
//
// It returns an error when there is no way to open one. The caller always prints
// the URL as well, so a failure here costs the person one copy and paste. It
// never blocks: `open` on macOS returns as soon as the browser has the URL.
func OpenBrowser(target string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("/usr/bin/open", target).Start()
	}
	return exec.Command("xdg-open", target).Start()
}

// HasDisplay reports whether this machine can show a browser.
//
// A machine with no display gets the device-code flow without being asked. The
// Mac mini answers SSH sessions, where `open` reaches the console session of
// whoever is logged in there, or fails. Neither is what the person at the
// terminal wants.
//
// The signals:
//   - SSH_CONNECTION or SSH_TTY set means this shell arrived over SSH.
//   - On Linux, no DISPLAY and no WAYLAND_DISPLAY means no graphical session.
//
// This is a guess about the environment, not a fact. `--device-code` forces the
// device-code flow, and `--browser` forces the browser flow, so a wrong guess
// costs one flag.
func HasDisplay() bool {
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
		return false
	}
	if runtime.GOOS == "darwin" {
		return true
	}
	return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
}
