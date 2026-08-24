package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client calls the hub over HTTPS.
//
// Token may be empty. The device endpoints that start a login accept no token,
// because the machine has none yet.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// New returns a client for one hub address.
func New(baseURL string, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		// The device flow polls for up to 15 minutes, but each single request is
		// short. The timeout below covers one request, not the whole flow.
		HTTP: &http.Client{Timeout: 30 * time.Second},
	}
}

// Error reports a hub response that was not a success.
//
// The hub answers every failure with an RFC 6749 error body, so the two fields
// below carry the hub's own words. `jf` prints Description rather than inventing
// its own message, because the hub knows why it refused.
type Error struct {
	Status      int
	Code        string
	Description string
}

func (err *Error) Error() string {
	switch {
	case err.Description != "" && err.Code != "":
		return fmt.Sprintf("%s (%s)", err.Description, err.Code)
	case err.Description != "":
		return err.Description
	case err.Code != "":
		return err.Code
	default:
		return fmt.Sprintf("the hub answered %d", err.Status)
	}
}

// Code returns the hub's error code, or an empty string for any other error.
// The device flow branches on "authorization_pending", so it needs this.
func Code(err error) string {
	var hubError *Error
	if errors.As(err, &hubError) {
		return hubError.Code
	}
	return ""
}

// StatusCode returns the HTTP status of a hub error, or 0 for any other error.
func StatusCode(err error) int {
	var hubError *Error
	if errors.As(err, &hubError) {
		return hubError.Status
	}
	return 0
}

/* ------------------------------------------------------------------ */
/* The device-authorization flow (RFC 8628)                            */
/* ------------------------------------------------------------------ */

// DeviceCode is the hub's answer to POST /device/code.
type DeviceCode struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// StartDeviceAuth asks the hub for a device code and a short user code.
func (client *Client) StartDeviceAuth(ctx context.Context, deviceName string) (DeviceCode, error) {
	var result DeviceCode
	err := client.do(ctx, http.MethodPost, "/device/code", map[string]string{"device_name": deviceName}, &result)
	return result, err
}

// DeviceToken is the hub's answer to a successful POST /device/token.
type DeviceToken struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	DeviceName  string `json:"device_name"`
}

// PollDeviceToken asks once whether the person approved the device yet.
//
// A pending approval is an error with the code "authorization_pending". The
// caller loops; this function never sleeps on its own, so a test can drive it.
func (client *Client) PollDeviceToken(ctx context.Context, deviceCode string) (DeviceToken, error) {
	var result DeviceToken
	err := client.do(ctx, http.MethodPost, "/device/token", map[string]string{
		"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
		"device_code": deviceCode,
	}, &result)
	return result, err
}

/* ------------------------------------------------------------------ */
/* Credentials                                                         */
/* ------------------------------------------------------------------ */

// Credential is one credential, as the hub returns it.
type Credential struct {
	Connection string `json:"connection"`
	Secret     string `json:"secret"`
	Identity   string `json:"identity"`
	UpdatedAt  int64  `json:"updated_at"`
}

// GetCredential reads one credential with the device token.
func (client *Client) GetCredential(ctx context.Context, connection string) (Credential, error) {
	var result Credential
	err := client.do(ctx, http.MethodGet, "/creds/"+url.PathEscape(connection), nil, &result)
	return result, err
}

// PutCredential writes one credential with an approval ticket.
//
// The device token is not sent here and would not help. The hub requires a fresh
// browser approval for every write.
func (client *Client) PutCredential(ctx context.Context, connection string, secret string, identity string, approvalTicket string) error {
	body := map[string]string{
		"secret":          secret,
		"identity":        identity,
		"approval_ticket": approvalTicket,
	}
	return client.doWithoutToken(ctx, http.MethodPut, "/creds/"+url.PathEscape(connection), body, nil)
}

/* ------------------------------------------------------------------ */
/* Status and devices                                                  */
/* ------------------------------------------------------------------ */

// Connection is one entry in the status panel.
//
// UpstreamOK is a pointer because the hub sends null while it has no probes. A
// pointer keeps "the hub did not check" apart from "the hub checked and the
// credential failed". A bool alone would report both as false.
type Connection struct {
	Connection string `json:"connection"`
	Identity   string `json:"identity"`
	UpdatedAt  int64  `json:"updated_at"`
	AgeSeconds int64  `json:"age_seconds"`
	UpstreamOK *bool  `json:"upstream_ok"`
}

// Status is the hub's answer to GET /status.
type Status struct {
	Connections       []Connection `json:"connections"`
	ProbesImplemented bool         `json:"probes_implemented"`
}

// GetStatus reads the liveness panel.
func (client *Client) GetStatus(ctx context.Context) (Status, error) {
	var result Status
	err := client.do(ctx, http.MethodGet, "/status", nil, &result)
	return result, err
}

// Device is one machine that holds a device token.
type Device struct {
	DeviceID   string `json:"device_id"`
	Name       string `json:"name"`
	CreatedAt  int64  `json:"created_at"`
	LastUsedAt *int64 `json:"last_used_at"`
	Current    bool   `json:"current"`
}

// DeviceList is the hub's answer to GET /devices.
type DeviceList struct {
	Devices []Device `json:"devices"`
}

// ListDevices reads every machine that holds a device token.
func (client *Client) ListDevices(ctx context.Context) ([]Device, error) {
	var result DeviceList
	if err := client.do(ctx, http.MethodGet, "/devices", nil, &result); err != nil {
		return nil, err
	}
	return result.Devices, nil
}

// RevokeDevice removes one device token by its device id.
func (client *Client) RevokeDevice(ctx context.Context, deviceID string) error {
	return client.do(ctx, http.MethodDelete, "/devices/"+url.PathEscape(deviceID), nil, nil)
}

/* ------------------------------------------------------------------ */
/* The request itself                                                  */
/* ------------------------------------------------------------------ */

func (client *Client) do(ctx context.Context, method string, path string, body any, out any) error {
	return client.request(ctx, method, path, body, out, true)
}

func (client *Client) doWithoutToken(ctx context.Context, method string, path string, body any, out any) error {
	return client.request(ctx, method, path, body, out, false)
}

func (client *Client) request(ctx context.Context, method string, path string, body any, out any, sendToken bool) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode the request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(ctx, method, client.BaseURL+path, reader)
	if err != nil {
		return fmt.Errorf("build the request: %w", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json")
	if sendToken && client.Token != "" {
		request.Header.Set("Authorization", "Bearer "+client.Token)
	}

	httpClient := client.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("reach the hub at %s: %w", client.BaseURL, err)
	}
	defer response.Body.Close()

	// The cap stops a wrong address, such as a proxy sign-in page, from filling
	// memory. Every real hub answer is far smaller.
	payload, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("read the answer from the hub: %w", err)
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return decodeError(response.StatusCode, payload)
	}
	if out == nil || len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("the hub sent an answer that is not the expected JSON: %w", err)
	}
	return nil
}

func decodeError(status int, payload []byte) error {
	var body struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	// A hub error is always JSON. Anything else means the request did not reach
	// the hub, so the raw text is more useful than a parse failure.
	if err := json.Unmarshal(payload, &body); err != nil {
		text := strings.TrimSpace(string(payload))
		if len(text) > 200 {
			text = text[:200] + "..."
		}
		return &Error{Status: status, Description: text}
	}
	return &Error{Status: status, Code: body.Error, Description: body.Description}
}
