package native

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// Recovery timing, recovered from FUN_180355d50 / FUN_1803d27b0: the first
// attempt is immediate, the pause between attempts is a fixed 1 s (there is no
// exponential growth in the DLL), the whole window is session_timeout and the
// TCP connect timeout is min(remaining window, 10 s). When the window expires
// the DLL raises "Request timeout."; the typed API then falls back to a full
// login (the plan's long path).
const (
	recoveryRetryPause     = time.Second
	recoveryConnectTimeout = 10 * time.Second
	defaultSessionTTL      = 120 * time.Second
)

// recoveryError is returned when the link could not be restored in time.
type recoveryError struct {
	SessionTimeout time.Duration
	Err            error
}

func (e *recoveryError) Error() string {
	return fmt.Sprintf("transaq: connection lost and not recovered within %v: %v", e.SessionTimeout, e.Err)
}

func (e *recoveryError) Unwrap() error { return e.Err }

// isTransportError reports whether err means the TCP link is unusable, so the
// recovery loop should try to restore the session. Protocol errors (a bad
// server response) are returned to the caller instead.
func isTransportError(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) || errors.Is(err, session.ErrBye) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

// isFatalRecoverError reports the recovery errors the DLL treats as fatal
// ("Recover unavailable."): BYE, a CRC failure while a recovery frame was
// expected, and another server (SID mismatch). In those cases the short path is
// abandoned at once instead of retrying until the window expires.
func isFatalRecoverError(err error) bool {
	return errors.Is(err, session.ErrBye) ||
		errors.Is(err, session.ErrNotSameServer) ||
		errors.Is(err, wire.ErrHeaderCRC) ||
		errors.Is(err, wire.ErrPayloadCRC)
}

// sessionTimeout is the server session lifetime from the RQ_PWD reply, the
// window the DLL allows for recovering a lost connection. RE-1
// (docs/protocol/connect.md) confirms: 0 or below 0x14 — 20 s, below 300 —
// seconds, otherwise milliseconds.
func (c *Client) sessionTimeout() time.Duration {
	c.mu.Lock()
	info := c.info
	c.mu.Unlock()
	if info == nil {
		return defaultSessionTTL
	}
	switch v := info.SessionTimeout; {
	case v < 0x14:
		return 20 * time.Second
	case v < 300:
		return time.Duration(v) * time.Second
	default:
		return time.Duration(v) * time.Millisecond
	}
}

// recover restores the session after a transport failure the way the DLL does:
// a fresh TCP connection, RQ_RELOGIN on the default keys, the SID check, then
// RQ_RECOVER on the saved session keys. The catalog and the subscriptions are
// not rebuilt (the server keeps the session, so the secids stay valid). Attempts
// repeat every second until session_timeout; a fatal refusal or the expired
// window falls back to a full login (rebuilt catalog and resubscribed symbols).
func (c *Client) recover(ctx context.Context) error {
	limit := c.sessionTimeout()
	deadline := time.Now().Add(limit)
	var lastErr error
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if attempt > 1 {
			if err := sleepCtx(ctx, recoveryRetryPause); err != nil {
				return err
			}
		}
		if time.Now().After(deadline) {
			break
		}
		remaining := time.Until(deadline)
		if remaining > recoveryConnectTimeout {
			remaining = recoveryConnectTimeout
		}
		attemptCtx, cancel := context.WithTimeout(ctx, remaining)
		err := c.shortRecover(attemptCtx)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		if isFatalRecoverError(err) {
			break
		}
	}
	// "Request timeout." in the DLL means the XML client must connect again;
	// the typed API keeps working with a full login instead.
	if err := c.relogin(ctx); err != nil {
		if lastErr != nil {
			return &recoveryError{SessionTimeout: limit, Err: fmt.Errorf("%v (short recovery: %w)", err, lastErr)}
		}
		return &recoveryError{SessionTimeout: limit, Err: err}
	}
	return nil
}

// shortRecover makes one DLL short-recovery attempt on a fresh TCP connection.
func (c *Client) shortRecover(ctx context.Context) error {
	conn := c.connection()
	if conn == nil {
		return errors.New("transaq: session connection is gone")
	}
	nc, err := c.cfg.Dial(ctx, c.cfg.Address)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	if err := conn.Restore(ctx, nc, c.cfg.Login); err != nil {
		nc.Close()
		return err
	}
	return nil
}

// relogin opens a new session with the configured credentials and restores the
// client state on it: catalog, securities callbacks and subscriptions. It is the
// fallback used when the short recovery is rejected or its window expired.
func (c *Client) relogin(ctx context.Context) error {
	nc, err := c.cfg.Dial(ctx, c.cfg.Address)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	conn, info, err := session.Login(ctx, nc, session.Config{
		Login: c.cfg.Login, Password: c.cfg.Password, Client: c.cfg.Client, RequestTimeout: c.cfg.RequestTimeout,
	})
	if err != nil {
		nc.Close()
		return fmt.Errorf("login: %w", err)
	}
	// Publish the new connection before load() so its RQ_MAIN polls use it;
	// concurrent requests fail fast while recovering instead of interleaving
	// with the login sequence.
	c.mu.Lock()
	old := c.conn
	c.conn, c.info, c.recovering = conn, info, true
	c.mu.Unlock()
	fail := func(err error) error {
		// Detach the failed connection so concurrent callers see "not
		// connected" instead of using a dead socket; the next attempt installs
		// the fresh one.
		c.mu.Lock()
		if c.conn == conn {
			c.conn = nil
		}
		c.recovering = false
		c.mu.Unlock()
		conn.Close()
		return err
	}
	// load re-runs the ClientConfig.OnDictionaries/OnSecurities callbacks, so
	// the Connector emits the dictionary and securities callbacks again after a
	// recovery. The DLL behaviour here is not recovered yet (needs the RE-2
	// capture of the recovery scenario); keep it until then.
	if err := c.load(ctx, conn); err != nil {
		return fail(fmt.Errorf("load: %w", err))
	}
	if err := c.resubscribe(ctx, conn); err != nil {
		return fail(fmt.Errorf("resubscribe: %w", err))
	}
	c.mu.Lock()
	c.recovering = false
	c.mu.Unlock()
	if old != nil && old != conn {
		old.Close()
	}
	return nil
}

// resubscribe re-sends the subscription masks of the current session on the
// new connection, resolving every model.Symbol through the rebuilt catalog:
// the session-local secids of the previous connection are not valid any more.
func (c *Client) resubscribe(ctx context.Context, conn *session.Conn) error {
	c.mu.Lock()
	subs := make(map[model.Symbol]byte, len(c.subs))
	for s, mask := range c.subs {
		subs[s] = mask
	}
	catalog := c.catalog
	c.mu.Unlock()
	if len(subs) == 0 || catalog == nil {
		return nil
	}
	items := make([]session.SubscribeItem, 0, len(subs))
	for s, mask := range subs {
		sec, board, ok := catalog.Lookup(s.Board, s.SecCode)
		if !ok {
			continue // the new session does not trade this symbol any more
		}
		items = append(items, session.SubscribeItem{SecID: sec.SecID, Board: board.Num, Mask: mask})
	}
	if len(items) == 0 {
		return nil
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].SecID != items[j].SecID {
			return items[i].SecID < items[j].SecID
		}
		return items[i].Board < items[j].Board
	})
	return conn.Subscribe(ctx, items)
}

// sleepCtx waits for d or until ctx is done.
func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
