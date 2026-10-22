// SPDX-License-Identifier: GPL-3.0-only

//go:build devfake

package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestMaskPhone(t *testing.T) {
	for in, want := range map[string]string{
		"":      "",
		"12345": "+12345",
	} {
		if got := maskPhone(in); got != want {
			t.Errorf("maskPhone(%q) = %q, want %q", in, got, want)
		}
	}
	got := maskPhone("201000000042")
	if !strings.HasPrefix(got, "+201") || !strings.HasSuffix(got, "42") || strings.Contains(got, "0000") || len([]rune(got)) != 13 {
		t.Errorf("a masked number keeps only its edges: %q", got)
	}
}

func TestStateCarriesTheHomeSummary(t *testing.T) {
	f := newAgentFixture(t)
	type home struct {
		LinkedPhone     string     `json:"linked_phone"`
		LastSentAt      *time.Time `json:"last_sent_at"`
		Recipients      int        `json:"recipients"`
		AgentsTotal     int        `json:"agents_total"`
		AgentsConnected int        `json:"agents_connected"`
		Paused          bool       `json:"paused"`
		PauseUntil      *time.Time `json:"pause_until"`
	}
	read := func() home {
		rr := f.ui.do(http.MethodGet, "/api/v1/ui/state", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("state: %d %s", rr.Code, rr.Body.String())
		}
		var out struct {
			Home home `json:"home"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.Home
	}

	h := read()
	if h.LinkedPhone != "" || h.LastSentAt != nil || h.Recipients != 1 || h.AgentsTotal != 0 || h.Paused {
		t.Fatalf("unexpected start: %+v", h)
	}

	f.agent(t, f.token, http.MethodPost, "/api/v1/agents/connect", `{"source":"codex","session":"one"}`)
	f.agent(t, f.token, http.MethodPost, "/api/v1/agents/connect", `{"source":"codex","session":"two"}`)
	f.agent(t, f.token, http.MethodPost, "/api/v1/agents/disconnect", `{"session":"two"}`)
	if h := read(); h.AgentsTotal != 2 || h.AgentsConnected != 1 {
		t.Errorf("agent counts: %+v", h)
	}

	if rr := f.ui.do(http.MethodPost, "/api/v1/ui/pause", map[string]int{"minutes": 60}); rr.Code != http.StatusOK {
		t.Fatalf("pause: %d", rr.Code)
	}
	if h := read(); !h.Paused || h.PauseUntil == nil {
		t.Errorf("a timed pause shows its end: %+v", h)
	}
	f.ui.do(http.MethodPost, "/api/v1/ui/pause", map[string]int{"minutes": 0})
	if h := read(); !h.Paused || h.PauseUntil != nil {
		t.Errorf("an open-ended pause has no end time: %+v", h)
	}
}

func TestLogLimitAndLastSentTime(t *testing.T) {
	f := newAgentFixture(t)
	for i := 0; i < 4; i++ {
		if code, body := f.agent(t, f.token, http.MethodPost, "/api/v1/notify", `{"level":"info","text":"m`+string(rune('a'+i))+`","source":"codex"}`); code != http.StatusOK {
			t.Fatalf("send: %d %s", code, body)
		}
	}
	count := func(query string) int {
		rr := f.ui.do(http.MethodGet, "/api/v1/ui/log"+query, nil)
		var list []map[string]interface{}
		_ = json.Unmarshal(rr.Body.Bytes(), &list)
		return len(list)
	}
	if got := count("?limit=3"); got != 3 {
		t.Errorf("limit=3 returned %d", got)
	}
	if got := count(""); got != 4 {
		t.Errorf("the default returns everything up to 100: %d", got)
	}
	if got := count("?limit=zero"); got != 4 {
		t.Errorf("a bad limit falls back to the default: %d", got)
	}
}
