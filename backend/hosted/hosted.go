// Package hosted is the client side of a Tonelab subscription: the device
// flow that turns a browser sign-in into a key for this install, and the
// account reads the settings screen shows. The app never sees a password
// or a provider; it sees a short code, and later a key it stores.
package hosted

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client speaks to one service URL as one device.
type Client struct {
	base     string
	deviceID string
	http     *http.Client
}

func NewClient(baseURL, deviceID string) *Client {
	return &Client{base: strings.TrimRight(baseURL, "/"), deviceID: deviceID, http: &http.Client{Timeout: 20 * time.Second}}
}

// Error is the service's own refusal, with its code and its sentence.
type Error struct {
	Status  int
	Code    string
	Message string
	// RetryAfter is what the service asked for, when it did.
	RetryAfter time.Duration
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

var ErrUnreachable = errors.New("hosted: the service did not answer")

// Started is what the person is shown while the browser does the rest.
type Started struct {
	deviceCode string
	UserCode   string
	// VerifyURL is where the code is typed; VerifyURLComplete carries it.
	VerifyURL         string
	VerifyURLComplete string
	ExpiresIn         time.Duration
	Interval          time.Duration
}

// Start asks for a device code, naming this machine so the approval page
// can say what is asking.
func (c *Client) Start(ctx context.Context, name string) (Started, error) {
	var out struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
		URI        string `json:"verification_uri"`
		URIFull    string `json:"verification_uri_complete"`
		ExpiresIn  int    `json:"expires_in"`
		Interval   int    `json:"interval"`
	}
	if err := c.call(ctx, http.MethodPost, "/device/code", "", map[string]string{"device_id": c.deviceID, "name": name}, &out); err != nil {
		return Started{}, err
	}
	return Started{deviceCode: out.DeviceCode, UserCode: out.UserCode, VerifyURL: out.URI, VerifyURLComplete: out.URIFull,
		ExpiresIn: time.Duration(out.ExpiresIn) * time.Second, Interval: time.Duration(out.Interval) * time.Second}, nil
}

// Poll asks once. Pending is the normal answer; the caller sleeps the
// interval between calls, longer when told to slow down.
func (c *Client) Poll(ctx context.Context, started Started) (key string, pending bool, err error) {
	var out struct {
		Key string `json:"key"`
	}
	err = c.call(ctx, http.MethodPost, "/device/token", "", map[string]string{"device_code": started.deviceCode}, &out)
	var refused *Error
	if errors.As(err, &refused) {
		switch refused.Code {
		case "authorization_pending", "slow_down":
			return "", true, nil
		}
	}
	if err != nil {
		return "", false, err
	}
	return out.Key, false, nil
}

// Wait runs the poll loop until a key, a refusal, the code's expiry or
// the context ends.
func (c *Client) Wait(ctx context.Context, started Started) (string, error) {
	deadline := time.Now().Add(started.ExpiresIn)
	interval := started.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	for {
		key, pending, err := c.Poll(ctx, started)
		if err != nil {
			return "", err
		}
		if !pending {
			return key, nil
		}
		if time.Now().After(deadline) {
			return "", &Error{Code: "expired_token", Message: "The code expired before it was approved. Start again."}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(interval):
		}
	}
}

// Window is one quota window as the service reports it.
type Window struct {
	Used     int64     `json:"used"`
	Limit    int64     `json:"limit"`
	ResetsAt time.Time `json:"resets_at"`
}

// Account is the settings screen's view: who, which plan, how much used.
type Account struct {
	Email    string
	Plan     string
	Active   bool
	Reason   string
	Month    Window
	Day      Window
	Searches Window
	Models   []string
	KeyID    int64
}

// Account reads /account, /usage and /v1/models with the key. Usage and
// models are missing without an active plan, which is not an error.
func (c *Client) Account(ctx context.Context, key string) (Account, error) {
	var me struct {
		Email string `json:"email"`
		Plan  struct {
			Code   string `json:"code"`
			Active bool   `json:"active"`
			Reason string `json:"reason"`
		} `json:"plan"`
		Keys []struct {
			ID         int64 `json:"id"`
			ThisDevice bool  `json:"this_device"`
		} `json:"keys"`
	}
	if err := c.call(ctx, http.MethodGet, "/account", key, nil, &me); err != nil {
		return Account{}, err
	}
	a := Account{Email: me.Email, Plan: me.Plan.Code, Active: me.Plan.Active, Reason: me.Plan.Reason}
	for _, k := range me.Keys {
		if k.ThisDevice {
			a.KeyID = k.ID
		}
	}
	if !a.Active {
		return a, nil
	}
	var usage struct {
		Usage struct {
			Month         Window `json:"month"`
			Day           Window `json:"day"`
			SearchesUsed  int64  `json:"searches_used"`
			SearchesLimit int64  `json:"searches_limit"`
		} `json:"usage"`
	}
	if err := c.call(ctx, http.MethodGet, "/usage", key, nil, &usage); err != nil {
		return Account{}, err
	}
	a.Month, a.Day = usage.Usage.Month, usage.Usage.Day
	a.Searches = Window{Used: usage.Usage.SearchesUsed, Limit: usage.Usage.SearchesLimit, ResetsAt: usage.Usage.Month.ResetsAt}
	var models struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := c.call(ctx, http.MethodGet, "/v1/models", key, nil, &models); err != nil {
		return Account{}, err
	}
	for _, m := range models.Data {
		a.Models = append(a.Models, m.ID)
	}
	return a, nil
}

// SignOut revokes this device's key at the service, so a key left in a
// config file after sign-out is dead rather than dormant.
func (c *Client) SignOut(ctx context.Context, key string, keyID int64) error {
	if keyID == 0 {
		return nil
	}
	return c.call(ctx, http.MethodDelete, fmt.Sprintf("/account/keys/%d", keyID), key, nil, nil)
}

func (c *Client) call(ctx context.Context, method, path, key string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Tonelab-Device", c.deviceID)
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	if response.StatusCode >= 400 {
		var refusal struct {
			Error struct {
				Code       string `json:"code"`
				Message    string `json:"message"`
				RetryAfter int    `json:"retry_after"`
			} `json:"error"`
		}
		_ = json.Unmarshal(payload, &refusal)
		if refusal.Error.Code == "" {
			return fmt.Errorf("%w: HTTP %d", ErrUnreachable, response.StatusCode)
		}
		return &Error{Status: response.StatusCode, Code: refusal.Error.Code, Message: refusal.Error.Message, RetryAfter: time.Duration(refusal.Error.RetryAfter) * time.Second}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("%w: unreadable answer", ErrUnreachable)
	}
	return nil
}
