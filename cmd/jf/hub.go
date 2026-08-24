package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/shreyansqt/jackfield/internal/hub"
)

// hubEnvironment holds what every hub command needs.
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
}

// isHubAction reports whether an action talks to the hub rather than to a
// local CLI. These actions need no manifest workspace.
func isHubAction(action string) bool {
	switch action {
	case "login", "status", "devices", "creds", "auth":
		return true
	default:
		return false
	}
}

// runHubAction dispatches one hub command.
func runHubAction(ctx context.Context, environment *hubEnvironment, action string, args []string) error {
	switch action {
	case "login":
		return runLogin(ctx, environment, args)
	case "status":
		return runStatus(ctx, environment, args)
	case "devices":
		return runDevices(ctx, environment, args)
	case "creds":
		return runCreds(ctx, environment, args)
	case "auth":
		return runAuth(ctx, environment, args)
	default:
		return fmt.Errorf("unknown action %q", action)
	}
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

/* ------------------------------------------------------------------ */
/* jf login                                                            */
/* ------------------------------------------------------------------ */

func runLogin(ctx context.Context, environment *hubEnvironment, args []string) error {
	flags := flag.NewFlagSet("login", flag.ContinueOnError)
	flags.SetOutput(environment.Stderr)
	deviceCodeFlow := flags.Bool("device-code", false, "Print the code and URL for another device instead of opening a browser")
	browserFlow := flags.Bool("browser", false, "Open a browser even when this machine looks headless")
	deviceName := flags.String("name", "", "The name this machine gets in `jf devices`")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *deviceCodeFlow && *browserFlow {
		return fmt.Errorf("use --device-code or --browser, not both")
	}

	baseURL, err := hub.BaseURL(environment.ManifestPath)
	if err != nil {
		return err
	}
	tokenPath, err := hub.TokenPath()
	if err != nil {
		return err
	}

	name := strings.TrimSpace(*deviceName)
	if name == "" {
		name = defaultDeviceName(environment)
	}

	client := hub.New(baseURL, "")
	code, err := client.StartDeviceAuth(ctx, name)
	if err != nil {
		return err
	}

	// Both flows use the same device grant. The only difference is whether this
	// machine also opens the browser itself.
	useBrowser := *browserFlow || (!*deviceCodeFlow && environment.HasDisplay())
	verificationURI := code.VerificationURIComplete
	if verificationURI == "" {
		verificationURI = code.VerificationURI
	}

	fmt.Fprintf(environment.Stdout, "Sign this machine in to %s\n\n", baseURL)
	fmt.Fprintf(environment.Stdout, "  code: %s\n", code.UserCode)
	fmt.Fprintf(environment.Stdout, "  open: %s\n\n", verificationURI)

	if useBrowser {
		if err := environment.OpenBrowser(verificationURI); err != nil {
			fmt.Fprintf(environment.Stderr, "jf: could not open a browser (%v). Open the URL above yourself.\n", err)
		}
	} else {
		fmt.Fprintln(environment.Stdout, "Open that URL on another device and type the code.")
	}
	fmt.Fprintf(environment.Stdout, "Waiting for the approval. The code expires in %s.\n", formatMinutes(code.ExpiresIn))

	token, err := hub.WaitForDeviceToken(ctx, client, code, environment.Sleep)
	if err != nil {
		return err
	}
	if err := hub.SaveToken(tokenPath, token.AccessToken); err != nil {
		return err
	}

	approvedName := token.DeviceName
	if approvedName == "" {
		approvedName = name
	}
	fmt.Fprintf(environment.Stdout, "\nThis machine is signed in as %q.\n", approvedName)
	fmt.Fprintf(environment.Stdout, "The device token is in %s.\n", tokenPath)
	return nil
}

// defaultDeviceName names this machine for `jf devices`.
//
// The short hostname is the name a person recognises, for example
// "grumpyorange" rather than "grumpyorange.local".
func defaultDeviceName(environment *hubEnvironment) string {
	hostname, err := environment.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		return "unnamed device"
	}
	return strings.TrimSuffix(strings.SplitN(hostname, ".", 2)[0], "\n")
}

func formatMinutes(seconds int) string {
	if seconds <= 0 {
		return "a few minutes"
	}
	minutes := seconds / 60
	if minutes < 1 {
		return fmt.Sprintf("%d seconds", seconds)
	}
	return fmt.Sprintf("%d minutes", minutes)
}

/* ------------------------------------------------------------------ */
/* jf status                                                           */
/* ------------------------------------------------------------------ */

