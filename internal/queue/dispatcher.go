// SPDX-License-Identifier: GPL-3.0-only

package queue

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"datacodex-notify/internal/audio"
	"datacodex-notify/internal/store"
	"datacodex-notify/internal/wa"
)

var BackoffSchedule = []time.Duration{
	5 * time.Second,
	15 * time.Second,
	60 * time.Second,
	5 * time.Minute,
	5 * time.Minute,
	5 * time.Minute,
}

const MaxAttempts = 6

// processTimeout bounds one pass over a message, including voice generation and upload.
const processTimeout = 2 * time.Minute

// VoiceProvider turns speech text into a ready-to-send voice note.
type VoiceProvider interface {
	Prepare(ctx context.Context, speech string) (*audio.Clip, error)
}

type Dispatcher struct {
	store    *store.Store
	sender   wa.Sender
	voice    VoiceProvider
	wakeChan chan struct{}
	stopChan chan struct{}
	wg       sync.WaitGroup
	running  bool
	mu       sync.Mutex
	now      func() time.Time
}

func NewDispatcher(s *store.Store, sender wa.Sender) *Dispatcher {
	return &Dispatcher{
		store:    s,
		sender:   sender,
		wakeChan: make(chan struct{}, 10),
		stopChan: make(chan struct{}),
		now:      time.Now,
	}
}

// SetVoice enables voice deliveries. Call it before Start.
func (d *Dispatcher) SetVoice(v VoiceProvider) {
	d.voice = v
}

func (d *Dispatcher) Start() {
	d.mu.Lock()
	if d.running {
		d.mu.Unlock()
		return
	}
	d.running = true
	d.mu.Unlock()

	d.wg.Add(1)
	go d.loop()
}

func (d *Dispatcher) Stop() {
	d.mu.Lock()
	if !d.running {
		d.mu.Unlock()
		return
	}
	d.running = false
	close(d.stopChan)
	d.mu.Unlock()

	d.wg.Wait()
}

func (d *Dispatcher) Wake() {
	select {
	case d.wakeChan <- struct{}{}:
	default:
	}
}

func (d *Dispatcher) loop() {
	defer d.wg.Done()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.stopChan:
			return
		case <-ticker.C:
			d.processNext()
		case <-d.wakeChan:
			d.processNext()
		}
	}
}

func (d *Dispatcher) processNext() {
	// Only send when connected
	if d.sender.GetState() != wa.StateConnected {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), processTimeout)
	defer cancel()

	msg, err := d.store.GetNextQueuedMessage(ctx, d.now())
	if err != nil {
		slog.Error("Failed to read the next queued message", "err", err)
		return
	}
	if msg == nil {
		return
	}

	deliveries, err := d.store.GetDeliveriesForMessage(ctx, msg.ID)
	if err != nil {
		slog.Error("Failed to load deliveries", "id", msg.ID, "err", err)
		return
	}
	if len(deliveries) == 0 {
		d.setStatus(ctx, msg.ID, store.StatusFailed, "no deliveries found")
		return
	}

	d.setStatus(ctx, msg.ID, store.StatusSending, "")

	out := &attemptOutcome{}
	for _, del := range deliveries {
		if deliveryDone(del.Status) {
			continue
		}

		recipient, err := d.store.GetRecipient(ctx, del.RecipientID)
		if err != nil || recipient == nil || !recipient.Enabled {
			out.fail(fmt.Sprintf("recipient %s not found or disabled", del.RecipientID))
			d.setDelivery(ctx, del.ID, "", store.StatusFailed)
			continue
		}

		if del.Kind == "voice" {
			d.deliverVoice(ctx, msg, del, recipient, deliveries, out)
		} else {
			d.deliverText(ctx, msg, del, recipient, out)
		}
	}

	attempt := msg.Attempts + 1
	switch {
	case !out.failed:
		note := out.voiceNote
		if note == "" {
			note = voiceNoteFrom(msg.LastError)
		}
		d.recordAttempt(ctx, msg.ID, store.StatusSent, note, nil)
		slog.Info("Message delivered successfully", "id", msg.ID)
	case attempt >= MaxAttempts:
		d.recordAttempt(ctx, msg.ID, store.StatusFailed, out.summary(), nil)
		slog.Error("Message failed permanently after max attempts", "id", msg.ID, "err", out.summary())
	default:
		next := d.now().UTC().Add(BackoffSchedule[attempt-1])
		d.recordAttempt(ctx, msg.ID, store.StatusQueued, out.summary(), &next)
		slog.Warn("Message delivery failed, queued for retry", "id", msg.ID, "attempt", attempt, "retry_at", next, "err", out.summary())
	}
}

func (d *Dispatcher) setStatus(ctx context.Context, id string, status store.MessageStatus, lastErr string) {
	if err := d.store.UpdateMessageStatus(ctx, id, status, lastErr); err != nil {
		slog.Error("Failed to update message status", "id", id, "status", status, "err", err)
	}
}

func (d *Dispatcher) setDelivery(ctx context.Context, id, waMessageID string, status store.MessageStatus) {
	if err := d.store.UpdateDeliveryStatus(ctx, id, waMessageID, status); err != nil {
		slog.Error("Failed to update delivery status", "id", id, "status", status, "err", err)
	}
}

func (d *Dispatcher) recordAttempt(ctx context.Context, id string, status store.MessageStatus, lastErr string, next *time.Time) {
	if err := d.store.RecordSendAttempt(ctx, id, status, lastErr, next); err != nil {
		slog.Error("Failed to record send attempt", "id", id, "status", status, "err", err)
	}
}
