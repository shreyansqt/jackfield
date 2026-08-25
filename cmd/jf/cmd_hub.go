package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shreyansqt/jackfield/internal/hub"
)

/* ------------------------------------------------------------------ */
/* jf status                                                           */
/* ------------------------------------------------------------------ */

func newStatusCommand(environment *hubEnvironment, manifest func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show where every connection stands",
		Long: `Print one panel for this machine.

The panel shows the hub address, the name of this machine, and one line per
connection: its identity, the age of its credential, and whether the upstream
service still accepts it. Run it first when a tool fails and you do not know
which credential is at fault.

The hub does not probe the upstream services yet, so the UPSTREAM column reads
"not probed yet". That is the honest answer: nobody checked. A credential shown
there can still be one that Slack or Google already refused.`,
		Example: `  # See the hub, this machine, and every connection.
  jf status`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, args []string) error {
			environment.ManifestPath = manifest()
			return runStatus(command.Context(), environment)
		},
	}
}

func runStatus(ctx context.Context, environment *hubEnvironment) error {
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
	return renderStatus(environment.Stdout, environment.theme(), client.BaseURL, deviceName, status, environment.now())
}

/* ------------------------------------------------------------------ */
/* jf login                                                            */
/* ------------------------------------------------------------------ */

func newLoginCommand(environment *hubEnvironment, manifest func() string) *cobra.Command {
	var deviceCodeFlow bool
	var browserFlow bool
	var deviceName string

	command := &cobra.Command{
		Use:   "login",
		Short: "Sign this machine in to the hub",
		Long: `Sign this machine in to the hub, and store a device token.

The token goes to ~/.config/jackfield/device-token with mode 0600. jf prints a
short code and a URL, then waits while you approve the machine in a browser. Run
it once per machine, and again after you revoke this machine.

jf picks the flow when you give no flag. A machine reached over SSH, or a Linux
machine with no graphical session, gets the device-code flow. That is a guess
about the environment, so --device-code and --browser override it.

jf login needs the hub address. Set a hub: key in jackfield.yaml, or set the
JF_HUB environment variable.`,
		Example: `  # Sign in, and let jf open a browser here.
  jf login

  # Sign in and name this machine in the device list.
  jf login --name macbook

  # Sign in over SSH, with a code you type on another device.
  jf login --device-code`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, args []string) error {
			environment.ManifestPath = manifest()
			if deviceCodeFlow && browserFlow {
				return fmt.Errorf("--device-code and --browser ask for different flows. Use one of them, not both")
			}
			return runLogin(command.Context(), environment, deviceCodeFlow, browserFlow, deviceName)
		},
	}

	command.Flags().BoolVar(&deviceCodeFlow, "device-code", false,
		"Print the code and URL for another device instead of opening a browser")
	command.Flags().BoolVar(&browserFlow, "browser", false,
		"Open a browser even when this machine looks headless")
	command.Flags().StringVar(&deviceName, "name", "",
		"The name this machine gets in `jf device list` (default: the short hostname)")
	return command
}

