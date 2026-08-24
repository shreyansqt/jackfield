package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shreyansqt/jackfield/internal/hub"
)

// commandHub plays the hub for the command tests.
//
// It is deliberately separate from the client package's fake. This one exercises
// the commands, so it records what a person would care about: which secret the
// hub received, and which device the command revoked.
type commandHub struct {
	server *httptest.Server

	approved   bool
	credential hub.Credential
	devices    []hub.Device
	status     hub.Status

	putBody          map[string]string
	revokedDeviceIDs []string
	tokenPolls       int
}

func newCommandHub(t *testing.T) *commandHub {
	t.Helper()
	fake := &commandHub{
		approved: true,
		credential: hub.Credential{
			Connection: "slack-smarta",
			Secret:     "xoxp-from-the-hub",
			Identity:   "shreyans@example.com",
			UpdatedAt:  time.Now().UnixMilli(),
		},
		status: hub.Status{
			Connections: []hub.Connection{
				{Connection: "slack-smarta", Identity: "shreyans@example.com", AgeSeconds: 120},
			},
		},
		devices: []hub.Device{
			{DeviceID: "aaa", Name: "macbook", CreatedAt: time.Now().UnixMilli(), Current: true},
			{DeviceID: "bbb", Name: "grumpyorange", CreatedAt: time.Now().UnixMilli()},
		},
	}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.server.Close)
	return fake
}

func (fake *commandHub) serve(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Content-Type", "application/json")
	encode := json.NewEncoder(response)

	switch {
	case request.URL.Path == "/device/code":
		encode.Encode(hub.DeviceCode{
			DeviceCode:              "the-device-code",
			UserCode:                "BCDF-GHJK",
			VerificationURI:         fake.server.URL + "/device",
			VerificationURIComplete: fake.server.URL + "/device?user_code=BCDF-GHJK",
			ExpiresIn:               900,
			Interval:                5,
		})

	case request.URL.Path == "/device/token":
		fake.tokenPolls++
		if !fake.approved {
			response.WriteHeader(http.StatusBadRequest)
			encode.Encode(map[string]string{"error": "authorization_pending"})
			return
		}
		encode.Encode(hub.DeviceToken{
			AccessToken: "jfd_the-device-token",
			TokenType:   "Bearer",
			DeviceName:  "grumpyorange",
		})

	case request.URL.Path == "/status":
		encode.Encode(fake.status)

	case request.URL.Path == "/devices" && request.Method == http.MethodGet:
		encode.Encode(hub.DeviceList{Devices: fake.devices})

	case strings.HasPrefix(request.URL.Path, "/devices/") && request.Method == http.MethodDelete:
		deviceID := strings.TrimPrefix(request.URL.Path, "/devices/")
		fake.revokedDeviceIDs = append(fake.revokedDeviceIDs, deviceID)
		encode.Encode(map[string]string{"revoked": deviceID})

	case strings.HasPrefix(request.URL.Path, "/creds/") && request.Method == http.MethodGet:
		encode.Encode(fake.credential)

	case strings.HasPrefix(request.URL.Path, "/creds/") && request.Method == http.MethodPut:
		json.NewDecoder(request.Body).Decode(&fake.putBody)
		encode.Encode(map[string]any{"connection": "slack-smarta", "updated_at": time.Now().UnixMilli()})

	default:
		response.WriteHeader(http.StatusNotFound)
		encode.Encode(map[string]string{"error": "not_found"})
	}
}

// testEnvironment points every path at a temporary directory and replaces the
// browser, the clock, and the terminal, so no test touches the real machine.
type testEnvironment struct {
	*hubEnvironment
	stdout       *bytes.Buffer
	stderr       *bytes.Buffer
	openedURLs   []string
	tokenPath    string
	cacheDir     string
	secretPrompt string
}

