// SPDX-License-Identifier: GPL-3.0-only

package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	user32                       = windows.NewLazySystemDLL("user32.dll")
	procGetSystemMetrics         = user32.NewProc("GetSystemMetrics")
	procGetWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procShowWindow               = user32.NewProc("ShowWindow")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
)

const (
	smCxScreen = 0
	smCyScreen = 1
	swRestore  = 9

	edgeAppPathKey = `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\msedge.exe`
)

// screenSize returns the primary screen size, or a common default when unavailable.
func screenSize() (int, int) {
	w, _, _ := procGetSystemMetrics.Call(smCxScreen)
	h, _, _ := procGetSystemMetrics.Call(smCyScreen)
	if w == 0 || h == 0 {
		return 1920, 1080
	}
	return int(w), int(h)
}

// findEdge locates msedge.exe through the App Paths registry key, then the
// standard install folders.
func findEdge() (string, error) {
	for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		key, err := registry.OpenKey(root, edgeAppPathKey, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		path, _, err := key.GetStringValue("")
		key.Close()
		if err == nil && fileExists(path) {
			return path, nil
		}
	}

	for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles"} {
		if base := os.Getenv(env); base != "" {
			path := filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe")
			if fileExists(path) {
				return path, nil
			}
		}
	}
	return "", fmt.Errorf("msedge.exe not found")
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// focusProcessWindow restores and focuses the first visible top-level window of pid.
func focusProcessWindow(pid int) {
	var target windows.HWND
	callback := windows.NewCallback(func(hwnd windows.HWND, _ uintptr) uintptr {
		var windowPID uint32
		_, _, _ = procGetWindowThreadProcessID.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&windowPID)))
		visible, _, _ := procIsWindowVisible.Call(uintptr(hwnd))
		if int(windowPID) == pid && visible != 0 {
			target = hwnd
			return 0
		}
		return 1
	})
	_ = windows.EnumWindows(callback, nil)
	if target == 0 {
		return
	}
	_, _, _ = procShowWindow.Call(uintptr(target), swRestore)
	_, _, _ = procSetForegroundWindow.Call(uintptr(target))
}
