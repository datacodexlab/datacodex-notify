// SPDX-License-Identifier: GPL-3.0-only

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runSkillCmd(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	e := env{stdout: &out, stderr: &errOut, newClient: func() (*Client, int, error) { return nil, exitNotRunning, os.ErrNotExist }}
	code := execute(append([]string{"skill"}, args...), "1", e)
	return code, out.String() + errOut.String()
}

func TestSkillCommandInstallsAndRemovesWithoutTheAppRunning(t *testing.T) {
	home := t.TempDir()
	for _, dir := range []string{".claude", ".codex"} {
		if err := os.MkdirAll(filepath.Join(home, dir, "skills"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	install := `D:\Apps\Notify`

	code, out := runSkillCmd(t, "install", "--home", home, "--install-dir", install)
	if code != exitOK {
		t.Fatalf("install: %d %s", code, out)
	}
	var result struct {
		Skill []struct {
			Installed bool `json:"installed"`
			UpToDate  bool `json:"up_to_date"`
		} `json:"skill"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil || len(result.Skill) != 2 || !result.Skill[0].UpToDate || !result.Skill[1].Installed {
		t.Fatalf("unexpected result: %v %s", err, out)
	}
	for _, dir := range []string{".claude", ".codex"} {
		data, err := os.ReadFile(filepath.Join(home, dir, "skills", "datacodex-notify", "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if !strings.Contains(text, `D:\Apps\Notify\datacodex-notify-cli.exe`) || strings.Contains(text, "{{") {
			t.Errorf("the installed copy must contain the real install folder and no placeholder")
		}
	}

	if code, out := runSkillCmd(t, "status", "--home", home, "--install-dir", install); code != exitOK || !strings.Contains(out, `"up_to_date":true`) {
		t.Errorf("status: %d %s", code, out)
	}
	if code, out := runSkillCmd(t, "remove", "--home", home, "--install-dir", `E:\Other\Install`); code != exitOK {
		t.Fatalf("remove for another install folder: %d %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "datacodex-notify", "SKILL.md")); err != nil {
		t.Fatal("removing for another install folder must leave this skill alone")
	}
	if code, out := runSkillCmd(t, "remove", "--home", home, "--install-dir", install); code != exitOK {
		t.Fatalf("remove for this install folder: %d %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "datacodex-notify")); !os.IsNotExist(err) {
		t.Fatal("the skill pointing at this install folder must be removed")
	}
	if code, out := runSkillCmd(t, "install", "--home", home, "--install-dir", install); code != exitOK {
		t.Fatalf("reinstall: %d %s", code, out)
	}
	if code, out := runSkillCmd(t, "remove", "--home", home); code != exitOK {
		t.Fatalf("remove: %d %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "datacodex-notify")); !os.IsNotExist(err) {
		t.Error("the skill folder must be gone after remove")
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills")); err != nil {
		t.Error("the skills root itself must stay")
	}
}

func TestSkillCommandValidation(t *testing.T) {
	for _, args := range [][]string{{}, {"explode"}, {"install", "--nope"}} {
		if code, _ := runSkillCmd(t, args...); code != exitValidation {
			t.Errorf("skill %v: got %d, want %d", args, code, exitValidation)
		}
	}
	// Without the tools' folders nothing is installed and nothing fails.
	if code, _ := runSkillCmd(t, "install", "--home", t.TempDir(), "--install-dir", `D:\x`); code != exitOK {
		t.Errorf("an empty home is not an error: %d", code)
	}
}
