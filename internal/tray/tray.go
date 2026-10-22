// SPDX-License-Identifier: GPL-3.0-only

// Package tray shows the notification-area icon, its state tooltip, and menu.
package tray

import (
	"fmt"
	"sync"

	"fyne.io/systray"

	"datacodex-notify/assets"
	"datacodex-notify/internal/locale"
	"datacodex-notify/internal/wa"
)

// Actions are the callbacks the menu triggers.
type Actions struct {
	Open     func()
	SendTest func()
	// TogglePause pauses non-critical notifications for an hour, or resumes them.
	TogglePause func()
	// ToggleAutostart turns "Start with Windows" on or off. Nil hides the item.
	ToggleAutostart func(enable bool)
	Exit            func()
}

// Tray owns the systray icon and menu.
type Tray struct {
	version string
	locale  *locale.Manager
	actions Actions

	mu        sync.Mutex
	state     wa.ConnectionState
	ready     bool
	paused    bool
	autostart bool
	pauseItem *systray.MenuItem
	autoItem  *systray.MenuItem
}

// New creates a tray that starts in the unlinked state.
func New(version string, loc *locale.Manager, actions Actions) *Tray {
	return &Tray{
		version: version,
		locale:  loc,
		actions: actions,
		state:   wa.StateUnlinked,
	}
}

// Run blocks the calling goroutine (it must be the main one) until Quit is called.
// onReady runs after the icon and menu are in place.
func (t *Tray) Run(onReady func()) {
	systray.Run(func() {
		t.build()
		if onReady != nil {
			onReady()
		}
	}, func() {})
}

// Quit removes the icon and makes Run return.
func (t *Tray) Quit() {
	systray.Quit()
}

// SetState updates the icon color and tooltip. It is safe to call before Run is ready.
func (t *Tray) SetState(s wa.ConnectionState) {
	t.mu.Lock()
	t.state = s
	ready := t.ready
	t.mu.Unlock()

	if ready {
		t.apply(s)
	}
}

// SetPaused switches the pause item between "pause" and "resume".
func (t *Tray) SetPaused(paused bool) {
	t.mu.Lock()
	t.paused = paused
	item := t.pauseItem
	t.mu.Unlock()

	if item != nil {
		item.SetTitle(pauseTitle(t.locale, paused))
	}
}

// SetAutostart shows whether the app starts with Windows.
func (t *Tray) SetAutostart(enabled bool) {
	t.mu.Lock()
	t.autostart = enabled
	item := t.autoItem
	t.mu.Unlock()

	if item == nil {
		return
	}
	if enabled {
		item.Check()
	} else {
		item.Uncheck()
	}
}

func pauseTitle(loc *locale.Manager, paused bool) string {
	if paused {
		return loc.GetTrayText("resume")
	}
	return loc.GetTrayText("pause")
}

func (t *Tray) build() {
	systray.SetOnTapped(t.actions.Open)

	t.mu.Lock()
	paused, autostart := t.paused, t.autostart
	t.mu.Unlock()

	open := systray.AddMenuItem(t.locale.GetTrayText("open"), "")
	sendTest := systray.AddMenuItem(t.locale.GetTrayText("send_test"), "")
	pause := systray.AddMenuItem(pauseTitle(t.locale, paused), "")

	// A nil channel never fires, so a hidden item needs no special case below.
	var autoClicks <-chan struct{}
	var auto *systray.MenuItem
	if t.actions.ToggleAutostart != nil {
		auto = systray.AddMenuItemCheckbox(t.locale.GetTrayText("start_with_windows"), "", autostart)
		autoClicks = auto.ClickedCh
	}
	systray.AddSeparator()
	exit := systray.AddMenuItem(t.locale.GetTrayText("exit"), "")

	t.mu.Lock()
	t.ready = true
	t.pauseItem, t.autoItem = pause, auto
	state := t.state
	t.mu.Unlock()
	t.apply(state)

	go func() {
		for {
			select {
			case <-open.ClickedCh:
				t.actions.Open()
			case <-sendTest.ClickedCh:
				t.actions.SendTest()
			case <-pause.ClickedCh:
				t.actions.TogglePause()
			case <-autoClicks:
				t.mu.Lock()
				enable := !t.autostart
				t.mu.Unlock()
				t.actions.ToggleAutostart(enable)
			case <-exit.ClickedCh:
				t.actions.Exit()
				return
			}
		}
	}()
}

func (t *Tray) apply(s wa.ConnectionState) {
	systray.SetIcon(iconFor(s))
	systray.SetTooltip(t.tooltip(s))
}

func (t *Tray) tooltip(s wa.ConnectionState) string {
	return fmt.Sprintf("DataCodex Notify v%s - %s", t.version, t.locale.GetTrayText("state_"+string(s)))
}

func iconFor(s wa.ConnectionState) []byte {
	switch s {
	case wa.StateConnected:
		return assets.IconGreen
	case wa.StateConnecting, wa.StateReconnecting:
		return assets.IconYellow
	default:
		return assets.IconRed
	}
}