func runStatus(ctx context.Context, environment *hubEnvironment, args []string) error {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	flags.SetOutput(environment.Stderr)
	if err := flags.Parse(args); err != nil {
		return err
	}

	client, err := environment.client()
	if err != nil {
		return err
	}
	status, err := client.GetStatus(ctx)
	if err != nil {
		return err
	}

	// The device name comes from the machine list, where the hub marks the
	// caller. A failure here costs one line of the panel, not the panel, so the
	// error is dropped on purpose.
	deviceName := ""
	if devices, listErr := client.ListDevices(ctx); listErr == nil {
		for _, device := range devices {
			if device.Current {
				deviceName = device.Name
				break
			}
		}
	}
	return hub.RenderStatus(environment.Stdout, client.BaseURL, deviceName, status, environment.now())
}

/* ------------------------------------------------------------------ */
/* jf devices                                                          */
/* ------------------------------------------------------------------ */

func runDevices(ctx context.Context, environment *hubEnvironment, args []string) error {
	flags := flag.NewFlagSet("devices", flag.ContinueOnError)
	flags.SetOutput(environment.Stderr)
	if err := flags.Parse(args); err != nil {
		return err
	}

	client, err := environment.client()
	if err != nil {
		return err
	}
	if flags.NArg() == 0 {
		devices, err := client.ListDevices(ctx)
		if err != nil {
			return err
		}
		return hub.RenderDevices(environment.Stdout, devices, environment.now())
	}

	switch action := flags.Arg(0); action {
	case "revoke":
		if flags.NArg() < 2 {
			return fmt.Errorf("jf devices revoke needs a device name; run `jf devices` to see the names")
		}
		return revokeDevice(ctx, environment, client, flags.Arg(1))
	default:
		return fmt.Errorf("unknown action %q; use `jf devices` or `jf devices revoke NAME`", action)
	}
}

// revokeDevice removes one machine's token.
//
// The hub deletes by device id, so the name is looked up first. Revoking this
// machine is allowed, and the person is told what happened, because the next
// command on this machine then needs `jf login` again.
func revokeDevice(ctx context.Context, environment *hubEnvironment, client *hub.Client, wanted string) error {
	devices, err := client.ListDevices(ctx)
	if err != nil {
		return err
	}
	device, err := hub.FindDeviceByName(devices, wanted)
	if err != nil {
		return err
	}
	if err := client.RevokeDevice(ctx, device.DeviceID); err != nil {
		return err
	}

	fmt.Fprintf(environment.Stdout, "Revoked %q (%s).\n", device.Name, device.DeviceID)
	if device.Current {
		fmt.Fprintln(environment.Stdout, "That was this machine. Run `jf login` to sign in again.")
	}
	return nil
}

/* ------------------------------------------------------------------ */
/* jf creds get                                                        */
/* ------------------------------------------------------------------ */

func runCreds(ctx context.Context, environment *hubEnvironment, args []string) error {
	flags := flag.NewFlagSet("creds", flag.ContinueOnError)
	flags.SetOutput(environment.Stderr)
	noCache := flags.Bool("no-cache", false, "Ask the hub even when a fresh cached copy exists")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() == 0 {
		return fmt.Errorf("use `jf creds get CONNECTION`")
	}

	switch action := flags.Arg(0); action {
	case "get":
		if flags.NArg() < 2 {
			return fmt.Errorf("jf creds get needs a connection name")
		}
		return getCredential(ctx, environment, flags.Arg(1), *noCache)
	default:
		return fmt.Errorf("unknown action %q; use `jf creds get CONNECTION`", action)
	}
}

// getCredential prints one credential to stdout.
//
// The value goes to stdout with no other text, so a script can read it with a
// command substitution. Every message about what happened goes to stderr.
func getCredential(ctx context.Context, environment *hubEnvironment, connection string, noCache bool) error {
	cacheDirectory, err := hub.CacheDir()
	if err != nil {
		return err
	}
	cache := hub.NewCache(cacheDirectory)
	cache.Now = environment.Now

	if !noCache {
		if credential, found := cache.Get(connection); found {
			fmt.Fprintln(environment.Stdout, credential.Secret)
			return nil
		}
	}

	client, err := environment.client()
	if err != nil {
		return err
	}
	credential, err := client.GetCredential(ctx, connection)
	if err != nil {
		return err
	}
	// A cache write failure is not a reason to fail the command. The credential
	// is already in hand, and the next call simply asks the hub again.
	if err := cache.Put(credential); err != nil {
		fmt.Fprintf(environment.Stderr, "jf: could not cache the credential: %v\n", err)
	}
	fmt.Fprintln(environment.Stdout, credential.Secret)
	return nil
}

/* ------------------------------------------------------------------ */
/* jf auth                                                             */
/* ------------------------------------------------------------------ */

