// SPDX-License-Identifier: GPL-3.0-only

package app

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

const defaultInstanceName = `Local\DatacodexNotify`

// instanceLock is a named mutex that allows one running instance per user session.
type instanceLock struct {
	handle windows.Handle
}

// acquireInstance takes the named mutex. alreadyRunning is true when another
// process holds it, in which case the returned lock is nil.
func acquireInstance(name string) (lock *instanceLock, alreadyRunning bool, err error) {
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, false, fmt.Errorf("invalid mutex name: %w", err)
	}

	handle, err := windows.CreateMutex(nil, false, namePtr)
	if err != nil {
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			_ = windows.CloseHandle(handle)
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("failed to create instance mutex: %w", err)
	}
	return &instanceLock{handle: handle}, false, nil
}

func (l *instanceLock) release() {
	_ = windows.CloseHandle(l.handle)
}
