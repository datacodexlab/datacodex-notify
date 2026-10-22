// SPDX-License-Identifier: GPL-3.0-only

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const skillPath = "../../skills/datacodex-notify/SKILL.md"

func readSkill(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.FromSlash(skillPath))
	if err != nil {
		t.Fatalf("cannot read the skill: %v", err)
	}
	return string(data)
}

func TestSkillHasFrontmatterAndInstalledPaths(t *testing.T) {
	skill := readSkill(t)

	if !strings.HasPrefix(skill, "---\nname: datacodex-notify\ndescription: ") {
		t.Error("the skill must start with frontmatter naming it datacodex-notify and describing when to use it")
	}
	for _, want := range []string{
		`{{INSTALL_DIR}}\datacodex-notify-cli.exe`,
		`{{INSTALL_DIR}}\datacodex-notify.exe`,
		`{{INSTALL_DIR_BASH}}/datacodex-notify-cli.exe`,
		"--text-file",
		"UTF-8",
		"BOM",
		"Egyptian Arabic",
		"heartbeat-stop",
		"PowerShell",
		"Bash",
	} {
		if !strings.Contains(skill, want) {
			t.Errorf("the skill does not mention %q", want)
		}
	}
}

func TestSkillIsAsciiOnly(t *testing.T) {
	for i, b := range []byte(readSkill(t)) {
		if b > 127 {
			t.Fatalf("non-ASCII byte at offset %d: the skill is written in English", i)
		}
	}
}

func TestSkillOnlyNamesFlagsAndCommandsThatExist(t *testing.T) {
	skill := readSkill(t)
	known := func(command, flag string) bool {
		var out, errOut bytes.Buffer
		e := env{stdout: &out, stderr: &errOut, newClient: func() (*Client, int, error) { return nil, exitNotRunning, os.ErrNotExist }}
		execute([]string{command, flag, "x"}, "1", e)
		return !strings.Contains(errOut.String(), "flag provided but not defined")
	}

	flags := map[string]bool{}
	for _, m := range regexp.MustCompile(`--[a-z][a-z-]*`).FindAllString(skill, -1) {
		flags[m] = true
	}
	if len(flags) == 0 {
		t.Fatal("expected the skill to document options")
	}
	// Flags of other tools the skill names in passing (git).
	delete(flags, "--show-toplevel")
	for flag := range flags {
		if !known("send", flag) && !known("heartbeat", flag) && !known("heartbeat-stop", flag) && !known("connect", flag) && !known("disconnect", flag) {
			t.Errorf("the skill mentions %s but no command accepts it", flag)
		}
	}

	for _, command := range []string{"send", "connect", "disconnect", "heartbeat", "heartbeat-stop", "status"} {
		if !strings.Contains(usage, command) {
			t.Errorf("the usage text does not list %q", command)
		}
		if !strings.Contains(skill, command) {
			t.Errorf("the skill does not mention the command %q", command)
		}
	}
}

func TestSkillDocumentsEveryExitCodeTheCLIUses(t *testing.T) {
	skill := readSkill(t)
	for code, hint := range map[string]string{
		"| 0 |": "accepted",
		"| 2 |": "not running",
		"| 3 |": "Authentication",
		"| 4 |": "invalid",
		"| 5 |": "rate limited",
		"| 6 |": "turned notifications off",
		"| 7 |": "still queued",
	} {
		line := ""
		for _, l := range strings.Split(skill, "\n") {
			if strings.HasPrefix(l, code) {
				line = l
			}
		}
		if line == "" || !strings.Contains(strings.ToLower(line), strings.ToLower(hint)) {
			t.Errorf("exit code row %s should mention %q: %q", code, hint, line)
		}
	}
}

func TestSkillNeverTellsTheAgentToChangeTheApp(t *testing.T) {
	skill := strings.ToLower(readSkill(t))
	for _, forbidden := range []string{"credential manager", "api-token", "gemini-api-key", "generativelanguage", "runtime.json", "app.db", "registry", "unlink --"} {
		if strings.Contains(skill, forbidden) {
			t.Errorf("the skill must not point agents at app internals: %q", forbidden)
		}
	}
	if !strings.Contains(skill, "never edit its settings") {
		t.Error("the skill must forbid changing the app")
	}
}

func TestSkillCoversActivationSessionsAndMute(t *testing.T) {
	skill := readSkill(t)
	for _, want := range []string{
		"Notifications are off until the user turns them on",
		"ignore every DataCodex Notify block",
		"<!-- datacodex-notify:start v1 -->",
		"<!-- datacodex-notify:end -->",
		"unique session name",
		"--rules-file",
		"--session",
		"Stop sending notifications for the rest of this session",
		"do not start a heartbeat",
		"disconnect",
	} {
		if !strings.Contains(skill, want) {
			t.Errorf("the skill does not mention %q", want)
		}
	}
}
