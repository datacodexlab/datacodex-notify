// SPDX-License-Identifier: GPL-3.0-only

// Package tts turns short speech text into WAV audio with the Gemini speech API.
package tts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Models, voices, and the speaking style live here and nowhere else.
const (
	// DefaultPrimaryModel and DefaultFallbackModel are only defaults: the effective
	// pair comes from the ModelSource (the settings store).
	DefaultPrimaryModel  = "gemini-3.8-flash-lite-tts"
	DefaultFallbackModel = "gemini-3.8-flash-tts"
	// ModelText is used only to test a key with a one-token generation.
	ModelText = "gemini-flash-lite-latest"

	VoiceKore    = "Kore"
	VoiceCharon  = "Charon"
	DefaultVoice = VoiceKore

	StyleEgyptian = "Speak in natural Egyptian Arabic dialect, calm and clear like a podcast host. Pronounce English words and technical terms in English."

	DefaultBaseURL  = "https://generativelanguage.googleapis.com/v1beta"
	requestTimeout  = 30 * time.Second
	maxResponseSize = 16 << 20
)

// Voices lists the selectable voices.
var Voices = []string{VoiceKore, VoiceCharon}

// Key is one Gemini API key. Secret must never be logged or returned to a browser.
type Key struct {
	ID     string
	Secret string
}

// KeySource returns the keys in the order they were added.
type KeySource interface {
	Keys(ctx context.Context) ([]Key, error)
}

// ModelSource supplies the effective primary and fallback model ids; an empty
// fallback means there is none.
type ModelSource interface {
	Models(ctx context.Context) (primary, fallback string)
}

// Config builds a Client. BaseURL, HTTPClient, and Now default to production values.
type Config struct {
	Keys       KeySource
	Models     ModelSource
	BaseURL    string
	HTTPClient *http.Client
	Now        func() time.Time
}

// Client calls the Gemini speech API with ordered key and model fallback.
type Client struct {
	keys       KeySource
	models     ModelSource
	baseURL    string
	httpClient *http.Client
	now        func() time.Time

	mu          sync.Mutex
	cooldowns   map[string]time.Time
	unavailable map[string]string
}

// New creates a client.
func New(cfg Config) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Client{
		keys:        cfg.Keys,
		models:      cfg.Models,
		baseURL:     cfg.BaseURL,
		httpClient:  cfg.HTTPClient,
		now:         cfg.Now,
		cooldowns:   make(map[string]time.Time),
		unavailable: make(map[string]string),
	}
}

// effectiveModels returns the primary model followed by the fallback, if any.
func (c *Client) effectiveModels(ctx context.Context) []string {
	primary, fallback := DefaultPrimaryModel, DefaultFallbackModel
	if c.models != nil {
		primary, fallback = c.models.Models(ctx)
	}
	if fallback == "" || fallback == primary {
		return []string{primary}
	}
	return []string{primary, fallback}
}

// UnavailableModels lists models Google reported as not found, with Google's message.
func (c *Client) UnavailableModels() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]string, len(c.unavailable))
	for model, message := range c.unavailable {
		out[model] = message
	}
	return out
}

// ForgetUnavailableModels clears the not-found marks, for example after the model settings change.
func (c *Client) ForgetUnavailableModels() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unavailable = make(map[string]string)
}

func (c *Client) markUnavailable(model, message string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unavailable[model] = message
}

func (c *Client) isUnavailable(model string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.unavailable[model]
	return ok
}

// NormalizeVoice returns voice when it is selectable, otherwise the default.
func NormalizeVoice(voice string) string {
	for _, v := range Voices {
		if v == voice {
			return voice
		}
	}
	return DefaultVoice
}

// Synthesize returns a complete WAV file for text. Keys are tried in the order
// they were added and, for each key, the primary model before the fallback:
// a rate limit puts that key and model on cooldown and moves to the next model,
// a rejected key moves to the next key, and any other error stops at once.
func (c *Client) Synthesize(ctx context.Context, text, voice string) ([]byte, error) {
	keys, err := c.keys.Keys(ctx)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, ErrNoKeys
	}
	voice = NormalizeVoice(voice)

	models := c.effectiveModels(ctx)
	var (
		sawRateLimit    bool
		retryAt         time.Time
		lastRejected    *Error
		lastUnavailable *Error
	)
	noteRetry := func(until time.Time) {
		sawRateLimit = true
		if retryAt.IsZero() || until.Before(retryAt) {
			retryAt = until
		}
	}

keyLoop:
	for _, key := range keys {
		for _, model := range models {
			if c.isUnavailable(model) {
				continue
			}
			pair := key.ID + ":" + model
			if until, cooling := c.coolingUntil(pair); cooling {
				noteRetry(until)
				continue
			}

			wav, reqErr := c.generateSpeech(ctx, key, model, text, voice)
			if reqErr == nil {
				return wav, nil
			}
			switch reqErr.Kind {
			case KindRateLimit:
				delay := reqErr.RetryAfter
				if delay <= 0 {
					delay = defaultCooldown
				}
				until := c.now().Add(delay)
				c.setCooldown(pair, until)
				noteRetry(until)
			case KindInvalidKey:
				lastRejected = reqErr
				continue keyLoop
			case KindModelUnavailable:
				c.markUnavailable(model, reqErr.Message)
				lastUnavailable = reqErr
			default:
				return nil, reqErr
			}
		}
	}

	if sawRateLimit {
		return nil, &QuotaError{RetryAt: retryAt}
	}
	if lastRejected != nil {
		return nil, errors.Join(ErrInvalidKey, lastRejected)
	}
	if lastUnavailable != nil {
		return nil, errors.Join(ErrModelUnavailable, lastUnavailable)
	}
	return nil, ErrModelUnavailable
}

