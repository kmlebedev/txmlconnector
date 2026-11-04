package native

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/session"
)

// ClientConfig configures a typed session.
type ClientConfig struct {
	Address  string // host:port
	Login    string
	Password string
	// Client is the host description sent at login; DefaultClientInfo if zero.
	Client session.ClientInfo
	// RequestTimeout bounds each exchange; session.DefaultRequestTimeout if 0.
	RequestTimeout time.Duration
	// Language is the <connect> language: "ru" (default) or "en", the trailer
	// byte of RQ_PWD (RE-1: ru=0, en=1). An unknown value fails Dial.
	Language string
	// Milliseconds, UTCTime, AutoPos and MicexRegisters are the XML-layer
	// <connect> options (RE-1). The typed client stores them; the Connector
	// applies them to the rendered callbacks.
	Milliseconds, UTCTime, AutoPos, MicexRegisters bool
	// RqDelay is the default RQ_MAIN poll period of Run when RunOptions does not
	// set one; 100 ms when 0.
	RqDelay time.Duration
	// SessionTimeout overrides the recovery window (the RQ_PWD value when 0);
	// values below 20 s are raised to 20 s, as the DLL does.
	SessionTimeout time.Duration
	// PushULimits and PushPosEquity are the client refresh periods of the
	// limits and positions requests (the DLL timers; used with tasks 20/21).
	PushULimits, PushPosEquity time.Duration
	// Dial opens the TCP connection; net.Dialer if nil.
	Dial func(ctx context.Context, address string) (net.Conn, error)
	// OnDictionaries is called once after RQ_STARTUP is parsed, before the
	// first RQ_MAIN poll, so the session owner can emit <markets>/<boards>/
	// <candlekinds> callbacks in the DLL's order.
	OnDictionaries func(*model.Startup)
	// OnSecurities is called after every RQ_MAIN poll that carried new
	// R_SEC_NEW records, with exactly those records, as the DLL emits
	// <securities> chunks while the snapshot arrives.
	OnSecurities func([]records.SecNew)
}

// Stats counts what decoding has seen.
type Stats struct {
	Polls, Trades, Duplicates, UnknownRecords int64
	// LastUnknownTag is the tag that stopped decoding of a payload.
	LastUnknownTag byte
}

// Client is a native TRANSAQ session with typed market data.
type Client struct {
	cfg      ClientConfig
	conn     *session.Conn
	info     *session.Info
	catalog  *records.Catalog
	zone     [16]byte
	serverTZ string
	bias     time.Duration // UTC = wall + bias

	mu         sync.Mutex
	subs       map[model.Symbol]byte // stable board+seccode -> subscription mask
	dedup      records.TradeDedup
	recovering bool // true while recover replaces the session
	stats      Stats
}

// Dial logs in, loads the dictionaries and the initial snapshot (RQ_TIME,
// RQ_STARTUP, RQ_MAIN until drained) and builds the security catalog.
func Dial(ctx context.Context, cfg ClientConfig) (*Client, error) {
	if cfg.Dial == nil {
		cfg.Dial = func(ctx context.Context, address string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", address)
		}
	}
	if isZeroClient(cfg.Client) {
		cfg.Client = DefaultClientInfo()
	}
	if cfg.Language != "" {
		trailer, err := languageTrailer(cfg.Language)
		if err != nil {
			return nil, err
		}
		cfg.Client.Trailer = trailer
	}
	nc, err := cfg.Dial(ctx, cfg.Address)
	if err != nil {
		return nil, err
	}
	conn, info, err := session.Login(ctx, nc, session.Config{
		Login: cfg.Login, Password: cfg.Password, Client: cfg.Client, RequestTimeout: cfg.RequestTimeout,
	})
	if err != nil {
		nc.Close()
		return nil, err
	}
	c := &Client{cfg: cfg, conn: conn, info: info, subs: map[model.Symbol]byte{}}
	if err := c.load(ctx, conn); err != nil {
		conn.Close()
		return nil, err
	}
	return c, nil
}

