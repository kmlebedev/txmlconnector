// Package native is a TRANSAQ XML Connector implemented in Go without the
// vendor DLL. Connector accepts the DLL's XML commands and emits its XML
// messages, so it can stand in for connector.New where a command is supported.
//
// Supported commands: connect, disconnect, server_status,
// get_connector_version, get_securities, and subscribe/unsubscribe for
// <alltrades>. Other commands registered by the DLL answer with an explicit
// "not implemented" error. See docs/protocol/commands.md. Client is the typed
// API without XML.
package native

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"os/user"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kmlebedev/txmlconnector/client/commands"
	"github.com/kmlebedev/txmlconnector/connector"
	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/xmlcompat"
)

// DefaultVersion is reported by get_connector_version: the DLL version whose
// behaviour this package reproduces, marked as the Go implementation.
const DefaultVersion = "6.47.2.26.4-native"

// Messages produced by the DLL dispatcher FUN_1800a5030 and command handlers.
const (
	msgUnknownCommand   = "Unknown command."
	msgNeedsConnection  = "Cannot process this command without connection."
	msgAlreadyConnected = "Соединение уже установлено..."
	msgNotConnected     = "Соединение не установлено..."
	msgConnectFailed    = "Ошибка подключения"
	msgNotImplemented   = "Command is not implemented by the native connector."
	defaultRqDelay      = 100 * time.Millisecond
	minRqDelay          = 10 * time.Millisecond
	logoutTimeout       = 4 * time.Second // FUN_18038d720 passes 4000 ms
	callbackQueueSize   = 256
)

// dllCommands lists command ids registered by FUN_1801774b0 and whether the
// DLL requires an established connection for them.
var dllCommands = map[string]bool{
	"set_csp": false, "connect": false, "authenticate": false, "disconnect": false,
	"gethistorydata": true, "subscribe": true, "unsubscribe": true, "makeordown": true,
	"neworder": true, "moveorder": true, "change_pass": true, "newcondorder": true,
	"newstoporder": true, "newrpsorder": true, "newrepoorder": true, "newmrepoorder": true,
	"cancelorder": true, "cancelstoporder": true, "cancelnegdeal": true,
	"get_forts_positions": true, "get_client_limits": true, "get_markets": true,
	"server_status": true, "get_securities": true, "get_servtime_difference": true,
	"get_portfolio": true, "get_portfolio_mct": true, "get_united_portfolio": true,
	"get_mc_portfolio": true, "get_max_buy_sell_tplus": true, "get_max_buy_sell": true,
	"subscribe_ticks": true, "get_old_news": true, "get_news_body": true,
	"get_server_id": true, "get_connector_version": false, "get_securities_info": true,
	"get_united_equity": true, "get_united_go": true, "get_cln_sec_permissions": true,
	"get_options": true, "get_option_families": true, "get_family_strikes": true,
}

type state int

const (
	stateDisconnected state = iota
	stateConnecting
	stateConnected
)

// Config configures the native connector.
type Config struct {
	// Client is the host description sent at login.
	Client session.ClientInfo
	// Version is reported by get_connector_version; DefaultVersion if empty.
	Version string
	// Dial opens the TCP connection; net.Dialer if nil (tests replace it).
	Dial func(ctx context.Context, address string) (net.Conn, error)
}

// Connector implements connector.Connector over the native protocol.
type Connector struct {
	cfg Config

	cmdMu sync.Mutex // serializes SendCommand like the DLL

	mu             sync.Mutex
	started        bool
	closed         bool
	state          state
	client         *Client
	withMillis     bool // connect <milliseconds>: <time> carries .fff
	withUTC        bool // connect <utc_time>: <time> carries the UTC instant
	autoPos        bool // connect <autopos>: stored, its XML effect is not recovered yet
	micexRegisters bool // connect <micex_registers>: affects <positions> (task 7)
	cancel         context.CancelFunc
	done           chan struct{}

	out     chan message
	outDone chan struct{}
	handler connector.MessageHandler
}

// message is a queued callback; delivered is closed after the handler ran.
type message struct {
	text      string
	delivered chan struct{}
}

