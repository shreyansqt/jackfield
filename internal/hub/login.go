package hub

import (
	"context"
	"fmt"
	"time"
)

// DefaultPollInterval is used when the hub names no interval.
const DefaultPollInterval = 5 * time.Second

// WaitForDeviceToken polls the hub until the person approves the device.
//
// The loop follows RFC 8628 section 3.5:
//   - "authorization_pending" means keep polling.
//   - "slow_down" means add five seconds to the interval, then keep polling.
//   - "expired_token" and "access_denied" end the loop.
//
// The interval comes from the hub's own answer to POST /device/code. sleep is
// the delay function; a test passes one that returns at once. A nil sleep means
// time.Sleep.
func WaitForDeviceToken(ctx context.Context, client *Client, code DeviceCode, sleep func(time.Duration)) (DeviceToken, error) {
	if sleep == nil {
		sleep = time.Sleep
	}
	interval := DefaultPollInterval
	if code.Interval > 0 {
		interval = time.Duration(code.Interval) * time.Second
	}

	deadline := time.Now().Add(15 * time.Minute)
	if code.ExpiresIn > 0 {
		deadline = time.Now().Add(time.Duration(code.ExpiresIn) * time.Second)
	}

	for {
		if err := ctx.Err(); err != nil {
			return DeviceToken{}, err
		}

		token, err := client.PollDeviceToken(ctx, code.DeviceCode)
		if err == nil {
			return token, nil
		}

		switch Code(err) {
		case "authorization_pending":
			// The person has not approved yet. This is the normal answer.
		case "slow_down":
			interval += 5 * time.Second
		case "expired_token":
			return DeviceToken{}, fmt.Errorf("the login code expired before it was approved; run `jf login` again")
		case "access_denied":
			return DeviceToken{}, fmt.Errorf("the login was refused in the browser")
		default:
			return DeviceToken{}, err
		}

		if time.Now().After(deadline) {
			return DeviceToken{}, fmt.Errorf("the login code expired before it was approved; run `jf login` again")
		}
		sleep(interval)
	}
}

// FindDeviceByName returns the device with a given name or device id.
//
// The hub revokes by device id, but a person knows the machine by its name, and
// `jf devices revoke` takes a name. This function is that translation.
//
// A name that two devices share is an error rather than a guess. Revoking the
// wrong machine locks a person out of the machine in their hand, so the command
// asks for the device id instead of choosing one.
func FindDeviceByName(devices []Device, wanted string) (Device, error) {
	var matches []Device
	for _, device := range devices {
		if device.DeviceID == wanted {
			return device, nil
		}
		if device.Name == wanted {
			matches = append(matches, device)
		}
	}

	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return Device{}, fmt.Errorf("no device is named %q; run `jf devices` to see the names", wanted)
	default:
		ids := make([]string, 0, len(matches))
		for _, device := range matches {
			ids = append(ids, device.DeviceID)
		}
		return Device{}, fmt.Errorf("%d devices are named %q; revoke one by its id instead: %v", len(matches), wanted, ids)
	}
}
