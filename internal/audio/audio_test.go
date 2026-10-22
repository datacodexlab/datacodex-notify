// SPDX-License-Identifier: GPL-3.0-only

package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// makeWAV builds a mono 16-bit PCM WAV file.
func makeWAV(samples []int16, rate int) []byte {
	var buf bytes.Buffer
	dataLen := len(samples) * 2
	buf.WriteString("RIFF")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(36+dataLen))
	buf.WriteString("WAVEfmt ")
	_ = binary.Write(&buf, binary.LittleEndian, struct {
		Size          uint32
		Format        uint16
		Channels      uint16
		SampleRate    uint32
		ByteRate      uint32
		BlockAlign    uint16
		BitsPerSample uint16
	}{16, 1, 1, uint32(rate), uint32(rate * 2), 2, 16})
	buf.WriteString("data")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(dataLen))
	_ = binary.Write(&buf, binary.LittleEndian, samples)
	return buf.Bytes()
}

func tone(rate int, seconds float64, amplitude float64) []int16 {
	samples := make([]int16, int(float64(rate)*seconds))
	for i := range samples {
		samples[i] = int16(amplitude * math.Sin(2*math.Pi*440*float64(i)/float64(rate)))
	}
	return samples
}

func TestParseWAV(t *testing.T) {
	samples := []int16{1, -2, 3, -4}
	pcm, err := ParseWAV(makeWAV(samples, 24000))
	if err != nil {
		t.Fatalf("ParseWAV failed: %v", err)
	}
	if pcm.SampleRate != 24000 || len(pcm.Samples) != 4 || pcm.Samples[1] != -2 {
		t.Errorf("unexpected PCM: %+v", pcm)
	}
}

func TestParseWAVClampsOversizedDataChunk(t *testing.T) {
	wav := makeWAV([]int16{5, 6, 7}, 24000)
	binary.LittleEndian.PutUint32(wav[40:], 0xFFFFFFFF)
	pcm, err := ParseWAV(wav)
	if err != nil || len(pcm.Samples) != 3 {
		t.Fatalf("got %d samples, err=%v", len(pcm.Samples), err)
	}
}

