// SPDX-License-Identifier: GPL-3.0-only

package wa

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/socket"
	"go.mau.fi/whatsmeow/types/events"
)

type fakeConn struct {
	mu         sync.Mutex
	paired     bool
	connectErr error
	channels   []chan whatsmeow.QRChannelItem
	connects   int
}

func (f *fakeConn) GetQRChannel(ctx context.Context) (<-chan whatsmeow.QRChannelItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan whatsmeow.QRChannelItem, 8)
	f.channels = append(f.channels, ch)
	return ch, nil
}
func (f *fakeConn) Connect() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connects++
	return f.connectErr
}
func (f *fakeConn) Disconnect()  {}
func (f *fakeConn) Paired() bool { return f.paired }
func (f *fakeConn) channel(i int) chan whatsmeow.QRChannelItem {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.channels[i]
}
func (f *fakeConn) channelCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.channels)
}

func newPairingClient(conn *fakeConn, clock *time.Time) *Client {
	c, _ := NewClient("unused", nil)
	c.pairer = conn
	c.runCtx = context.Background()
	c.now = func() time.Time { return *clock }
	return c
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}

func TestPairingTimeoutThenNewCodeOnRequest(t *testing.T) {
	conn := &fakeConn{}
	clock := time.Unix(1000, 0)
	c := newPairingClient(conn, &clock)

	res, err := c.RequestPairing(context.Background())
	if err != nil || res != PairingStarted {
		t.Fatalf("got %v %v", res, err)
	}
	ch := conn.channel(0)
	ch <- whatsmeow.QRChannelItem{Event: "code", Code: "A", Timeout: 60 * time.Second}
	eventually(t, func() bool { return c.GetLatestQR() == "A" })
	if got := c.PairingStatus().CodeExpiresAt; !got.Equal(clock.Add(60 * time.Second)) {
		t.Fatalf("expiry %v", got)
	}

	ch <- whatsmeow.QRChannelTimeout
	close(ch)
	eventually(t, func() bool { return c.PairingStatus().Expired && !c.PairingStatus().Active })
	if c.GetLatestQR() != "" {
		t.Fatal("QR must be cleared after timeout")
	}
	if conn.channelCount() != 1 {
		t.Fatal("no new pairing may start by itself")
	}

	clock = clock.Add(11 * time.Second)
	res, err = c.RequestPairing(context.Background())
	if err != nil || res != PairingStarted || conn.channelCount() != 2 {
		t.Fatalf("got %v %v channels=%d", res, err, conn.channelCount())
	}
	conn.channel(1) <- whatsmeow.QRChannelItem{Event: "code", Code: "B", Timeout: 20 * time.Second}
	eventually(t, func() bool { return c.GetLatestQR() == "B" })
	if c.PairingStatus().Expired {
		t.Fatal("expired flag must clear on a new code")
	}
}

func TestPairingThrottleAndSingleInFlight(t *testing.T) {
	conn := &fakeConn{}
	clock := time.Unix(1000, 0)
	c := newPairingClient(conn, &clock)

	if res, _ := c.RequestPairing(context.Background()); res != PairingStarted {
		t.Fatalf("first: %v", res)
	}
	if res, _ := c.RequestPairing(context.Background()); res != PairingActive {
		t.Fatalf("while active: %v", res)
	}
	conn.channel(0) <- whatsmeow.QRChannelTimeout
	close(conn.channel(0))
	eventually(t, func() bool { return !c.PairingStatus().Active })

	clock = clock.Add(5 * time.Second)
	if res, _ := c.RequestPairing(context.Background()); res != PairingThrottled {
		t.Fatalf("within throttle: %v", res)
	}
	clock = clock.Add(6 * time.Second)
	if res, _ := c.RequestPairing(context.Background()); res != PairingStarted {
		t.Fatalf("after throttle: %v", res)
	}
	if conn.channelCount() != 2 {
		t.Fatalf("channels=%d", conn.channelCount())
	}
}

func TestPairingNotNeededWhenLinked(t *testing.T) {
	conn := &fakeConn{paired: true}
	clock := time.Unix(1000, 0)
	c := newPairingClient(conn, &clock)
	if res, _ := c.RequestPairing(context.Background()); res != PairingNotNeeded {
		t.Fatalf("got %v", res)
	}
	if conn.channelCount() != 0 {
		t.Fatal("must not open a channel")
	}
}

func TestPairingOutdatedOnConnectError(t *testing.T) {
	conn := &fakeConn{connectErr: socket.ErrWithStatusCode{StatusCode: 405}}
	clock := time.Unix(1000, 0)
	c := newPairingClient(conn, &clock)
	res, err := c.RequestPairing(context.Background())
	if err != nil || res != PairingOutdated {
		t.Fatalf("got %v %v", res, err)
	}
	if c.GetState() != StateOutdated {
		t.Fatalf("state %v", c.GetState())
	}
	clock = clock.Add(time.Minute)
	if res, _ := c.RequestPairing(context.Background()); res != PairingOutdated {
		t.Fatalf("later request: %v", res)
	}
	if conn.connects != 1 {
		t.Fatal("must not retry connecting while outdated")
	}
}

func TestOutdatedEventsSetState(t *testing.T) {
	for name, ev := range map[string]interface{}{
		"client outdated": &events.ClientOutdated{},
		"connect 405":     &events.ConnectFailure{Reason: events.ConnectFailureClientOutdated},
	} {
		c, _ := NewClient("unused", nil)
		c.handleEvent(ev)
		if c.GetState() != StateOutdated {
			t.Errorf("%s: state %v", name, c.GetState())
		}
	}
}

func TestOutdatedErrorDetection(t *testing.T) {
	if !isOutdatedError(errors.Join(errors.New("x"), socket.ErrWithStatusCode{StatusCode: 405})) {
		t.Fatal("405 must be detected")
	}
	if isOutdatedError(socket.ErrWithStatusCode{StatusCode: 500}) || isOutdatedError(nil) {
		t.Fatal("other errors must not match")
	}
}
