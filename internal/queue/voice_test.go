// SPDX-License-Identifier: GPL-3.0-only

//go:build devfake

package queue

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"datacodex-notify/internal/audio"
	"datacodex-notify/internal/store"
	"datacodex-notify/internal/wa"
)

type fakeVoice struct {
	clip   *audio.Clip
	err    error
	speech []string
}

func (f *fakeVoice) Prepare(_ context.Context, speech string) (*audio.Clip, error) {
	f.speech = append(f.speech, speech)
	if f.err != nil {
		return nil, f.err
	}
	return f.clip, nil
}

func goodClip() *audio.Clip {
	return &audio.Clip{Ogg: []byte("OggS-fake"), DurationSec: 3, Waveform: make([]byte, audio.WaveformSize)}
}

func enqueueMode(t *testing.T, s *store.Store, recipientID, text, speech string, mode store.Mode) *store.Message {
	t.Helper()
	msg, err := s.CreateMessage(context.Background(), store.LevelWarning, "test", text, speech, mode, "", store.StatusQueued, []string{recipientID})
	if err != nil {
		t.Fatalf("CreateMessage failed: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	return msg
}

func records(sender *flakySender) []wa.FakeRecord {
	return sender.Sender.(*wa.FakeSender).GetRecords()
}

func kinds(recs []wa.FakeRecord) string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.Kind
	}
	return strings.Join(out, ",")
}

func deliveryStatuses(t *testing.T, s *store.Store, messageID string) string {
	t.Helper()
	deliveries, err := s.GetDeliveriesForMessage(context.Background(), messageID)
	if err != nil {
		t.Fatalf("GetDeliveriesForMessage failed: %v", err)
	}
	out := make([]string, len(deliveries))
	for i, d := range deliveries {
		out[i] = d.Kind + ":" + string(d.Status)
	}
	return strings.Join(out, ",")
}

func TestVoiceModeSendsOnlyAVoiceNote(t *testing.T) {
	d, s, sender, _, rid := newQueueFixture(t)
	voice := &fakeVoice{clip: goodClip()}
	d.SetVoice(voice)
	msg := enqueueMode(t, s, rid, "formatted text", "speak this", store.ModeVoice)

	d.processNext()

	recs := records(sender)
	if kinds(recs) != "voice" || recs[0].DurationSec != 3 || recs[0].WaveformLen != audio.WaveformSize {
		t.Fatalf("unexpected sends: %+v", recs)
	}
	if len(voice.speech) != 1 || voice.speech[0] != "speak this" {
		t.Errorf("the speech field must be what is spoken, got %v", voice.speech)
	}
	got, _ := s.GetMessage(context.Background(), msg.ID)
	if got.Status != store.StatusSent || got.LastError != "" || got.Attempts != 1 {
		t.Errorf("unexpected message: %+v", got)
	}
}

func TestBothModeSendsTextBeforeVoice(t *testing.T) {
	d, s, sender, _, rid := newQueueFixture(t)
	d.SetVoice(&fakeVoice{clip: goodClip()})
	msg := enqueueMode(t, s, rid, "formatted text", "speak this", store.ModeBoth)

	d.processNext()

	if got := kinds(records(sender)); got != "text,voice" {
		t.Fatalf("send order = %s, want text,voice", got)
	}
	if got := deliveryStatuses(t, s, msg.ID); got != "text:sent,voice:sent" {
		t.Errorf("deliveries = %s", got)
	}
}

func TestVoiceOnlyFailureFallsBackToText(t *testing.T) {
	d, s, sender, _, rid := newQueueFixture(t)
	d.SetVoice(&fakeVoice{err: errors.New("the Gemini rate limit is reached for every key")})
	msg := enqueueMode(t, s, rid, "formatted text", "speak this", store.ModeVoice)

	d.processNext()

	recs := records(sender)
	if kinds(recs) != "text" || recs[0].Text != "formatted text" {
		t.Fatalf("expected a single text fallback, got %+v", recs)
	}
	got, _ := s.GetMessage(context.Background(), msg.ID)
	if got.Status != store.StatusSent || !strings.HasPrefix(got.LastError, "voice_failed: ") || !strings.Contains(got.LastError, "rate limit") {
		t.Errorf("voice failure must be recorded with its reason: %+v", got)
	}
	if status := deliveryStatuses(t, s, msg.ID); status != "text:sent,voice:voice_failed" {
		t.Errorf("deliveries = %s", status)
	}

	// A later pass has nothing left to send.
	d.processNext()
	if len(records(sender)) != 1 {
		t.Error("nothing should be sent twice")
	}
}

