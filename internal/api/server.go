// SPDX-License-Identifier: GPL-3.0-only

package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"datacodex-notify/internal/locale"
	"datacodex-notify/internal/notify"
	"datacodex-notify/internal/queue"
	"datacodex-notify/internal/secrets"
	"datacodex-notify/internal/store"
	"datacodex-notify/internal/wa"
)

type Server struct {
	httpServer         *http.Server
	listener           net.Listener
	port               int
	tokenMu            sync.RWMutex
	apiToken           string
	tokens             secrets.Store
	agents             AgentService
	skill              SkillManager
	cliPath            string
	store              *store.Store
	notifySvc          *notify.Service
	waSender           wa.Sender
	dispatcher         *queue.Dispatcher
	locale             *locale.Manager
	rateLimiter        *RateLimiter
	version            string
	voice              VoiceService
	heartbeats         HeartbeatService
	autostart          Autostarter
	uiOpener           func()
	staticFS           http.Handler
	launchMu           sync.Mutex
	currentLaunchToken string
	sessionsMu         sync.RWMutex
	uiSessions         map[string]string // sessionID -> csrfToken
}

type Config struct {
	Port       int
	APIToken   string
	Store      *store.Store
	NotifySvc  *notify.Service
	WASender   wa.Sender
	Dispatcher *queue.Dispatcher
	Locale     *locale.Manager
	Version    string
	Voice      VoiceService
	Heartbeats HeartbeatService
	Autostart  Autostarter
	Agents     AgentService
	Skill      SkillManager
	// CLIPath is the full path of the console CLI, written into generated agent prompts.
	CLIPath string
	// Tokens stores the API token when it is rotated from the admin window.
	Tokens   secrets.Store
	UIOpener func()
	StaticFS http.Handler
}