func runLogin(ctx context.Context, environment *hubEnvironment, deviceCodeFlow bool, browserFlow bool, deviceName string) error {
	baseURL, err := hub.BaseURL(environment.ManifestPath)
	if err != nil {
		return err
	}
	tokenPath, err := hub.TokenPath()
	if err != nil {
		return err
	}

	name := strings.TrimSpace(deviceName)
	if name == "" {
		name = defaultDeviceName(environment)
	}

	// The token this machine already holds is read before the new one replaces
	// it. A second `jf login` would otherwise leave the old device alive at the
	// hub, and nothing on this machine could reach it again: the file that named
	// it is gone. A machine with no token yet reads an empty string here, which
	// is the normal first login.
	previousToken, _ := hub.LoadToken(tokenPath)

	client := hub.New(baseURL, "")
	code, err := client.StartDeviceAuth(ctx, name)
	if err != nil {
		return err
	}

	// Both flows use the same device grant. The only difference is whether this
	// machine also opens the browser itself.
	useBrowser := browserFlow || (!deviceCodeFlow && environment.HasDisplay())
	verificationURI := code.VerificationURIComplete
	if verificationURI == "" {
		verificationURI = code.VerificationURI
	}

	style := environment.theme()
	fmt.Fprintf(environment.Stdout, "Sign this machine in to %s\n\n", style.Value.Render(baseURL))
	fmt.Fprintf(environment.Stdout, "  %s %s\n", style.Label.Render("code:"), style.Accent.Render(code.UserCode))
	fmt.Fprintf(environment.Stdout, "  %s %s\n\n", style.Label.Render("open:"), verificationURI)

	if useBrowser {
		if err := environment.OpenBrowser(verificationURI); err != nil {
			fmt.Fprintf(environment.Stderr, "jf: could not open a browser (%v). Open the URL above yourself.\n", err)
		}
	} else {
		fmt.Fprintln(environment.Stdout, "Open that URL on another device and type the code.")
	}

	wait := newSpinner(environment.Stdout, style,
		fmt.Sprintf("Waiting for the approval. The code expires in %s.", formatMinutes(code.ExpiresIn)))
	wait.Start()
	token, err := hub.WaitForDeviceToken(ctx, client, code, environment.Sleep)
	wait.Stop()
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
	fmt.Fprintf(environment.Stdout, "\n%s This machine is signed in as %q.\n",
		style.Alive.Render("Done."), approvedName)
	fmt.Fprintf(environment.Stdout, "The device token is in %s.\n", tokenPath)

	revokePreviousToken(ctx, environment, baseURL, previousToken, token.AccessToken)
	return nil
}

// revokePreviousToken removes the device that this machine held before.
//
// A second `jf login` replaces the token file, and the old device would stay
// alive at the hub with nothing on this machine able to name it. So the old
// token revokes itself, with its own authority, while it still works.
//
// This never fails the login. The new token is already saved and working by the
// time this runs, so a hub that refuses the revoke costs one stale device in the
// list, not the sign-in the person asked for. The message says what to run.
func revokePreviousToken(ctx context.Context, environment *hubEnvironment, baseURL string, previousToken string, newToken string) {
	// Nothing to revoke on a first login, and nothing to do when the hub handed
	// back the same token.
	if previousToken == "" || previousToken == newToken {
		return
	}

	// The old token is the authority here, not the new one. It names the device
	// it belongs to, so the hub marks that device as the caller.
	client := hub.New(baseURL, previousToken)
	devices, err := client.ListDevices(ctx)
	if err != nil {
		reportStaleDevice(environment, err)
		return
	}
	for _, device := range devices {
		if !device.Current {
			continue
		}
		if err := client.RevokeDevice(ctx, device.DeviceID); err != nil {
			reportStaleDevice(environment, err)
			return
		}
		fmt.Fprintf(environment.Stdout, "Revoked this machine's previous device token (%s).\n", device.DeviceID)
		return
	}
}

// reportStaleDevice says that the old device is still registered.
func reportStaleDevice(environment *hubEnvironment, err error) {
	fmt.Fprintf(environment.Stderr, "jf: could not revoke this machine's previous device token (%v).\n", err)
	fmt.Fprintln(environment.Stderr, "jf: the hub may still list the old device. Run `jf device list` to see it, then `jf device revoke NAME`.")
}

// defaultDeviceName names this machine for the device list.
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
/* jf logout                                                           */
/* ------------------------------------------------------------------ */

func newLogoutCommand(environment *hubEnvironment, manifest func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Sign this machine out of the hub",
		Long: `Delete this machine's device token, and revoke it at the hub.

jf revokes the token at the hub first, then deletes the local file. The local
file is deleted even when the hub call fails, because the token on this disk is
what a person who takes this machine would read. jf says when the hub call did
not succeed, so you know to run "jf device revoke NAME" from another machine.

Run "jf login" to sign in again.`,
		Example: `  # Sign this machine out.
  jf logout`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, args []string) error {
			environment.ManifestPath = manifest()
			return runLogout(command.Context(), environment)
		},
	}
}

