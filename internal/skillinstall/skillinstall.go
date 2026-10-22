// SPDX-License-Identifier: GPL-3.0-only

// Package skillinstall installs and removes the agent skill in the per-user
// skill folders of Claude Code and Codex. It touches only the exact folder
// "datacodex-notify" inside each skills root and refuses symbolic links.
package skillinstall

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// FolderName is the only folder this package creates or removes inside a skills root.
	FolderName = "datacodex-notify"
	fileName   = "SKILL.md"

	// installDirToken and installDirBashToken stand for the install folder in the skill
	// source; Render replaces them with the real folder of the running program.
	installDirToken     = "{{INSTALL_DIR}}"
	installDirBashToken = "{{INSTALL_DIR_BASH}}"
)

// ErrUnsafeTarget means a target folder is not the expected plain folder.
var ErrUnsafeTarget = errors.New("refusing to touch an unexpected skill folder")

// Manager installs one skill text into several skills roots.
type Manager struct {
	roots      []string
	content    []byte
	installDir string
}

// New creates a manager. roots are skills folders such as %USERPROFILE%\.claude\skills;
// content is the skill source and installDir is where the app is installed (used to
// fill the install-folder tokens inside the text).
func New(roots []string, content []byte, installDir string) *Manager {
	return &Manager{roots: roots, content: content, installDir: installDir}
}

// Render returns the skill text with the install-folder tokens replaced by installDir
// (Windows form) and its Git Bash form (/c/...).
func Render(content []byte, installDir string) []byte {
	bash := installDir
	if len(installDir) >= 3 {
		bash = "/" + strings.ToLower(installDir[:1]) + filepath.ToSlash(installDir[2:])
	}
	out := bytes.ReplaceAll(content, []byte(installDirBashToken), []byte(bash))
	return bytes.ReplaceAll(out, []byte(installDirToken), []byte(installDir))
}

// TargetStatus describes one skills root.
type TargetStatus struct {
	// Path is the skill folder (root plus datacodex-notify).
	Path string `json:"path"`
	// Available is false when the skills root does not exist (the tool is not installed).
	Available bool `json:"available"`
	Installed bool `json:"installed"`
	// UpToDate is true when the installed text equals the current one.
	UpToDate bool `json:"up_to_date"`
}

func (m *Manager) rendered() []byte {
	if len(m.installDir) < 3 {
		return m.content
	}
	return Render(m.content, m.installDir)
}

// safeTarget returns the skill folder for a root after checking it is exactly
// <root>\datacodex-notify and not a link.
func safeTarget(root string) (string, error) {
	dir := filepath.Join(root, FolderName)
	if filepath.Base(dir) != FolderName || filepath.Clean(filepath.Dir(dir)) != filepath.Clean(root) {
		return "", ErrUnsafeTarget
	}
	info, err := os.Lstat(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return dir, nil
	case err != nil:
		return "", err
	case info.Mode()&os.ModeSymlink != 0 || !info.IsDir():
		return "", fmt.Errorf("%w: %s", ErrUnsafeTarget, dir)
	}
	return dir, nil
}

func rootExists(root string) bool {
	info, err := os.Stat(root)
	return err == nil && info.IsDir()
}

// Status reports every root.
func (m *Manager) Status() []TargetStatus {
	want := m.rendered()
	out := make([]TargetStatus, 0, len(m.roots))
	for _, root := range m.roots {
		st := TargetStatus{Path: filepath.Join(root, FolderName), Available: rootExists(root)}
		if st.Available {
			if dir, err := safeTarget(root); err == nil {
				if have, err := os.ReadFile(filepath.Join(dir, fileName)); err == nil {
					st.Installed = true
					st.UpToDate = bytes.Equal(have, want)
				}
			}
		}
		out = append(out, st)
	}
	return out
}

// Install writes the skill into every available root, replacing the text of an
// existing copy. Roots that do not exist are skipped.
func (m *Manager) Install() error {
	want := m.rendered()
	var errs []error
	for _, root := range m.roots {
		if !rootExists(root) {
			continue
		}
		dir, err := safeTarget(root)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			errs = append(errs, fmt.Errorf("failed to create %s: %w", dir, err))
			continue
		}
		if err := writeAtomic(filepath.Join(dir, fileName), want); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Remove deletes the skill text from every root and then the folder itself when
// it is empty. Anything else in the folder is left alone.
func (m *Manager) Remove() error {
	var errs []error
	for _, root := range m.roots {
		if !rootExists(root) {
			continue
		}
		dir, err := safeTarget(root)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := os.Remove(filepath.Join(dir, fileName)); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("failed to remove the skill text in %s: %w", dir, err))
			continue
		}
		// Fails harmlessly when the folder holds other files or does not exist.
		_ = os.Remove(dir)
	}
	return errors.Join(errs...)
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to finish %s: %w", path, err)
	}
	return nil
}
