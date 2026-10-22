// SPDX-License-Identifier: GPL-3.0-only

// Command docsgen regenerates every picture used by the README and the
// presentation from the code at HEAD: the demo screenshots, the chat mockups
// (texts produced by the real message template), and the architecture diagram.
//
//	go run ./tools/docsgen
//
// It refuses to run when web/ has uncommitted changes (the pictures must match
// a commit) unless -allow-dirty is given, and records the commit in
// docs/assets/BUILD-INFO.txt. It needs Go, Node, and Microsoft Edge.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func main() {
	allowDirty := flag.Bool("allow-dirty", false, "capture even when web/ has uncommitted changes")
	flag.Parse()

	root, err := run(".", "git", "rev-parse", "--show-toplevel")
	check(err)
	root = filepath.FromSlash(strings.TrimSpace(root))

	// A repository without a commit yet (the public export, before its first commit) cannot be dirty.
	_, headErr := run(root, "git", "rev-parse", "--verify", "HEAD")
	dirty := ""
	if headErr == nil {
		dirty, err = run(root, "git", "status", "--porcelain", "--", "web", "internal/notify", "internal/locale")
		check(err)
	}
	if strings.TrimSpace(dirty) != "" && !*allowDirty {
		fail("web/, internal/notify, or internal/locale has uncommitted changes; commit them first (or pass -allow-dirty):\n" + dirty)
	}
	commit := "unreleased"
	if headErr == nil {
		out, err := run(root, "git", "rev-parse", "--short", "HEAD")
		check(err)
		commit = strings.TrimSpace(out)
	}

	version := appVersion(root)
	assets := filepath.Join(root, "docs", "assets")
	steps := []struct {
		name string
		cmd  []string
		env  []string
	}{
		{"chat texts from the real template", []string{"go", "test", "-count=1", "-run", "TestGenerateMockupMessages", "./internal/notify"}, []string{"DCN_MOCKUP_OUT=" + filepath.Join(assets, "mockups")}},
		{"demo screenshots", []string{"node", "tools/uicheck/demo.mjs", filepath.Join(assets, "screenshots")}, []string{"DCN_VERSION=" + version}},
		{"mockups and diagrams", []string{"node", "tools/uicheck/docs-art.mjs"}, nil},
	}
	for _, step := range steps {
		fmt.Println("==>", step.name)
		cmd := exec.Command(step.cmd[0], step.cmd[1:]...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), step.env...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		check(cmd.Run())
	}

	info := fmt.Sprintf("commit: %s\nversion: %s\nweb_clean: %t\ngenerated: %s\n", commit, version, strings.TrimSpace(dirty) == "", time.Now().UTC().Format(time.RFC3339))
	check(os.WriteFile(filepath.Join(assets, "BUILD-INFO.txt"), []byte(info), 0o644))
	fmt.Print(info)
}

var versionPattern = regexp.MustCompile(`var version = "([^"]+)"`)

func appVersion(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "cmd", "datacodex-notify", "main.go"))
	if err != nil {
		return "unknown"
	}
	if m := versionPattern.FindSubmatch(data); m != nil {
		return string(m[1])
	}
	return "unknown"
}

func run(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}

func check(err error) {
	if err != nil {
		fail(err.Error())
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "docsgen:", msg)
	os.Exit(1)
}