var _ connector.Connector = (*Connector)(nil)

// DefaultClientInfo describes this host for RQ_PWD: host name, user name and
// GOOS/GOARCH (the Finam demo server accepts these strings).
func DefaultClientInfo() session.ClientInfo {
	host, _ := os.Hostname()
	ci := session.ClientInfo{ComputerName: host, OS: runtime.GOOS + "/" + runtime.GOARCH + " "}
	if u, err := user.Current(); err == nil {
		ci.UserName = u.Username
	}
	return ci
}

func New(cfg Config) *Connector {
	if isZeroClient(cfg.Client) {
		cfg.Client = DefaultClientInfo()
	}
	if cfg.Version == "" {
		cfg.Version = DefaultVersion
	}
	if cfg.Dial == nil {
		cfg.Dial = func(ctx context.Context, address string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", address)
		}
	}
	return &Connector{cfg: cfg}
}

// Start registers the message handler. Messages are delivered in order on a
// single goroutine, as the DLL delivers them on its callback thread.
func (c *Connector) Start(handler connector.MessageHandler) error {
	if handler == nil {
		return errors.New("message handler is required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		return connector.ErrAlreadyStarted
	}
	c.started = true
	c.handler = handler
	c.out = make(chan message, callbackQueueSize)
	c.outDone = make(chan struct{})
	go func() {
		defer close(c.outDone)
		for m := range c.out {
			c.handler(m.text)
			if m.delivered != nil {
				close(m.delivered)
			}
		}
	}()
	return nil
}

func (c *Connector) emit(text string) {
	c.out <- message{text: text}
}

// emitSync returns after the handler has received text.
func (c *Connector) emitSync(text string) {
	delivered := make(chan struct{})
	c.out <- message{text: text, delivered: delivered}
	<-delivered
}

// SendCommand executes one XML command and returns the <result> document.
func (c *Connector) SendCommand(ctx context.Context, command string) (string, error) {
	c.cmdMu.Lock()
	defer c.cmdMu.Unlock()
	c.mu.Lock()
	started, closed := c.started, c.closed
	c.mu.Unlock()
	if !started || closed {
		return "", connector.ErrNotStarted
	}

	var head struct {
		XMLName xml.Name
		ID      string `xml:"id,attr"`
	}
	if err := xml.Unmarshal([]byte(command), &head); err != nil || head.XMLName.Local != "command" {
		return failure(msgUnknownCommand), nil
	}
	id := strings.TrimSpace(head.ID)
	needsConn, known := dllCommands[id]
	if !known {
		return failure(msgUnknownCommand), nil
	}
	if needsConn && c.currentState() != stateConnected {
		return failure(msgNeedsConnection), nil
	}
	switch id {
	case "connect":
		return c.connect(command)
	case "disconnect":
		return c.disconnect(ctx)
	case "server_status":
		c.emit(c.statusXML(true))
		return success(), nil
	case "get_connector_version":
		c.emit("<connector_version>" + escapeText(c.cfg.Version) + "</connector_version>")
		return success(), nil
	case "get_securities":
		c.mu.Lock()
		client := c.client
		c.mu.Unlock()
		if client == nil {
			return failure(msgNeedsConnection), nil
		}
		c.emit(xmlcompat.SecuritiesDumpXML(client.Catalog().Startup(), client.Catalog().List()))
		return success(), nil
	case "get_server_id":
		c.mu.Lock()
		client := c.client
		c.mu.Unlock()
		if client == nil {
			return failure(msgNeedsConnection), nil
		}
		// DLL FUN_180197ea0: <current_server id="SID"/> from the RQ_PWD cache.
		c.emit(fmt.Sprintf("<current_server id=%q/>", strconv.Itoa(client.ServerID())))
		return success(), nil
	case "get_servtime_difference":
		c.mu.Lock()
		client := c.client
		c.mu.Unlock()
		if client == nil {
			return failure(msgNeedsConnection), nil
		}
		// DLL FUN_180197d20 sends RQ_TIME and reports server minus local time
		// in seconds.
		diff, err := client.ServerTimeDifference(ctx)
		if err != nil {
			return failure(err.Error()), nil
		}
		seconds := int64(math.Round(diff.Seconds()))
		return fmt.Sprintf("<result success=%q diff=%q/>", "true", strconv.FormatInt(seconds, 10)), nil
	case "get_markets":
		c.mu.Lock()
		client := c.client
		c.mu.Unlock()
		if client == nil {
			return failure(msgNeedsConnection), nil
		}
		msg, err := xmlcompat.Marshal(xmlcompat.MarketsXML(client.Catalog().Startup()))
		if err != nil {
			return failure(err.Error()), nil
		}
		c.emit(msg)
		return success(), nil
	case "subscribe", "unsubscribe":
		return c.subscribe(ctx, command, id == "subscribe")
	default:
		return failure(msgNotImplemented), nil
	}
}

func (c *Connector) currentState() state {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

func (c *Connector) connect(command string) (string, error) {
	var req commands.Connect
	if err := xml.Unmarshal([]byte(command), &req); err != nil {
		return failure(err.Error()), nil
	}
	c.mu.Lock()
	if c.state != stateDisconnected {
		c.mu.Unlock()
		return failure(msgAlreadyConnected), nil
	}
	if req.Host == "" || req.Port == "" || req.Login == "" {
		c.mu.Unlock()
		return failure("host, port and login are required"), nil
	}
	switch req.Language {
	case "", "ru", "en":
	default:
		// The DLL statcard of FUN_1801196a0: unknown values fail the command.
		c.mu.Unlock()
		return failure("Unknown language."), nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.state = stateConnecting
	c.cancel = cancel
	c.done = make(chan struct{})
	done := c.done
	c.mu.Unlock()

	// Like the DLL, connect returns at once and reports the outcome with
	// <server_status>.
	go c.run(ctx, done, req)
	return success(), nil
}

// run performs login, initial load and RQ_MAIN polling until ctx is done or
// the connection fails.
func (c *Connector) run(ctx context.Context, done chan struct{}, req commands.Connect) {
	defer close(done)
	timeout := session.DefaultRequestTimeout
	if req.RequestTimeout > 0 {
		timeout = time.Duration(req.RequestTimeout) * time.Second
	}
	delay := defaultRqDelay
	if req.Rqdelay > 0 {
		delay = max(time.Duration(req.Rqdelay)*time.Millisecond, minRqDelay)
	}

	client, err := c.open(ctx, req, timeout)
	if err != nil {
		c.finish(ctx, nil, func() string { return connectErrorXML(err) })
		return
	}
	// Run polls RQ_MAIN and returns its events in wire order; the connector
	// renders them as the DLL callbacks and keeps the securities chunks, which
	// the catalog queues while polling.
	reportedDown := false
	err = client.Run(ctx, RunOptions{RqDelay: delay}, func(events []model.Event) error {
		var trades []model.Trade
		for _, ev := range events {
			switch e := ev.(type) {
			case *model.Trade:
				trades = append(trades, *e)
			case model.ServerStatus:
				switch {
				case e.Recover:
					c.emit(c.recoveryStatusXML())
				case e.Connected:
					c.emit(c.statusXML(true))
				default:
					c.emit(c.statusXML(false))
					reportedDown = true
				}
			}
		}
		if len(trades) > 0 {
			c.emit(xmlcompat.AllTradesXMLOptions(trades, xmlcompat.Options{
				Milliseconds: c.withMillis,
				UTCTime:      c.withUTC,
			}))
		}
		if secs := client.Catalog().DrainPending(); len(secs) > 0 {
			st := client.Catalog().Startup()
			c.emit(xmlcompat.SecuritiesXML(st, secs))
			c.emit(xmlcompat.PitsXML(st, secs))
		}
		return nil
	})
	if ctx.Err() != nil {
		return // disconnect cancelled the run; it owns the RQ_LOGOUT
	}
	if reportedDown {
		// The client already reported the give-up (DLL order: recover status on
		// the loss, connected="false" when the recovery window expires).
		c.finish(ctx, client, nil)
		return
	}
	c.finish(ctx, client, func() string { return c.statusLocked(false) })
}

// open logs in, loads dictionaries and the snapshot, then reports
// connected="true" (the DLL reports it after the snapshot). Callbacks follow
// the DLL order: <markets>, <boards>, <candlekinds>, then one <securities>
// chunk per RQ_MAIN poll that carried new instruments, then <pits>.
func (c *Connector) open(ctx context.Context, req commands.Connect, timeout time.Duration) (*Client, error) {
	var (
		st   *model.Startup
		mErr error
	)
	client, err := Dial(ctx, ClientConfig{
		Address: net.JoinHostPort(req.Host, req.Port), Login: req.Login, Password: req.Password,
		Client: c.cfg.Client, RequestTimeout: timeout, Dial: c.cfg.Dial,
		Language: req.Language, Milliseconds: req.Milliseconds, UTCTime: req.UtcTime,
		AutoPos: req.Autopos, MicexRegisters: req.MicexRegisters,
		RqDelay:        time.Duration(req.Rqdelay) * time.Millisecond,
		SessionTimeout: time.Duration(req.SessionTimeout) * time.Second,
		PushULimits:    time.Duration(req.PushUlimits) * time.Second,
		PushPosEquity:  time.Duration(req.PushPosEquity) * time.Second,
		OnDictionaries: func(s *model.Startup) {
			st = s
			for _, v := range []any{xmlcompat.MarketsXML(s), xmlcompat.BoardsXML(s), xmlcompat.CandleKindsXML(s)} {
				if mErr != nil {
					return
				}
				m, err := xmlcompat.Marshal(v)
				if err != nil {
					mErr = err
					return
				}
				c.emit(m)
			}
		},
		OnSecurities: func(secs []records.SecNew) {
			if mErr == nil && st != nil {
				c.emit(xmlcompat.SecuritiesXML(st, secs))
				c.emit(xmlcompat.PitsXML(st, secs))
			}
		},
	})
	if err != nil {
		return nil, err
	}
	if mErr != nil {
		client.Close()
		return nil, mErr
	}

	c.mu.Lock()
	if ctx.Err() != nil {
		c.mu.Unlock()
		client.Close()
		return nil, ctx.Err()
	}
	c.client, c.state = client, stateConnected
	c.withMillis, c.withUTC = req.Milliseconds, req.UtcTime
	c.autoPos, c.micexRegisters = req.Autopos, req.MicexRegisters
	status := c.statusLocked(true)
	c.mu.Unlock()
	c.emit(status)
	return client, nil
}

// subscribe handles <alltrades> of subscribe/unsubscribe, in either the
// secid or the board+seccode form. Quotations and quotes are not implemented.
func (c *Connector) subscribe(ctx context.Context, command string, on bool) (string, error) {
	var req struct {
		AllTrades struct {
			SecIDs     []int `xml:"secid"`
			Securities []struct {
				Board   string `xml:"board"`
				SecCode string `xml:"seccode"`
			} `xml:"security"`
		} `xml:"alltrades"`
		Quotations *struct{} `xml:"quotations"`
		Quotes     *struct{} `xml:"quotes"`
	}
	if err := xml.Unmarshal([]byte(command), &req); err != nil {
		return failure(err.Error()), nil
	}
	if req.Quotations != nil || req.Quotes != nil {
		return failure(msgNotImplemented), nil
	}
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	var symbols []model.Symbol
	for _, s := range req.AllTrades.Securities {
		symbols = append(symbols, model.Symbol{Board: s.Board, SecCode: s.SecCode})
	}
	for _, id := range req.AllTrades.SecIDs {
		sec, ok := client.Catalog().Security(uint16(id))
		p, hasPit := sec.DefaultPit()
		board, hasBoard := client.Catalog().Board(p.Board)
		if !ok || !hasPit || !hasBoard {
			return failure(fmt.Sprintf("unknown secid %d", id)), nil
		}
		symbols = append(symbols, model.Symbol{Board: board.Code, SecCode: sec.SecCode})
	}
	update := client.SubscribeAllTrades
	if !on {
		update = client.UnsubscribeAllTrades
	}
	if err := update(ctx, symbols...); err != nil {
		return failure(err.Error()), nil
	}
	return success(), nil
}

// finish handles a connection that ended without disconnect: it resets the
// state and reports it, unless disconnect is already in charge (ctx done).
func (c *Connector) finish(ctx context.Context, client *Client, report func() string) {
	c.mu.Lock()
	if ctx.Err() != nil {
		// disconnect cancelled an exchange in flight; it still owns the
		// connection and sends RQ_LOGOUT on it.
		c.mu.Unlock()
		return
	}
	var message string
	if report != nil {
		message = report() // called with c.mu held
	}
	c.state, c.client = stateDisconnected, nil
	c.mu.Unlock()
	if client != nil {
		client.Close()
	}
	if message != "" {
		c.emit(message)
	}
}

func (c *Connector) disconnect(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.state == stateDisconnected {
		c.mu.Unlock()
		return failure(msgNotConnected), nil
	}
	cancel, done := c.cancel, c.done
	c.mu.Unlock()

	cancel()
	<-done
	c.mu.Lock()
	client := c.client
	status := c.statusLocked(false)
	c.state, c.client = stateDisconnected, nil
	c.mu.Unlock()
	if client != nil {
		logoutCtx, stop := context.WithTimeout(ctx, logoutTimeout)
		_ = client.Logout(logoutCtx)
		stop()
	}
	// The DLL reports connected="false" before disconnect returns.
	c.emitSync(status)
	return success(), nil
}

// Close disconnects if needed and stops message delivery.
func (c *Connector) Close() error {
	c.cmdMu.Lock()
	defer c.cmdMu.Unlock()
	c.mu.Lock()
	if !c.started || c.closed {
		c.mu.Unlock()
		return nil
	}
	connected, done := c.state != stateDisconnected, c.done
	c.mu.Unlock()
	if connected {
		_, _ = c.disconnect(context.Background())
	}
	if done != nil {
		<-done // the worker may still be reporting a lost connection
	}
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	close(c.out)
	<-c.outDone
	return nil
}

// recoveryStatusXML renders the <server_status> the DLL reports when the link
// is lost and the reconnect starts: connected="true" with recover="true" right
// after it (E05 capture: native/session/testdata/recover-short-live.xml,
// docs/protocol/connect.md).
func (c *Connector) recoveryStatusXML() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.TrimSuffix(c.statusLocked(true), "/>") + ` recover="true"/>`
}

func (c *Connector) statusXML(connected bool) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statusLocked(connected)
}

// statusLocked renders <server_status> with the attribute order of the DLL:
// sys_ver, build, server_tz, id, connected.
func (c *Connector) statusLocked(connected bool) string {
	var b strings.Builder
	b.WriteString("<server_status")
	if c.client != nil {
		info := c.client.Info()
		b.WriteString(` sys_ver="` + strconv.Itoa(int(info.SysVersion)) + `"`)
		b.WriteString(` build="` + strconv.Itoa(int(info.Build)) + `"`)
		b.WriteString(` server_tz="` + escapeText(c.client.ServerTZ()) + `"`)
		b.WriteString(` id="` + strconv.Itoa(int(info.SID)) + `"`)
	}
	b.WriteString(` connected="` + strconv.FormatBool(connected) + `"/>`)
	return b.String()
}

// connectErrorXML reports a failed connect as the DLL does: connected="error"
// with the server's login message, or a generic text.
func connectErrorXML(err error) string {
	message := msgConnectFailed
	var le *session.LoginError
	if errors.As(err, &le) && le.Message != "" {
		message = records.DecodeCP1251(le.Message)
	}
	return `<server_status connected="error">` + escapeText(message) + `</server_status>`
}

func success() string { return `<result success="true"/>` }

func failure(message string) string {
	return `<result success="false"><message>` + escapeText(message) + `</message></result>`
}

func escapeText(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
