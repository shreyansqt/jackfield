package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/shreyansqt/jackfield/internal/hub"
)

/* ------------------------------------------------------------------ */
/* jf cred get | jf cred set                                           */
/* ------------------------------------------------------------------ */

func newCredCommand(environment *hubEnvironment, manifest func() string) *cobra.Command {
	group := &cobra.Command{
		Use:   "cred",
		Short: "Read one credential from the hub, or write one",
		Long: `Work with the credentials that the hub holds.

"jf cred get" prints one credential, for a script. "jf cred set" writes one, and
every write needs a fresh browser approval.

Reading is cheap, because an agent reads constantly. Writing is rare, and you are
present when it happens, so a write costs one browser approval.`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, args []string) error {
			return command.Help()
		},
	}

	group.AddCommand(
		newCredGetCommand(environment, manifest),
		newCredSetCommand(environment, manifest),
	)
	return group
}

func newCredGetCommand(environment *hubEnvironment, manifest func() string) *cobra.Command {
	var noCache bool

	command := &cobra.Command{
		Use:   "get NAME",
		Short: "Print one credential to standard output, for scripts",
		Long: `Print one credential to standard output, and nothing else.

A script reads the value with a command substitution. Every message other than
the secret goes to standard error, so the substitution captures the secret alone.

jf caches the value under ~/.cache/jackfield for five minutes, and asks the hub
again after that. The lifetime means a credential that "jf cred set" replaced
reaches every machine within five minutes, with no action on those machines.

This is mostly internal plumbing, exposed for scripts and for a person who debugs
a connection.`,
		Example: `  # Print the Slack credential.
  jf cred get slack-smarta

  # Read it into a shell variable.
  token=$(jf cred get slack-smarta)

  # Skip the local cache and ask the hub.
  jf cred get --no-cache slack-smarta`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			environment.ManifestPath = manifest()
			return runCredGet(command.Context(), environment, args[0], noCache)
		},
	}

	command.Flags().BoolVar(&noCache, "no-cache", false,
		"Ask the hub even when a fresh cached copy exists")
	return command
}

// runCredGet prints one credential to stdout.
//
// The value goes to stdout with no other text, so a script can read it with a
// command substitution. Every message about what happened goes to stderr.
func runCredGet(ctx context.Context, environment *hubEnvironment, connection string, noCache bool) error {
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

func newCredSetCommand(environment *hubEnvironment, manifest func() string) *cobra.Command {
	var identity string
	var ticket string
	var fromStdin bool

	command := &cobra.Command{
		Use:   "set NAME",
		Short: "Store a credential in the hub",
		Long: `Write one credential to the hub, where every machine then reads it.

A write needs a fresh browser approval every time, so jf opens the hub's approval
page and asks you to paste back the ticket it shows. jf reads the secret from a
hidden prompt, or from standard input with --stdin, and never from a command
argument.

A secret is never a command argument, because arguments appear in the process
list where any other process on the machine reads them.

The ticket works once, for that one connection, for five minutes. The secret
never passes through the browser: only the ticket does, and the secret goes
straight from this machine to the hub.

jf clears this machine's cached copy after a write, so the next read here fetches
the value you just stored.`,
		Example: `  # Store a Slack credential, with a hidden prompt for the secret.
  jf cred set slack-smarta

  # Record who the credential acts as.
  jf cred set --identity you@example.com slack-smarta

  # Store a secret from a script.
  printf '%s' "$SECRET" | jf cred set --stdin --ticket TICKET slack-smarta`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			environment.ManifestPath = manifest()
			return runCredSet(command.Context(), environment, args[0], identity, ticket, fromStdin)
		},
	}

	command.Flags().StringVar(&identity, "identity", "",
		"Who this credential acts as, for the status panel")
	command.Flags().StringVar(&ticket, "ticket", "",
		"An approval ticket from the hub's approval page")
	command.Flags().BoolVar(&fromStdin, "stdin", false,
		"Read the secret from standard input instead of prompting")
	return command
}

