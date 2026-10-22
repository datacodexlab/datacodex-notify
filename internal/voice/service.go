// SPDX-License-Identifier: GPL-3.0-only

// Package voice combines the speech client, audio conversion, and cache into
// ready-to-send voice notes, and owns the voice settings and Gemini key list.
package voice

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"datacodex-notify/internal/audio"
	"datacodex-notify/internal/secrets"
	"datacodex-notify/internal/store"
	"datacodex-notify/internal/tts"
)

// Options configures a Service. BaseURL and FindFFmpeg exist for tests.
type Options struct {
	CacheDir   string
	TmpDir     string
	BaseURL    string
	FindFFmpeg func(configured string) (string, error)
}

// Service produces voice notes. Safe for concurrent use.
type Service struct {
	store      *store.Store
	secrets    secrets.Store
	tts        *tts.Client
	cache      *audio.Cache
	tmpDir     string
	findFFmpeg func(configured string) (string, error)

	keyMu sync.Mutex
}

// New creates the service.
func New(st *store.Store, sec secrets.Store, opts Options) *Service {
	s := &Service{
		store:      st,
		secrets:    sec,
		cache:      audio.NewCache(opts.CacheDir, audio.DefaultCacheMaxBytes),
		tmpDir:     opts.TmpDir,
		findFFmpeg: opts.FindFFmpeg,
	}
	if s.findFFmpeg == nil {
		s.findFFmpeg = audio.FindFFmpeg
	}
	s.tts = tts.New(tts.Config{Keys: s, Models: s, BaseURL: opts.BaseURL})
	return s
}

func (s *Service) ffmpeg(ctx context.Context) (string, error) {
	return s.findFFmpeg(s.FFmpegPath(ctx))
}

// Available reports whether voice notes can be produced: at least one key and ffmpeg.
func (s *Service) Available(ctx context.Context) bool {
	keys, err := s.ListKeys(ctx)
	if err != nil || len(keys) == 0 {
		return false
	}
	_, err = s.ffmpeg(ctx)
	return err == nil
}

// Prepare returns the voice note for speech: from the cache when the same text,
// voice, model, and style were produced before, otherwise from Gemini and ffmpeg.
func (s *Service) Prepare(ctx context.Context, speech string) (*audio.Clip, error) {
	voice := s.Voice(ctx)
	primary, _ := s.Models(ctx)
	speed := s.Speed(ctx)
	// The speed is part of the key: the cache holds finished, speed-adjusted notes.
	cacheKey := audio.CacheKey(speech, voice, primary, tts.StyleEgyptian+speedKeySuffix(speed))
	if clip, ok := s.cache.Get(cacheKey); ok {
		return clip, nil
	}

	ffmpeg, err := s.ffmpeg(ctx)
	if err != nil {
		return nil, err
	}
	wav, err := s.tts.Synthesize(ctx, speech, voice)
	if err != nil {
		return nil, err
	}
	clip, err := audio.ToOpus(ctx, ffmpeg, wav, s.tmpDir, speed)
	if err != nil {
		return nil, err
	}
	if err := s.cache.Put(cacheKey, clip); err != nil {
		slog.Warn("Failed to cache a voice note", "err", err)
	}
	return clip, nil
}

// Sample synthesizes text with the selected voice at the selected speed and
// returns the WAV for playback in the UI. When ffmpeg is missing the sample plays at normal speed.
func (s *Service) Sample(ctx context.Context, text string) ([]byte, error) {
	wav, err := s.tts.Synthesize(ctx, text, s.Voice(ctx))
	if err != nil {
		return nil, err
	}
	speed := s.Speed(ctx)
	if speed == audio.SpeedNormal {
		return wav, nil
	}
	ffmpeg, err := s.ffmpeg(ctx)
	if err != nil {
		return wav, nil
	}
	return audio.ChangeSpeed(ctx, ffmpeg, wav, s.tmpDir, speed)
}

// Status is the voice state shown in the Voice tab.
type Status struct {
	KeyCount          int               `json:"key_count"`
	FFmpegFound       bool              `json:"ffmpeg_found"`
	FFmpegPath        string            `json:"ffmpeg_path"`
	FFmpegConfigured  string            `json:"ffmpeg_configured"`
	Available         bool              `json:"available"`
	Voice             string            `json:"voice"`
	Voices            []string          `json:"voices"`
	Models            ModelPair         `json:"models"`
	DefaultPrimary    string            `json:"default_primary"`
	DefaultFallback   string            `json:"default_fallback"`
	UnavailableModels map[string]string `json:"unavailable_models"`
	NextAvailableAt   *time.Time        `json:"next_available_at,omitempty"`
	Modes             map[string]string `json:"modes"`
	Speed             string            `json:"speed"`
	Speeds            []string          `json:"speeds"`
}

// Status gathers the current voice state. It never includes a key.
func (s *Service) Status(ctx context.Context) (Status, error) {
	keys, err := s.ListKeys(ctx)
	if err != nil {
		return Status{}, fmt.Errorf("failed to read the key list: %w", err)
	}
	ids := make([]string, len(keys))
	for i, k := range keys {
		ids[i] = k.ID
	}

	ffmpegPath, ffmpegErr := s.ffmpeg(ctx)
	status := Status{
		KeyCount:          len(keys),
		FFmpegFound:       ffmpegErr == nil,
		FFmpegPath:        ffmpegPath,
		FFmpegConfigured:  s.FFmpegPath(ctx),
		Available:         len(keys) > 0 && ffmpegErr == nil,
		Voice:             s.Voice(ctx),
		Voices:            tts.Voices,
		Models:            s.ModelPair(ctx),
		DefaultPrimary:    tts.DefaultPrimaryModel,
		DefaultFallback:   tts.DefaultFallbackModel,
		UnavailableModels: s.tts.UnavailableModels(),
		Modes:             s.Modes(ctx),
		Speed:             s.SpeedName(ctx),
		Speeds:            SpeedNames(),
	}
	if next := s.tts.NextAvailableAt(ctx, ids); !next.IsZero() {
		status.NextAvailableAt = &next
	}
	return status, nil
}