func runLogout(ctx context.Context, environment *hubEnvironment) error {
	tokenPath, err := hub.TokenPath()
	if err != nil {
		return err
	}
	style := environment.theme()

	// A machine with no token is already signed out. Saying so is friendlier
	// than an error, because the state the person asked for is the state they
	// have.
	if _, loadErr := hub.LoadToken(tokenPath); loadErr != nil {
		fmt.Fprintln(environment.Stdout, "This machine holds no device token, so it is already signed out.")
		return nil
	}

	// The revoke runs first, while the token still works. Its failure is not
	// fatal: the local file must go either way.
	revoked := false
	var revokeErr error
	if client, clientErr := environment.client(); clientErr == nil {
		if devices, listErr := client.ListDevices(ctx); listErr == nil {
			for _, device := range devices {
				if device.Current {
					revokeErr = client.RevokeDevice(ctx, device.DeviceID)
					revoked = revokeErr == nil
					break
				}
			}
		} else {
			revokeErr = listErr
		}
	} else {
		revokeErr = clientErr
	}

	if err := hub.DeleteToken(tokenPath); err != nil {
		return err
	}

	fmt.Fprintf(environment.Stdout, "%s Deleted the device token in %s.\n", style.Alive.Render("Done."), tokenPath)
	if revoked {
		fmt.Fprintln(environment.Stdout, "The hub revoked this machine's token as well.")
		return nil
	}
	if revokeErr != nil {
		fmt.Fprintf(environment.Stderr, "jf: the hub did not revoke the token (%v).\n", revokeErr)
	}
	fmt.Fprintln(environment.Stdout, "The hub may still list this machine. Run `jf device revoke NAME` from another machine to remove it.")
	return nil
}

/* ------------------------------------------------------------------ */
/* jf device list | jf device revoke                                   */
/* ------------------------------------------------------------------ */

func newDeviceCommand(environment *hubEnvironment, manifest func() string) *cobra.Command {
	group := &cobra.Command{
		Use:   "device",
		Short: "List the machines that hold a device token, or revoke one",
		Long: `Work with the machines that are signed in to the hub.

"jf device list" shows every machine and marks the one you are on. "jf device
revoke" removes one machine's token, by the name that the list shows or by its
device id. Any machine can revoke any other, so you can revoke a lost laptop from
the machine still in your hand.`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, args []string) error {
			return command.Help()
		},
	}

	list := &cobra.Command{
		Use:   "list",
		Short: "List every machine that is signed in to the hub",
		Long: `List every machine that holds a device token.

The table names each machine, its device id, when it was created, and when it
last called the hub. The machine you are on carries the note "this machine".`,
		Example: `  # List every signed-in machine.
  jf device list`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, args []string) error {
			environment.ManifestPath = manifest()
			return runDeviceList(command.Context(), environment)
		},
	}

	revoke := &cobra.Command{
		Use:   "revoke NAME",
		Short: "Remove one machine's device token",
		Long: `Remove one machine's device token, by its name or its device id.

Revoking this machine is allowed. jf says so when it happens, because the next
hub command here then needs "jf login" again.

Two machines with the same name are an error, not a guess. jf prints both device
ids and asks you to revoke by id, because revoking the wrong machine is not
something you can undo from the machine you just locked yourself out of.`,
		Example: `  # Revoke the machine named grumpyorange.
  jf device revoke grumpyorange`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			environment.ManifestPath = manifest()
			return runDeviceRevoke(command.Context(), environment, args[0])
		},
	}

	group.AddCommand(list, revoke)
	return group
}

func runDeviceList(ctx context.Context, environment *hubEnvironment) error {
	client, err := environment.client()
	if err != nil {
		return err
	}
	devices, err := client.ListDevices(ctx)
	if err != nil {
		return err
	}
	return renderDevices(environment.Stdout, environment.theme(), devices, environment.now())
}

// runDeviceRevoke removes one machine's token.
//
// The hub deletes by device id, so the name is looked up first. Revoking this
// machine is allowed, and the person is told what happened, because the next
// command on this machine then needs `jf login` again.
func runDeviceRevoke(ctx context.Context, environment *hubEnvironment, wanted string) error {
	client, err := environment.client()
	if err != nil {
		return err
	}
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
