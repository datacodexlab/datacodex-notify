// SPDX-License-Identifier: GPL-3.0-only

package api

import (
	"encoding/json"
	"net/http"

	"datacodex-notify/internal/agentprompt"
)

const settingConnectOptions = "agent_connect_options"

// savedPromptOptions returns the last saved form choices laid over the defaults.
func (s *Server) savedPromptOptions(r *http.Request) (agentprompt.Options, error) {
	options := agentprompt.Defaults()
	raw, err := s.store.GetSetting(r.Context(), settingConnectOptions)
	if err != nil || raw == "" {
		return options, err
	}
	// A damaged saved value falls back to the defaults instead of blocking the tab.
	if json.Unmarshal([]byte(raw), &options) != nil {
		return agentprompt.Defaults(), nil
	}
	if options.Levels == nil {
		options.Levels = map[string]string{}
	}
	if options.Modes == nil {
		options.Modes = map[string]string{}
	}
	return options, nil
}

func (s *Server) promptEnv() agentprompt.Env {
	return agentprompt.Env{CLIPath: s.cliPath}
}

// handleUIPromptOptions serves GET and PUT /ui/agents/prompt-options.
func (s *Server) handleUIPromptOptions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		options, err := s.savedPromptOptions(r)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "Failed to load the saved choices")
			return
		}
		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"options":       options,
			"defaults":      agentprompt.Defaults(),
			"templates":     agentprompt.Templates(),
			"mode_defaults": agentprompt.ModeDefaults(),
			"cli_path":      s.cliPath,
		})

	case http.MethodPut:
		var options agentprompt.Options
		if err := json.NewDecoder(r.Body).Decode(&options); err != nil {
			WriteError(w, http.StatusBadRequest, "Invalid request body")
			return
		}
		if err := options.Validate(); err != nil {
			WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		data, err := json.Marshal(options)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "Failed to encode the choices")
			return
		}
		if err := s.store.SetSetting(r.Context(), settingConnectOptions, string(data)); err != nil {
			WriteError(w, http.StatusInternalServerError, "Failed to save the choices")
			return
		}
		WriteJSON(w, http.StatusOK, map[string]bool{"success": true})

	default:
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleUIPrompt serves POST /ui/agents/prompt: the connect prompt for the given
// choices, and the generic disconnect prompt.
func (s *Server) handleUIPrompt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var options agentprompt.Options
	if err := json.NewDecoder(r.Body).Decode(&options); err != nil {
		WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	connect, err := agentprompt.Connect(options, s.promptEnv())
	if err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]string{
		"connect":            connect,
		"disconnect_generic": agentprompt.DisconnectGeneric(s.promptEnv()),
	})
}

// handleUIDisconnectPrompt serves GET /ui/agents/sessions/{id}/disconnect-prompt.
func (s *Server) handleUIDisconnectPrompt(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	session, err := s.agents.Session(r.Context(), id)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "Failed to read the session")
		return
	}
	if session == nil {
		WriteError(w, http.StatusNotFound, "Session not found")
		return
	}
	prompt := agentprompt.DisconnectSession(agentprompt.SessionRef{
		Name:       session.SessionName,
		Source:     session.Source,
		RulesFiles: session.RulesFiles,
	}, s.promptEnv())
	WriteJSON(w, http.StatusOK, map[string]string{"prompt": prompt})
}