func runCredSet(ctx context.Context, environment *hubEnvironment, connection string, identity string, ticket string, fromStdin bool) error {
	baseURL, err := hub.BaseURL(environment.ManifestPath)
	if err != nil {
		return err
	}

	// The approval ticket comes from the browser, never from this machine's
	// device token. The hub refuses a write that carries only a device token,
	// and that refusal is the point of the write path.
	approvalTicket := strings.TrimSpace(ticket)
	if approvalTicket == "" {
		approvalTicket, err = collectApprovalTicket(environment, baseURL, connection)
		if err != nil {
			return err
		}
	}

	secret, err := readSecret(environment, fromStdin, connection)
	if err != nil {
		return err
	}
	if strings.TrimSpace(secret) == "" {
		return fmt.Errorf("the secret is empty; nothing was sent to the hub")
	}

	client := hub.New(baseURL, "")
	if err := client.PutCredential(ctx, connection, secret, identity, approvalTicket); err != nil {
		return err
	}

	// The old value is now wrong on this machine. Removing it means the next
	// read here fetches the credential that was just written.
	if cacheDirectory, cacheErr := hub.CacheDir(); cacheErr == nil {
		if err := hub.NewCache(cacheDirectory).Forget(connection); err != nil {
			fmt.Fprintf(environment.Stderr, "jf: %v\n", err)
		}
	}

	style := environment.theme()
	fmt.Fprintf(environment.Stdout, "%s Stored the credential for %q in the hub.\n",
		style.Alive.Render("Done."), connection)
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
// The page answers `GET /ui/approvals?connection=<name>`. Its form then posts back
// to the same path, and the hub tells a browser apart from this client by the
// Accept and Content-Type headers. This client always sends JSON headers, so it
// keeps receiving the JSON answer.
func collectApprovalTicket(environment *hubEnvironment, baseURL string, connection string) (string, error) {
	approvalPageURL := fmt.Sprintf("%s/ui/approvals?connection=%s", baseURL, url.QueryEscape(connection))
	style := environment.theme()

	fmt.Fprintf(environment.Stdout, "Writing a credential needs a fresh browser approval, every time.\n\n")
	fmt.Fprintf(environment.Stdout, "Approve the write for %q, then copy the ticket the page shows:\n\n", connection)
	fmt.Fprintf(environment.Stdout, "  %s %s\n\n", style.Label.Render("open:"), approvalPageURL)

	if err := environment.OpenBrowser(approvalPageURL); err != nil {
		fmt.Fprintf(environment.Stderr, "jf: could not open a browser (%v). Open the URL above yourself.\n", err)
	}

	fmt.Fprint(environment.Stdout, "Approval ticket: ")
	reader := bufio.NewReader(environment.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("read the approval ticket: %w", err)
	}
	approvalTicket := strings.TrimSpace(line)
	if approvalTicket == "" {
		return "", fmt.Errorf("no approval ticket was given; nothing was sent to the hub")
	}
	return approvalTicket, nil
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

/* ------------------------------------------------------------------ */
/* jf auth — the deprecated alias for `jf cred set`                    */
/* ------------------------------------------------------------------ */

// newAuthAliasCommand keeps `jf auth` working after the rename.
//
// The command is hidden, so it does not appear in the help or in the schema, but
// it still runs. Yesterday's documents and a person's muscle memory both keep
// working, and the pointer line teaches the new name.
func newAuthAliasCommand(environment *hubEnvironment, manifest func() string) *cobra.Command {
	var identity string
	var ticket string
	var fromStdin bool

	command := &cobra.Command{
		Use:        "auth NAME",
		Short:      "Deprecated. Use `jf cred set` instead",
		Hidden:     true,
		Deprecated: "use `jf cred set NAME` instead. `jf auth` still works, and it will be removed in a later release.",
		Args:       cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			environment.ManifestPath = manifest()
			return runCredSet(command.Context(), environment, args[0], identity, ticket, fromStdin)
		},
	}

	command.Flags().StringVar(&identity, "identity", "",
		"Who this credential acts as, for the status panel")
	command.Flags().StringVar(&ticket, "ticket", "",
		"An approval ticket from the hub's approval page")
	command.Flags().BoolVar(&fromStdin, "stdin", false,
		"Read the secret from standard input instead of prompting")
	return command
}
