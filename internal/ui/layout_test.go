// SPDX-License-Identifier: GPL-3.0-only

package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Renders every tab and sub-tab in headless Edge, in both languages, and fails
// when a page needs scrolling at the app window size. It needs node and Edge;
// without them (or with -short) it is skipped.
func TestEveryTabFitsTheAppWindowWithoutScrolling(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped with -short: the headless browser run takes about a minute")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; skipping the layout check")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "tools", "uicheck", "check.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, script, t.TempDir(), "460", "680")
	out, err := cmd.CombinedOutput()
	text := string(out)
	if strings.Contains(text, "SKIP:") {
		t.Skip(strings.TrimSpace(text))
	}
	if err != nil {
		t.Fatalf("layout check failed:\n%s", text)
	}
	_ = os.Stdout
}
