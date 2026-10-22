// SPDX-License-Identifier: GPL-3.0-only

// Package audio prepares WhatsApp voice notes: it parses WAV audio, converts it
// to Ogg Opus with ffmpeg, and computes the 64-bar waveform.
package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

const (
	// WaveformSize is the number of bars WhatsApp draws for a voice note.
	WaveformSize = 64

	waveformFullScale = 16000.0
	waveformMax       = 100
)

// PCM is mono 16-bit audio.
type PCM struct {
	SampleRate int
	Samples    []int16
}

// DurationSeconds is the length rounded up to whole seconds, at least one.
func (p PCM) DurationSeconds() int {
	if p.SampleRate <= 0 {
		return 1
	}
	seconds := int(math.Ceil(float64(len(p.Samples)) / float64(p.SampleRate)))
	return max(seconds, 1)
}

// ParseWAV reads a RIFF/WAVE file holding mono 16-bit PCM, the format Gemini
// returns. A data chunk that claims more bytes than the file has is clamped.
func ParseWAV(data []byte) (PCM, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return PCM{}, errors.New("not a RIFF/WAVE file")
	}

	var sampleRate int
	formatSeen := false
	for offset := 12; offset+8 <= len(data); {
		id := string(data[offset : offset+4])
		size := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		body := offset + 8

		switch id {
		case "fmt ":
			if body+16 > len(data) {
				return PCM{}, errors.New("truncated fmt chunk")
			}
			format := binary.LittleEndian.Uint16(data[body:])
			channels := binary.LittleEndian.Uint16(data[body+2:])
			sampleRate = int(binary.LittleEndian.Uint32(data[body+4:]))
			bits := binary.LittleEndian.Uint16(data[body+14:])
			if format != 1 || channels != 1 || bits != 16 || sampleRate <= 0 {
				return PCM{}, fmt.Errorf("unsupported WAV format (tag %d, %d channels, %d bits)", format, channels, bits)
			}
			formatSeen = true
		case "data":
			if !formatSeen {
				return PCM{}, errors.New("data chunk before fmt chunk")
			}
			end := min(body+size, len(data))
			count := (end - body) / 2
			samples := make([]int16, count)
			for i := range samples {
				samples[i] = int16(binary.LittleEndian.Uint16(data[body+2*i:]))
			}
			return PCM{SampleRate: sampleRate, Samples: samples}, nil
		}

		offset = body + size + size%2
	}
	return PCM{}, errors.New("no audio data in WAV file")
}

// Waveform splits the samples into 64 equal parts and returns each part's RMS
// scaled to 0..100 (full scale at 16000), the bars WhatsApp shows for a voice note.
func Waveform(samples []int16) []byte {
	bars := make([]byte, WaveformSize)
	n := len(samples)
	for i := range bars {
		from, to := i*n/WaveformSize, (i+1)*n/WaveformSize
		if to <= from {
			continue
		}
		var sum float64
		for _, s := range samples[from:to] {
			sum += float64(s) * float64(s)
		}
		rms := math.Sqrt(sum / float64(to-from))
		bars[i] = byte(min(waveformMax, int(rms/waveformFullScale*waveformMax)))
	}
	return bars
}