func newTestEnvironment(t *testing.T, fake *commandHub, stdin string) *testEnvironment {
	t.Helper()
	home := t.TempDir()
	tokenPath := filepath.Join(home, "config", "device-token")
	cacheDir := filepath.Join(home, "cache")

	t.Setenv(hub.EnvBaseURL, fake.server.URL)
	t.Setenv("JF_TOKEN_FILE", tokenPath)
	t.Setenv("JF_CACHE_DIR", cacheDir)

	environment := &testEnvironment{
		stdout:    &bytes.Buffer{},
		stderr:    &bytes.Buffer{},
		tokenPath: tokenPath,
		cacheDir:  cacheDir,
	}
	environment.hubEnvironment = &hubEnvironment{
		Stdin:       strings.NewReader(stdin),
		Stdout:      environment.stdout,
		Stderr:      environment.stderr,
		OpenBrowser: func(target string) error { environment.openedURLs = append(environment.openedURLs, target); return nil },
		HasDisplay:  func() bool { return true },
		ReadSecret: func(prompt string) (string, error) {
			environment.secretPrompt = prompt
			return "xoxp-typed-by-hand", nil
		},
		Sleep:    func(time.Duration) {},
		Now:      func() time.Time { return time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC) },
		Hostname: func() (string, error) { return "grumpyorange.local", nil },
	}
	return environment
}

func (environment *testEnvironment) signIn(t *testing.T) {
	t.Helper()
	if err := hub.SaveToken(environment.tokenPath, "jfd_the-device-token"); err != nil {
		t.Fatal(err)
	}
}

/* ------------------------------------------------------------------ */
/* jf login                                                            */
/* ------------------------------------------------------------------ */