func TestBothModeVoiceFailureDoesNotSendTextTwice(t *testing.T) {
	d, s, sender, _, rid := newQueueFixture(t)
	d.SetVoice(&fakeVoice{err: errors.New("the Gemini API key was rejected")})
	msg := enqueueMode(t, s, rid, "formatted text", "speak this", store.ModeBoth)

	d.processNext()

	if got := kinds(records(sender)); got != "text" {
		t.Fatalf("expected exactly one text, got %s", got)
	}
	if status := deliveryStatuses(t, s, msg.ID); status != "text:sent,voice:voice_failed" {
		t.Errorf("deliveries = %s", status)
	}
	got, _ := s.GetMessage(context.Background(), msg.ID)
	if got.Status != store.StatusSent || !strings.Contains(got.LastError, "voice_failed:") {
		t.Errorf("unexpected message: %+v", got)
	}
}

func TestVoiceWithoutProviderFallsBackToText(t *testing.T) {
	d, s, sender, _, rid := newQueueFixture(t)
	msg := enqueueMode(t, s, rid, "formatted text", "speak this", store.ModeVoice)

	d.processNext()

	if got := kinds(records(sender)); got != "text" {
		t.Fatalf("sends = %s", got)
	}
	got, _ := s.GetMessage(context.Background(), msg.ID)
	if !strings.Contains(got.LastError, "not configured") {
		t.Errorf("last error = %q", got.LastError)
	}
}

func TestVoiceWithoutSpeechTextFallsBackToText(t *testing.T) {
	d, s, sender, _, rid := newQueueFixture(t)
	voice := &fakeVoice{clip: goodClip()}
	d.SetVoice(voice)
	enqueueMode(t, s, rid, "formatted text", "", store.ModeVoice)

	d.processNext()

	if got := kinds(records(sender)); got != "text" || len(voice.speech) != 0 {
		t.Fatalf("sends = %s, voice calls = %v", got, voice.speech)
	}
}

func TestVoiceFallbackTextFailureIsRetried(t *testing.T) {
	d, s, sender, clk, rid := newQueueFixture(t, "formatted text")
	d.SetVoice(&fakeVoice{err: errors.New("no key")})
	msg := enqueueMode(t, s, rid, "formatted text", "speak this", store.ModeVoice)

	d.processNext()

	got, _ := s.GetMessage(context.Background(), msg.ID)
	if got.Status != store.StatusQueued || got.Attempts != 1 || !strings.Contains(got.LastError, "voice_failed:") || !strings.Contains(got.LastError, "simulated send failure") {
		t.Fatalf("unexpected message after a failed fallback: %+v", got)
	}

	delete(sender.failing, "formatted text")
	clk.t = clk.t.Add(time.Minute)
	d.processNext()

	got, _ = s.GetMessage(context.Background(), msg.ID)
	if got.Status != store.StatusSent || !strings.Contains(got.LastError, "voice_failed:") {
		t.Errorf("the voice note should be kept after the retry: %+v", got)
	}
	if kinds(records(sender)) != "text" {
		t.Errorf("sends = %s", kinds(records(sender)))
	}
}

func TestVoiceSendFailureIsRetriedWithoutResendingText(t *testing.T) {
	d, s, sender, clk, rid := newQueueFixture(t)
	voice := &fakeVoice{clip: goodClip()}
	d.SetVoice(voice)
	msg := enqueueMode(t, s, rid, "formatted text", "speak this", store.ModeBoth)

	fake := sender.Sender.(*wa.FakeSender)
	// Text goes out, then the connection drops before the voice upload.
	failVoice := &voiceFailingSender{Sender: sender, remaining: 1}
	d.sender = failVoice

	d.processNext()

	got, _ := s.GetMessage(context.Background(), msg.ID)
	if got.Status != store.StatusQueued || deliveryStatuses(t, s, msg.ID) != "text:sent,voice:failed" {
		t.Fatalf("unexpected state: %+v / %s", got, deliveryStatuses(t, s, msg.ID))
	}

	clk.t = clk.t.Add(time.Minute)
	d.processNext()

	if got := kinds(fake.GetRecords()); got != "text,voice" {
		t.Errorf("text must not be resent: %s", got)
	}
	got, _ = s.GetMessage(context.Background(), msg.ID)
	if got.Status != store.StatusSent || got.Attempts != 2 {
		t.Errorf("unexpected final message: %+v", got)
	}
}

// voiceFailingSender fails the first SendVoice calls and passes everything else through.
type voiceFailingSender struct {
	wa.Sender
	remaining int
}

func (v *voiceFailingSender) SendVoice(ctx context.Context, jid string, ogg []byte, seconds int, waveform []byte) (string, error) {
	if v.remaining > 0 {
		v.remaining--
		return "", errors.New("upload failed")
	}
	return v.Sender.SendVoice(ctx, jid, ogg, seconds, waveform)
}

func TestTextModeNeverTouchesTheVoiceProvider(t *testing.T) {
	d, s, sender, _, rid := newQueueFixture(t)
	voice := &fakeVoice{clip: goodClip()}
	d.SetVoice(voice)
	enqueueMode(t, s, rid, "formatted text", "speak this", store.ModeText)

	d.processNext()

	if kinds(records(sender)) != "text" || len(voice.speech) != 0 {
		t.Errorf("sends = %s, voice calls = %v", kinds(records(sender)), voice.speech)
	}
}
