// SPDX-License-Identifier: GPL-3.0-only

// Package app wires the service together: storage, WhatsApp, queue, local API,
// admin window, and tray, and shuts them down in order.
package app

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"log/slog"
	"path/filepath"
	"time"

	"datacodex-notify/internal/agents"
	"datacodex-notify/internal/alerts"
	"datacodex-notify/internal/api"
	"datacodex-notify/internal/autostart"
	"datacodex-notify/internal/cli"
	"datacodex-notify/internal/config"
	"datacodex-notify/internal/heartbeat"
	"datacodex-notify/internal/locale"
	"datacodex-notify/internal/notify"
	"datacodex-notify/internal/queue"
	"datacodex-notify/internal/rules"
	"datacodex-notify/internal/secrets"
	"datacodex-notify/internal/skillinstall"
	"datacodex-notify/internal/store"
	"datacodex-notify/internal/tray"
	"datacodex-notify/internal/ui"
	"datacodex-notify/internal/voice"
	"datacodex-notify/internal/wa"
)

const (
	exitFlushTimeout = 10 * time.Second
	pruneInterval    = 24 * time.Hour
	releaseInterval  = 30 * time.Second
	maxLogMessages   = 5000
	maxInboxRows     = 2000
	shutdownTimeout  = 5 * time.Second
	settingLanguage  = "language"
)

// Options configures a run. The zero value runs the full tray application.
type Options struct {
	Version string
	// InstanceName overrides the single-instance mutex name (tests only).
	InstanceName string
	// Headless skips the tray icon and the admin window.
	Headless bool
	// OpenWindow replaces the Edge launcher when set (tests only).
	OpenWindow func(url string) error
	// SkillRoots replaces the Claude and Codex skills folders (tests only).
	SkillRoots []string
	// AutostartRegistry replaces the per-user Run key (tests only).
	AutostartRegistry autostart.Registry
	// HeartbeatTick overrides how often missed heartbeats are checked (tests only).
	HeartbeatTick time.Duration
}

type app struct {
	opts       Options
	cfg        *config.Config
	store      *store.Store
	locale     *locale.Manager
	sender     wa.Sender
	notifySvc  *notify.Service
	queue      *queue.Dispatcher
	voice      *voice.Service
	heartbeat  *heartbeat.Service
	autostart  *autostart.Manager
	agents     *agents.Service
	skill      *skillinstall.Manager
	connection *alerts.Connection
	api        *api.Server
	launcher   *ui.Launcher
	tray       *tray.Tray
	cancel     context.CancelFunc
}

// Run starts the service and blocks until the context ends or the user exits.
// When another instance is already running it asks that instance to open its
// window and returns.
func Run(ctx context.Context, opts Options) error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return err
	}

	name := opts.InstanceName
	if name == "" {
		name = defaultInstanceName
	}
	lock, alreadyRunning, err := acquireInstance(name)
	if err != nil {
		return err
	}
	if alreadyRunning {
		return askRunningInstanceToOpen()
	}
	defer lock.release()

	logFile, err := setupLogging(cfg.LogsDir)
	if err != nil {
		return err
	}
	defer logFile.Close()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	a := &app{opts: opts, cfg: cfg, cancel: cancel}
	if err := a.start(ctx); err != nil {
		a.shutdown()
		return err
	}
	defer a.shutdown()

	a.wait(ctx)
	return nil
}

func askRunningInstanceToOpen() error {
	client, _, err := cli.NewClient()
	if err != nil {
		return fmt.Errorf("another instance is running but did not answer: %w", err)
	}
	if _, err := client.Post("/ui/open", nil, nil); err != nil {
		return fmt.Errorf("another instance is running but did not answer: %w", err)
	}
	return nil
}

