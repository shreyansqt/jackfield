package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeHub plays the jackfield hub over HTTP.
//
// It answers the endpoints the client calls, and it records what it received, so
// a test can check that the client sent the right token and the right body.
type fakeHub struct {
	server *httptest.Server

	// pollsBeforeApproval is how many times POST /device/token answers
	// "authorization_pending" before it hands out the token.
	pollsBeforeApproval int
	pollCount           int

	// lastAuthorization is the Authorization header of the last request.
	lastAuthorization string
	// lastPutBody is the decoded body of the last PUT /creds request.
	lastPutBody map[string]string
	// revokedDeviceIDs records every DELETE /devices/:id.
	revokedDeviceIDs []string

	credential Credential
	devices    []Device
	status     Status
}

func newFakeHub(t *testing.T) *fakeHub {
	t.Helper()
	hub := &fakeHub{
		credential: Credential{
			Connection: "slack-smarta",
			Secret:     "xoxp-the-secret",
			Identity:   "shreyans@example.com",
			UpdatedAt:  time.Now().UnixMilli(),
		},
	}
	hub.server = httptest.NewServer(http.HandlerFunc(hub.serve))
	t.Cleanup(hub.server.Close)
	return hub
}

func (hub *fakeHub) client(token string) *Client {
	client := New(hub.server.URL, token)
	client.HTTP = hub.server.Client()
	return client
}

func (hub *fakeHub) serve(response http.ResponseWriter, request *http.Request) {
	hub.lastAuthorization = request.Header.Get("Authorization")
	response.Header().Set("Content-Type", "application/json")

	switch {
	case request.URL.Path == "/device/code" && request.Method == http.MethodPost:
		var body map[string]string
		json.NewDecoder(request.Body).Decode(&body)
		json.NewEncoder(response).Encode(DeviceCode{
			DeviceCode:              "the-device-code",
			UserCode:                "BCDF-GHJK",
			VerificationURI:         hub.server.URL + "/ui/device",
			VerificationURIComplete: hub.server.URL + "/ui/device?user_code=BCDF-GHJK",
			ExpiresIn:               900,
			Interval:                5,
		})

	case request.URL.Path == "/device/token" && request.Method == http.MethodPost:
		hub.pollCount++
		if hub.pollCount <= hub.pollsBeforeApproval {
			response.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(response).Encode(map[string]string{
				"error":             "authorization_pending",
				"error_description": "The user has not approved this device yet",
			})
			return
		}
		json.NewEncoder(response).Encode(DeviceToken{
			AccessToken: "jfd_the-device-token",
			TokenType:   "Bearer",
			DeviceName:  "grumpyorange",
		})

	case strings.HasPrefix(request.URL.Path, "/creds/") && request.Method == http.MethodGet:
		if hub.lastAuthorization == "" {
			hub.unauthorized(response)
			return
		}
		json.NewEncoder(response).Encode(hub.credential)

	case strings.HasPrefix(request.URL.Path, "/creds/") && request.Method == http.MethodPut:
		var body map[string]string
		json.NewDecoder(request.Body).Decode(&body)
		hub.lastPutBody = body
		if body["approval_ticket"] == "" {
			response.WriteHeader(http.StatusForbidden)
			json.NewEncoder(response).Encode(map[string]string{
				"error":             "approval_required",
				"error_description": "Writing a credential needs a fresh browser approval.",
			})
			return
		}
		json.NewEncoder(response).Encode(map[string]any{
			"connection": strings.TrimPrefix(request.URL.Path, "/creds/"),
			"identity":   body["identity"],
			"updated_at": time.Now().UnixMilli(),
		})

	case request.URL.Path == "/status" && request.Method == http.MethodGet:
		if hub.lastAuthorization == "" {
			hub.unauthorized(response)
			return
		}
		json.NewEncoder(response).Encode(hub.status)

	case request.URL.Path == "/devices" && request.Method == http.MethodGet:
		if hub.lastAuthorization == "" {
			hub.unauthorized(response)
			return
		}
		json.NewEncoder(response).Encode(DeviceList{Devices: hub.devices})

	case strings.HasPrefix(request.URL.Path, "/devices/") && request.Method == http.MethodDelete:
		deviceID := strings.TrimPrefix(request.URL.Path, "/devices/")
		for _, device := range hub.devices {
			if device.DeviceID == deviceID {
				hub.revokedDeviceIDs = append(hub.revokedDeviceIDs, deviceID)
				json.NewEncoder(response).Encode(map[string]string{"revoked": deviceID})
				return
			}
		}
		response.WriteHeader(http.StatusNotFound)
		json.NewEncoder(response).Encode(map[string]string{
			"error":             "not_found",
			"error_description": "No such device.",
		})

	default:
		response.WriteHeader(http.StatusNotFound)
		json.NewEncoder(response).Encode(map[string]string{"error": "not_found"})
	}
}

