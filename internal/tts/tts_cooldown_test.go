// SPDX-License-Identifier: GPL-3.0-only

package tts

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestClassificationOf400(t *testing.T) {
	tests := []struct {
		body string
		want ErrorKind
	}{
		{`{"error":{"message":"API key not valid. Please pass a valid API key."}}`, KindInvalidKey},
		{`{"error":{"status":"API_KEY_INVALID","message":"x"}}`, KindInvalidKey},
		{`{"error":{"status":"PERMISSION_DENIED","message":"x"}}`, KindInvalidKey},
		{`{"error":{"message":"api KEY expired"}}`, KindInvalidKey},
		{`{"error":{"message":"Invalid value at generation_config"}}`, KindOther},
	}
	for _, tc := range tests {
		if got := classify(400, []byte(tc.body), "k").Kind; got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.body, got, tc.want)
		}
	}
	for status, want := range map[int]ErrorKind{401: KindInvalidKey, 403: KindInvalidKey, 429: KindRateLimit, 404: KindModelUnavailable, 500: KindOther, 503: KindOther} {
		if got := classify(status, []byte(`{}`), "k").Kind; got != want {
			t.Errorf("status %d: got %s, want %s", status, got, want)
		}
	}
}

func TestRetryDelayParsing(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		message string
		want    time.Duration
	}{
		{"seconds in message", `{}`, "Please retry in 38s.", 38 * time.Second},
		{"fractional seconds", `{}`, "retry in 38.5s", 38*time.Second + 500*time.Millisecond},
		{"hours minutes seconds", `{}`, "You exceeded your quota, retry in 11h30m39s", 11*time.Hour + 30*time.Minute + 39*time.Second},
		{"case insensitive", `{}`, "Retry In 7s", 7 * time.Second},
		{"RetryInfo detail wins", `{"error":{"message":"x","details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"12s"}]}}`, "retry in 99s", 12 * time.Second},
		{"default", `{}`, "slow down", time.Minute},
	}
	for _, tc := range tests {
		if got := retryDelay([]byte(tc.body), tc.message); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCooldownSkipsPairsUntilExpiry(t *testing.T) {
	var limited bool
	client, google, clock := newClient(t, func(c call, w http.ResponseWriter) {
		if limited {
			writeStatus(w, 429, "retry in 38s")
			return
		}
		success(c, w)
	}, twoKeys[0])

	limited = true
	_, err := client.Synthesize(context.Background(), "hi", "")
	var quota *QuotaError
	if !errors.As(err, &quota) || !errors.Is(err, ErrQuotaExhausted) {
		t.Fatalf("got %v, want a QuotaError", err)
	}
	if want := clock.Add(38 * time.Second); !quota.RetryAt.Equal(want) {
		t.Errorf("RetryAt = %v, want %v", quota.RetryAt, want)
	}
	if len(google.calls) != 2 {
		t.Fatalf("expected both models tried once, got %d calls", len(google.calls))
	}

	// While both pairs cool down no request is made at all.
	if _, err := client.Synthesize(context.Background(), "hi", ""); !errors.Is(err, ErrQuotaExhausted) {
		t.Fatalf("got %v, want ErrQuotaExhausted", err)
	}
	if len(google.calls) != 2 {
		t.Errorf("cooling pairs must be skipped, calls = %d", len(google.calls))
	}
	if next := client.NextAvailableAt(context.Background(), []string{"A"}); !next.Equal(clock.Add(38 * time.Second)) {
		t.Errorf("NextAvailableAt = %v", next)
	}

	// After the cooldown the pairs are tried again.
	limited = false
	*clock = clock.Add(39 * time.Second)
	if _, err := client.Synthesize(context.Background(), "hi", ""); err != nil {
		t.Fatalf("expected success after the cooldown, got %v", err)
	}
	if !client.NextAvailableAt(context.Background(), []string{"A"}).IsZero() {
		t.Error("NextAvailableAt should be zero when pairs are available")
	}
}

func TestNextAvailableAtIsZeroWhenAnyPairIsFree(t *testing.T) {
	client, _, clock := newClient(t, success, twoKeys...)
	client.setCooldown("A:"+DefaultPrimaryModel, clock.Add(time.Minute))
	client.setCooldown("A:"+DefaultFallbackModel, clock.Add(2*time.Minute))
	if !client.NextAvailableAt(context.Background(), []string{"A", "B"}).IsZero() {
		t.Error("key B is free, so nothing is blocked")
	}
	client.setCooldown("B:"+DefaultPrimaryModel, clock.Add(30*time.Second))
	client.setCooldown("B:"+DefaultFallbackModel, clock.Add(3*time.Minute))
	if got := client.NextAvailableAt(context.Background(), []string{"A", "B"}); !got.Equal(clock.Add(30 * time.Second)) {
		t.Errorf("got %v, want the earliest expiry", got)
	}
}
