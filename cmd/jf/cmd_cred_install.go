package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

/* ------------------------------------------------------------------ */
/* jf cred install NAME                                                 */
/* ------------------------------------------------------------------ */

// The hub is a vault, not a broker. It stores the smallest durable secret for a
// connection and never mints a live token. `jf cred install` is the step that
// takes that durable secret and makes the local tool ready to use it.
//
// For a gog credential the durable secret is the Google refresh token. The hub
// holds it; this command fetches it and runs `gog auth import`, so gog then
// refreshes its own access tokens with no further help from the hub.

// gogSecret is the JSON convention stored in the hub for a gog credential.
//
// The hub value is an opaque string, so a richer credential rides inside it as
// JSON. The hub never parses this; only this machine does. RefreshToken is the
// one field a machine needs to make gog work. The rest is metadata that keeps
// the import unambiguous and lets `jf status` show who the credential acts as.
type gogSecret struct {
	// RefreshToken is the long-lived Google OAuth refresh token. It is the only
	// secret. gog exchanges it for short-lived access tokens on its own.
	RefreshToken string `json:"refresh_token"`
	// Email is the Google account this token belongs to, for example
	// "shreyansqt@gmail.com". gog stores the token under this account.
	Email string `json:"email"`
	// Client is the gog OAuth client name that owns the token bucket. gog
	// defaults it to "default"; this machine passes it through so the import
	// lands in the same bucket the token was minted under.
	Client string `json:"client,omitempty"`
	// ClientID is the Google OAuth client id, recorded for provenance. gog does
	// not need it for an import, because the client credentials already live in
	// the gog config. It is stored so the credential is self-describing.
	ClientID string `json:"client_id,omitempty"`
}

// credInstaller makes one connection's durable secret usable by its local tool.
//
// The value the installer receives is the decrypted hub secret. The installer
// turns it into whatever local state the tool needs. It reports what it did in
// one line, for the person watching.
type credInstaller struct {
	// connection is the hub connection name this installer handles.
	connection string
	// summary is the one-line help shown in `jf cred install --help`.
	summary string
	// install performs the work. It returns a short line naming what it did.
	install func(environment *hubEnvironment, secret string) (string, error)
}

// credInstallers is the registry of connections `jf cred install` can set up.
//
// A connection that is only fetched and printed does not need an entry here;
// `jf cred get` covers that. An entry is needed only when the value must be
// turned into local tool state, as a gog refresh token must.
var credInstallers = map[string]credInstaller{
	"gog-personal": {
		connection: "gog-personal",
		summary:    "Import the personal Google refresh token into gog on this machine",
		install:    installGogCredential,
	},
}

func newCredInstallCommand(environment *hubEnvironment, manifest func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "install NAME",
		Short: "Make one hub credential usable by its local tool",
		Long: `Fetch one credential from the hub and set up the local tool that uses it.

Some credentials are more than a value a script reads. A gog credential is a
Google refresh token that the gog CLI must hold in its own keyring before any
gog command works. "jf cred install" fetches the durable secret from the hub and
hands it to the tool, so the tool is ready on this machine.

The hub stays a vault. It holds the refresh token and nothing more; gog mints its
own access tokens from that token afterwards, with no further call to the hub.

The refresh token never appears in the process list. jf pipes it to
"gog auth import --refresh-token-stdin" on standard input, never as an argument.

Reading the credential needs a device token, so run "jf login" first. The install
itself writes nothing back to the hub.`,
		Example: `  # Import the personal Google refresh token into gog here.
  jf cred install gog-personal`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			environment.ManifestPath = manifest()
			return runCredInstall(command.Context(), environment, args[0])
		},
	}
}

func runCredInstall(ctx context.Context, environment *hubEnvironment, connection string) error {
	installer, ok := credInstallers[connection]
	if !ok {
		known := make([]string, 0, len(credInstallers))
		for name := range credInstallers {
			known = append(known, name)
		}
		return fmt.Errorf("no installer for %q. jf cred install knows: %s. For any other credential, read it with `jf cred get`", connection, strings.Join(known, ", "))
	}

	client, err := environment.client()
	if err != nil {
		return err
	}
	credential, err := client.GetCredential(ctx, installer.connection)
	if err != nil {
		return err
	}

	summary, err := installer.install(environment, credential.Secret)
	if err != nil {
		return err
	}

	style := environment.theme()
	fmt.Fprintf(environment.Stdout, "%s %s\n", style.Alive.Render("Done."), summary)
	return nil
}