func (hub *fakeHub) unauthorized(response http.ResponseWriter) {
	response.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(response).Encode(map[string]string{
		"error":             "unauthorized",
		"error_description": "A device token is required. Run `jf login`.",
	})
}

/* ------------------------------------------------------------------ */

func TestStartDeviceAuthReturnsBothCodes(t *testing.T) {
	hub := newFakeHub(t)
	code, err := hub.client("").StartDeviceAuth(context.Background(), "grumpyorange")
	if err != nil {
		t.Fatal(err)
	}
	if code.UserCode != "BCDF-GHJK" {
		t.Fatalf("got user code %q, want BCDF-GHJK", code.UserCode)
	}
	if code.DeviceCode != "the-device-code" {
		t.Fatalf("got device code %q, want the-device-code", code.DeviceCode)
	}
	if code.Interval != 5 {
		t.Fatalf("got interval %d, want 5", code.Interval)
	}
}

func TestWaitForDeviceTokenPollsUntilTheApproval(t *testing.T) {
	hub := newFakeHub(t)
	hub.pollsBeforeApproval = 3

	slept := 0
	token, err := WaitForDeviceToken(
		context.Background(),
		hub.client(""),
		DeviceCode{DeviceCode: "the-device-code", Interval: 5, ExpiresIn: 900},
		func(time.Duration) { slept++ },
	)
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != "jfd_the-device-token" {
		t.Fatalf("got token %q, want jfd_the-device-token", token.AccessToken)
	}
	if hub.pollCount != 4 {
		t.Fatalf("the client polled %d times, want 4", hub.pollCount)
	}
	if slept != 3 {
		t.Fatalf("the client waited %d times, want 3", slept)
	}
}

func TestWaitForDeviceTokenStopsWhenTheCodeExpires(t *testing.T) {
	hub := newFakeHub(t)
	hub.server.Config.Handler = http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(response).Encode(map[string]string{
			"error":             "expired_token",
			"error_description": "This device code expired",
		})
	})

	_, err := WaitForDeviceToken(
		context.Background(),
		hub.client(""),
		DeviceCode{DeviceCode: "the-device-code", Interval: 1, ExpiresIn: 900},
		func(time.Duration) {},
	)
	if err == nil {
		t.Fatal("expected an error for an expired code")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Fatalf("got %q, want a message about the expired code", err)
	}
}

func TestWaitForDeviceTokenSlowsDownWhenAsked(t *testing.T) {
	hub := newFakeHub(t)
	answered := 0
	hub.server.Config.Handler = http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		answered++
		if answered == 1 {
			response.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(response).Encode(map[string]string{"error": "slow_down"})
			return
		}
		json.NewEncoder(response).Encode(DeviceToken{AccessToken: "jfd_late", TokenType: "Bearer"})
	})

	var waits []time.Duration
	if _, err := WaitForDeviceToken(
		context.Background(),
		hub.client(""),
		DeviceCode{DeviceCode: "the-device-code", Interval: 5, ExpiresIn: 900},
		func(duration time.Duration) { waits = append(waits, duration) },
	); err != nil {
		t.Fatal(err)
	}
	if len(waits) != 1 || waits[0] != 10*time.Second {
		t.Fatalf("got waits %v, want one wait of 10s", waits)
	}
}

func TestGetCredentialSendsTheDeviceToken(t *testing.T) {
	hub := newFakeHub(t)
	credential, err := hub.client("jfd_abc").GetCredential(context.Background(), "slack-smarta")
	if err != nil {
		t.Fatal(err)
	}
	if credential.Secret != "xoxp-the-secret" {
		t.Fatalf("got secret %q, want xoxp-the-secret", credential.Secret)
	}
	if hub.lastAuthorization != "Bearer jfd_abc" {
		t.Fatalf("got authorization %q, want Bearer jfd_abc", hub.lastAuthorization)
	}
}

func TestGetCredentialWithoutATokenIsRefused(t *testing.T) {
	hub := newFakeHub(t)
	_, err := hub.client("").GetCredential(context.Background(), "slack-smarta")
	if err == nil {
		t.Fatal("expected an error without a device token")
	}
	if Code(err) != "unauthorized" {
		t.Fatalf("got code %q, want unauthorized", Code(err))
	}
	if StatusCode(err) != http.StatusUnauthorized {
		t.Fatalf("got status %d, want 401", StatusCode(err))
	}
	if !strings.Contains(err.Error(), "jf login") {
		t.Fatalf("got %q, want the hub's own message about `jf login`", err)
	}
}

