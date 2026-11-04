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

	mu    sync.Mutex
	subs  map[[2]uint16]byte // (secid, board) -> mask
	dedup records.TradeDedup
	stats Stats
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
	c := &Client{cfg: cfg, conn: conn, info: info, subs: map[[2]uint16]byte{}}
	if err := c.load(ctx); err != nil {
		conn.Close()
		return nil, err
	}
	return c, nil
}

// load consumes the RQ_STARTUP dictionaries and the initial RQ_MAIN snapshot.
// Events seen while loading are dropped: trades are not expected before the
// subscription (the DLL delivers none either), and other event families only
// start flowing after it; unknown layouts are counted in Stats, not reported.
func (c *Client) load(ctx context.Context) error {
	st, err := c.conn.Time(ctx)
	if err != nil {
		return err
	}
	c.zone = st.Zone
	c.serverTZ, _ = records.TimeZoneName(st.Zone)
	// Bytes 0..1 of the zone block hold the bias in minutes (-180 for MSK),
	// with Windows sign: UTC = local + bias.
	c.bias = time.Duration(int16(uint16(st.Zone[0])|uint16(st.Zone[1])<<8)) * time.Minute
	payload, err := c.conn.Startup(ctx)
	if err != nil {
		return err
	}
	startup, err := records.ParseStartup(payload)
	if err != nil {
		return err
	}
	c.catalog = records.NewCatalog(startup)
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

func (c *Client) Info() *session.Info              { return c.info }
func (c *Client) Catalog() *records.Catalog        { return c.catalog }
func (c *Client) ServerTZ() string                 { return c.serverTZ }
func (c *Client) Conn() *session.Conn              { return c.conn }
func (c *Client) Close() error                     { return c.conn.Close() }
func (c *Client) Logout(ctx context.Context) error { return c.conn.Logout(ctx) }

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
func (c *Client) update(ctx context.Context, symbols []model.Symbol, bit byte, on bool) error {
	c.mu.Lock()
	var items []session.SubscribeItem
	next := map[[2]uint16]byte{}
	for _, s := range symbols {
		sec, board, ok := c.catalog.Lookup(s.Board, s.SecCode)
		if !ok {
			c.mu.Unlock()
			return fmt.Errorf("unknown security %s", s)
		}
		key := [2]uint16{sec.SecID, board.Num}
		mask := c.subs[key]
		if m, ok := next[key]; ok {
			mask = m
		}
		if on {
			mask |= bit
		} else {
			mask &^= bit
		}
		next[key] = mask
	}
	for key, mask := range next {
		items = append(items, session.SubscribeItem{SecID: key[0], Board: key[1], Mask: mask})
	}
	c.mu.Unlock()
	sort.Slice(items, func(i, j int) bool { return items[i].SecID < items[j].SecID })
	if err := c.conn.Subscribe(ctx, items); err != nil {
		return err
	}
	c.mu.Lock()
	for key, mask := range next {
		if mask == 0 {
			delete(c.subs, key)
		} else {
			c.subs[key] = mask
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
	reply, err := c.conn.Main(ctx)
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
// returned to the caller). h runs on the Run goroutine with the client lock
// released, so it may use Client methods. An unknown record ends a batch: its
// model.UnknownRecord is the last event of that batch and polling continues
// with the next RQ_MAIN.
func (c *Client) Run(ctx context.Context, opt RunOptions, h func([]model.Event) error) error {
	delay := opt.RqDelay
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
				return err
			}
			if len(events) > 0 {
				if err := h(events); err != nil {
					return err
				}
			}
			if !more {
				break
			}
		}
	}
}

// WallTime renders the trade time as the server (and the DLL) shows it.
func (c *Client) WallTime(t model.Trade) time.Time { return t.Time.Add(-c.bias) }

func isZeroClient(ci session.ClientInfo) bool {
	return ci.ComputerName == "" && ci.UserName == "" && ci.OS == "" && ci.HardwareID == 0 && len(ci.Addresses) == 0
}