func TestParseWAVRejectsBadInput(t *testing.T) {
	stereo := makeWAV([]int16{1, 2}, 24000)
	binary.LittleEndian.PutUint16(stereo[22:], 2)
	eightBit := makeWAV([]int16{1, 2}, 24000)
	binary.LittleEndian.PutUint16(eightBit[34:], 8)

	tests := map[string][]byte{
		"empty":        nil,
		"not riff":     []byte("hello world, not audio"),
		"stereo":       stereo,
		"8 bit":        eightBit,
		"no data":      makeWAV(nil, 24000)[:36],
		"truncated":    makeWAV([]int16{1}, 24000)[:20],
		"data first":   append([]byte("RIFF\x04\x00\x00\x00WAVEdata\x00\x00\x00\x00"), 0),
		"missing wave": []byte("RIFF\x04\x00\x00\x00AVI "),
	}
	for name, data := range tests {
		if _, err := ParseWAV(data); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestDurationSecondsRoundsUp(t *testing.T) {
	tests := []struct {
		samples, rate, want int
	}{
		{0, 24000, 1},
		{24000, 24000, 1},
		{24001, 24000, 2},
		{72000, 24000, 3},
		{100, 0, 1},
	}
	for _, tc := range tests {
		got := PCM{SampleRate: tc.rate, Samples: make([]int16, tc.samples)}.DurationSeconds()
		if got != tc.want {
			t.Errorf("%d samples at %d Hz: got %d, want %d", tc.samples, tc.rate, got, tc.want)
		}
	}
}

func TestWaveform(t *testing.T) {
	t.Run("silence", func(t *testing.T) {
		for i, bar := range Waveform(make([]int16, 6400)) {
			if bar != 0 {
				t.Fatalf("bar %d = %d, want 0", i, bar)
			}
		}
	})

	t.Run("constant amplitude maps linearly and clamps", func(t *testing.T) {
		half := make([]int16, 6400)
		for i := range half {
			half[i] = 8000
		}
		for _, bar := range Waveform(half) {
			if bar != 50 {
				t.Fatalf("RMS 8000 should map to 50, got %d", bar)
			}
		}

		loud := make([]int16, 6400)
		for i := range loud {
			loud[i] = 32000
		}
		for _, bar := range Waveform(loud) {
			if bar != 100 {
				t.Fatalf("loud audio should clamp to 100, got %d", bar)
			}
		}
	})

	t.Run("always 64 bars even for tiny input", func(t *testing.T) {
		if got := len(Waveform([]int16{1000})); got != WaveformSize {
			t.Errorf("len = %d, want %d", got, WaveformSize)
		}
		if got := len(Waveform(nil)); got != WaveformSize {
			t.Errorf("len = %d, want %d", got, WaveformSize)
		}
	})

	t.Run("bars follow the loudness over time", func(t *testing.T) {
		samples := append(make([]int16, 3200), tone(24000, 0.2, 20000)[:3200]...)
		bars := Waveform(samples)
		if bars[0] != 0 || bars[WaveformSize-1] == 0 {
			t.Errorf("first=%d last=%d", bars[0], bars[WaveformSize-1])
		}
	})
}

func TestFindFFmpegOrder(t *testing.T) {
	dir := t.TempDir()
	configured := filepath.Join(dir, "custom-ffmpeg.exe")
	if err := os.WriteFile(configured, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	onPath := func(string) (string, error) { return `C:\bin\ffmpeg.exe`, nil }
	noPath := func(string) (string, error) { return "", errors.New("not found") }

	if got, _ := findFFmpeg(configured, onPath, dir); got != configured {
		t.Errorf("configured path should win, got %q", got)
	}
	if got, _ := findFFmpeg(filepath.Join(dir, "missing.exe"), onPath, dir); got != `C:\bin\ffmpeg.exe` {
		t.Errorf("a missing configured path should fall through to PATH, got %q", got)
	}

	wingetBin := filepath.Join(dir, "Microsoft", "WinGet", "Packages", "Gyan.FFmpeg_x", "ffmpeg-8.1-full_build", "bin")
	if err := os.MkdirAll(wingetBin, 0o755); err != nil {
		t.Fatal(err)
	}
	wingetExe := filepath.Join(wingetBin, "ffmpeg.exe")
	if err := os.WriteFile(wingetExe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := findFFmpeg("", noPath, dir); err != nil || got != wingetExe {
		t.Errorf("winget folder: got %q err=%v", got, err)
	}

	if _, err := findFFmpeg("", noPath, t.TempDir()); !errors.Is(err, ErrFFmpegNotFound) {
		t.Errorf("expected ErrFFmpegNotFound, got %v", err)
	}
}

func TestToOpusWithRealFFmpeg(t *testing.T) {
	ffmpeg, err := FindFFmpeg("")
	if err != nil {
		t.Skip("ffmpeg is not installed on this machine; skipping the real conversion test")
	}
	if _, err := exec.LookPath(ffmpeg); err != nil && !isFile(ffmpeg) {
		t.Skip("ffmpeg path is not executable; skipping")
	}

	tmp := t.TempDir()
	wav := makeWAV(tone(24000, 2.5, 12000), 24000)
	clip, err := ToOpus(context.Background(), ffmpeg, wav, tmp, SpeedNormal)
	if err != nil {
		t.Fatalf("ToOpus failed: %v", err)
	}

	if !bytes.HasPrefix(clip.Ogg, []byte("OggS")) {
		t.Fatal("output is not an Ogg container")
	}
	head := bytes.Index(clip.Ogg, []byte("OpusHead"))
	if head < 0 {
		t.Fatal("output has no Opus stream header")
	}
	if channels := clip.Ogg[head+9]; channels != 1 {
		t.Errorf("channels = %d, want mono", channels)
	}
	if clip.DurationSec != 3 {
		t.Errorf("duration = %d, want 3 (2.5 s rounded up)", clip.DurationSec)
	}
	if len(clip.Waveform) != WaveformSize || clip.Waveform[10] == 0 {
		t.Errorf("unexpected waveform: %v", clip.Waveform)
	}

	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Errorf("temporary files were left behind: %d entries", len(entries))
	}
}

func TestToOpusRejectsInvalidInputAndCleansUp(t *testing.T) {
	tmp := t.TempDir()
	if _, err := ToOpus(context.Background(), "ffmpeg-does-not-matter", []byte("not a wav"), tmp, SpeedNormal); err == nil {
		t.Fatal("expected an error for invalid audio")
	}

	wav := makeWAV(tone(24000, 0.2, 5000), 24000)
	if _, err := ToOpus(context.Background(), filepath.Join(tmp, "no-such-ffmpeg.exe"), wav, tmp, SpeedNormal); err == nil {
		t.Fatal("expected an error when ffmpeg cannot run")
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Errorf("temporary files were left behind after a failure: %d entries", len(entries))
	}
}

func TestSpeedArgsCarryTheTempoFilter(t *testing.T) {
	for speed, want := range map[float64]string{SpeedFast: "atempo=1.25", SpeedVeryFast: "atempo=1.5", 2: "atempo=2"} {
		args := speedArgs("in.wav", "out.wav", speed)
		joined := " " + strings.Join(args, " ") + " "
		if !strings.Contains(joined, " -filter:a "+want+" ") {
			t.Errorf("speed %g: args %v must contain -filter:a %s", speed, args, want)
		}
		if args[len(args)-1] != "out.wav" || !strings.Contains(joined, " -i in.wav ") {
			t.Errorf("speed %g: input and output paths missing: %v", speed, args)
		}
	}
	if joined := strings.Join(opusArgs("in.wav", "out.ogg"), " "); strings.Contains(joined, "atempo") || !strings.Contains(joined, "libopus") {
		t.Errorf("the opus step itself never changes the speed: %s", joined)
	}
}

func TestToOpusAndChangeSpeedWithRealFFmpeg(t *testing.T) {
	ffmpeg, err := FindFFmpeg("")
	if err != nil {
		t.Skip("ffmpeg is not installed on this machine; skipping the real speed test")
	}

	const rate, seconds = 24000, 6.0
	wav := makeWAV(tone(rate, seconds, 12000), rate)
	tmp := t.TempDir()

	for _, speed := range []float64{SpeedNormal, SpeedFast, SpeedVeryFast} {
		clip, err := ToOpus(context.Background(), ffmpeg, wav, tmp, speed)
		if err != nil {
			t.Fatalf("speed %g: %v", speed, err)
		}
		want := int(math.Ceil(seconds / speed))
		// The duration comes from the final audio; allow one second for the filter's rounding.
		if clip.DurationSec < want || clip.DurationSec > want+1 {
			t.Errorf("speed %g: duration = %d s, want about %d (input %.0f s / speed)", speed, clip.DurationSec, want, seconds)
		}
		if !bytes.HasPrefix(clip.Ogg, []byte("OggS")) || len(clip.Waveform) != WaveformSize {
			t.Errorf("speed %g: not a valid voice note", speed)
		}
	}

	fast, err := ChangeSpeed(context.Background(), ffmpeg, wav, tmp, SpeedVeryFast)
	if err != nil {
		t.Fatal(err)
	}
	pcm, err := ParseWAV(fast)
	if err != nil {
		t.Fatalf("the sample must stay a valid WAV: %v", err)
	}
	if got := float64(len(pcm.Samples)) / float64(pcm.SampleRate); got < 3.9 || got > 4.2 {
		t.Errorf("sample length = %.2f s, want about 4 s", got)
	}
	if same, _ := ChangeSpeed(context.Background(), ffmpeg, wav, tmp, SpeedNormal); !bytes.Equal(same, wav) {
		t.Error("normal speed returns the audio untouched")
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Errorf("temporary files were left behind: %d entries", len(entries))
	}
}
