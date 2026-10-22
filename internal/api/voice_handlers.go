// SPDX-License-Identifier: GPL-3.0-only

package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"strings"

	"datacodex-notify/internal/notify"
	"datacodex-notify/internal/store"
	"datacodex-notify/internal/tts"
	"datacodex-notify/internal/voice"
)

// VoiceService is what the Voice tab needs. No method returns a key's secret.
type VoiceService interface {
	Status(ctx context.Context) (voice.Status, error)
	ListKeys(ctx context.Context) ([]voice.KeyInfo, error)
	AddKey(ctx context.Context, label, secret string) (voice.KeyInfo, error)
	DeleteKey(ctx context.Context, id string) error
	TestKey(ctx context.Context, id string) (tts.KeyTestResult, error)
	SetVoice(ctx context.Context, voice string) error
	SpokenIntro(ctx context.Context) bool
	SetSpokenIntro(ctx context.Context, enabled bool) error
	SetSpeed(ctx context.Context, name string) error
	SetModes(ctx context.Context, modes map[string]string) error
	SetFFmpegPath(ctx context.Context, path string) error
	ListModels(ctx context.Context) ([]tts.ModelInfo, error)
	SaveModels(ctx context.Context, primary, fallback string) error
	ResetModels(ctx context.Context) error
	Sample(ctx context.Context, text string) ([]byte, error)
}

const (
	voicePath      = "/api/v1/ui/voice"
	voiceKeysPath  = voicePath + "/keys"
	voiceModelPath = voicePath + "/models"
)

// requireVoice wraps a voice handler so it answers 503 when voice is not wired.
func (s *Server) requireVoice(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.voice == nil {
			WriteError(w, http.StatusServiceUnavailable, "Voice is not configured")
			return
		}
		next(w, r)
	}
}

func (s *Server) handleUIVoice(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		status, err := s.voice.Status(r.Context())
		if err != nil {
			writeVoiceError(w, err)
			return
		}
		keys, err := s.voice.ListKeys(r.Context())
		if err != nil {
			writeVoiceError(w, err)
			return
		}
		if keys == nil {
			keys = []voice.KeyInfo{}
		}
		WriteJSON(w, http.StatusOK, map[string]interface{}{"status": status, "keys": keys, "spoken_intro": s.voice.SpokenIntro(r.Context())})

	case http.MethodPut:
		var req struct {
			Voice       *string           `json:"voice"`
			Modes       map[string]string `json:"modes"`
			FFmpegPath  *string           `json:"ffmpeg_path"`
			SpokenIntro *bool             `json:"spoken_intro"`
			Speed       *string           `json:"speed"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "Invalid request body")
			return
		}
		if req.Voice != nil {
			if err := s.voice.SetVoice(r.Context(), *req.Voice); err != nil {
				writeVoiceError(w, err)
				return
			}
		}
		if req.Modes != nil {
			if err := s.voice.SetModes(r.Context(), req.Modes); err != nil {
				writeVoiceError(w, err)
				return
			}
		}
		if req.FFmpegPath != nil {
			if err := s.voice.SetFFmpegPath(r.Context(), *req.FFmpegPath); err != nil {
				writeVoiceError(w, err)
				return
			}
		}
		if req.Speed != nil {
			if err := s.voice.SetSpeed(r.Context(), *req.Speed); err != nil {
				writeVoiceError(w, err)
				return
			}
		}
		if req.SpokenIntro != nil {
			if err := s.voice.SetSpokenIntro(r.Context(), *req.SpokenIntro); err != nil {
				writeVoiceError(w, err)
				return
			}
		}
		WriteJSON(w, http.StatusOK, map[string]bool{"success": true})

	default:
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (s *Server) handleUIVoiceKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var req struct {
		Label string `json:"label"`
		Key   string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	info, err := s.voice.AddKey(r.Context(), req.Label, req.Key)
	if err != nil {
		writeVoiceError(w, err)
		return
	}
	WriteJSON(w, http.StatusCreated, info)
}

// handleUIVoiceKeyItem serves DELETE /ui/voice/keys/{id} and POST /ui/voice/keys/{id}/test.
func (s *Server) handleUIVoiceKeyItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, voiceKeysPath+"/"), "/")
	id, action, hasAction := strings.Cut(rest, "/")
	if id == "" {
		WriteError(w, http.StatusBadRequest, "Missing key ID")
		return
	}

	switch {
	case r.Method == http.MethodDelete && !hasAction:
		if err := s.voice.DeleteKey(r.Context(), id); err != nil {
			writeVoiceError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]bool{"success": true})

	case r.Method == http.MethodPost && action == "test":
		result, err := s.voice.TestKey(r.Context(), id)
		if err != nil {
			writeVoiceError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"valid":        result.Valid,
			"rate_limited": result.RateLimited,
			"message":      result.Message,
		})

	default:
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (s *Server) handleUIVoiceModels(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		models, err := s.voice.ListModels(r.Context())
		if err != nil {
			writeVoiceError(w, err)
			return
		}
		if models == nil {
			models = []tts.ModelInfo{}
		}
		WriteJSON(w, http.StatusOK, map[string]interface{}{"models": models})

	case http.MethodPut:
		var req struct {
			Primary  string `json:"primary"`
			Fallback string `json:"fallback"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "Invalid request body")
			return
		}
		if err := s.voice.SaveModels(r.Context(), req.Primary, req.Fallback); err != nil {
			writeVoiceError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]bool{"success": true})

	case http.MethodDelete:
		if err := s.voice.ResetModels(r.Context()); err != nil {
			writeVoiceError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]bool{"success": true})

	default:
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleUIVoiceSample returns a WAV the page plays to preview the selected voice.
func (s *Server) handleUIVoiceSample(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	wav, err := s.voice.Sample(r.Context(), s.locale.GetMessage("voice_sample", nil))
	if err != nil {
		writeVoiceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "audio/wav")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(wav)
}

