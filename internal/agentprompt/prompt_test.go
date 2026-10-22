// SPDX-License-Identifier: GPL-3.0-only

package agentprompt

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files")

var testEnv = Env{CLIPath: `C:\Users\demo\AppData\Local\Programs\DatacodexNotify\datacodex-notify-cli.exe`}

func baseOptions() Options {
	o := Defaults()
	// Keep golden files independent of the Arabic defaults in defaults.json.
	o.ActivatePhrases = []string{"notify on", "start alerts"}
	o.DeactivatePhrases = []string{"notify off", "stop alerts"}
	return o
}

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden file %s (run the tests with -update): %v", path, err)
	}
	if strings.ReplaceAll(string(want), "\r\n", "\n") != got {
		t.Errorf("%s differs from its golden file; review the change and run the tests with -update\n--- got ---\n%s", name, got)
	}
}

func TestConnectPromptGoldenFilesForEveryAgentTemplateAndScope(t *testing.T) {
	for _, agent := range []Agent{AgentClaudeCode, AgentCodex, AgentGemini, AgentOther} {
		for _, template := range []Template{TemplateCalm, TemplateNormal, TemplateDetailed} {
			for _, scope := range []Scope{ScopeGlobal, ScopeProject} {
				name := fmt.Sprintf("connect-%s-%s-%s", agent, template, scope)
				t.Run(name, func(t *testing.T) {
					o := baseOptions()
					o.Agent, o.Template, o.Scope = agent, template, scope
					if agent == AgentOther {
						o.OtherFile = "RULES.md"
					}
					got, err := Connect(o, testEnv)
					if err != nil {
						t.Fatalf("Connect failed: %v", err)
					}
					checkGolden(t, name, got)
				})
			}
		}
	}
}

func TestPromptVariantsGolden(t *testing.T) {
	variants := map[string]func(*Options){
		"modes-custom": func(o *Options) {
			o.Modes = map[string]string{"task_end": "voice", "failure": "both", "critical_stop": "text"}
		},
		"language-arabic":    func(o *Options) { o.Language = LanguageArabic },
		"language-english":   func(o *Options) { o.Language = LanguageEnglish },
		"activation-suggest": func(o *Options) { o.Activation = ActivationSuggestLong },
		"activation-always":  func(o *Options) { o.Activation = ActivationAlways },
		"no-heartbeat":       func(o *Options) { o.Heartbeat = false },
		"custom-levels":      func(o *Options) { o.Levels = map[string]string{"task_end": "warning", "failure": "critical"} },
		"extra-instructions": func(o *Options) { o.Extra = "Only notify for tasks longer than ten minutes.\nUse `code` sparingly." },
		"custom-heartbeat":   func(o *Options) { o.HeartbeatEvery = 120 },
	}
	for name, mutate := range variants {
		t.Run(name, func(t *testing.T) {
			o := baseOptions()
			mutate(&o)
			got, err := Connect(o, testEnv)
			if err != nil {
				t.Fatalf("Connect failed: %v", err)
			}
			checkGolden(t, "variant-"+name, got)
		})
	}
}

func TestDisconnectPromptsGolden(t *testing.T) {
	checkGolden(t, "disconnect-session", DisconnectSession(SessionRef{
		Name: "myproject-k7f2", Source: "claude-code",
		RulesFiles: []string{`C:\Users\x\.claude\CLAUDE.md`, `D:\work\myproject\CLAUDE.md`},
	}, testEnv))
	checkGolden(t, "disconnect-session-no-files", DisconnectSession(SessionRef{Name: "s1", Source: "codex"}, testEnv))
	checkGolden(t, "disconnect-generic", DisconnectGeneric(testEnv))
}

func TestBlockMarkersAppearExactlyOnce(t *testing.T) {
	for _, agent := range []Agent{AgentClaudeCode, AgentCodex, AgentGemini, AgentOther} {
		o := baseOptions()
		o.Agent = agent
		o.OtherFile = "RULES.md"
		prompt, err := Connect(o, testEnv)
		if err != nil {
			t.Fatal(err)
		}
		block, _ := Block(o, testEnv)

		if !strings.HasPrefix(block, StartMarker+"\n") || !strings.HasSuffix(block, EndMarker+"\n") {
			t.Errorf("%s: the block must start and end with the exact markers", agent)
		}
		// Each marker is a line of the block exactly once; the instructions mention them in prose too.
		for _, marker := range []string{StartMarker, EndMarker} {
			lines := 0
			for _, l := range strings.Split(prompt, "\n") {
				if l == marker {
					lines++
				}
			}
			if lines != 1 {
				t.Errorf("%s: marker %q appears as a line %d times in the connect prompt, want 1", agent, marker, lines)
			}
		}
	}
}