// load consumes the RQ_STARTUP dictionaries and the initial RQ_MAIN snapshot.
// Events seen while loading are dropped: trades are not expected before the
// subscription (the DLL delivers none either), and other event families only
// start flowing after it; unknown layouts are counted in Stats, not reported.
func (c *Client) load(ctx context.Context, conn *session.Conn) error {
	st, err := conn.Time(ctx)
	if err != nil {
		return err
	}
	// Bytes 0..1 of the zone block hold the bias in minutes (-180 for MSK),
	// with Windows sign: UTC = local + bias.
	bias := time.Duration(int16(uint16(st.Zone[0])|uint16(st.Zone[1])<<8)) * time.Minute
	serverTZ, _ := records.TimeZoneName(st.Zone)
	c.mu.Lock()
	c.zone, c.serverTZ, c.bias = st.Zone, serverTZ, bias
	c.mu.Unlock()
	payload, err := conn.Startup(ctx)
	if err != nil {
		return err
	}
	startup, err := records.ParseStartup(payload)
	if err != nil {
		return err
	}
	catalog := records.NewCatalog(startup)
	c.mu.Lock()
	c.catalog = catalog
	c.mu.Unlock()
	if c.cfg.OnDictionaries != nil {
		c.cfg.OnDictionaries(startup)
	}
	for more := true; more; {
		events, m, err := c.Poll(ctx)
		if err != nil {
			return err
		}
		more = m
		for _, ev := range events {
			if _, ok := ev.(*model.Trade); ok {
				return errors.New("trades before subscription")
			}
		}
		if c.cfg.OnSecurities != nil {
			if secs := c.catalog.DrainPending(); len(secs) > 0 {
				c.cfg.OnSecurities(secs)
			}
		}
	}
	return nil
}

func (c *Client) Info() *session.Info {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.info
}

func (c *Client) Catalog() *records.Catalog {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.catalog
}

func (c *Client) ServerTZ() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.serverTZ
}

// connection returns the current session connection.
func (c *Client) connection() *session.Conn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn
}

func (c *Client) Conn() *session.Conn { return c.connection() }

func (c *Client) Close() error {
	conn := c.connection()
	if conn == nil {
		return nil
	}
	return conn.Close()
}

func (c *Client) Logout(ctx context.Context) error {
	conn := c.connection()
	if conn == nil {
		return nil
	}
	return conn.Logout(ctx)
}

// Stats returns a snapshot of the counters.
func (c *Client) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats
}

// SubscribeAllTrades adds symbols to the <alltrades> feed.
func (c *Client) SubscribeAllTrades(ctx context.Context, symbols ...model.Symbol) error {
	return c.update(ctx, symbols, session.SubAllTrades, true)
}

// UnsubscribeAllTrades removes symbols from the <alltrades> feed.
func (c *Client) UnsubscribeAllTrades(ctx context.Context, symbols ...model.Symbol) error {
	return c.update(ctx, symbols, session.SubAllTrades, false)
}