// installGogCredential imports the personal Google refresh token into gog.
//
// The hub value is the JSON convention above. This function parses it, then runs
// `gog auth import --refresh-token-stdin -a <email>` with the refresh token on
// standard input. The token is never a command argument, so it never reaches the
// process list.
//
// This calls the real gog binary directly, not the jackfield shim. An import
// must pass `-a <email>`, and the shim's gog profile denies `-a`. The direct
// binary path also keeps the install working on a machine that has no manifest
// yet, which is the state a fresh machine is in.
func installGogCredential(environment *hubEnvironment, secret string) (string, error) {
	var parsed gogSecret
	if err := json.Unmarshal([]byte(secret), &parsed); err != nil {
		return "", fmt.Errorf("the gog-personal credential in the hub is not the expected JSON. Store it with the refresh_token and email fields: %w", err)
	}
	if strings.TrimSpace(parsed.RefreshToken) == "" {
		return "", fmt.Errorf("the gog-personal credential has no refresh_token")
	}
	if strings.TrimSpace(parsed.Email) == "" {
		return "", fmt.Errorf("the gog-personal credential has no email; gog needs an account to store the token under")
	}

	gogPath, err := environment.lookGog()
	if err != nil {
		return "", err
	}

	command := exec.Command(gogPath, gogImportArgs(parsed)...)
	command.Stdin = strings.NewReader(parsed.RefreshToken)
	// gog prints its own confirmation to stdout. Send both streams to stderr so
	// nothing but jf's own "Done." line reaches stdout, which keeps the command
	// quiet enough to run from a script.
	command.Stdout = environment.Stderr
	command.Stderr = environment.Stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("gog auth import failed: %w", err)
	}

	return fmt.Sprintf("Imported the Google refresh token for %s into gog.", parsed.Email), nil
}

// lookGog finds the REAL gog binary, never the jackfield shim.
//
// A test replaces this to point at a stub, so the install path can be exercised
// with no real gog and no real account. The default follows the same order as
// scripts/extract-gog-token.sh.
//
// The shim must be skipped. The import passes `-a` and `--client`, which the
// gog shim profile denies by design, so a shim on PATH ahead of the real binary
// makes the import fail with "argument -a can override the selected identity".
// The shim is a symlink back to the jf binary, so findRealGog rejects any
// candidate that resolves to a file named "jf".
func (environment *hubEnvironment) lookGog() (string, error) {
	if environment.LookGog != nil {
		return environment.LookGog()
	}
	return findRealGog(os.Getenv("GOG_BIN"))
}

// findRealGog returns the path of the real gog binary, skipping the shim.
//
// The order matches the extract script:
//  1. gogBinOverride (from GOG_BIN), when it is set and executable.
//  2. /opt/homebrew/bin/gog, the Homebrew install, when it is executable.
//  3. gog on PATH, but never a candidate that resolves to the jf binary.
//
// gogBinOverride is a parameter, not read from the environment here, so a test
// drives every branch without setting a process-wide variable.
func findRealGog(gogBinOverride string) (string, error) {
	return findRealGogIn(gogBinOverride, homebrewGogPath)
}

// findRealGogIn is findRealGog with the Homebrew path injected, so a test drives
// the PATH branch on a machine that also has a real Homebrew gog.
func findRealGogIn(gogBinOverride string, homebrewPath string) (string, error) {
	if override := strings.TrimSpace(gogBinOverride); override != "" {
		if isExecutableFile(override) {
			return override, nil
		}
		return "", fmt.Errorf("GOG_BIN is set to %q, which is not an executable file", override)
	}

	if homebrewPath != "" && isExecutableFile(homebrewPath) {
		return homebrewPath, nil
	}

	path, err := exec.LookPath("gog")
	if err != nil {
		return "", fmt.Errorf("cannot find the real gog binary. Set GOG_BIN to its path, or install gog, then run this again")
	}
	if isJackfieldShim(path) {
		return "", fmt.Errorf("the only gog on PATH is the jackfield shim, which cannot run the import (it denies -a). Set GOG_BIN to the real gog binary, for example /opt/homebrew/bin/gog")
	}
	return path, nil
}

// homebrewGogPath is where Homebrew installs gog on this machine. It is the same
// path the extract script and the manifest profiles name.
const homebrewGogPath = "/opt/homebrew/bin/gog"

// isJackfieldShim reports whether a path is a jackfield shim rather than the real
// gog. Every shim is a symlink to the jf binary, so a candidate whose resolved
// target is named "jf" is the shim.
func isJackfieldShim(path string) bool {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	return filepath.Base(resolved) == "jf"
}

// isExecutableFile reports whether a path is a regular file with an execute bit.
func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode().Perm()&0o111 != 0
}

// gogImportArgs is the argument vector installGogCredential builds for gog.
//
// It is a small pure helper, so a test can assert the contract (stdin token,
// --email, no token in argv) without running gog.
//
// The import needs its own required `--email` flag, not the global `-a/--account`
// flag. `-a` selects the account for a normal command and does not satisfy the
// import's `--email`; gog fails with "missing flags: --email=STRING" when only
// `-a` is given.
func gogImportArgs(parsed gogSecret) []string {
	args := []string{"auth", "import", "--refresh-token-stdin", "--email", parsed.Email}
	if strings.TrimSpace(parsed.Client) != "" {
		args = append(args, "--client", parsed.Client)
	}
	return args
}