func TestExtraInstructionsCannotBreakTheFence(t *testing.T) {
	o := baseOptions()
	o.Extra = "Use ````` five backticks and ``` three."
	prompt, err := Connect(o, testEnv)
	if err != nil {
		t.Fatal(err)
	}
	fence := regexp.MustCompile("(?m)^(`{4,})markdown$").FindStringSubmatch(prompt)
	if fence == nil || len(fence[1]) < 6 {
		t.Fatalf("the fence must be longer than any backtick run in the block, got %v", fence)
	}
	if strings.Count(prompt, fence[1]+"\n") != 1 || !strings.HasSuffix(prompt, fence[1]+"\n") {
		t.Error("the closing fence must appear once, at the end")
	}
}

func TestNoTokenOrKeyEverAppearsInAPrompt(t *testing.T) {
	secretLike := regexp.MustCompile(`AIza[0-9A-Za-z_-]{20,}|[A-Za-z0-9_-]{40,}`)
	forbidden := []string{"token", "bearer", "authorization", "api-token", "credential manager", "x-goog-api-key"}

	var prompts []string
	for _, agent := range []Agent{AgentClaudeCode, AgentCodex, AgentGemini, AgentOther} {
		for _, template := range []Template{TemplateCalm, TemplateNormal, TemplateDetailed} {
			o := baseOptions()
			o.Agent, o.Template, o.OtherFile = agent, template, "RULES.md"
			p, err := Connect(o, testEnv)
			if err != nil {
				t.Fatal(err)
			}
			prompts = append(prompts, p)
		}
	}
	prompts = append(prompts, DisconnectGeneric(testEnv), DisconnectSession(SessionRef{Name: "s", Source: "codex", RulesFiles: []string{`C:\a\AGENTS.md`}}, testEnv))

	for _, p := range prompts {
		// The block tells agents never to put "tokens" in a message; that sentence is the only allowed mention.
		lower := strings.ReplaceAll(strings.ToLower(p), "secrets, tokens, passwords", "")
		for _, word := range forbidden {
			if strings.Contains(lower, word) {
				t.Errorf("a prompt mentions %q, which does not belong in it", word)
			}
		}
		for _, match := range secretLike.FindAllString(p, -1) {
			if !strings.Contains(match, "datacodex-notify") && !strings.Contains(match, `\`) {
				t.Errorf("a prompt contains a secret-looking string: %q", match)
			}
		}
	}
}

func TestPromptsAreEnglishAndPhrasesAreInsertedAsData(t *testing.T) {
	o := Defaults()
	prompt, err := Connect(o, testEnv)
	if err != nil {
		t.Fatal(err)
	}
	for _, phrase := range append(append([]string{}, o.ActivatePhrases...), o.DeactivatePhrases...) {
		if !strings.Contains(prompt, `"`+phrase+`"`) {
			t.Errorf("the phrase %q is missing from the prompt", phrase)
		}
	}
	if len(o.ActivatePhrases) < 2 || len(o.DeactivatePhrases) < 2 {
		t.Error("the defaults file must provide an Arabic and an English phrase for each switch")
	}

	// Apart from the user's phrases, the prompt is plain English.
	stripped := prompt
	for _, phrase := range append(append([]string{}, o.ActivatePhrases...), o.DeactivatePhrases...) {
		stripped = strings.ReplaceAll(stripped, phrase, "")
	}
	for i, r := range stripped {
		if r > 127 {
			t.Fatalf("non-English character %q at offset %d", r, i)
		}
	}
}

func TestTemplatesSetTheDefaultModeOfEveryEvent(t *testing.T) {
	tests := []struct {
		template Template
		info     string
		warn     string
		crit     string
	}{
		{TemplateCalm, "text", "text", "both"},
		{TemplateNormal, "text", "text", "both"},
		{TemplateDetailed, "text", "both", "both"},
	}
	for _, tc := range tests {
		o := baseOptions()
		o.Template = tc.template
		got := [3]string{defaultMode(tc.template, LevelInfo), defaultMode(tc.template, LevelWarning), defaultMode(tc.template, LevelCritical)}
		if got != [3]string{tc.info, tc.warn, tc.crit} {
			t.Errorf("%s: modes = %v", tc.template, got)
		}
		if ModeDefaults()[tc.template][LevelCritical] != tc.crit {
			t.Errorf("%s: ModeDefaults disagrees", tc.template)
		}
	}
}

func TestEventModeOverridesReachTheTableAndVoiceOnlyIsAllowed(t *testing.T) {
	o := baseOptions()
	o.Template = TemplateCalm
	o.Modes = map[string]string{"task_end": "voice", "failure": "both", "critical_stop": "text"}
	block, err := Block(o, testEnv)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"| The task is finished (short summary and the next step) | info | --mode voice |",
		"| A build or test fails, or an error stops progress | warning | --mode both |",
		"| A stop condition: three failed builds in a row, a broken core feature, or a security problem | critical | --mode text |",
		"| A task starts (include a short plan summary and the number of steps) | info | --mode text |",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the table is missing %q:\n%s", want, block)
		}
	}
	if !strings.Contains(block, "--project") {
		t.Error("the block must tell the agent to pass --project")
	}
}

func TestTemplatesAddEventsInOrder(t *testing.T) {
	calm := len(templateEvents[TemplateCalm])
	normal := len(templateEvents[TemplateNormal])
	detailed := len(templateEvents[TemplateDetailed])
	if !(calm < normal && normal < detailed) {
		t.Fatalf("templates must grow: %d %d %d", calm, normal, detailed)
	}

	has := func(template Template, e Event) bool {
		for _, ev := range templateEvents[template] {
			if ev == e {
				return true
			}
		}
		return false
	}
	for _, e := range []Event{EventTaskStart, EventTaskEnd, EventFailure, EventBlocked, EventCriticalStop} {
		if !has(TemplateCalm, e) {
			t.Errorf("calm must include %s", e)
		}
	}
	if has(TemplateCalm, EventPhaseFinished) || has(TemplateCalm, EventImplementerStopped) || has(TemplateNormal, EventStepMilestone) {
		t.Error("calm and normal must not include the later events")
	}
	if !has(TemplateNormal, EventPhaseFinished) || !has(TemplateNormal, EventImplementerStopped) || !has(TemplateDetailed, EventStepMilestone) {
		t.Error("normal adds the phase and implementer events; detailed adds milestones")
	}
	for template, infos := range Templates() {
		if len(infos) != len(templateEvents[template]) {
			t.Errorf("Templates() must list every event of %s", template)
		}
		for _, info := range infos {
			if info.DefaultLevel == "" {
				t.Errorf("event %s has no default level", info.Event)
			}
		}
	}
}

func TestLevelOverridesApplyAndUnknownOnesAreRejected(t *testing.T) {
	o := baseOptions()
	o.Levels = map[string]string{"task_end": "critical"}
	block, err := Block(o, testEnv)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(block, "| The task is finished (short summary and the next step) | critical | --mode both |") {
		t.Errorf("the override must reach the table:\n%s", block)
	}
}

func TestValidation(t *testing.T) {
	bad := map[string]func(*Options){
		"unknown agent":       func(o *Options) { o.Agent = "bing" },
		"other without file":  func(o *Options) { o.Agent = AgentOther },
		"other path":          func(o *Options) { o.Agent = AgentOther; o.OtherFile = `..\x.md` },
		"other slash":         func(o *Options) { o.Agent = AgentOther; o.OtherFile = "dir/x.md" },
		"unknown scope":       func(o *Options) { o.Scope = "galaxy" },
		"unknown template":    func(o *Options) { o.Template = "loud" },
		"unknown event":       func(o *Options) { o.Levels = map[string]string{"party": "info"} },
		"unknown level":       func(o *Options) { o.Levels = map[string]string{"task_end": "urgent"} },
		"unknown mode":        func(o *Options) { o.Modes = map[string]string{"task_end": "shout"} },
		"unknown mode event":  func(o *Options) { o.Modes = map[string]string{"party": "text"} },
		"unknown language":    func(o *Options) { o.Language = "klingon" },
		"unknown activation":  func(o *Options) { o.Activation = "sometimes" },
		"heartbeat too short": func(o *Options) { o.HeartbeatEvery = 5 },
		"heartbeat too long":  func(o *Options) { o.HeartbeatEvery = 999999 },
		"no activate phrases": func(o *Options) { o.ActivatePhrases = nil },
		"no stop phrases":     func(o *Options) { o.DeactivatePhrases = []string{} },
		"empty phrase":        func(o *Options) { o.ActivatePhrases = []string{"  "} },
		"quote in phrase":     func(o *Options) { o.ActivatePhrases = []string{`say "now"`} },
		"newline in phrase":   func(o *Options) { o.DeactivatePhrases = []string{"a\nb"} },
		"too many phrases":    func(o *Options) { o.ActivatePhrases = []string{"a", "b", "c", "d", "e", "f", "g"} },
		"long phrase":         func(o *Options) { o.ActivatePhrases = []string{strings.Repeat("x", 101)} },
		"long extra":          func(o *Options) { o.Extra = strings.Repeat("x", 2001) },
	}
	for name, mutate := range bad {
		o := baseOptions()
		mutate(&o)
		if err := o.Validate(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
		if _, err := Connect(o, testEnv); err == nil {
			t.Errorf("%s: Connect must refuse invalid options", name)
		}
	}

	o := baseOptions()
	o.Heartbeat = false
	o.HeartbeatEvery = 0
	if err := o.Validate(); err != nil {
		t.Errorf("the interval is ignored when the heartbeat is off: %v", err)
	}
	if err := Defaults().Validate(); err != nil {
		t.Errorf("the defaults must be valid: %v", err)
	}
}

func TestRulesFileNamesAndHints(t *testing.T) {
	cases := []struct {
		agent Agent
		file  string
	}{{AgentClaudeCode, "CLAUDE.md"}, {AgentCodex, "AGENTS.md"}, {AgentGemini, "GEMINI.md"}}
	for _, tc := range cases {
		o := baseOptions()
		o.Agent = tc.agent
		if RulesFileName(o) != tc.file {
			t.Errorf("%s: file = %s, want %s", tc.agent, RulesFileName(o), tc.file)
		}
		o.Scope = ScopeProject
		if hint := rulesFileHint(o); !strings.Contains(hint, tc.file) || !strings.Contains(hint, "current project") {
			t.Errorf("%s project hint = %s", tc.agent, hint)
		}
	}
	o := baseOptions()
	o.Agent, o.OtherFile = AgentOther, "  MY-RULES.md "
	if RulesFileName(o) != "MY-RULES.md" {
		t.Errorf("the typed file name is trimmed, got %q", RulesFileName(o))
	}
}

func TestClaudeBlockReferencesTheSkillAndOthersCarryTheCommands(t *testing.T) {
	claude := baseOptions()
	block, _ := Block(claude, testEnv)
	if !strings.Contains(block, "`datacodex-notify` skill") || strings.Contains(block, "--text-file <file>") {
		t.Error("the Claude Code block must point at the skill and not copy the command reference")
	}

	codex := baseOptions()
	codex.Agent = AgentCodex
	block, _ = Block(codex, testEnv)
	for _, want := range []string{"--text-file <file>", "connect --source codex", "heartbeat-stop", "UTF-8 file without a BOM", testEnv.CLIPath} {
		if !strings.Contains(block, want) {
			t.Errorf("the Codex block is missing %q", want)
		}
	}
}

func TestBlockCoversTheRequiredRules(t *testing.T) {
	block, _ := Block(baseOptions(), testEnv)
	for _, want := range []string{
		"ignore this whole block",     // subagents
		"**OFF**",                     // off until activated
		"Exit 6",                      // muted
		"Exit 2",                      // app not running
		"never fail, block, or delay", // never get in the way
		"heartbeat-stop",              // stop heartbeats
		"--rules-file",                // connect with rules file paths
		"unique session name",         // session naming
		"Never include code",          // content rules
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the block is missing %q", want)
		}
	}
}
