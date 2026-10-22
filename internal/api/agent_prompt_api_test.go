// SPDX-License-Identifier: GPL-3.0-only

//go:build devfake

package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"datacodex-notify/internal/agentprompt"
)

type promptOptionsResponse struct {
	Options   agentprompt.Options                `json:"options"`
	Defaults  agentprompt.Options                `json:"defaults"`
	Templates map[string][]agentprompt.EventInfo `json:"templates"`
	CLIPath   string                             `json:"cli_path"`
}

func getPromptOptions(t *testing.T, f *agentFixture) promptOptionsResponse {
	t.Helper()
	rr := f.ui.do(http.MethodGet, "/api/v1/ui/agents/prompt-options", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET prompt-options: %d %s", rr.Code, rr.Body.String())
	}
	var out promptOptionsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad payload: %v", err)
	}
	return out
}

func TestPromptOptionsStartWithDefaultsAndRememberTheLastChoices(t *testing.T) {
	f := newAgentFixture(t)

	first := getPromptOptions(t, f)
	if first.Options.Agent != agentprompt.AgentClaudeCode || first.Options.Activation != agentprompt.ActivationOnRequest ||
		first.Options.Language != agentprompt.LanguageEgyptian || len(first.Options.ActivatePhrases) < 2 {
		t.Errorf("unexpected defaults: %+v", first.Options)
	}
	if first.CLIPath != testCLIPath || len(first.Templates) != 3 || len(first.Templates["detailed"]) <= len(first.Templates["calm"]) {
		t.Errorf("cli path and templates must be provided: %+v", first)
	}

	chosen := first.Options
	chosen.Agent, chosen.Template, chosen.Scope = agentprompt.AgentCodex, agentprompt.TemplateDetailed, agentprompt.ScopeProject
	chosen.Language = agentprompt.LanguageEnglish
	chosen.Modes = map[string]string{"task_end": "voice"}
	chosen.ActivatePhrases = []string{"alerts please"}
	chosen.Levels = map[string]string{"task_end": "warning"}
	if rr := f.ui.do(http.MethodPut, "/api/v1/ui/agents/prompt-options", chosen); rr.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", rr.Code, rr.Body.String())
	}

	again := getPromptOptions(t, f)
	if again.Options.Agent != agentprompt.AgentCodex || again.Options.Template != agentprompt.TemplateDetailed ||
		again.Options.Scope != agentprompt.ScopeProject || again.Options.Language != agentprompt.LanguageEnglish ||
		again.Options.Modes["task_end"] != "voice" || again.Options.ActivatePhrases[0] != "alerts please" ||
		again.Options.Levels["task_end"] != "warning" {
		t.Errorf("the last choices were not remembered: %+v", again.Options)
	}
	if again.Defaults.Agent != agentprompt.AgentClaudeCode {
		t.Errorf("the defaults must stay available: %+v", again.Defaults)
	}
}

func TestPromptOptionsRejectInvalidChoices(t *testing.T) {
	f := newAgentFixture(t)
	good := getPromptOptions(t, f).Options

	bad := good
	bad.Agent = "bing"
	if rr := f.ui.do(http.MethodPut, "/api/v1/ui/agents/prompt-options", bad); rr.Code != http.StatusBadRequest {
		t.Errorf("an unknown agent: %d, want 400", rr.Code)
	}
	bad = good
	bad.ActivatePhrases = nil
	if rr := f.ui.do(http.MethodPut, "/api/v1/ui/agents/prompt-options", bad); rr.Code != http.StatusBadRequest {
		t.Errorf("no activation phrase: %d, want 400", rr.Code)
	}
	if got := getPromptOptions(t, f).Options.Agent; got != good.Agent {
		t.Errorf("a rejected save must change nothing, agent is %s", got)
	}
	if rr := f.ui.do(http.MethodDelete, "/api/v1/ui/agents/prompt-options", nil); rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE: %d, want 405", rr.Code)
	}
}

