// SPDX-License-Identifier: GPL-3.0-only

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"datacodex-notify/internal/config"
	"datacodex-notify/internal/secrets"
)

// Exit codes (PRD section 4).
const (
	exitOK          = 0
	exitServerError = 1
	exitNotRunning  = 2
	exitAuth        = 3
	exitValidation  = 4
	exitThrottled   = 5
	exitMuted       = 6
	exitStillQueued = 7 // --wait ran out while the message was still queued; not an error for the agent
)

const requestTimeout = 10 * time.Second

// Client talks to the local DataCodex Notify service.
type Client struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

// NewClient discovers the running service through runtime.json and reads the
// API token from Credential Manager. The returned int is the exit code to use
// when err is not nil.
func NewClient() (*Client, int, error) {
	cfg, err := config.LoadConfig()
	if err != nil {
		return nil, exitServerError, fmt.Errorf("failed to load config: %w", err)
	}

	runtime, err := cfg.ReadRuntime()
	if err != nil {
		return nil, exitNotRunning, fmt.Errorf("DataCodex Notify is not running (runtime.json missing)")
	}

	token, err := secrets.NewWinCredStore().Get(secrets.TargetAPIToken)
	if err != nil || token == "" {
		return nil, exitAuth, fmt.Errorf("api token not found in Credential Manager")
	}

	return &Client{
		BaseURL:    fmt.Sprintf("http://127.0.0.1:%d/api/v1", runtime.Port),
		Token:      token,
		HTTPClient: &http.Client{Timeout: requestTimeout},
	}, exitOK, nil
}

// Post sends payload as JSON and decodes the response into out (both optional).
func (c *Client) Post(endpoint string, payload, out interface{}) (int, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return exitValidation, fmt.Errorf("failed to encode request: %w", err)
		}
		body = bytes.NewReader(data)
	}
	return c.do(http.MethodPost, endpoint, body, out)
}

// Delete sends a DELETE and decodes the response into out (optional).
func (c *Client) Delete(endpoint string, out interface{}) (int, error) {
	return c.do(http.MethodDelete, endpoint, nil, out)
}

// Get fetches endpoint and decodes the response into out (optional).
func (c *Client) Get(endpoint string, out interface{}) (int, error) {
	return c.do(http.MethodGet, endpoint, nil, out)
}

func (c *Client) do(method, endpoint string, body io.Reader, out interface{}) (int, error) {
	req, err := http.NewRequest(method, c.BaseURL+endpoint, body)
	if err != nil {
		return exitServerError, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return exitNotRunning, fmt.Errorf("service request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return exitServerError, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return statusToExitCode(resp.StatusCode), fmt.Errorf("%s", errorMessage(resp.StatusCode, data))
	}

	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return exitServerError, fmt.Errorf("invalid response: %w", err)
		}
	}
	return exitOK, nil
}

func statusToExitCode(status int) int {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return exitAuth
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
		return exitValidation
	case http.StatusTooManyRequests:
		return exitThrottled
	case http.StatusLocked:
		return exitMuted
	default:
		return exitServerError
	}
}

func errorMessage(status int, data []byte) string {
	var parsed struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(data, &parsed); err == nil && parsed.Error != "" {
		return fmt.Sprintf("%d: %s", status, parsed.Error)
	}
	return fmt.Sprintf("%d: %s", status, bytes.TrimSpace(data))
}
