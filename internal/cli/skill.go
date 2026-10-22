// SPDX-License-Identifier: GPL-3.0-only

package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"datacodex-notify/internal/skillinstall"
	"datacodex-notify/skills"
)

// runSkill installs, removes, or reports the agent skill in the per-user skill
// folders of Claude Code and Codex. It works without the app running (the
// installer and uninstaller call it), and touches only the datacodex-notify
// folder inside each skills root.
func runSkill(args []string, e env) int {
	if len(args) == 0 || (args[0] != "install" && args[0] != "remove" && args[0] != "status") {
		return fail(e, exitValidation, fmt.Errorf("usage: skill install|remove|status [--home FOLDER] [--install-dir FOLDER]"))
	}
	action := args[0]
	fs := flag.NewFlagSet("skill", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	home := fs.String("home", "", "user folder that holds .claude and .codex (default: your user profile)")
	installDir := fs.String("install-dir", "", "folder of the installed program (default: the folder of this program); with remove, only copies that point at this folder are removed")
	if err := fs.Parse(args[1:]); err != nil {
		return exitValidation
	}

	if *home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return fail(e, exitServerError, fmt.Errorf("the user folder is unknown: %w", err))
		}
		*home = h
	}
	explicitDir := *installDir != ""
	if *installDir == "" {
		exe, err := os.Executable()
		if err != nil {
			return fail(e, exitServerError, err)
		}
		*installDir = filepath.Dir(exe)
	}
	roots := []string{filepath.Join(*home, ".claude", "skills"), filepath.Join(*home, ".codex", "skills")}
	if action == "remove" && explicitDir {
		roots = rootsPointingAt(roots, *installDir)
	}
	manager := skillinstall.New(roots, skills.DatacodexNotify, *installDir)

	var err error
	switch action {
	case "install":
		err = manager.Install()
	case "remove":
		err = manager.Remove()
	}
	if err != nil {
		return fail(e, exitServerError, err)
	}
	return printJSON(e, map[string]interface{}{"action": action, "skill": manager.Status()})
}

// rootsPointingAt keeps the skills roots whose installed skill names installDir, so
// uninstalling one installation never removes the skill of another.
func rootsPointingAt(roots []string, installDir string) []string {
	var keep []string
	for _, root := range roots {
		data, err := os.ReadFile(filepath.Join(root, skillinstall.FolderName, "SKILL.md"))
		if err == nil && strings.Contains(string(data), installDir) {
			keep = append(keep, root)
		}
	}
	return keep
}