func TestDamagedSavedChoicesFallBackToTheDefaults(t *testing.T) {
	f := newAgentFixture(t)
	if err := f.srv.store.SetSetting(t.Context(), settingConnectOptions, "{not json"); err != nil {
		t.Fatal(err)
	}
	if got := getPromptOptions(t, f).Options; got.Agent != agentprompt.AgentClaudeCode || len(got.ActivatePhrases) == 0 {
		t.Errorf("damaged data must not break the tab: %+v", got)
	}
}

func TestPromptEndpointBuildsThePromptsWithoutAnyToken(t *testing.T) {
	f := newAgentFixture(t)
	options := getPromptOptions(t, f).Options
	options.Agent = agentprompt.AgentGemini
	options.Scope = agentprompt.ScopeProject

	rr := f.ui.do(http.MethodPost, "/api/v1/ui/agents/prompt", options)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST prompt: %d %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Connect           string `json:"connect"`
		DisconnectGeneric string `json:"disconnect_generic"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)

	for _, want := range []string{agentprompt.StartMarker, agentprompt.EndMarker, "`GEMINI.md` in the root folder of the current project", testCLIPath} {
		if !strings.Contains(out.Connect, want) {
			t.Errorf("the connect prompt is missing %q", want)
		}
	}
	if !strings.Contains(out.DisconnectGeneric, agentprompt.StartMarker) || !strings.Contains(out.DisconnectGeneric, testCLIPath) {
		t.Errorf("the generic disconnect prompt is incomplete: %s", out.DisconnectGeneric)
	}
	if strings.Contains(rr.Body.String(), f.token) {
		t.Error("the API token must never appear in a generated prompt")
	}

	options.Template = "loud"
	if rr := f.ui.do(http.MethodPost, "/api/v1/ui/agents/prompt", options); rr.Code != http.StatusBadRequest {
		t.Errorf("an invalid choice: %d, want 400", rr.Code)
	}
	if rr := f.ui.do(http.MethodGet, "/api/v1/ui/agents/prompt", nil); rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET prompt: %d, want 405", rr.Code)
	}
}

func TestDisconnectPromptUsesTheRecordedRulesFiles(t *testing.T) {
	f := newAgentFixture(t)
	payload, _ := json.Marshal(map[string]interface{}{
		"source":      "claude-code",
		"session":     "proj-1",
		"rules_files": []string{`C:\Users\x\.claude\CLAUDE.md`, `D:\work\p\CLAUDE.md`},
	})
	if code, body := f.agent(t, f.token, http.MethodPost, "/api/v1/agents/connect", string(payload)); code != http.StatusOK {
		t.Fatalf("connect: %d %s", code, body)
	}
	id := f.list(t).Sessions[0].ID

	rr := f.ui.do(http.MethodGet, "/api/v1/ui/agents/sessions/"+id+"/disconnect-prompt", nil)
	var out struct {
		Prompt string `json:"prompt"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if rr.Code != http.StatusOK {
		t.Fatalf("disconnect prompt: %d %s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{`C:\Users\x\.claude\CLAUDE.md`, `D:\work\p\CLAUDE.md`, "disconnect --source claude-code --session proj-1", testCLIPath, agentprompt.StartMarker} {
		if !strings.Contains(out.Prompt, want) {
			t.Errorf("the disconnect prompt is missing %q:\n%s", want, out.Prompt)
		}
	}
	if strings.Contains(rr.Body.String(), f.token) {
		t.Error("the API token must never appear in a prompt")
	}

	if rr := f.ui.do(http.MethodGet, "/api/v1/ui/agents/sessions/missing/disconnect-prompt", nil); rr.Code != http.StatusNotFound {
		t.Errorf("unknown session: %d, want 404", rr.Code)
	}
	if rr := f.ui.do(http.MethodPost, "/api/v1/ui/agents/sessions/"+id+"/disconnect-prompt", nil); rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d, want 405", rr.Code)
	}
}
