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

// The project is GPL-3.0-only: LICENSE is the GPL text, every Go file carries the
// SPDX line, and no project document still names the old permissive license.
func TestProjectIsLicensedGPL3(t *testing.T) {
	root := filepath.Join("..", "..")

	license, err := os.ReadFile(filepath.Join(root, "LICENSE"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(license)
	if !strings.Contains(text, "GNU GENERAL PUBLIC LICENSE") || !strings.Contains(text, "Version 3, 29 June 2007") || !strings.Contains(text, "END OF TERMS AND CONDITIONS") {
		t.Error("LICENSE must hold the full GPL-3.0 text")
	}

	for _, readme := range []string{"README.md", "README.ar.md"} {
		data, err := os.ReadFile(filepath.Join(root, readme))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "Copyright (C) 2026 DataCodex") && !strings.Contains(string(data), "(C) 2026 DataCodex") {
			t.Errorf("%s must carry the copyright line", readme)
		}
		if !strings.Contains(string(data), "GPL-3.0-only") {
			t.Errorf("%s must name GPL-3.0-only", readme)
		}
	}

	const spdx = "// SPDX-License-Identifier: GPL-3.0-only"
	mit := regexp.MustCompile(`\b` + "M" + "IT" + `\b`)
	// Third-party notices and the license detector legitimately mention other projects' licenses.
	exempt := map[string]bool{"THIRD_PARTY_NOTICES.md": true, "tools/notices/main.go": true}

	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch d.Name() {
			case ".git", "dist", "node_modules", "plans", "reports", "spikes", "fonts":
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".go" {
			data, err := os.ReadFile(path)
			if err != nil || !strings.HasPrefix(strings.ReplaceAll(string(data), "\r\n", "\n"), spdx+"\n") {
				t.Errorf("%s must start with %q", rel, spdx)
			}
		}
		switch ext {
		case ".md", ".html", ".txt", ".iss", ".yml", ".mjs", ".go", ".ps1":
			if exempt[rel] || strings.HasPrefix(rel, "docs/0") || rel == "changelog.md" || rel == "project-context.md" || rel == "project-key.md" || rel == "master-constitution.md" {
				return nil
			}
			data, err := os.ReadFile(path)
			if err == nil && mit.Match(data) {
				t.Errorf("%s still names the old license", rel)
			}
		}
		return nil
	})
}