// update sends the full new mask of every touched security, as the DLL does.
// Subscriptions are kept per stable model.Symbol, so they survive a reconnect
// that changes the session-local secids; the ids are resolved on every send.
func (c *Client) update(ctx context.Context, symbols []model.Symbol, bit byte, on bool) error {
	c.mu.Lock()
	next := make(map[model.Symbol]byte, len(symbols))
	for _, s := range symbols {
		mask := c.subs[s]
		if m, ok := next[s]; ok {
			mask = m
		}
		if on {
			mask |= bit
		} else {
			mask &^= bit
		}
		next[s] = mask
	}
	if c.recovering {
		c.mu.Unlock()
		return errors.New("transaq: connection is being recovered")
	}
	conn := c.conn
	catalog := c.catalog
	c.mu.Unlock()
	if conn == nil || catalog == nil {
		return errors.New("transaq: client is not connected")
	}
	items := make([]session.SubscribeItem, 0, len(next))
	for s, mask := range next {
		sec, board, ok := catalog.Lookup(s.Board, s.SecCode)
		if !ok {
			return fmt.Errorf("unknown security %s", s)
		}
		items = append(items, session.SubscribeItem{SecID: sec.SecID, Board: board.Num, Mask: mask})
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
	c.mu.Lock()
	for s, mask := range next {
		if mask == 0 {
			delete(c.subs, s)
		} else {
			c.subs[s] = mask
		}
	}
	c.mu.Unlock()
	return nil
}

// Poll sends one RQ_MAIN and returns the events of that reply in wire order;
// more reports that the reply filled the buffer and the server has queued data.
// Records with an unimplemented layout end decoding of that payload: they are
// counted in Stats and reported as one model.UnknownRecord whose Payload is
// the remaining tail (the rest of the payload cannot be split without the
// layout). Poll keeps the client lock only while decoding, so Run can hand the
// events to a callback without the lock held.
func (c *Client) Poll(ctx context.Context) (events []model.Event, more bool, err error) {
	conn := c.connection()
	reply, err := conn.Main(ctx)
	if err != nil {
		return nil, false, err
	}
	recs, splitErr := records.Records(reply.Payload)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stats.Polls++
	var unknown *records.UnknownRecordError
	if splitErr != nil {
		if !errors.Is(splitErr, records.ErrUnknownRecord) {
			return nil, reply.More, splitErr
		}
		errors.As(splitErr, &unknown)
		c.stats.UnknownRecords++
		if unknown != nil {
			c.stats.LastUnknownTag = unknown.Tag
		}
	}
	for _, r := range recs {
		if _, err := c.catalog.ApplyMutations(r); err != nil {
			return events, reply.More, err
		}
		decode := decoders[r.Tag]
		if decode == nil {
			continue
		}
		evs, err := decode(c, r.Body)
		if err != nil {
			return events, reply.More, err
		}
		events = append(events, evs...)
	}
	if unknown != nil {
		events = append(events, model.UnknownRecord{
			Tag:     unknown.Tag,
			Offset:  unknown.Offset,
			Payload: reply.Payload[unknown.Offset:],
		})
	}
	return events, reply.More, nil
}

const (
	defaultRunRqDelay = 100 * time.Millisecond
	minRunRqDelay     = 10 * time.Millisecond
)

// RunOptions configures Run.
type RunOptions struct {
	// RqDelay is the pause between RQ_MAIN poll rounds. Zero means 100ms;
	// values below 10ms are raised to 10ms, as the DLL does.
	RqDelay time.Duration
}

// Run polls RQ_MAIN and hands every reply's events to h in wire order until the
// context is cancelled, an exchange fails or h returns an error (the error is
// returned to the caller). h is called exactly once per RQ_MAIN reply,
// including replies without events (e.g. one that carried only catalog
// mutations); a nil/empty slice means there were no events. h runs on the Run
// goroutine with the client lock released, so it may use Client methods. An
// unknown record ends a batch: its model.UnknownRecord is the last event of
// that batch and polling continues with the next RQ_MAIN.
func (c *Client) Run(ctx context.Context, opt RunOptions, h func([]model.Event) error) error {
	delay := opt.RqDelay
	if delay == 0 {
		delay = c.cfg.RqDelay
	}
	if delay == 0 {
		delay = defaultRunRqDelay
	}
	if delay < minRunRqDelay {
		delay = minRqDelay
	}
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		for {
			events, more, err := c.Poll(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if !isTransportError(err) {
					return err
				}
				// The DLL reports recover="true" right on the loss, before the
				// reconnect attempts.
				if herr := h([]model.Event{model.ServerStatus{Connected: true, Recover: true}}); herr != nil {
					return herr
				}
				if rerr := c.recover(ctx); rerr != nil {
					// DLL: "Request timeout." -> connected="false".
					if herr := h([]model.Event{model.ServerStatus{Err: rerr.Error()}}); herr != nil {
						return herr
					}
					return rerr
				}
				// DLL: a plain connected="true" after "Recover succeeded.".
				if herr := h([]model.Event{model.ServerStatus{Connected: true}}); herr != nil {
					return herr
				}
				break
			}
			if err := h(events); err != nil {
				return err
			}
			if !more {
				break
			}
		}
	}
}

// symbolOf resolves the stable symbol of a decoded record through the current
// catalog; the decoder uses it for the cross-session trade dedup. It runs
// under the client lock held by Poll.
func (c *Client) symbolOf(t model.AllTrade) (model.Symbol, bool) {
	if c.catalog == nil {
		return model.Symbol{}, false
	}
	sec, ok := c.catalog.Security(t.SecID)
	if !ok {
		return model.Symbol{}, false
	}
	board, ok := c.catalog.Board(t.Board)
	if !ok {
		return model.Symbol{}, false
	}
	return model.Symbol{Board: board.Code, SecCode: sec.SecCode}, true
}

// WallTime renders the trade time as the server (and the DLL) shows it.
func (c *Client) WallTime(t model.Trade) time.Time { return t.Time.Add(-c.bias) }

func isZeroClient(ci session.ClientInfo) bool {
	return ci.ComputerName == "" && ci.UserName == "" && ci.OS == "" && ci.HardwareID == 0 && len(ci.Addresses) == 0
}

// languageTrailer maps the <connect> language to the RQ_PWD trailer byte the DLL
// writes (RE-1: "ru" -> 0, "en" -> 1).
func languageTrailer(language string) (byte, error) {
	switch language {
	case "", "ru":
		return 0, nil
	case "en":
		return 1, nil
	default:
		return 0, fmt.Errorf("unknown language %q", language)
	}
}
