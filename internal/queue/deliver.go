// SPDX-License-Identifier: GPL-3.0-only

package queue

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"datacodex-notify/internal/audio"
	"datacodex-notify/internal/store"
)

const voiceNotePrefix = "voice_failed:"

// attemptOutcome collects what happened to the deliveries of one message in one attempt.
type attemptOutcome struct {
	failed    bool
	errs      []string
	voiceNote string
}

func (o *attemptOutcome) fail(reason string) {
	o.failed = true
	o.errs = append(o.errs, reason)
}

// summary is the text stored as the message's last error after a failed attempt.
func (o *attemptOutcome) summary() string {
	parts := o.errs
	if o.voiceNote != "" {
		parts = append([]string{o.voiceNote}, parts...)
	}
	return strings.Join(parts, "; ")
}

// deliveryDone reports whether a delivery needs no further sending.
func deliveryDone(status store.MessageStatus) bool {
	switch status {
	case store.StatusSent, store.StatusDelivered, store.StatusRead, store.StatusVoiceFailed:
		return true
	}
	return false
}

// voiceNoteFrom extracts a "voice_failed: reason" note from an earlier last-error text.
func voiceNoteFrom(lastErr string) string {
	idx := strings.Index(lastErr, voiceNotePrefix)
	if idx < 0 {
		return ""
	}
	note, _, _ := strings.Cut(lastErr[idx:], "; ")
	return note
}

func (d *Dispatcher) deliverText(ctx context.Context, msg *store.Message, del *store.Delivery, recipient *store.Recipient, out *attemptOutcome) {
	wamid, err := d.sender.SendText(ctx, recipient.JID, msg.Text)
	if err != nil {
		out.fail(err.Error())
		d.setDelivery(ctx, del.ID, "", store.StatusFailed)
		return
	}
	d.setDelivery(ctx, del.ID, wamid, store.StatusSent)
}

// deliverVoice sends a voice note. When the note cannot be produced, the delivery
// is marked voice_failed and the text is sent instead unless this recipient already
// has a text delivery for the message.
func (d *Dispatcher) deliverVoice(ctx context.Context, msg *store.Message, del *store.Delivery, recipient *store.Recipient, all []*store.Delivery, out *attemptOutcome) {
	clip, err := d.prepareVoice(ctx, msg)
	if err != nil {
		d.voiceFailed(ctx, msg, del, recipient, all, out, err)
		return
	}

	wamid, err := d.sender.SendVoice(ctx, recipient.JID, clip.Ogg, clip.DurationSec, clip.Waveform)
	if err != nil {
		out.fail(err.Error())
		d.setDelivery(ctx, del.ID, "", store.StatusFailed)
		return
	}
	d.setDelivery(ctx, del.ID, wamid, store.StatusSent)
}

func (d *Dispatcher) voiceFailed(ctx context.Context, msg *store.Message, del *store.Delivery, recipient *store.Recipient, all []*store.Delivery, out *attemptOutcome, cause error) {
	out.voiceNote = fmt.Sprintf("%s %v", voiceNotePrefix, cause)
	slog.Warn("Voice note could not be produced", "id", msg.ID, "err", cause)

	if !hasTextDelivery(all, del.RecipientID) {
		wamid, err := d.sender.SendText(ctx, recipient.JID, msg.Text)
		if err != nil {
			out.fail(err.Error())
			d.setDelivery(ctx, del.ID, "", store.StatusFailed)
			return
		}
		if _, err := d.store.AddDelivery(ctx, msg.ID, del.RecipientID, "text", wamid, store.StatusSent); err != nil {
			slog.Error("Failed to record the text fallback delivery", "id", msg.ID, "err", err)
		}
	}
	d.setDelivery(ctx, del.ID, "", store.StatusVoiceFailed)
}

func (d *Dispatcher) prepareVoice(ctx context.Context, msg *store.Message) (*audio.Clip, error) {
	if d.voice == nil {
		return nil, fmt.Errorf("voice notes are not configured")
	}
	if strings.TrimSpace(msg.Speech) == "" {
		return nil, fmt.Errorf("the message has no speech text")
	}
	return d.voice.Prepare(ctx, msg.Speech)
}

func hasTextDelivery(all []*store.Delivery, recipientID string) bool {
	for _, d := range all {
		if d.RecipientID == recipientID && d.Kind == "text" {
			return true
		}
	}
	return false
}