func NewServer(cfg Config) *Server {
	if cfg.Locale == nil {
		cfg.Locale = locale.GetManager()
	}

	s := &Server{
		port:        cfg.Port,
		apiToken:    cfg.APIToken,
		store:       cfg.Store,
		notifySvc:   cfg.NotifySvc,
		waSender:    cfg.WASender,
		dispatcher:  cfg.Dispatcher,
		locale:      cfg.Locale,
		version:     cfg.Version,
		voice:       cfg.Voice,
		heartbeats:  cfg.Heartbeats,
		autostart:   cfg.Autostart,
		agents:      cfg.Agents,
		skill:       cfg.Skill,
		cliPath:     cfg.CLIPath,
		tokens:      cfg.Tokens,
		uiOpener:    cfg.UIOpener,
		staticFS:    cfg.StaticFS,
		rateLimiter: NewRateLimiter(60, time.Minute),
		uiSessions:  make(map[string]string),
	}

	mux := http.NewServeMux()

	// Agent API routes
	mux.HandleFunc("/api/v1/health", s.handleHealth)
	mux.HandleFunc("/api/v1/status", s.RequireAgentAuth(s.handleStatus))
	mux.HandleFunc("/api/v1/notify", s.RequireAgentAuth(s.handleNotify))
	mux.HandleFunc("/api/v1/ui/open", s.RequireAgentAuth(s.handleUIOpen))
	mux.HandleFunc(agentHeartbeatsPath, s.RequireAgentAuth(s.requireHeartbeats(s.handleAgentHeartbeat)))
	mux.HandleFunc("/api/v1/inbox", s.RequireAgentAuth(s.routeInbox))
	mux.HandleFunc("/api/v1/inbox/", s.RequireAgentAuth(s.routeInbox))

	// UI API routes
	mux.HandleFunc("/api/v1/ui/session", s.handleUISession)
	mux.HandleFunc("/api/v1/ui/state", s.RequireUIAuth(s.handleUIState))
	mux.HandleFunc("/api/v1/ui/qr", s.RequireUIAuth(s.handleUIQR))
	mux.HandleFunc("/api/v1/ui/alerts", s.RequireUIAuth(s.handleUIAlerts))
	mux.HandleFunc("/api/v1/ui/pair", s.RequireUIAuth(s.handleUIPair))
	mux.HandleFunc("/api/v1/ui/recipients", s.RequireUIAuth(s.handleUIRecipients))
	mux.HandleFunc("/api/v1/ui/recipients/", s.RequireUIAuth(s.handleUIRecipientItem))
	mux.HandleFunc("/api/v1/ui/log", s.RequireUIAuth(s.handleUILog))
	mux.HandleFunc("/api/v1/ui/log/", s.RequireUIAuth(s.handleUILogItem))
	mux.HandleFunc("/api/v1/ui/settings", s.RequireUIAuth(s.handleUISettings))
	mux.HandleFunc("/api/v1/ui/test", s.RequireUIAuth(s.handleUITest))
	mux.HandleFunc("/api/v1/ui/unlink", s.RequireUIAuth(s.handleUIUnlink))
	mux.HandleFunc(uiHeartbeatsPath, s.RequireUIAuth(s.requireHeartbeats(s.handleUIHeartbeats)))
	mux.HandleFunc(uiHeartbeatsPath+"/", s.RequireUIAuth(s.requireHeartbeats(s.handleUIHeartbeatItem)))

	// Agent routes (bearer token)
	mux.HandleFunc(agentsPath+"connect", s.RequireAgentAuth(s.requireAgents(s.handleAgentConnect)))
	mux.HandleFunc(agentsPath+"disconnect", s.RequireAgentAuth(s.requireAgents(s.handleAgentDisconnect)))

	// Connect-agent tab routes (UI session)
	mux.HandleFunc(uiAgentsPath, s.RequireUIAuth(s.requireAgents(s.handleUIAgents)))
	mux.HandleFunc(uiAgentsPath+"/prompt-options", s.RequireUIAuth(s.requireAgents(s.handleUIPromptOptions)))
	mux.HandleFunc(uiAgentsPath+"/prompt", s.RequireUIAuth(s.requireAgents(s.handleUIPrompt)))
	mux.HandleFunc(uiAgentsPath+"/notify-connect", s.RequireUIAuth(s.requireAgents(s.handleUIAgentNotifyConnect)))
	mux.HandleFunc(uiAgentsPath+"/mute-all", s.RequireUIAuth(s.requireAgents(s.handleUIAgentMuteAll)))
	mux.HandleFunc(uiAgentsPath+"/sessions/", s.RequireUIAuth(s.requireAgents(s.handleUIAgentSession)))
	mux.HandleFunc(uiAgentsPath+"/rotate-token", s.RequireUIAuth(s.handleUIRotateToken))
	mux.HandleFunc(uiAgentsPath+"/skill", s.RequireUIAuth(s.handleUISkill))

	// Rules tab routes
	mux.HandleFunc("/api/v1/ui/rules", s.RequireUIAuth(s.handleUIRules))
	mux.HandleFunc("/api/v1/ui/pause", s.RequireUIAuth(s.handleUIPause))
	mux.HandleFunc("/api/v1/ui/autostart", s.RequireUIAuth(s.handleUIAutostart))

	// Voice tab routes
	mux.HandleFunc(voicePath, s.RequireUIAuth(s.requireVoice(s.handleUIVoice)))
	mux.HandleFunc(voicePath+"/sample", s.RequireUIAuth(s.requireVoice(s.handleUIVoiceSample)))
	mux.HandleFunc(voicePath+"/test", s.RequireUIAuth(s.requireVoice(s.handleUIVoiceTest)))
	mux.HandleFunc(voiceKeysPath, s.RequireUIAuth(s.requireVoice(s.handleUIVoiceKeys)))
	mux.HandleFunc(voiceKeysPath+"/", s.RequireUIAuth(s.requireVoice(s.handleUIVoiceKeyItem)))
	mux.HandleFunc(voiceModelPath, s.RequireUIAuth(s.requireVoice(s.handleUIVoiceModels)))

	// Embedded Static Web UI
	if s.staticFS != nil {
		mux.Handle("/", s.staticFS)
	}

	// Wrap with max body size limit
	s.httpServer = &http.Server{
		Handler:      s.HostGuard(s.LimitBodySize(mux)),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return s
}

func (s *Server) Start() (int, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", s.port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return 0, fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	s.listener = ln
	s.port = ln.Addr().(*net.TCPAddr).Port

	go func() {
		_ = s.httpServer.Serve(ln)
	}()

	return s.port, nil
}

func (s *Server) Stop(ctx context.Context) error {
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}

func (s *Server) Port() int {
	return s.port
}

func (s *Server) GenerateLaunchToken() (string, error) {
	token, err := secrets.GenerateSecureToken(32)
	if err != nil {
		return "", fmt.Errorf("failed to generate launch token: %w", err)
	}
	s.launchMu.Lock()
	defer s.launchMu.Unlock()
	s.currentLaunchToken = token
	return token, nil
}

func (s *Server) currentToken() string {
	s.tokenMu.RLock()
	defer s.tokenMu.RUnlock()
	return s.apiToken
}

// setToken swaps the agent API token at once; requests with the old one fail from now on.
func (s *Server) setToken(token string) {
	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()
	s.apiToken = token
}