func TestLoginOpensTheBrowserAndSavesTheToken(t *testing.T) {
	fake := newCommandHub(t)
	environment := newTestEnvironment(t, fake, "")

	if err := runLogin(context.Background(), environment.hubEnvironment, nil); err != nil {
		t.Fatal(err)
	}

	if len(environment.openedURLs) != 1 {
		t.Fatalf("the command opened %d URLs, want 1", len(environment.openedURLs))
	}
	if !strings.Contains(environment.openedURLs[0], "user_code=BCDF-GHJK") {
		t.Fatalf("got URL %q, want the code already filled in", environment.openedURLs[0])
	}

	token, err := hub.LoadToken(environment.tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if token != "jfd_the-device-token" {
		t.Fatalf("got token %q, want jfd_the-device-token", token)
	}

	// The browser flow still prints the code and the URL, because opening a
	// browser can fail silently on a machine with an odd default handler.
	output := environment.stdout.String()
	if !strings.Contains(output, "BCDF-GHJK") {
		t.Fatalf("got %q, want the short code printed as well", output)
	}
	if !strings.Contains(output, fake.server.URL) {
		t.Fatalf("got %q, want the URL printed as well", output)
	}
}

func TestLoginWithDeviceCodeOpensNoBrowser(t *testing.T) {
	fake := newCommandHub(t)
	environment := newTestEnvironment(t, fake, "")

	if err := runLogin(context.Background(), environment.hubEnvironment, []string{"--device-code"}); err != nil {
		t.Fatal(err)
	}

	if len(environment.openedURLs) != 0 {
		t.Fatalf("--device-code must open no browser, opened %v", environment.openedURLs)
	}
	output := environment.stdout.String()
	if !strings.Contains(output, "BCDF-GHJK") {
		t.Fatalf("got %q, want the short code for the other device", output)
	}
	if !strings.Contains(output, "another device") {
		t.Fatalf("got %q, want the instruction to use another device", output)
	}
	if _, err := hub.LoadToken(environment.tokenPath); err != nil {
		t.Fatalf("the device-code flow must save a token too: %v", err)
	}
}

// A machine with no display gets the device-code flow without being asked.
func TestLoginWithoutADisplayOpensNoBrowser(t *testing.T) {
	fake := newCommandHub(t)
	environment := newTestEnvironment(t, fake, "")
	environment.HasDisplay = func() bool { return false }

	if err := runLogin(context.Background(), environment.hubEnvironment, nil); err != nil {
		t.Fatal(err)
	}
	if len(environment.openedURLs) != 0 {
		t.Fatalf("a headless machine must open no browser, opened %v", environment.openedURLs)
	}
}

// --browser overrides the guess, for a machine the check reads wrongly.
func TestLoginWithBrowserFlagOverridesTheDisplayCheck(t *testing.T) {
	fake := newCommandHub(t)
	environment := newTestEnvironment(t, fake, "")
	environment.HasDisplay = func() bool { return false }

	if err := runLogin(context.Background(), environment.hubEnvironment, []string{"--browser"}); err != nil {
		t.Fatal(err)
	}
	if len(environment.openedURLs) != 1 {
		t.Fatalf("--browser must open a browser, opened %v", environment.openedURLs)
	}
}

func TestLoginPollsUntilThePersonApproves(t *testing.T) {
	fake := newCommandHub(t)
	fake.approved = false
	environment := newTestEnvironment(t, fake, "")

	// The approval lands after the third poll.
	polls := 0
	environment.Sleep = func(time.Duration) {
		polls++
		if polls == 3 {
			fake.approved = true
		}
	}

	if err := runLogin(context.Background(), environment.hubEnvironment, nil); err != nil {
		t.Fatal(err)
	}
	if fake.tokenPolls != 4 {
		t.Fatalf("the command polled %d times, want 4", fake.tokenPolls)
	}
	if _, err := hub.LoadToken(environment.tokenPath); err != nil {
		t.Fatal(err)
	}
}

func TestLoginRefusesBothFlowFlagsTogether(t *testing.T) {
	fake := newCommandHub(t)
	environment := newTestEnvironment(t, fake, "")

	err := runLogin(context.Background(), environment.hubEnvironment, []string{"--device-code", "--browser"})
	if err == nil {
		t.Fatal("expected an error for two conflicting flags")
	}
}

// The device name is what `jf devices` shows, so it defaults to the short
// hostname rather than to the full one.
func TestLoginNamesTheMachineByItsShortHostname(t *testing.T) {
	fake := newCommandHub(t)
	environment := newTestEnvironment(t, fake, "")
	if name := defaultDeviceName(environment.hubEnvironment); name != "grumpyorange" {
		t.Fatalf("got name %q, want grumpyorange", name)
	}
}

/* ------------------------------------------------------------------ */
/* jf status                                                           */
/* ------------------------------------------------------------------ */

func TestStatusRendersThePanel(t *testing.T) {
	fake := newCommandHub(t)
	environment := newTestEnvironment(t, fake, "")
	environment.signIn(t)

	if err := runStatus(context.Background(), environment.hubEnvironment, nil); err != nil {
		t.Fatal(err)
	}

	output := environment.stdout.String()
	for _, want := range []string{fake.server.URL, "slack-smarta", "shreyans@example.com", "2m", "not probed yet"} {
		if !strings.Contains(output, want) {
			t.Fatalf("got %q, want it to contain %q", output, want)
		}
	}
	// The hub marks the calling device, and the panel names it.
	if !strings.Contains(output, "macbook") {
		t.Fatalf("got %q, want this machine's device name", output)
	}
}

func TestStatusWithoutATokenAsksForLogin(t *testing.T) {
	fake := newCommandHub(t)
	environment := newTestEnvironment(t, fake, "")

	err := runStatus(context.Background(), environment.hubEnvironment, nil)
	if err == nil {
		t.Fatal("expected an error on a machine with no device token")
	}
	if !strings.Contains(err.Error(), "jf login") {
		t.Fatalf("got %q, want a message naming `jf login`", err)
	}
}

/* ------------------------------------------------------------------ */
/* jf devices                                                          */
/* ------------------------------------------------------------------ */

func TestDevicesListsEveryMachine(t *testing.T) {
	fake := newCommandHub(t)
	environment := newTestEnvironment(t, fake, "")
	environment.signIn(t)

	if err := runDevices(context.Background(), environment.hubEnvironment, nil); err != nil {
		t.Fatal(err)
	}
	output := environment.stdout.String()
	if !strings.Contains(output, "macbook") || !strings.Contains(output, "grumpyorange") {
		t.Fatalf("got %q, want both machines", output)
	}
	if !strings.Contains(output, "this machine") {
		t.Fatalf("got %q, want the calling machine marked", output)
	}
}

// The person names the machine; the hub deletes by device id. The command does
// that translation.
func TestDevicesRevokeResolvesTheNameToADeviceID(t *testing.T) {
	fake := newCommandHub(t)
	environment := newTestEnvironment(t, fake, "")
	environment.signIn(t)

	if err := runDevices(context.Background(), environment.hubEnvironment, []string{"revoke", "grumpyorange"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.revokedDeviceIDs) != 1 || fake.revokedDeviceIDs[0] != "bbb" {
		t.Fatalf("got revoked ids %v, want [bbb]", fake.revokedDeviceIDs)
	}
	if !strings.Contains(environment.stdout.String(), "grumpyorange") {
		t.Fatalf("got %q, want the revoked machine named", environment.stdout.String())
	}
}

// Revoking this machine is allowed. The person must be told, because the next
// command here needs `jf login` again.
func TestDevicesRevokeSaysWhenItWasThisMachine(t *testing.T) {
	fake := newCommandHub(t)
	environment := newTestEnvironment(t, fake, "")
	environment.signIn(t)

	if err := runDevices(context.Background(), environment.hubEnvironment, []string{"revoke", "macbook"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(environment.stdout.String(), "jf login") {
		t.Fatalf("got %q, want the note that this machine must sign in again", environment.stdout.String())
	}
}

func TestDevicesRevokeRejectsAnUnknownName(t *testing.T) {
	fake := newCommandHub(t)
	environment := newTestEnvironment(t, fake, "")
	environment.signIn(t)

	err := runDevices(context.Background(), environment.hubEnvironment, []string{"revoke", "no-such-machine"})
	if err == nil {
		t.Fatal("expected an error for an unknown machine")
	}
	if len(fake.revokedDeviceIDs) != 0 {
		t.Fatalf("nothing may be revoked, revoked %v", fake.revokedDeviceIDs)
	}
}

func TestDevicesRevokeNeedsAName(t *testing.T) {
	fake := newCommandHub(t)
	environment := newTestEnvironment(t, fake, "")
	environment.signIn(t)

	if err := runDevices(context.Background(), environment.hubEnvironment, []string{"revoke"}); err == nil {
		t.Fatal("expected an error when no machine is named")
	}
}

/* ------------------------------------------------------------------ */
/* jf creds get                                                        */
/* ------------------------------------------------------------------ */

// The secret goes to stdout alone, so a script reads it with a command
// substitution. Every other message goes to stderr.
func TestCredsGetPrintsOnlyTheSecret(t *testing.T) {
	fake := newCommandHub(t)
	environment := newTestEnvironment(t, fake, "")
	environment.signIn(t)

	if err := runCreds(context.Background(), environment.hubEnvironment, []string{"get", "slack-smarta"}); err != nil {
		t.Fatal(err)
	}
	if got := environment.stdout.String(); got != "xoxp-from-the-hub\n" {
		t.Fatalf("got %q, want the secret and nothing else", got)
	}
}

func TestCredsGetCachesTheCredential(t *testing.T) {
	fake := newCommandHub(t)
	environment := newTestEnvironment(t, fake, "")
	environment.signIn(t)

	if err := runCreds(context.Background(), environment.hubEnvironment, []string{"get", "slack-smarta"}); err != nil {
		t.Fatal(err)
	}

	// The hub now answers with a different secret. A second call inside the
	// lifetime must still return the cached one, which proves it did not ask.
	fake.credential.Secret = "xoxp-changed-at-the-hub"
	environment.stdout.Reset()
	if err := runCreds(context.Background(), environment.hubEnvironment, []string{"get", "slack-smarta"}); err != nil {
		t.Fatal(err)
	}
	if got := environment.stdout.String(); got != "xoxp-from-the-hub\n" {
		t.Fatalf("got %q, want the cached secret", got)
	}
}

func TestCredsGetAsksTheHubAgainAfterTheCacheExpires(t *testing.T) {
	fake := newCommandHub(t)
	environment := newTestEnvironment(t, fake, "")
	environment.signIn(t)

	clock := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	environment.Now = func() time.Time { return clock }

	if err := runCreds(context.Background(), environment.hubEnvironment, []string{"get", "slack-smarta"}); err != nil {
		t.Fatal(err)
	}

	fake.credential.Secret = "xoxp-changed-at-the-hub"
	clock = clock.Add(hub.CacheTTL + time.Second)
	environment.stdout.Reset()
	if err := runCreds(context.Background(), environment.hubEnvironment, []string{"get", "slack-smarta"}); err != nil {
		t.Fatal(err)
	}
	if got := environment.stdout.String(); got != "xoxp-changed-at-the-hub\n" {
		t.Fatalf("got %q, want the new secret from the hub", got)
	}
}

func TestCredsGetWithNoCacheAlwaysAsksTheHub(t *testing.T) {
	fake := newCommandHub(t)
	environment := newTestEnvironment(t, fake, "")
	environment.signIn(t)

	if err := runCreds(context.Background(), environment.hubEnvironment, []string{"get", "slack-smarta"}); err != nil {
		t.Fatal(err)
	}

	fake.credential.Secret = "xoxp-changed-at-the-hub"
	environment.stdout.Reset()
	if err := runCreds(context.Background(), environment.hubEnvironment, []string{"--no-cache", "get", "slack-smarta"}); err != nil {
		t.Fatal(err)
	}
	if got := environment.stdout.String(); got != "xoxp-changed-at-the-hub\n" {
		t.Fatalf("got %q, want the hub's current secret", got)
	}
}

func TestCredsGetNeedsAConnectionName(t *testing.T) {
	fake := newCommandHub(t)
	environment := newTestEnvironment(t, fake, "")
	environment.signIn(t)

	if err := runCreds(context.Background(), environment.hubEnvironment, []string{"get"}); err == nil {
		t.Fatal("expected an error when no connection is named")
	}
}

/* ------------------------------------------------------------------ */
/* jf auth                                                             */
/* ------------------------------------------------------------------ */

func TestAuthSendsTheSecretWithTheApprovalTicket(t *testing.T) {
	fake := newCommandHub(t)
	// The person pastes the ticket at the prompt.
	environment := newTestEnvironment(t, fake, "the-approval-ticket\n")

	err := runAuth(context.Background(), environment.hubEnvironment,
		[]string{"--identity", "shreyans@example.com", "slack-smarta"})
	if err != nil {
		t.Fatal(err)
	}

	if fake.putBody["approval_ticket"] != "the-approval-ticket" {
		t.Fatalf("got ticket %q, want the-approval-ticket", fake.putBody["approval_ticket"])
	}
	if fake.putBody["secret"] != "xoxp-typed-by-hand" {
		t.Fatalf("got secret %q, want the one from the hidden prompt", fake.putBody["secret"])
	}
	if fake.putBody["identity"] != "shreyans@example.com" {
		t.Fatalf("got identity %q, want shreyans@example.com", fake.putBody["identity"])
	}
	// The browser is where the approval happens. The page needs the connection
	// in the query, so it can name the write the person is approving.
	if len(environment.openedURLs) != 1 {
		t.Fatalf("got opened URLs %v, want the hub's approval page", environment.openedURLs)
	}
	if !strings.Contains(environment.openedURLs[0], "/approvals?connection=slack-smarta") {
		t.Fatalf("got URL %q, want the approval page for this connection", environment.openedURLs[0])
	}
}

// The hub answers /approvals as HTML or as JSON, and it chooses by the headers.
// This client must stay on the JSON side: it sends Accept: application/json and
// never a form content type, so a browser form and this client stay apart.
func TestClientHeadersKeepTheJSONContract(t *testing.T) {
	var accept, contentType string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		accept = request.Header.Get("Accept")
		contentType = request.Header.Get("Content-Type")
		response.Header().Set("Content-Type", "application/json")
		json.NewEncoder(response).Encode(map[string]any{"connection": "slack-smarta"})
	}))
	defer server.Close()

	client := hub.New(server.URL, "")
	client.HTTP = server.Client()
	if err := client.PutCredential(context.Background(), "slack-smarta", "xoxp-1", "", "the-ticket"); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(accept, "application/json") {
		t.Fatalf("got Accept %q, want application/json", accept)
	}
	if strings.Contains(accept, "text/html") {
		t.Fatalf("got Accept %q; text/html would switch the hub to its HTML answer", accept)
	}
	if strings.Contains(contentType, "application/x-www-form-urlencoded") {
		t.Fatalf("got Content-Type %q; a form type would switch the hub to its HTML answer", contentType)
	}
}

// The secret is read from a hidden prompt or from standard input, never from an
// argument. Arguments appear in the process list.
func TestAuthReadsTheSecretFromStandardInput(t *testing.T) {
	fake := newCommandHub(t)
	environment := newTestEnvironment(t, fake, "xoxp-piped-in\n")

	err := runAuth(context.Background(), environment.hubEnvironment,
		[]string{"--ticket", "the-approval-ticket", "--stdin", "slack-smarta"})
	if err != nil {
		t.Fatal(err)
	}
	if fake.putBody["secret"] != "xoxp-piped-in" {
		t.Fatalf("got secret %q, want xoxp-piped-in", fake.putBody["secret"])
	}
	// With a ticket in hand there is no reason to open a browser.
	if len(environment.openedURLs) != 0 {
		t.Fatalf("got opened URLs %v, want none when the ticket is given", environment.openedURLs)
	}
}

// The write replaces what this machine cached, so the old value must go.
func TestAuthClearsTheCachedCredential(t *testing.T) {
	fake := newCommandHub(t)
	environment := newTestEnvironment(t, fake, "")
	environment.signIn(t)

	if err := runCreds(context.Background(), environment.hubEnvironment, []string{"get", "slack-smarta"}); err != nil {
		t.Fatal(err)
	}
	// The same frozen clock as the command, so the entry is inside its lifetime.
	cache := hub.NewCache(environment.cacheDir)
	cache.Now = environment.Now
	if _, found := cache.Get("slack-smarta"); !found {
		t.Fatal("the read must fill the cache first")
	}

	environment.Stdin = strings.NewReader("xoxp-new\n")
	err := runAuth(context.Background(), environment.hubEnvironment,
		[]string{"--ticket", "the-approval-ticket", "--stdin", "slack-smarta"})
	if err != nil {
		t.Fatal(err)
	}
	if _, found := cache.Get("slack-smarta"); found {
		t.Fatal("the write must clear the cached value, so the next read reaches the hub")
	}
}

func TestAuthRefusesAnEmptySecret(t *testing.T) {
	fake := newCommandHub(t)
	environment := newTestEnvironment(t, fake, "")

	err := runAuth(context.Background(), environment.hubEnvironment,
		[]string{"--ticket", "the-approval-ticket", "--stdin", "slack-smarta"})
	if err == nil {
		t.Fatal("expected an error for an empty secret")
	}
	if fake.putBody != nil {
		t.Fatal("nothing may reach the hub when the secret is empty")
	}
}

func TestAuthRefusesWhenNoTicketIsGiven(t *testing.T) {
	fake := newCommandHub(t)
	environment := newTestEnvironment(t, fake, "\n")

	err := runAuth(context.Background(), environment.hubEnvironment, []string{"slack-smarta"})
	if err == nil {
		t.Fatal("expected an error when the person gives no approval ticket")
	}
	if fake.putBody != nil {
		t.Fatal("nothing may reach the hub without an approval ticket")
	}
}

func TestAuthNeedsAConnectionName(t *testing.T) {
	fake := newCommandHub(t)
	environment := newTestEnvironment(t, fake, "")

	if err := runAuth(context.Background(), environment.hubEnvironment, nil); err == nil {
		t.Fatal("expected an error when no connection is named")
	}
}

/* ------------------------------------------------------------------ */
/* dispatch                                                            */
/* ------------------------------------------------------------------ */

func TestHubActionsAreRecognised(t *testing.T) {
	for _, action := range []string{"login", "status", "devices", "creds", "auth"} {
		if !isHubAction(action) {
			t.Fatalf("%q must be a hub action", action)
		}
	}
	for _, action := range []string{"run", "resolve"} {
		if isHubAction(action) {
			t.Fatalf("%q must stay a local action", action)
		}
	}
}

func TestRunHubActionRejectsAnUnknownAction(t *testing.T) {
	fake := newCommandHub(t)
	environment := newTestEnvironment(t, fake, "")
	if err := runHubAction(context.Background(), environment.hubEnvironment, "nonsense", nil); err == nil {
		t.Fatal("expected an error for an unknown action")
	}
}

// `jf login` is the first command a fresh machine runs, before any manifest
// exists there. A missing manifest must not stop it.
func TestLoginWorksWithNoManifest(t *testing.T) {
	fake := newCommandHub(t)
	environment := newTestEnvironment(t, fake, "")
	environment.ManifestPath = ""

	if err := runLogin(context.Background(), environment.hubEnvironment, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.LoadToken(environment.tokenPath); err != nil {
		t.Fatal(err)
	}
}

// The manifest carries the hub address, so every machine reading it reaches the
// same hub with no further setup.
func TestHubAddressComesFromTheManifest(t *testing.T) {
	fake := newCommandHub(t)
	environment := newTestEnvironment(t, fake, "")
	t.Setenv(hub.EnvBaseURL, "")

	manifestPath := filepath.Join(t.TempDir(), "jackfield.yaml")
	manifest := "version: 1\nhub: " + fake.server.URL + "\n"
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	environment.ManifestPath = manifestPath

	if err := runLogin(context.Background(), environment.hubEnvironment, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.LoadToken(environment.tokenPath); err != nil {
		t.Fatal(err)
	}
}
