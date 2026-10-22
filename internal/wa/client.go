// SPDX-License-Identifier: GPL-3.0-only

package wa

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"
)

type EventCallback interface {
	OnDeliveryReceipt(waMessageID string, status string)
	OnInboundMessage(senderJID, waMessageID, text string)
}

type Client struct {
	mu         sync.RWMutex
	client     *whatsmeow.Client
	container  *sqlstore.Container
	state      ConnectionState
	stateChans []chan ConnectionState
	latestQR   string

	// Pairing state, see pairing.go.
	pairer        pairingConn
	runCtx        context.Context
	pairActive    bool
	pairExpired   bool
	codeExpiresAt time.Time
	lastPairStart time.Time
	now           func() time.Time

	callback EventCallback
	logger   waLog.Logger
	dbPath   string
}

func NewClient(dbPath string, callback EventCallback) (*Client, error) {
	c := &Client{
		state:    StateUnlinked,
		callback: callback,
		logger:   newSlogLogger("whatsmeow", slog.LevelWarn),
		dbPath:   dbPath,
		now:      time.Now,
	}
	return c, nil
}

func (c *Client) Start(ctx context.Context) error {
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)", filepath.ToSlash(c.dbPath))
	container, err := sqlstore.New(ctx, "sqlite", dsn, c.logger)
	if err != nil {
		return fmt.Errorf("failed to open whatsmeow sqlstore: %w", err)
	}

	deviceStore, err := container.GetFirstDevice(ctx)
	if err != nil {
		return fmt.Errorf("failed to get first device: %w", err)
	}
	if deviceStore == nil {
		deviceStore = container.NewDevice()
	}

	client := whatsmeow.NewClient(deviceStore, c.logger)
	client.AddEventHandler(c.handleEvent)

	c.mu.Lock()
	c.container = container
	c.client = client
	c.runCtx = ctx
	c.pairer = whatsmeowConn{client: client}
	if client.Store.ID == nil {
		c.state = StateUnlinked
	} else {
		c.state = StateConnecting
	}
	c.mu.Unlock()

	// An unlinked device does not connect until someone asks for a code (RequestPairing),
	// so WhatsApp never sees pairing attempts nobody is waiting for.
	if client.Store.ID == nil {
		return nil
	}

	if err := client.Connect(); err != nil {
		if isOutdatedError(err) {
			c.setOutdated()
			return nil
		}
		return fmt.Errorf("failed to connect to whatsapp: %w", err)
	}
	return nil
}

func (c *Client) GetState() ConnectionState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

func (c *Client) SubscribeState() <-chan ConnectionState {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan ConnectionState, 10)
	c.stateChans = append(c.stateChans, ch)
	return ch
}

// LinkedPhone returns the digits of the linked phone number, or "" when not linked.
func (c *Client) LinkedPhone() string {
	c.mu.RLock()
	cli := c.client
	c.mu.RUnlock()
	if cli == nil || cli.Store == nil || cli.Store.ID == nil {
		return ""
	}
	return cli.Store.ID.User
}

func (c *Client) GetLatestQR() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.latestQR
}

func (c *Client) Disconnect() {
	c.mu.RLock()
	cli, container := c.client, c.container
	c.mu.RUnlock()

	if cli != nil {
		cli.Disconnect()
	}
	if container != nil {
		if err := container.Close(); err != nil {
			slog.Warn("Failed to close whatsmeow store", "err", err)
		}
	}
}

func (c *Client) Logout(ctx context.Context) error {
	c.mu.RLock()
	cli := c.client
	c.mu.RUnlock()

	if cli == nil {
		return nil
	}
	err := cli.Logout(ctx)
	c.setState(StateLoggedOut, true)
	return err
}

func (c *Client) IsOnWhatsApp(ctx context.Context, phoneE164 string) (bool, string, error) {
	c.mu.RLock()
	cli := c.client
	c.mu.RUnlock()

	if cli == nil {
		return false, "", fmt.Errorf("whatsapp client not initialized")
	}

	resp, err := cli.IsOnWhatsApp(ctx, []string{phoneE164})
	if err != nil {
		return false, "", fmt.Errorf("failed to check phone on whatsapp: %w", err)
	}
	if len(resp) == 0 || !resp[0].IsIn {
		return false, "", nil
	}
	return true, resp[0].JID.String(), nil
}

func (c *Client) SendText(ctx context.Context, recipientJID string, text string) (string, error) {
	c.mu.RLock()
	cli := c.client
	st := c.state
	c.mu.RUnlock()

	if cli == nil || st != StateConnected {
		return "", fmt.Errorf("whatsapp client is not connected (current state=%s)", st)
	}

	targetJID, err := types.ParseJID(recipientJID)
	if err != nil {
		return "", fmt.Errorf("invalid recipient JID %s: %w", recipientJID, err)
	}

	resp, err := cli.SendMessage(ctx, targetJID, &waE2E.Message{
		Conversation: proto.String(text),
	})
	if err != nil {
		return "", fmt.Errorf("failed to send text message: %w", err)
	}
	return resp.ID, nil
}

func (c *Client) SendVoice(ctx context.Context, recipientJID string, oggBytes []byte, durationSec int, waveform []byte) (string, error) {
	c.mu.RLock()
	cli := c.client
	st := c.state
	c.mu.RUnlock()

	if cli == nil || st != StateConnected {
		return "", fmt.Errorf("whatsapp client is not connected (current state=%s)", st)
	}

	targetJID, err := types.ParseJID(recipientJID)
	if err != nil {
		return "", fmt.Errorf("invalid recipient JID %s: %w", recipientJID, err)
	}

	uploadResp, err := cli.Upload(ctx, oggBytes, whatsmeow.MediaAudio)
	if err != nil {
		return "", fmt.Errorf("failed to upload audio attachment: %w", err)
	}

	audioMsg := &waE2E.AudioMessage{
		URL:           proto.String(uploadResp.URL),
		DirectPath:    proto.String(uploadResp.DirectPath),
		MediaKey:      uploadResp.MediaKey,
		FileSHA256:    uploadResp.FileSHA256,
		FileEncSHA256: uploadResp.FileEncSHA256,
		FileLength:    proto.Uint64(uploadResp.FileLength),
		Mimetype:      proto.String("audio/ogg; codecs=opus"),
		Seconds:       proto.Uint32(uint32(durationSec)),
		PTT:           proto.Bool(true),
		Waveform:      waveform,
	}

	resp, err := cli.SendMessage(ctx, targetJID, &waE2E.Message{
		AudioMessage: audioMsg,
	})
	if err != nil {
		return "", fmt.Errorf("failed to send voice message: %w", err)
	}
	return resp.ID, nil
}
