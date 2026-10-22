// SPDX-License-Identifier: GPL-3.0-only

package cli

import (
	"os"

	"golang.org/x/sys/windows"
)

const attachParentProcess = ^uint32(0)

var procAttachConsole = windows.NewLazySystemDLL("kernel32.dll").NewProc("AttachConsole")

// attachParentConsole makes output visible when a GUI-subsystem binary is run
// from an interactive console. It does nothing when stdout is already valid
// (redirected to a file or a pipe).
func attachParentConsole() {
	if h, err := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE); err == nil && h != 0 && h != windows.InvalidHandle {
		return
	}
	if ok, _, _ := procAttachConsole.Call(uintptr(attachParentProcess)); ok == 0 {
		return
	}
	if out, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout = out
		os.Stderr = out
	}
}