func (a *app) start(ctx context.Context) error {
	db, err := store.Open(a.cfg.AppDBPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	a.store = db

	a.locale = locale.NewManager("ar")
	if lang, err := db.GetSetting(ctx, settingLanguage); err == nil && lang != "" {
		a.locale.SetLanguage(lang)
	}
	a.prune(ctx)

	credentials := secrets.NewWinCredStore()
	token, err := credentials.EnsureAPIToken()
	if err != nil {
		return fmt.Errorf("failed to prepare the API token: %w", err)
	}

	sender, connect, err := newSender(a.cfg, &eventHandler{store: db})
	if err != nil {
		return fmt.Errorf("failed to create the WhatsApp client: %w", err)
	}
	a.sender = sender

	a.voice = voice.New(db, credentials, voice.Options{
		CacheDir: filepath.Join(a.cfg.DataDir, "voice-cache"),
		TmpDir:   a.cfg.TmpDir,
	})
	if err := a.voice.MigrateLegacyKey(ctx); err != nil {
		slog.Warn("Could not migrate the previous Gemini key", "err", err)
	}

	a.notifySvc = notify.NewService(db, a.locale)
	a.notifySvc.SetVoice(a.voice)
	a.queue = queue.NewDispatcher(db, sender)
	a.queue.SetVoice(a.voice)
	a.heartbeat = heartbeat.New(db, a.notifySvc, a.locale, a.heartbeatDefaults)
	a.autostart = a.newAutostart()
	a.setupAgents()
	a.connection = alerts.NewConnection(alerts.Options{OpenWindow: a.openWindow, WarnDisconnect: a.warnDisconnect})
	if !a.opts.Headless {
		a.tray = a.buildTray()
	}
	a.launcher = ui.NewLauncher(a.cfg.EdgeProfile)

	port, err := config.FindFreePort(config.DefaultPort, config.MaxPort)
	if err != nil {
		return err
	}
	a.api = api.NewServer(api.Config{
		Port:       port,
		APIToken:   token,
		Store:      db,
		NotifySvc:  a.notifySvc,
		WASender:   sender,
		Dispatcher: a.queue,
		Locale:     a.locale,
		Version:    a.opts.Version,
		Voice:      a.voice,
		Heartbeats: a.heartbeat,
		Autostart:  a.autostart,
		Agents:     a.agents,
		Skill:      a.skill,
		Tokens:     credentials,
		CLIPath:    a.cliPath(),
		UIOpener:   a.openWindow,
		StaticFS:   ui.Handler(),
	})
	port, err = a.api.Start()
	if err != nil {
		return err
	}
	if _, err := a.cfg.WriteRuntime(port, a.opts.Version); err != nil {
		return err
	}
	a.queue.Start()

	go func() {
		if err := connect(ctx); err != nil {
			slog.Error("WhatsApp connection failed to start", "err", err)
		}
	}()
	go a.pruneDaily(ctx)
	go a.releaseLoop(ctx)
	go a.heartbeat.Run(ctx, a.heartbeatTick())
	go a.watchState(ctx)
	if a.tray != nil {
		go a.syncTray(ctx)
	}
	slog.Info("DataCodex Notify started", "version", a.opts.Version, "port", port)
	return nil
}

// wait blocks until the application should stop.
func (a *app) wait(ctx context.Context) {
	if a.tray == nil {
		<-ctx.Done()
		return
	}

	go func() {
		<-ctx.Done()
		a.tray.Quit()
	}()
	a.tray.Run(nil)
	a.cancel()
}

func (a *app) openWindow() {
	if a.opts.OpenWindow == nil && a.launcher.IsOpen() {
		a.launcher.Focus()
		return
	}

	token, err := a.api.GenerateLaunchToken()
	if err != nil {
		slog.Error("Cannot open the admin window", "err", err)
		return
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/?launch=%s", a.api.Port(), token)

	open := a.launcher.Open
	if a.opts.OpenWindow != nil {
		open = a.opts.OpenWindow
	}
	if err := open(url); err != nil {
		slog.Error("Failed to open the admin window", "err", err)
	}
}

func (a *app) sendTestMessage() {
	_, err := a.notifySvc.ValidateAndEnqueue(context.Background(), &notify.SendRequest{
		Level:     store.LevelInfo,
		Source:    "tray",
		DedupeKey: uuid.New().String(),
		Text:      a.locale.GetMessage("test_message", nil),
	})
	if err != nil {
		slog.Warn("Test message was not queued", "err", err)
		return
	}
	a.queue.Wake()
}

// requestExit lets queued messages flush for a short time, then stops the app.
func (a *app) requestExit() {
	go func() {
		deadline := time.Now().Add(exitFlushTimeout)
		for time.Now().Before(deadline) {
			queued, err := a.store.HasQueuedMessages(context.Background())
			if err != nil || !queued {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		a.cancel()
	}()
}

func (a *app) prune(ctx context.Context) {
	if err := a.store.Prune(ctx, maxLogMessages, maxInboxRows); err != nil {
		slog.Error("Failed to prune old records", "err", err)
	}
}

func (a *app) pruneDaily(ctx context.Context) {
	ticker := time.NewTicker(pruneInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.prune(ctx)
		}
	}
}

// heartbeatDefaults supplies the interval and grace used for new heartbeats.
func (a *app) heartbeatDefaults(ctx context.Context) (int, int) {
	settings, err := a.notifySvc.RulesSettings(ctx)
	if err != nil {
		return rules.DefaultHeartbeatInterval, rules.DefaultHeartbeatGraceSecs
	}
	return settings.HeartbeatIntervalS, settings.HeartbeatGraceS
}

func (a *app) heartbeatTick() time.Duration {
	if a.opts.HeartbeatTick > 0 {
		return a.opts.HeartbeatTick
	}
	return heartbeat.DefaultTick
}

// releaseLoop lets held messages go when a pause or quiet hours have ended.
func (a *app) releaseLoop(ctx context.Context) {
	ticker := time.NewTicker(releaseInterval)
	defer ticker.Stop()
	for {
		released, err := a.notifySvc.ReleaseHeld(ctx)
		if err != nil {
			slog.Error("Failed to release held messages", "err", err)
		} else if released > 0 {
			a.queue.Wake()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// shutdown stops components in reverse start order. It tolerates a partial start.
func (a *app) shutdown() {
	if a.queue != nil {
		a.queue.Stop()
	}
	if a.api != nil {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		if err := a.api.Stop(ctx); err != nil {
			slog.Warn("API server did not stop cleanly", "err", err)
		}
		cancel()
	}
	if a.sender != nil {
		a.sender.Disconnect()
	}
	if a.store != nil {
		if err := a.store.Close(); err != nil {
			slog.Warn("Failed to close the database", "err", err)
		}
	}
	if err := a.cfg.RemoveRuntime(); err != nil {
		slog.Warn("Failed to remove runtime.json", "err", err)
	}
	slog.Info("DataCodex Notify stopped")
}