// The write path must carry the approval ticket and must not carry the device
// token. A device token is never an alternative to the browser approval.
func TestPutCredentialSendsTheTicketAndNotTheDeviceToken(t *testing.T) {
	hub := newFakeHub(t)
	client := hub.client("jfd_abc")

	err := client.PutCredential(context.Background(), "slack-smarta", "xoxp-new", "shreyans@example.com", "the-ticket")
	if err != nil {
		t.Fatal(err)
	}
	if hub.lastAuthorization != "" {
		t.Fatalf("the client sent %q; the write path must send no device token", hub.lastAuthorization)
	}
	if hub.lastPutBody["approval_ticket"] != "the-ticket" {
		t.Fatalf("got ticket %q, want the-ticket", hub.lastPutBody["approval_ticket"])
	}
	if hub.lastPutBody["secret"] != "xoxp-new" {
		t.Fatalf("got secret %q, want xoxp-new", hub.lastPutBody["secret"])
	}
	if hub.lastPutBody["identity"] != "shreyans@example.com" {
		t.Fatalf("got identity %q, want shreyans@example.com", hub.lastPutBody["identity"])
	}
}

func TestPutCredentialWithoutATicketIsRefused(t *testing.T) {
	hub := newFakeHub(t)
	err := hub.client("jfd_abc").PutCredential(context.Background(), "slack-smarta", "xoxp-new", "", "")
	if err == nil {
		t.Fatal("expected an error without an approval ticket")
	}
	if Code(err) != "approval_required" {
		t.Fatalf("got code %q, want approval_required", Code(err))
	}
}

func TestListDevicesMarksThisMachine(t *testing.T) {
	hub := newFakeHub(t)
	hub.devices = []Device{
		{DeviceID: "aaa", Name: "macbook", CreatedAt: 1000, Current: true},
		{DeviceID: "bbb", Name: "grumpyorange", CreatedAt: 2000},
	}

	devices, err := hub.client("jfd_abc").ListDevices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 2 {
		t.Fatalf("got %d devices, want 2", len(devices))
	}
	if !devices[0].Current || devices[1].Current {
		t.Fatal("the hub marks exactly the calling device as current")
	}
}

func TestRevokeDeviceDeletesByDeviceID(t *testing.T) {
	hub := newFakeHub(t)
	hub.devices = []Device{{DeviceID: "bbb", Name: "grumpyorange", CreatedAt: 2000}}

	if err := hub.client("jfd_abc").RevokeDevice(context.Background(), "bbb"); err != nil {
		t.Fatal(err)
	}
	if len(hub.revokedDeviceIDs) != 1 || hub.revokedDeviceIDs[0] != "bbb" {
		t.Fatalf("got revoked ids %v, want [bbb]", hub.revokedDeviceIDs)
	}
}

func TestRevokeUnknownDeviceReportsNotFound(t *testing.T) {
	hub := newFakeHub(t)
	err := hub.client("jfd_abc").RevokeDevice(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected an error for an unknown device")
	}
	if StatusCode(err) != http.StatusNotFound {
		t.Fatalf("got status %d, want 404", StatusCode(err))
	}
}

// The hub revokes by device id, and a person names the machine. This is that
// translation, including the case where two machines share one name.
func TestFindDeviceByName(t *testing.T) {
	devices := []Device{
		{DeviceID: "aaa", Name: "macbook"},
		{DeviceID: "bbb", Name: "grumpyorange"},
	}

	device, err := FindDeviceByName(devices, "grumpyorange")
	if err != nil {
		t.Fatal(err)
	}
	if device.DeviceID != "bbb" {
		t.Fatalf("got device %q, want bbb", device.DeviceID)
	}

	// A device id works as well, so a duplicate name always has a way out.
	device, err = FindDeviceByName(devices, "aaa")
	if err != nil {
		t.Fatal(err)
	}
	if device.Name != "macbook" {
		t.Fatalf("got name %q, want macbook", device.Name)
	}

	if _, err := FindDeviceByName(devices, "unknown"); err == nil {
		t.Fatal("expected an error for an unknown name")
	}
}

func TestFindDeviceByNameRefusesADuplicateName(t *testing.T) {
	devices := []Device{
		{DeviceID: "aaa", Name: "macbook"},
		{DeviceID: "bbb", Name: "macbook"},
	}
	_, err := FindDeviceByName(devices, "macbook")
	if err == nil {
		t.Fatal("expected an error; revoking the wrong machine is not recoverable")
	}
	if !strings.Contains(err.Error(), "aaa") || !strings.Contains(err.Error(), "bbb") {
		t.Fatalf("got %q, want both device ids so the person can choose", err)
	}
}

func TestErrorTextUsesTheHubsOwnDescription(t *testing.T) {
	err := &Error{Status: 404, Code: "not_found", Description: `No credential is stored for "slack".`}
	if !strings.Contains(err.Error(), `No credential is stored for "slack".`) {
		t.Fatalf("got %q, want the hub's description", err)
	}
	if !strings.Contains(err.Error(), "not_found") {
		t.Fatalf("got %q, want the hub's error code", err)
	}
}

func TestNonJSONFailureIsReportedAsText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusBadGateway)
		response.Write([]byte("<html>a proxy sign-in page</html>"))
	}))
	defer server.Close()

	client := New(server.URL, "jfd_abc")
	client.HTTP = server.Client()
	_, err := client.GetStatus(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "proxy sign-in page") {
		t.Fatalf("got %q, want the raw body so a wrong address is visible", err)
	}
}
