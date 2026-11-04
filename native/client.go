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

// Stats counts what Poll has seen.
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
		var batch []model.Trade
		if batch, more, err = c.Poll(ctx); err != nil {
			return err
		}
		if len(batch) != 0 {
			return errors.New("trades before subscription")
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

// Poll sends one RQ_MAIN and returns the new trades it carried; more reports
// that the reply filled the buffer and the server has queued data. Records
// with an unimplemented layout end decoding of that payload; they are
// counted in Stats.
func (c *Client) Poll(ctx context.Context) (trades []model.Trade, more bool, err error) {
	reply, err := c.conn.Main(ctx)
	if err != nil {
		return nil, false, err
	}
	recs, splitErr := records.Records(reply.Payload)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stats.Polls++
	if splitErr != nil {
		if !errors.Is(splitErr, records.ErrUnknownRecord) {
			return nil, reply.More, splitErr
		}
		c.stats.UnknownRecords++
		var unknown *records.UnknownRecordError
		if errors.As(splitErr, &unknown) {
			c.stats.LastUnknownTag = unknown.Tag
		}
	}
	for _, r := range recs {
		if _, err := c.catalog.ApplyMutations(r); err != nil {
			return trades, reply.More, err
		}
		if r.Tag != records.TagAllTrade {
			continue
		}
		raw, err := records.ParseAllTrade(r.Body)
		if err != nil {
			return trades, reply.More, err
		}
		if !c.dedup.Fresh(raw) {
			c.stats.Duplicates++
			continue
		}
		trades = append(trades, c.catalog.Trade(raw, c.bias))
	}
	c.stats.Trades += int64(len(trades))
	return trades, reply.More, nil
}

// WallTime renders the trade time as the server (and the DLL) shows it.
func (c *Client) WallTime(t model.Trade) time.Time { return t.Time.Add(-c.bias) }

func isZeroClient(ci session.ClientInfo) bool {
	return ci.ComputerName == "" && ci.UserName == "" && ci.OS == "" && ci.HardwareID == 0 && len(ci.Addresses) == 0
}