// NextAvailableAt reports when speech can be requested again when every key and
// model of the given keys is cooling down. It returns the zero time when at least
// one combination is available.
func (c *Client) NextAvailableAt(ctx context.Context, keyIDs []string) time.Time {
	if len(keyIDs) == 0 {
		return time.Time{}
	}
	var earliest time.Time
	for _, id := range keyIDs {
		for _, model := range c.effectiveModels(ctx) {
			until, cooling := c.coolingUntil(id + ":" + model)
			if !cooling {
				return time.Time{}
			}
			if earliest.IsZero() || until.Before(earliest) {
				earliest = until
			}
		}
	}
	return earliest
}

func (c *Client) coolingUntil(pair string) (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	until, ok := c.cooldowns[pair]
	if !ok {
		return time.Time{}, false
	}
	if !until.After(c.now()) {
		delete(c.cooldowns, pair)
		return time.Time{}, false
	}
	return until, true
}

func (c *Client) setCooldown(pair string, until time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cooldowns[pair] = until
}

type speechRequest struct {
	Model            string           `json:"model"`
	Input            []inputItem      `json:"input"`
	ResponseFormat   responseFormat   `json:"response_format"`
	GenerationConfig generationConfig `json:"generation_config"`
}

type inputItem struct {
	Type    string        `json:"type"`
	Content []contentItem `json:"content"`
}

type contentItem struct {
	Type        string       `json:"type"`
	Text        string       `json:"text,omitempty"`
	Data        string       `json:"data,omitempty"`
	Annotations []annotation `json:"annotations,omitempty"`
}

type annotation struct {
	Type  string `json:"type"`
	Style string `json:"style"`
}

type responseFormat struct {
	Type     string `json:"type"`
	MimeType string `json:"mime_type"`
}

type generationConfig struct {
	SpeechConfig []speechVoice `json:"speech_config"`
}

type speechVoice struct {
	Voice string `json:"voice"`
}

type speechResponse struct {
	Steps []struct {
		Type    string        `json:"type"`
		Content []contentItem `json:"content"`
	} `json:"steps"`
}

func (c *Client) generateSpeech(ctx context.Context, key Key, model, text, voice string) ([]byte, *Error) {
	body, err := json.Marshal(speechRequest{
		Model: model,
		Input: []inputItem{{
			Type: "user_input",
			Content: []contentItem{{
				Type:        "text",
				Text:        text,
				Annotations: []annotation{{Type: "speech_metadata", Style: StyleEgyptian}},
			}},
		}},
		ResponseFormat:   responseFormat{Type: "audio", MimeType: "audio/wav"},
		GenerationConfig: generationConfig{SpeechConfig: []speechVoice{{Voice: voice}}},
	})
	if err != nil {
		return nil, &Error{Kind: KindOther, Message: "failed to encode the request"}
	}

	data, apiErr := c.do(ctx, http.MethodPost, key, "interactions", body)
	if apiErr != nil {
		return nil, apiErr
	}
	return decodeSpeech(data)
}

// do sends one request to path under the base URL with a 30 second limit.
func (c *Client) do(ctx context.Context, method string, key Key, path string, body []byte) ([]byte, *Error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	pathPart, rawQuery, _ := strings.Cut(path, "?")
	endpoint, err := url.JoinPath(c.baseURL, pathPart)
	if err != nil {
		return nil, &Error{Kind: KindOther, Message: "invalid TTS base URL"}
	}
	if rawQuery != "" {
		endpoint += "?" + rawQuery
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, &Error{Kind: KindOther, Message: "failed to build the request"}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("x-goog-api-key", key.Secret)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &Error{Kind: KindNetwork, Message: networkMessage(ctx)}
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return nil, &Error{Kind: KindNetwork, Message: "failed to read the TTS response"}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, classify(resp.StatusCode, data, key.Secret)
	}
	return data, nil
}

func networkMessage(ctx context.Context) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "the TTS request timed out"
	}
	return "the TTS request failed"
}

// decodeSpeech extracts the last audio item of the model output and checks it is a WAV file.
func decodeSpeech(data []byte) ([]byte, *Error) {
	var parsed speechResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, &Error{Kind: KindOther, Message: "the TTS response is not valid JSON"}
	}

	encoded := ""
	for _, step := range parsed.Steps {
		if step.Type != "model_output" {
			continue
		}
		for _, item := range step.Content {
			if item.Type == "audio" && item.Data != "" {
				encoded = item.Data
			}
		}
	}
	if encoded == "" {
		return nil, &Error{Kind: KindOther, Message: "the TTS response has no audio"}
	}

	wav, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, &Error{Kind: KindOther, Message: "the TTS audio is not valid base64"}
	}
	if len(wav) < 12 || string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		return nil, &Error{Kind: KindOther, Message: "the TTS audio is not a WAV file"}
	}
	return wav, nil
}
