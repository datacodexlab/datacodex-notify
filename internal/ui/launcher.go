// SPDX-License-Identifier: GPL-3.0-only

package ui

import (
	"fmt"
	"os/exec"
	"sync"
)

const (
	windowWidth  = 460
	windowHeight = 680
)

// Launcher opens the admin UI in a dedicated Edge app window and tracks the
// process it started so a second click does not open a second window.
type Launcher struct {
	profileDir string

	mu   sync.Mutex
	pid  int
	done chan struct{}
}

// NewLauncher creates a launcher that keeps Edge state in profileDir. A
// dedicated profile is required: an Edge instance that is already running with
// the default profile would ignore the window size and position.
func NewLauncher(profileDir string) *Launcher {
	return &Launcher{profileDir: profileDir}
}

// IsOpen reports whether the window started by this launcher is still alive.
func (l *Launcher) IsOpen() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.aliveLocked()
}

// Focus brings the existing window to the foreground (best effort).
func (l *Launcher) Focus() {
	l.mu.Lock()
	pid := l.pid
	alive := l.aliveLocked()
	l.mu.Unlock()
	if alive {
		focusProcessWindow(pid)
	}
}

// Open starts the window at url. It does nothing when the window is already open.
func (l *Launcher) Open(url string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.aliveLocked() {
		return nil
	}

	edge, err := findEdge()
	if err != nil {
		return openInDefaultBrowser(url)
	}

	x, y := centeredPosition(screenSize())
	cmd := exec.Command(edge, edgeArgs(url, l.profileDir, x, y)...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start Edge: %w", err)
	}

	done := make(chan struct{})
	l.pid = cmd.Process.Pid
	l.done = done
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	return nil
}

func (l *Launcher) aliveLocked() bool {
	if l.done == nil {
		return false
	}
	select {
	case <-l.done:
		return false
	default:
		return true
	}
}

func openInDefaultBrowser(url string) error {
	if err := exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start(); err != nil {
		return fmt.Errorf("failed to open the default browser: %w", err)
	}
	return nil
}

func edgeArgs(url, profileDir string, x, y int) []string {
	return []string{
		"--app=" + url,
		fmt.Sprintf("--window-size=%d,%d", windowWidth, windowHeight),
		fmt.Sprintf("--window-position=%d,%d", x, y),
		"--user-data-dir=" + profileDir,
		"--no-first-run",
		"--no-default-browser-check",
	}
}

// centeredPosition returns the top-left corner that centers the window on a
// screen of the given size. Positions never go negative.
func centeredPosition(screenW, screenH int) (int, int) {
	return max((screenW-windowWidth)/2, 0), max((screenH-windowHeight)/2, 0)
}
