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
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// Recovery timing, recovered from FUN_180355d50 / FUN_1803d27b0: the first
// attempt is immediate, the pause between attempts is a fixed 1 s (there is no
// exponential growth in the DLL), the whole window is session_timeout and the
// TCP connect timeout is min(remaining window, 10 s). When the window expires
// the DLL raises "Request timeout."; the typed API returns *RecoveryError and
// offers the explicit Client.Reconnect for the new login.
const (
	recoveryRetryPause     = time.Second
	recoveryConnectTimeout = 10 * time.Second
	defaultSessionTTL      = 120 * time.Second
)

// ErrRecoveryTimeout reports that the recovery window (session_timeout) expired:
// the DLL raises "Request timeout." there and leaves the new login to the XML
// client's connect command.
var ErrRecoveryTimeout = errors.New("transaq: session recovery timeout")

// RecoveryError is returned by Run when the link could not be restored within
// session_timeout. Check it with errors.As; it matches both ErrRecoveryTimeout
// (the window expired) and the last recovery error (e.g. session.ErrBye).
type RecoveryError struct {
	SessionTimeout time.Duration
	Err            error
}

func (e *RecoveryError) Error() string {
	return fmt.Sprintf("transaq: connection lost and not recovered within %v: %v", e.SessionTimeout, e.Err)
}

// Unwrap exposes both the timeout sentinel and the last attempt error.
func (e *RecoveryError) Unwrap() []error { return []error{ErrRecoveryTimeout, e.Err} }

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

// isFatalRecoverError reports the recovery errors the DLL treats as fatal at
// once (docs/protocol/connect.md, "Recover unavailable."):
//   - BYE from the server (-0xd);
//   - a wrong RELOGIN or RECOVER reply (the DLL throws "Incorrect server
//     RELOGIN/RECOVER response");
//   - a payload CRC failure only when the damaged frame is the RQ_RECOVER reply
//     (type 0x40, error -0xb).
//
// A SID mismatch (-0xe) and a header CRC are NOT fatal: the DLL keeps retrying
// them until session_timeout and only then raises "Request timeout.".
func isFatalRecoverError(err error) bool {
	if errors.Is(err, session.ErrBye) ||
		errors.Is(err, session.ErrIncorrectRelogin) ||
		errors.Is(err, session.ErrIncorrectRecover) {
		return true
	}
	var crc *wire.PayloadCRCError
	return errors.As(err, &crc) && crc.Frame.Type == session.RqRecover
}

// sessionTimeout is the server session lifetime from the RQ_PWD reply, the
// window the DLL allows for recovering a lost connection. RE-1
// (docs/protocol/connect.md) confirms: 0 or below 0x14 — 20 s, below 300 —
// seconds, otherwise milliseconds.
func (c *Client) sessionTimeout() time.Duration {
	c.mu.Lock()
	info := c.info
	c.mu.Unlock()
	return effectiveSessionTTL(c.cfg.SessionTimeout, info)
}

// effectiveSessionTTL is the session lifetime (the recovery window): the
// <connect> override first, otherwise the RQ_PWD value with the DLL
// normalization (below 0x14 -> 20 s, below 300 -> seconds, otherwise
// milliseconds).
func effectiveSessionTTL(override time.Duration, info *session.Info) time.Duration {
	if override != 0 {
		switch {
		case override < 0 || override < 20*time.Second:
			// The DLL statcard: negative and below 20 s become 20 s.
			return 20 * time.Second
		case override < 300*time.Second:
			return override
		default:
			// From 300 on the value is treated as milliseconds.
			return override / time.Second * time.Millisecond
		}
	}
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

// normalizeRequestTimeout applies the DLL request_timeout rules: 20 s by default,
// and a value not below the session window becomes half of it.
func normalizeRequestTimeout(request, sessionTTL time.Duration) time.Duration {
	if request <= 0 {
		request = session.DefaultRequestTimeout
	}
	if sessionTTL > 0 && request >= sessionTTL {
		request = sessionTTL / 2
	}
	return request
}

// recover restores the session after a transport failure the way the DLL does:
// a fresh TCP connection, RQ_RELOGIN on the default keys, the strict SID check,
// then RQ_RECOVER on the saved session keys. The catalog and the subscriptions
// are not rebuilt (the server keeps the session, so the secids stay valid).
// Attempts start immediately and repeat every second until session_timeout; a
// fatal refusal or the expired window returns a RecoveryError, exactly as the
// DLL gives up (it does not log in again by itself - the XML client issues the
// connect command, which Client.Reconnect mirrors for the typed API).
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
	if lastErr == nil {
		lastErr = errors.New("no attempt within the session timeout")
	}
	return &RecoveryError{SessionTimeout: limit, Err: lastErr}
}

// Reconnect performs the full connect the DLL leaves to the XML client after
// the recovery window expired ("Request timeout."): a new TCP connection, login,
// reload of the dictionaries and the snapshot, a rebuilt catalog and the
// subscriptions restored by model.Symbol. Call it after Run returned a
// RecoveryError; the trade dedup state survives, so replayed trades are dropped.
func (c *Client) Reconnect(ctx context.Context) error {
	return c.relogin(ctx)
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
// explicit Client.Reconnect path.
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
	conn.SetRequestTimeout(normalizeRequestTimeout(c.cfg.RequestTimeout, effectiveSessionTTL(c.cfg.SessionTimeout, info)))
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
		// the fresh one. The previous socket is closed here too, otherwise a
		// failed Reconnect would leak its file descriptor.
		c.mu.Lock()
		if c.conn == conn {
			c.conn = nil
		}
		c.recovering = false
		c.mu.Unlock()
		if old != nil && old != conn {
			old.Close()
		}
		conn.Close()
		return err
	}
	// The new session brings a new client store, positions, overnight flag and
	// sec spec cache.
	c.mu.Lock()
	c.resetAccountsLocked()
	c.secSpecs = map[uint16]records.SecSpec{}
	c.failHistoryParts(errors.New("transaq: connection was recovered"))
	c.history = map[historyKey]*historyCollector{}
	c.orders = map[uint64]*model.Order{}
	c.pendingOrders = nil
	c.orderOrder = nil
	c.mu.Unlock()
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
	if c.catalog == nil {
		c.mu.Unlock()
		return nil
	}
	items := make([]session.SubscribeItem, 0, len(c.subs))
	var specIDs []uint16
	for s, mask := range c.subs {
		// Poll mutates the catalog under c.mu, so resolve under the same lock.
		sec, board, ok := c.catalog.Lookup(s.Board, s.SecCode)
		if !ok {
			continue // the new session does not trade this symbol any more
		}
		items = append(items, session.SubscribeItem{SecID: sec.SecID, Board: board.Num, Mask: mask})
		specIDs = append(specIDs, sec.SecID)
	}
	c.mu.Unlock()
	if len(items) == 0 {
		return nil
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].SecID != items[j].SecID {
			return items[i].SecID < items[j].SecID
		}
		return items[i].Board < items[j].Board
	})
	if err := conn.Subscribe(ctx, items); err != nil {
		return err
	}
	// The session-local secids changed, so the RQ_SEC_SPEC cache of the old
	// connection is stale: refetch it best-effort (the DLL does the same after
	// the resubscribe; a failure must not break the recovery).
	reverseIDs(specIDs)
	if err := c.loadSecSpecs(ctx, specIDs); err != nil {
		return nil
	}
	return nil
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
