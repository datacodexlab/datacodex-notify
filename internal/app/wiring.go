// SPDX-License-Identifier: GPL-3.0-only

package app

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"datacodex-notify/internal/agents"
	"datacodex-notify/internal/autostart"
	"datacodex-notify/internal/notify"
	"datacodex-notify/internal/skillinstall"
	"datacodex-notify/internal/store"
	"datacodex-notify/internal/tray"
	"datacodex-notify/skills"
)

const (
	pauseDuration  = time.Hour
	traySyncPeriod = 3 * time.Second
)

// newAutostart creates the per-user startup manager for the running executable.
// It returns nil when the executable path is unknown, which hides the feature.
func (a *app) newAutostart() *autostart.Manager {
	exe, err := os.Executable()
	if err != nil {
		slog.Warn("Start with Windows is unavailable: the executable path is unknown", "err", err)
		return nil
	}
	registry := a.opts.AutostartRegistry
	if registry == nil {
		registry = autostart.RunKey{}
	}
	return autostart.New(registry, autostart.ValueName, exe)
}

func (a *app) buildTray() *tray.Tray {
	actions := tray.Actions{
		Open:        a.openWindow,
		SendTest:    a.sendTestMessage,
		TogglePause: a.togglePause,
		Exit:        a.requestExit,
	}
	if a.autostart != nil {
		actions.ToggleAutostart = a.toggleAutostart
	}
	return tray.New(a.opts.Version, a.locale, actions)
}

// watchState forwards connection changes to the tray and to the connection alerts.
func (a *app) watchState(ctx context.Context) {
	states := a.sender.SubscribeState()
	if a.tray != nil {
		a.tray.SetState(a.sender.GetState())
	}
	for {
		select {
		case <-ctx.Done():
			return
		case state := <-states:
			if a.tray != nil {
				a.tray.SetState(state)
			}
			a.connection.OnState(state)
		}
	}
}

// warnDisconnect tells the recipients that the connection was down for a long time.
func (a *app) warnDisconnect(from, to time.Time) {
	text := a.locale.GetMessage("disconnected_alert", map[string]string{
		"from": from.Local().Format("15:04"),
		"to":   to.Local().Format("15:04"),
	})
	if _, err := a.notifySvc.Alert(context.Background(), notify.Alert{Type: notify.TypeConnection, Level: store.LevelWarning, Source: "whatsapp", Text: text}); err != nil {
		slog.Warn("Disconnect warning was not queued", "err", err)
		return
	}
	a.queue.Wake()
}

// syncTray keeps the pause and startup menu items in line with changes made in the admin window.
func (a *app) syncTray(ctx context.Context) {
	ticker := time.NewTicker(traySyncPeriod)
	defer ticker.Stop()
	for {
		a.refreshTrayItems(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *app) refreshTrayItems(ctx context.Context) {
	if state, err := a.notifySvc.PauseState(ctx); err == nil {
		a.tray.SetPaused(state.Paused)
	}
	if a.autostart != nil {
		if enabled, err := a.autostart.Enabled(); err == nil {
			a.tray.SetAutostart(enabled)
		}
	}
}

func (a *app) togglePause() {
	ctx := context.Background()
	state, err := a.notifySvc.PauseState(ctx)
	if err != nil {
		slog.Error("Cannot read the pause state", "err", err)
		return
	}

	if state.Paused {
		err = a.notifySvc.Resume(ctx)
	} else {
		err = a.notifySvc.Pause(ctx, pauseDuration)
	}
	if err != nil {
		slog.Error("Cannot change the pause", "err", err)
		return
	}
	if released, err := a.notifySvc.ReleaseHeld(ctx); err == nil && released > 0 {
		a.queue.Wake()
	}
	a.refreshTrayItems(ctx)
}

func (a *app) toggleAutostart(enable bool) {
	if err := a.autostart.Set(enable); err != nil {
		slog.Error("Cannot change Start with Windows", "err", err)
	}
	a.refreshTrayItems(context.Background())
}

// setupAgents creates the agent registry and the skill manager, and silences the
// heartbeat monitor while every agent is muted.
func (a *app) setupAgents() {
	a.agents = agents.New(a.store, a.notifySvc, a.locale)
	a.heartbeat.SetSuppress(func(ctx context.Context) bool {
		muted, err := a.agents.MuteAll(ctx)
		return err == nil && muted
	})

	installDir := ""
	if exe, err := os.Executable(); err == nil {
		installDir = filepath.Dir(exe)
	}
	a.skill = skillinstall.New(a.skillRoots(), skills.DatacodexNotify, installDir)
}

// skillRoots lists the per-user skills folders of Claude Code and Codex.
func (a *app) skillRoots() []string {
	if len(a.opts.SkillRoots) > 0 {
		return a.opts.SkillRoots
	}
	home, err := os.UserHomeDir()
	if err != nil {
		slog.Warn("Skill installation is unavailable: the home folder is unknown", "err", err)
		return nil
	}
	return []string{
		filepath.Join(home, ".claude", "skills"),
		filepath.Join(home, ".codex", "skills"),
	}
}

// cliPath is the full path of the console CLI that sits next to the running executable.
func (a *app) cliPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), "datacodex-notify-cli.exe")
}