// handleUIVoiceTest queues a voice-only test message to the enabled recipients.
func (s *Server) handleUIVoiceTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	status, err := s.voice.Status(r.Context())
	if err != nil {
		writeVoiceError(w, err)
		return
	}
	if !status.Available {
		WriteError(w, http.StatusBadRequest, "Voice notes need at least one Gemini key and ffmpeg")
		return
	}

	msg, err := s.notifySvc.ValidateAndEnqueue(r.Context(), &notify.SendRequest{
		Level:     store.LevelInfo,
		Source:    "admin-ui",
		DedupeKey: uuid.New().String(),
		Text:      s.locale.GetMessage("test_message", nil),
		Speech:    s.locale.GetMessage("voice_sample", nil),
		Mode:      store.ModeVoice,
	})
	if err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.dispatcher != nil {
		s.dispatcher.Wake()
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{"success": true, "id": msg.ID})
}

// writeVoiceError maps a voice failure to an HTTP status. Messages come from the
// voice and speech layers, which never contain a key.
func writeVoiceError(w http.ResponseWriter, err error) {
	var rejected *voice.ModelRejectedError
	var apiErr *tts.Error

	switch {
	case errors.Is(err, voice.ErrDuplicateKey):
		WriteError(w, http.StatusConflict, err.Error())
	case errors.Is(err, voice.ErrKeyNotFound):
		WriteError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, voice.ErrInvalidKeyFormat), errors.Is(err, voice.ErrNoModel), errors.Is(err, voice.ErrSameModel),
		errors.Is(err, voice.ErrInvalidVoice), errors.Is(err, voice.ErrInvalidSpeed), errors.Is(err, voice.ErrInvalidMode), errors.Is(err, voice.ErrFFmpegPath),
		errors.Is(err, tts.ErrNoKeys):
		WriteError(w, http.StatusBadRequest, err.Error())
	case errors.As(err, &rejected):
		WriteError(w, http.StatusUnprocessableEntity, rejected.Message)
	case errors.Is(err, tts.ErrQuotaExhausted):
		WriteError(w, http.StatusTooManyRequests, err.Error())
	case errors.Is(err, tts.ErrInvalidKey), errors.Is(err, tts.ErrModelUnavailable), errors.As(err, &apiErr):
		WriteError(w, http.StatusBadGateway, err.Error())
	default:
		WriteError(w, http.StatusInternalServerError, "Voice request failed")
	}
}
