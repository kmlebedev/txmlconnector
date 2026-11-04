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
)

// Recovery timing: the DLL retries a lost connection with a growing pause and
// gives up after session_timeout. The RQ_RELOGIN/RQ_RECOVER short path needs
// the payload format recovered from the DLL (docs/protocol/connect.md); until
// then every recovery is a full login.
const (
	recoveryFirstDelay = time.Second
	recoveryMaxDelay   = 30 * time.Second
	defaultSessionTTL  = 120 * time.Second
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

// sessionTimeout is the server session lifetime from the RQ_PWD reply, the
// limit the DLL allows for recovering a lost connection. Values below 300 are
// seconds, bigger ones milliseconds (parsePwdReply).
func (c *Client) sessionTimeout() time.Duration {
	c.mu.Lock()
	info := c.info
	c.mu.Unlock()
	if info == nil || info.SessionTimeout <= 0 {
		return defaultSessionTTL
	}
	if v := info.SessionTimeout; v < 300 {
		return time.Duration(v) * time.Second
	} else {
		return time.Duration(v) * time.Millisecond
	}
}

// recover restores the session after a transport failure: it retries with the
// 1, 2, 4 ... 30 s pauses the DLL uses, up to session_timeout. Every attempt is
// a fresh login: dial, RQ_LOGIN/RQ_PWD, reload the dictionaries and the
// snapshot, rebuild the catalog and re-subscribe every model.Symbol that was
// subscribed. The trade dedup state survives, so replayed trades are dropped.
func (c *Client) recover(ctx context.Context) error {
	limit := c.sessionTimeout()
	deadline := time.Now().Add(limit)
	delay := recoveryFirstDelay
	var lastErr error
	for {
		if err := sleepCtx(ctx, delay); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			break
		}
		if err := c.relogin(ctx); err != nil {
			lastErr = err
			if delay *= 2; delay > recoveryMaxDelay {
				delay = recoveryMaxDelay
			}
			continue
		}
		return nil
	}
	if lastErr == nil {
		lastErr = errors.New("no attempt within the session timeout")
	}
	return &recoveryError{SessionTimeout: limit, Err: lastErr}
}

// relogin opens a new session with the configured credentials and restores the
// client state on it: catalog, securities callbacks and subscriptions.
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
		c.mu.Lock()
		c.recovering = false
		c.mu.Unlock()
		conn.Close()
		return err
	}
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
