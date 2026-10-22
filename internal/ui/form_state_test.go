// SPDX-License-Identifier: GPL-3.0-only

package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The helpers that keep unsaved edits alive across a refresh are plain DOM-free
// logic, so Node can exercise them with fake fields.
func TestUnsavedEditHelpers(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; skipping the form-state helper test")
	}
	dom, err := filepath.Abs(filepath.Join("..", "..", "web", "js", "dom.js"))
	if err != nil {
		t.Fatal(err)
	}
	script := `
const { trackEdits, setIfClean, markClean, isDirty } = await import("file:///" + process.argv[1].split("\\").join("/"));
const field = () => {
  const handlers = {};
  return { dataset: {}, value: "22:00", type: "time", addEventListener(n, f) { handlers[n] = f; }, fire(n) { handlers[n](); } };
};
const assert = (ok, msg) => { if (!ok) { console.error("FAIL: " + msg); process.exit(1); } };

const start = field();
trackEdits(start);
setIfClean(start, "22:00");
assert(start.value === "22:00" && !isDirty(start), "a clean field takes the server value");

start.value = "23:30";
start.fire("input");
setIfClean(start, "22:00");
assert(start.value === "23:30" && isDirty(start), "a refresh must not overwrite an unsaved edit");

markClean(start);
setIfClean(start, "23:30");
assert(!isDirty(start) && start.value === "23:30", "after a save the field follows the server again");
setIfClean(start, "07:00");
assert(start.value === "07:00", "a clean field is refreshed");

const box = field();
box.type = "checkbox";
box.checked = false;
trackEdits(box);
box.fire("change");
setIfClean(box, true);
assert(box.checked === false, "a changed checkbox keeps the user's choice");
`
	cmd := exec.Command(node, "--input-type=module", "-e", script, dom)
	cmd.Dir = t.TempDir()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node failed: %v\n%s", err, strings.TrimSpace(string(out)))
	}
	_ = os.Stdout
}
