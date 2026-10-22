// SPDX-License-Identifier: GPL-3.0-only

package tts

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var (
	// ErrNoKeys means no Gemini API key has been added.
	ErrNoKeys = errors.New("no Gemini API key is configured")
	// ErrInvalidKey means every key was rejected by Google.
	ErrInvalidKey = errors.New("the Gemini API key was rejected")
	// ErrQuotaExhausted means every key and model is rate limited right now.
	ErrQuotaExhausted = errors.New("the Gemini rate limit is reached for every key")
	// ErrModelUnavailable means the selected TTS models do not exist for this account.
	ErrModelUnavailable = errors.New("the selected TTS model is not available")
)

// QuotaError wraps ErrQuotaExhausted with the time the first key or model frees up.
type QuotaError struct {
	RetryAt time.Time
}

func (e *QuotaError) Error() string {
	return fmt.Sprintf("%s (next attempt possible at %s)", ErrQuotaExhausted, e.RetryAt.Format(time.RFC3339))
}

func (e *QuotaError) Unwrap() error { return ErrQuotaExhausted }

// ErrorKind classifies one failed request.
type ErrorKind string

const (
	KindRateLimit        ErrorKind = "rate_limit"
	KindInvalidKey       ErrorKind = "invalid_key"
	KindModelUnavailable ErrorKind = "model_unavailable"
	KindNetwork          ErrorKind = "network"
	KindOther            ErrorKind = "other"
)

// Error is one failed request. Message is Google's own text with the key redacted.
type Error struct {
	Kind       ErrorKind
	Status     int
	Message    string
	RetryAfter time.Duration
}

func (e *Error) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("tts %s (HTTP %d): %s", e.Kind, e.Status, e.Message)
	}
	return fmt.Sprintf("tts %s: %s", e.Kind, e.Message)
}

var invalidKeyMarkers = []string{"api key", "api_key_invalid", "permission_denied"}

// classify maps an HTTP failure to an Error and removes the key from its text.
func classify(status int, body []byte, secret string) *Error {
	message := googleMessage(body)
	if message == "" {
		message = http.StatusText(status)
	}

	e := &Error{Kind: KindOther, Status: status, Message: redact(message, secret)}
	switch status {
	case http.StatusTooManyRequests:
		e.Kind = KindRateLimit
		e.RetryAfter = retryDelay(body, message)
	case http.StatusUnauthorized, http.StatusForbidden:
		e.Kind = KindInvalidKey
	case http.StatusNotFound:
		e.Kind = KindModelUnavailable
	case http.StatusBadRequest:
		lower := strings.ToLower(string(body))
		for _, marker := range invalidKeyMarkers {
			if strings.Contains(lower, marker) {
				e.Kind = KindInvalidKey
				break
			}
		}
	}
	return e
}

func redact(text, secret string) string {
	if secret == "" {
		return text
	}
	return strings.ReplaceAll(text, secret, "[redacted]")
}

type googleError struct {
	Error struct {
		Message string                       `json:"message"`
		Details []map[string]json.RawMessage `json:"details"`
	} `json:"error"`
}

func googleMessage(body []byte) string {
	var parsed googleError
	if json.Unmarshal(body, &parsed) == nil && parsed.Error.Message != "" {
		return parsed.Error.Message
	}
	return ""
}

const defaultCooldown = time.Minute

var retryInPattern = regexp.MustCompile(`(?i)retry in ([0-9]+(?:\.[0-9]+)?(?:h|m|s|ms)(?:[0-9]+(?:\.[0-9]+)?(?:h|m|s|ms))*)`)

// retryDelay reads Google's suggested wait: the RetryInfo detail first, then
// "retry in 38s" / "retry in 11h30m39s" in the message. It defaults to one minute.
func retryDelay(body []byte, message string) time.Duration {
	var parsed googleError
	if json.Unmarshal(body, &parsed) == nil {
		for _, detail := range parsed.Error.Details {
			var raw string
			if json.Unmarshal(detail["retryDelay"], &raw) != nil {
				continue
			}
			if d, err := time.ParseDuration(raw); err == nil && d > 0 {
				return d
			}
		}
	}
	if m := retryInPattern.FindStringSubmatch(message); m != nil {
		if d, err := time.ParseDuration(m[1]); err == nil && d > 0 {
			return d
		}
	}
	return defaultCooldown
}
