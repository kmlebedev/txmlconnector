package session

import (
	"context"
	"fmt"
	"time"

	"github.com/kmlebedev/txmlconnector/native/wire"
)

// mainFullThreshold mirrors FUN_18038d7d0: when the packet size (65520) minus
// the received RQ_MAIN length is below 500, the DLL polls again at once.
const mainFullThreshold = 500

// ServerTime is the RQ_TIME reply (FUN_1803b7ca0).
type ServerTime struct {
	// Wall is the server wall-clock time; the DLL logs it as server local time
	// (MSK on the Finam demo server), so it carries no zone of its own.
	Wall time.Time
	// Zone is the 16-byte timezone block that follows. Bytes 0..1 held -180
	// (int16 LE, minutes) on the demo server; the rest is not decoded.
	Zone [16]byte
}

// Time sends RQ_TIME.
func (c *Conn) Time(ctx context.Context) (ServerTime, error) {
	p, err := c.Request(ctx, RqTime, nil)
	if err != nil {
		return ServerTime{}, fmt.Errorf("RQ_TIME: %w", err)
	}
	return parseTime(p)
}

func parseTime(p []byte) (ServerTime, error) {
	r := wire.NewReader(p)
	secs, err := r.Int32()
	if err != nil {
		return ServerTime{}, fmt.Errorf("RQ_TIME: %w", err)
	}
	zone, err := r.Raw(16)
	if err != nil {
		return ServerTime{}, fmt.Errorf("RQ_TIME timezone: %w", err)
	}
	return ServerTime{Wall: time.Unix(int64(uint32(secs)), 0).UTC(), Zone: [16]byte(zone)}, nil
}

// Startup sends RQ_STARTUP and returns the raw reply (markets, boards and
// other dictionaries; not decoded yet).
func (c *Conn) Startup(ctx context.Context) ([]byte, error) {
	p, err := c.Request(ctx, RqStartup, nil)
	if err != nil {
		return nil, fmt.Errorf("RQ_STARTUP: %w", err)
	}
	return p, nil
}

// MainReply is one RQ_MAIN reply: a sequence of tagged event records.
type MainReply struct {
	Flags   byte
	Payload []byte
	// More reports that the reply filled the buffer, so the server likely has
	// more queued events and the caller should poll again without delay.
	More bool
}

// Main sends RQ_MAIN (empty request) and returns the queued events.
func (c *Conn) Main(ctx context.Context) (MainReply, error) {
	f, err := c.exchange(ctx, RqMain, nil, true)
	if err != nil {
		return MainReply{}, fmt.Errorf("RQ_MAIN: %w", err)
	}
	return MainReply{
		Flags:   f.Flags,
		Payload: f.Payload,
		More:    wire.ClientBufferSize+wire.HeaderSize-len(f.Payload) < mainFullThreshold,
	}, nil
}

// Logout sends RQ_LOGOUT and closes the connection. The DLL does not wait for
// a reply (FUN_18038d720) and the demo server sends none.
func (c *Conn) Logout(ctx context.Context) error {
	_, err := c.exchange(ctx, RqLogout, nil, false)
	if cerr := c.Close(); err == nil {
		err = cerr
	}
	return err
}
