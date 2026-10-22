// SPDX-License-Identifier: GPL-3.0-only

package audio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

const convertTimeout = 30 * time.Second

// Clip is a voice note ready to upload.
type Clip struct {
	Ogg         []byte
	DurationSec int
	Waveform    []byte
}

// ErrFFmpegNotFound means no usable ffmpeg executable was located.
var ErrFFmpegNotFound = errors.New("ffmpeg not found")

// FindFFmpeg locates ffmpeg: the configured path first, then PATH, then the
// winget package folders under %LOCALAPPDATA%.
func FindFFmpeg(configured string) (string, error) {
	return findFFmpeg(configured, exec.LookPath, os.Getenv("LOCALAPPDATA"))
}

func findFFmpeg(configured string, lookPath func(string) (string, error), localAppData string) (string, error) {
	if configured != "" && isFile(configured) {
		return configured, nil
	}
	if path, err := lookPath("ffmpeg"); err == nil {
		return path, nil
	}
	if localAppData != "" {
		for _, pattern := range []string{
			filepath.Join(localAppData, "Microsoft", "WinGet", "Packages", "*FFmpeg*", "*", "bin", "ffmpeg.exe"),
			filepath.Join(localAppData, "Microsoft", "WinGet", "Packages", "*FFmpeg*", "bin", "ffmpeg.exe"),
		} {
			matches, err := filepath.Glob(pattern)
			if err == nil && len(matches) > 0 {
				return matches[len(matches)-1], nil
			}
		}
	}
	return "", ErrFFmpegNotFound
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// Speaking speeds applied after the speech is synthesized. ffmpeg's atempo filter
// speeds the audio up without raising the pitch.
const (
	SpeedNormal   = 1.0
	SpeedFast     = 1.25
	SpeedVeryFast = 1.5
)

// tempoFilter is the ffmpeg audio filter for a speed factor.
func tempoFilter(speed float64) string {
	return "atempo=" + strconv.FormatFloat(speed, 'f', -1, 64)
}

// speedArgs are the ffmpeg arguments that turn in into a speed-adjusted mono WAV at out.
func speedArgs(in, out string, speed float64) []string {
	return []string{
		"-y", "-hide_banner", "-loglevel", "error",
		"-i", in,
		"-filter:a", tempoFilter(speed),
		"-c:a", "pcm_s16le", "-ac", "1",
		out,
	}
}

// opusArgs are the ffmpeg arguments that turn a WAV into an Ogg Opus voice note.
func opusArgs(in, out string) []string {
	return []string{
		"-y", "-hide_banner", "-loglevel", "error",
		"-i", in,
		"-c:a", "libopus", "-b:a", "32k", "-ac", "1", "-ar", "48000", "-application", "voip",
		out,
	}
}

func runFFmpeg(ctx context.Context, ffmpeg string, args []string) error {
	ctx, cancel := context.WithTimeout(ctx, convertTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	hideWindow(cmd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg failed: %w: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	return nil
}

// changeSpeed writes wav to workDir, speeds it up, and returns the new WAV.
func changeSpeed(ctx context.Context, ffmpeg, workDir string, wav []byte, speed float64) ([]byte, error) {
	in := filepath.Join(workDir, "slow.wav")
	out := filepath.Join(workDir, "fast.wav")
	if err := os.WriteFile(in, wav, 0o600); err != nil {
		return nil, fmt.Errorf("failed to write the input audio: %w", err)
	}
	if err := runFFmpeg(ctx, ffmpeg, speedArgs(in, out, speed)); err != nil {
		return nil, err
	}
	fast, err := os.ReadFile(out)
	if err != nil {
		return nil, fmt.Errorf("failed to read the speed-adjusted audio: %w", err)
	}
	return fast, nil
}

// ChangeSpeed returns wav played at the given speed (1 returns it unchanged).
// Temporary files live in a fresh folder under tmpDir that is removed before returning.
func ChangeSpeed(ctx context.Context, ffmpeg string, wav []byte, tmpDir string, speed float64) ([]byte, error) {
	if speed <= 0 || speed == SpeedNormal {
		return wav, nil
	}
	if _, err := ParseWAV(wav); err != nil {
		return nil, fmt.Errorf("invalid input audio: %w", err)
	}
	workDir, err := os.MkdirTemp(tmpDir, "voice-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create a temporary folder: %w", err)
	}
	defer os.RemoveAll(workDir)
	return changeSpeed(ctx, ffmpeg, workDir, wav, speed)
}

// ToOpus converts a mono WAV file to an Ogg Opus voice note (mono, 48 kHz,
// voice profile) at the given speed and computes its duration and waveform from
// the final, speed-adjusted audio. Temporary files live in a fresh folder under
// tmpDir that is removed before returning.
func ToOpus(ctx context.Context, ffmpeg string, wav []byte, tmpDir string, speed float64) (*Clip, error) {
	if _, err := ParseWAV(wav); err != nil {
		return nil, fmt.Errorf("invalid input audio: %w", err)
	}

	workDir, err := os.MkdirTemp(tmpDir, "voice-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create a temporary folder: %w", err)
	}
	defer os.RemoveAll(workDir)

	final := wav
	if speed > 0 && speed != SpeedNormal {
		if final, err = changeSpeed(ctx, ffmpeg, workDir, wav, speed); err != nil {
			return nil, err
		}
	}
	pcm, err := ParseWAV(final)
	if err != nil {
		return nil, fmt.Errorf("invalid speed-adjusted audio: %w", err)
	}

	in := filepath.Join(workDir, "in.wav")
	out := filepath.Join(workDir, "out.ogg")
	if err := os.WriteFile(in, final, 0o600); err != nil {
		return nil, fmt.Errorf("failed to write the input audio: %w", err)
	}
	if err := runFFmpeg(ctx, ffmpeg, opusArgs(in, out)); err != nil {
		return nil, err
	}

	ogg, err := os.ReadFile(out)
	if err != nil {
		return nil, fmt.Errorf("failed to read the converted audio: %w", err)
	}
	return &Clip{Ogg: ogg, DurationSec: pcm.DurationSeconds(), Waveform: Waveform(pcm.Samples)}, nil
}
