// SPDX-License-Identifier: GPL-3.0-only

package ui

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The launch materials (README files, presentation, screenshots, mockups, and the
// tools that produce them) must never carry personal paths, real phone numbers,
// e-mail addresses, or keys. Only the fake demo number is allowed.
func TestLaunchMaterialsHaveNoPersonalData(t *testing.T) {
	root := filepath.Join("..", "..")
	targets := []string{"README.md", "README.ar.md", "LICENSE", filepath.Join("docs", "assets"), filepath.Join("docs", "presentation"), filepath.Join("tools", "uicheck"), filepath.Join("tools", "docsgen"), filepath.Join("tools", "demoqr")}

	// The developer's account name is assembled here so this file never contains it.
	username := regexp.MustCompile(`(?i)` + "dell" + "-pc3k|dell" + "-p~1")
	userPath := regexp.MustCompile(`(?i)[a-z]:[\/]+users[\/]+[^\/\s"']+`)
	email := regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	apiKey := regexp.MustCompile(`AIza[0-9A-Za-z_-]{20,}`)
	number := regexp.MustCompile(`\+?\d[\d ()-]{8,}\d`)
	allowedNumbers := map[string]bool{"201000000000": true, "2010000000": true}
	binary := map[string]bool{".png": true, ".woff2": true, ".ico": true}

	var scanned int
	for _, target := range targets {
		base := filepath.Join(root, target)
		info, err := os.Stat(base)
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		walk := func(path string) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Errorf("%s: %v", path, err)
				return
			}
			scanned++
			text := string(data)
			rel, _ := filepath.Rel(root, path)
			if username.MatchString(text) {
				t.Errorf("%s contains the developer's user name", rel)
			}
			if binary[strings.ToLower(filepath.Ext(path))] {
				return
			}
			if m := userPath.FindString(text); m != "" && !strings.Contains(strings.ToLower(m), `\users\dev`) && !strings.Contains(strings.ToLower(m), `/users/dev`) {
				t.Errorf("%s contains a personal path: %s", rel, m)
			}
			if m := email.FindString(text); m != "" && !strings.HasSuffix(m, "@s.whatsapp.net") {
				t.Errorf("%s contains an e-mail address: %s", rel, m)
			}
			if m := apiKey.FindString(text); m != "" {
				t.Errorf("%s contains something that looks like an API key", rel)
			}
			for _, m := range number.FindAllString(text, -1) {
				digits := regexp.MustCompile(`\D`).ReplaceAllString(m, "")
				if len(digits) < 10 || len(digits) > 15 || allowedNumbers[digits] || strings.HasPrefix(digits, "2010000000") {
					continue
				}
				// Timestamps such as 2026-10-21 19:05 and long ids are not phone numbers.
				if regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`).MatchString(strings.TrimSpace(m)) {
					continue
				}
				t.Errorf("%s contains a number that could be a real phone number: %q", rel, m)
			}
		}
		if !info.IsDir() {
			walk(base)
			continue
		}
		_ = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				walk(path)
			}
			return nil
		})
	}
	if scanned < 20 {
		t.Errorf("only %d files were scanned; the documentation assets are missing", scanned)
	}
}