func runAuth(ctx context.Context, environment *hubEnvironment, args []string) error {
	flags := flag.NewFlagSet("auth", flag.ContinueOnError)
	flags.SetOutput(environment.Stderr)
	identity := flags.String("identity", "", "Who this credential acts as, for the status panel")
	ticket := flags.String("ticket", "", "An approval ticket from the hub's approval page")
	fromStdin := flags.Bool("stdin", false, "Read the secret from standard input instead of prompting")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() == 0 {
		return fmt.Errorf("use `jf auth CONNECTION`")
	}
	connection := flags.Arg(0)

	baseURL, err := hub.BaseURL(environment.ManifestPath)
	if err != nil {
		return err
	}

	// The approval ticket comes from the browser, never from this machine's
	// device token. The hub refuses a write that carries only a device token,
	// and that refusal is the point of the write path.
	approvalTicket := strings.TrimSpace(*ticket)
	if approvalTicket == "" {
		approvalTicket, err = collectApprovalTicket(environment, baseURL, connection)
		if err != nil {
			return err
		}
	}

	secret, err := readSecret(environment, *fromStdin, connection)
	if err != nil {
		return err
	}
	if strings.TrimSpace(secret) == "" {
		return fmt.Errorf("the secret is empty; nothing was sent to the hub")
	}

	client := hub.New(baseURL, "")
	if err := client.PutCredential(ctx, connection, secret, *identity, approvalTicket); err != nil {
		return err
	}

	// The old value is now wrong on this machine. Removing it means the next
	// read here fetches the credential that was just written.
	if cacheDirectory, cacheErr := hub.CacheDir(); cacheErr == nil {
		if err := hub.NewCache(cacheDirectory).Forget(connection); err != nil {
			fmt.Fprintf(environment.Stderr, "jf: %v\n", err)
		}
	}

	fmt.Fprintf(environment.Stdout, "Stored the credential for %q in the hub.\n", connection)
	fmt.Fprintln(environment.Stdout, "Every machine reads the new value within the cache lifetime of five minutes.")
	return nil
}

// collectApprovalTicket sends the person to the hub's approval page.
//
// The hub issues the ticket to a signed-in browser caller only, so this machine
// cannot obtain one on its own. The person approves the write on that page,
// copies the ticket it shows, and pastes it here. The secret itself never
// passes through the browser.
//
// The page answers `GET /approvals?connection=<name>`. Its form then posts back
// to the same path, and the hub tells a browser apart from this client by the
// Accept and Content-Type headers. This client always sends JSON headers, so it
// keeps receiving the JSON answer.
func collectApprovalTicket(environment *hubEnvironment, baseURL string, connection string) (string, error) {
	approvalPageURL := fmt.Sprintf("%s/approvals?connection=%s", baseURL, url.QueryEscape(connection))

	fmt.Fprintf(environment.Stdout, "Writing a credential needs a fresh browser approval, every time.\n\n")
	fmt.Fprintf(environment.Stdout, "Approve the write for %q, then copy the ticket the page shows:\n\n", connection)
	fmt.Fprintf(environment.Stdout, "  open: %s\n\n", approvalPageURL)

	if err := environment.OpenBrowser(approvalPageURL); err != nil {
		fmt.Fprintf(environment.Stderr, "jf: could not open a browser (%v). Open the URL above yourself.\n", err)
	}

	fmt.Fprint(environment.Stdout, "Approval ticket: ")
	reader := bufio.NewReader(environment.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("read the approval ticket: %w", err)
	}
	ticket := strings.TrimSpace(line)
	if ticket == "" {
		return "", fmt.Errorf("no approval ticket was given; nothing was sent to the hub")
	}
	return ticket, nil
}

// readSecret reads the secret from standard input or from a hidden prompt.
//
// The secret is never a command argument. Arguments appear in the process list,
// where any other process on the machine reads them.
func readSecret(environment *hubEnvironment, fromStdin bool, connection string) (string, error) {
	if fromStdin {
		data, err := io.ReadAll(environment.Stdin)
		if err != nil {
			return "", fmt.Errorf("read the secret from standard input: %w", err)
		}
		return strings.TrimRight(string(data), "\r\n"), nil
	}
	if environment.ReadSecret == nil {
		return "", fmt.Errorf("cannot prompt for a secret here; pipe it in and use --stdin")
	}
	return environment.ReadSecret(fmt.Sprintf("Secret for %s: ", connection))
}

// readSecretFromTerminal reads one line with the echo turned off.
func readSecretFromTerminal(prompt string) (string, error) {
	descriptor := int(os.Stdin.Fd())
	if !term.IsTerminal(descriptor) {
		return "", fmt.Errorf("standard input is not a terminal; pipe the secret in and use --stdin")
	}
	fmt.Fprint(os.Stderr, prompt)
	data, err := term.ReadPassword(descriptor)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read the secret: %w", err)
	}
	return string(data), nil
}
