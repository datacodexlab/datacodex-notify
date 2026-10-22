// SPDX-License-Identifier: GPL-3.0-only

//go:build devfake

package wa

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
)

type FakeRecord struct {
	Timestamp   time.Time `json:"timestamp"`
	Recipient   string    `json:"recipient"`
	Kind        string    `json:"kind"`
	Text        string    `json:"text,omitempty"`
	DurationSec int       `json:"duration_sec,omitempty"`
	WaveformLen int       `json:"waveform_len,omitempty"`
	WAMessageID string    `json:"wa_message_id"`
}

type FakeSender struct {
	mu         sync.RWMutex
	state      ConnectionState
	stateChans []chan ConnectionState
	latestQR   string
	logFile    string
	records    []FakeRecord
	pairing    PairingStatus
	pairCalls  int
	phone      string
}

func NewFakeSender(logFile string) *FakeSender {
	return &FakeSender{
		state:   StateConnected,
		logFile: logFile,
	}
}

func (f *FakeSender) SetState(s ConnectionState) {
	f.mu.Lock()
	f.state = s
	chans := make([]chan ConnectionState, len(f.stateChans))
	copy(chans, f.stateChans)
	f.mu.Unlock()

	for _, ch := range chans {
		select {
		case ch <- s:
		default:
		}
	}
}

func (f *FakeSender) SetLatestQR(qr string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.latestQR = qr
}

func (f *FakeSender) GetState() ConnectionState {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.state
}

func (f *FakeSender) SubscribeState() <-chan ConnectionState {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan ConnectionState, 10)
	f.stateChans = append(f.stateChans, ch)
	return ch
}

func (f *FakeSender) GetLatestQR() string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.latestQR
}

func (f *FakeSender) Disconnect() {
	f.SetState(StateUnlinked)
}

func (f *FakeSender) Logout(ctx context.Context) error {
	f.SetState(StateLoggedOut)
	return nil
}

func (f *FakeSender) IsOnWhatsApp(ctx context.Context, phoneE164 string) (bool, string, error) {
	// Every well-formed number is treated as registered on WhatsApp.
	return true, phoneE164[1:] + "@s.whatsapp.net", nil
}

func (f *FakeSender) SendText(ctx context.Context, recipientJID string, text string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.state != StateConnected {
		return "", fmt.Errorf("fake sender is not connected (state=%s)", f.state)
	}

	wamid := "FAKE-" + uuid.New().String()
	rec := FakeRecord{
		Timestamp:   time.Now().UTC(),
		Recipient:   recipientJID,
		Kind:        "text",
		Text:        text,
		WAMessageID: wamid,
	}
	f.records = append(f.records, rec)

	if f.logFile != "" {
		_ = f.appendLog(rec)
	}
	return wamid, nil
}

func (f *FakeSender) SendVoice(ctx context.Context, recipientJID string, oggBytes []byte, durationSec int, waveform []byte) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.state != StateConnected {
		return "", fmt.Errorf("fake sender is not connected (state=%s)", f.state)
	}

	wamid := "FAKE-VOICE-" + uuid.New().String()
	rec := FakeRecord{
		Timestamp:   time.Now().UTC(),
		Recipient:   recipientJID,
		Kind:        "voice",
		DurationSec: durationSec,
		WaveformLen: len(waveform),
		WAMessageID: wamid,
	}
	f.records = append(f.records, rec)

	if f.logFile != "" {
		_ = f.appendLog(rec)
	}
	return wamid, nil
}

func (f *FakeSender) appendLog(rec FakeRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	fHandle, err := os.OpenFile(f.logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer fHandle.Close()
	_, err = fHandle.WriteString(string(data) + "\n")
	return err
}

func (f *FakeSender) GetRecords() []FakeRecord {
	f.mu.RLock()
	defer f.mu.RUnlock()
	res := make([]FakeRecord, len(f.records))
	copy(res, f.records)
	return res
}

// RequestPairing marks a pairing as started unless one is already active.
func (f *FakeSender) RequestPairing(ctx context.Context) (PairingResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pairCalls++
	if f.pairing.Active {
		return PairingActive, nil
	}
	f.pairing = PairingStatus{Active: true, Started: true}
	return PairingStarted, nil
}

func (f *FakeSender) PairingStatus() PairingStatus {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.pairing
}

// SetPairingStatus overrides the pairing status reported to the UI.
func (f *FakeSender) SetPairingStatus(p PairingStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pairing = p
}

// PairCalls returns how many times RequestPairing was called.
func (f *FakeSender) PairCalls() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.pairCalls
}

// LinkedPhone returns the phone number set with SetLinkedPhone.
func (f *FakeSender) LinkedPhone() string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.phone
}

// SetLinkedPhone sets the digits reported as the linked phone number.
func (f *FakeSender) SetLinkedPhone(digits string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.phone = digits
}
