// SPDX-License-Identifier: GPL-3.0-only

package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type heartbeatCall struct {
	method string
	path   string
	body   map[string]interface{}
}

func newHeartbeatServer(t *testing.T, calls *[]heartbeatCall, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		*calls = append(*calls, heartbeatCall{method: r.Method, path: r.URL.EscapedPath(), body: body})
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "status": "active", "error": "bad id"})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHeartbeatCommandSendsOnlyTheGivenFields(t *testing.T) {
	var calls []heartbeatCall
	srv := newHeartbeatServer(t, &calls, http.StatusOK)
	e, out, _ := testEnv(srv, "tok")

	code := execute([]string{"heartbeat", "--id", "plan 25/run", "--every", "600", "--grace", "0", "--label", "Plan 25", "--source", "claude:core"}, "1", e)
	if code != exitOK || len(calls) != 1 {
		t.Fatalf("code=%d calls=%d", code, len(calls))
	}
	c := calls[0]
	if c.method != http.MethodPost || c.path != "/api/v1/heartbeats/plan%2025%2Frun" {
		t.Errorf("unexpected request %s %s", c.method, c.path)
	}
	if c.body["every_s"] != float64(600) || c.body["grace_s"] != float64(0) || c.body["label"] != "Plan 25" || c.body["source"] != "claude:core" {
		t.Errorf("unexpected body: %v", c.body)
	}
	if !strings.Contains(out.String(), `"status":"active"`) {
		t.Errorf("stdout = %q", out.String())
	}

	// A plain beat sends no interval and no grace, so the stored values are kept.
	calls = nil
	if code := execute([]string{"heartbeat", "--id", "run-1"}, "1", e); code != exitOK {
		t.Fatalf("code = %d", code)
	}
	if _, has := calls[0].body["every_s"]; has {
		t.Errorf("every_s must be omitted: %v", calls[0].body)
	}
	if _, has := calls[0].body["grace_s"]; has {
		t.Errorf("grace_s must be omitted when not given: %v", calls[0].body)
	}
}

func TestHeartbeatStopCommand(t *testing.T) {
	var calls []heartbeatCall
	srv := newHeartbeatServer(t, &calls, http.StatusOK)
	e, out, _ := testEnv(srv, "tok")

	if code := execute([]string{"heartbeat-stop", "--id", "run-1"}, "1", e); code != exitOK {
		t.Fatalf("code = %d", code)
	}
	if len(calls) != 1 || calls[0].method != http.MethodDelete || calls[0].path != "/api/v1/heartbeats/run-1" {
		t.Errorf("unexpected calls: %+v", calls)
	}
	if !strings.Contains(out.String(), `"success":true`) {
		t.Errorf("stdout = %q", out.String())
	}
}

func TestHeartbeatCommandValidationAndServerErrors(t *testing.T) {
	var calls []heartbeatCall
	srv := newHeartbeatServer(t, &calls, http.StatusBadRequest)

	for name, args := range map[string][]string{
		"missing id":        {"heartbeat"},
		"negative every":    {"heartbeat", "--id", "x", "--every", "-5"},
		"unknown flag":      {"heartbeat", "--id", "x", "--nope"},
		"stop missing id":   {"heartbeat-stop"},
		"stop unknown flag": {"heartbeat-stop", "--nope"},
	} {
		e, _, _ := testEnv(srv, "tok")
		if code := execute(args, "1", e); code != exitValidation {
			t.Errorf("%s: got %d, want %d", name, code, exitValidation)
		}
	}
	if len(calls) != 0 {
		t.Errorf("invalid input must not reach the server: %+v", calls)
	}

	e, _, errOut := testEnv(srv, "tok")
	if code := execute([]string{"heartbeat", "--id", "x"}, "1", e); code != exitValidation || !strings.Contains(errOut.String(), "bad id") {
		t.Errorf("a 400 from the server maps to exit %d: code=%d err=%q", exitValidation, code, errOut.String())
	}
}
