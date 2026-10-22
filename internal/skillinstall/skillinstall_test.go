// SPDX-License-Identifier: GPL-3.0-only

package skillinstall

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = "name: x\nCLI: {{INSTALL_DIR}}\\datacodex-notify-cli.exe\nBash: {{INSTALL_DIR_BASH}}/datacodex-notify-cli.exe\n"

func newManager(t *testing.T) (*Manager, string, string) {
	t.Helper()
	base := t.TempDir()
	claude := filepath.Join(base, ".claude", "skills")
	codex := filepath.Join(base, ".codex", "skills")
	for _, dir := range []string{claude, codex} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return New([]string{claude, codex}, []byte(sample), `D:\Apps\Notify`), claude, codex
}

func TestInstallWritesTheRenderedSkillIntoBothRoots(t *testing.T) {
	m, claude, codex := newManager(t)
	if err := m.Install(); err != nil {
		t.Fatalf("Install failed: %v", err)
	}
	for _, root := range []string{claude, codex} {
		data, err := os.ReadFile(filepath.Join(root, FolderName, "SKILL.md"))
		if err != nil {
			t.Fatalf("skill not installed in %s: %v", root, err)
		}
		text := string(data)
		if !strings.Contains(text, `D:\Apps\Notify\datacodex-notify-cli.exe`) || !strings.Contains(text, "/d/Apps/Notify/datacodex-notify-cli.exe") {
			t.Errorf("install paths were not rewritten:\n%s", text)
		}
		if strings.Contains(text, "{{") {
			t.Errorf("no placeholder may be left in an installed copy:\n%s", text)
		}
	}
	for _, st := range m.Status() {
		if !st.Available || !st.Installed || !st.UpToDate {
			t.Errorf("unexpected status: %+v", st)
		}
	}
}

func TestRenderFillsTheTokensForTheRunningProgramsFolder(t *testing.T) {
	out := string(Render([]byte(sample), `C:\Users\demo\AppData\Local\Programs\DatacodexNotify`))
	if !strings.Contains(out, `CLI: C:\Users\demo\AppData\Local\Programs\DatacodexNotify\datacodex-notify-cli.exe`) ||
		!strings.Contains(out, "Bash: /c/Users/demo/AppData/Local/Programs/DatacodexNotify/datacodex-notify-cli.exe") {
		t.Errorf("both path forms must be filled in:\n%s", out)
	}
	if strings.Contains(out, "{{") {
		t.Errorf("no token may remain:\n%s", out)
	}
}

func TestStatusDetectsOutdatedAndMissingCopies(t *testing.T) {
	m, claude, codex := newManager(t)
	_ = m.Install()
	if err := os.WriteFile(filepath.Join(claude, FolderName, "SKILL.md"), []byte("old text"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(codex, FolderName)); err != nil {
		t.Fatal(err)
	}

	status := m.Status()
	if !status[0].Installed || status[0].UpToDate {
		t.Errorf("an edited copy is installed but not up to date: %+v", status[0])
	}
	if status[1].Installed || status[1].UpToDate || !status[1].Available {
		t.Errorf("a removed copy is available but not installed: %+v", status[1])
	}

	if err := m.Install(); err != nil {
		t.Fatal(err)
	}
	for _, st := range m.Status() {
		if !st.UpToDate {
			t.Errorf("reinstall must bring every copy up to date: %+v", st)
		}
	}
}

func TestMissingRootsAreSkippedNotCreated(t *testing.T) {
	base := t.TempDir()
	missing := filepath.Join(base, ".codex", "skills")
	m := New([]string{missing}, []byte(sample), `D:\Apps\Notify`)

	if err := m.Install(); err != nil {
		t.Fatalf("a missing root is not an error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, ".codex")); !os.IsNotExist(err) {
		t.Error("Install must not create the tool's folders")
	}
	if st := m.Status()[0]; st.Available || st.Installed {
		t.Errorf("status = %+v", st)
	}
	if err := m.Remove(); err != nil {
		t.Errorf("Remove on a missing root is not an error: %v", err)
	}
}

func TestRemoveDeletesOnlyTheSkillAndKeepsOtherFiles(t *testing.T) {
	m, claude, codex := newManager(t)
	_ = m.Install()

	neighbour := filepath.Join(claude, "other-skill")
	if err := os.MkdirAll(neighbour, 0o755); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(claude, FolderName, "notes.txt")
	if err := os.WriteFile(extra, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := m.Remove(); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(claude, FolderName, "SKILL.md")); !os.IsNotExist(err) {
		t.Error("the skill text must be removed")
	}
	if _, err := os.Stat(extra); err != nil {
		t.Error("another file inside the folder must survive")
	}
	if _, err := os.Stat(neighbour); err != nil {
		t.Error("a neighbouring skill must never be touched")
	}
	if _, err := os.Stat(filepath.Join(codex, FolderName)); !os.IsNotExist(err) {
		t.Error("an otherwise empty folder is removed")
	}
	if _, err := os.Stat(codex); err != nil {
		t.Error("the skills root itself must stay")
	}
}

func TestSymbolicLinksAndFilesAreRefused(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "skills")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	// A plain file where the folder should be.
	if err := os.WriteFile(filepath.Join(root, FolderName), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := New([]string{root}, []byte(sample), `D:\Apps\Notify`)
	if err := m.Install(); !errors.Is(err, ErrUnsafeTarget) {
		t.Errorf("a file in the way must be refused, got %v", err)
	}
	if err := m.Remove(); !errors.Is(err, ErrUnsafeTarget) {
		t.Errorf("Remove must refuse it too, got %v", err)
	}

	// A symbolic link, when the system allows creating one.
	linkRoot := filepath.Join(base, "linked")
	victim := filepath.Join(base, "victim")
	_ = os.MkdirAll(linkRoot, 0o755)
	_ = os.MkdirAll(victim, 0o755)
	if err := os.Symlink(victim, filepath.Join(linkRoot, FolderName)); err != nil {
		t.Skip("symbolic links cannot be created here: " + err.Error())
	}
	if err := New([]string{linkRoot}, []byte(sample), `D:\Apps\Notify`).Install(); !errors.Is(err, ErrUnsafeTarget) {
		t.Errorf("a symbolic link must be refused, got %v", err)
	}
	if entries, _ := os.ReadDir(victim); len(entries) != 0 {
		t.Error("nothing may be written through a link")
	}
}
