// Package native is a TRANSAQ XML Connector implemented in Go without the
// vendor DLL. Connector accepts the DLL's XML commands and emits its XML
// messages, so it can stand in for connector.New where a command is supported.
//
// Supported commands: connect, disconnect, server_status,
// get_connector_version, get_securities, get_server_id,
// get_servtime_difference, get_markets, get_securities_info, and
// subscribe/unsubscribe for <alltrades>, <quotations> and <quotes>. The
// remaining commands registered by the DLL answer with an explicit
// "not implemented" error. See docs/protocol/commands.md. Client is the typed
// API without XML.
package native

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
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
		c.mu.Lock()
		client := c.client
		c.mu.Unlock()
		c.emit(c.overnightXML(client))
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
	case "subscribe_ticks":
		var req struct {
			Filter     string `xml:"filter"`
			FilterAttr string `xml:"filter,attr"`
			Securities []struct {
				Board   string `xml:"board"`
				SecCode string `xml:"seccode"`
				TradeNo *int64 `xml:"tradeno"`
			} `xml:"security"`
		}
		if err := xml.Unmarshal([]byte(command), &req); err != nil {
			return failure(err.Error()), nil
		}
		c.mu.Lock()
		client := c.client
		c.mu.Unlock()
		if client == nil {
			return failure(msgNeedsConnection), nil
		}
		filter := strings.EqualFold(req.Filter, "true") || strings.EqualFold(req.FilterAttr, "true")
		var reqs []TickRequest
		for _, sec := range req.Securities {
			// The DLL requires the tradeno child element (D: "Cannot find
			// tradeno element."); the attribute form is I.
			if sec.TradeNo == nil {
				return failure("Cannot find tradeno element."), nil
			}
			symbol, ok := client.SymbolByBoard(sec.Board, sec.SecCode)
			if !ok {
				return failure("unknown security " + sec.SecCode), nil
			}
			reqs = append(reqs, TickRequest{Symbol: symbol, TradeNo: *sec.TradeNo})
		}
		// Without <security> elements the DLL sends the mask 0 and subscribes
		// nothing (D/S r14): the filter byte alone is sent.
		if err := client.SubscribeTicks(ctx, filter, reqs...); err != nil {
			return failure(err.Error()), nil
		}
		return success(), nil
	case "gethistorydata":
		var req struct {
			Security *struct {
				Board   string `xml:"board"`
				SecCode string `xml:"seccode"`
			} `xml:"security"`
			Period int    `xml:"period"`
			Count  int    `xml:"count"`
			Reset  string `xml:"reset"`
			SecID  int    `xml:"secid,attr"`
			// Attribute form: <command id="gethistorydata" secid=... period=...>
			APeriod int    `xml:"period,attr"`
			ACount  int    `xml:"count,attr"`
			AReset  string `xml:"reset,attr"`
		}
		if err := xml.Unmarshal([]byte(command), &req); err != nil {
			return failure(err.Error()), nil
		}
		c.mu.Lock()
		client := c.client
		c.mu.Unlock()
		if client == nil {
			return failure(msgNeedsConnection), nil
		}
		var symbol model.Symbol
		var ok bool
		switch {
		case req.Security != nil && req.Security.SecCode != "":
			symbol, ok = client.SymbolByBoard(req.Security.Board, req.Security.SecCode)
		case req.SecID != 0:
			symbol, ok = client.SymbolBySecID(uint16(req.SecID))
		}
		if !ok {
			return failure("unknown security"), nil
		}
		period, count := req.Period, req.Count
		if period == 0 {
			period = req.APeriod
		}
		if count == 0 {
			count = req.ACount
		}
		if period <= 0 {
			// DLL text (D): the period comes from <candlekinds> and is required.
			return failure("'period' is absent"), nil
		}
		if count <= 0 || count > 0xffff {
			return failure("count is out of range"), nil
		}
		reset := req.Reset == "true" || req.Reset == "1" || req.AReset == "true" || req.AReset == "1"
		// The DLL renders <result success="true"/> at once (S r14: result at
		// 7.310 s, <candles> at 7.340 s): start the collection in the background
		// and emit the candles when the parts are complete.
		err := client.StartHistory(ctx, HistoryRequest{Symbol: symbol, Period: period, Count: count, Reset: reset},
			func(candles model.Candles, err error) {
				if err != nil {
					return
				}
				c.mu.Lock()
				closed := c.closed
				c.mu.Unlock()
				if closed {
					return
				}
				c.emit(xmlcompat.CandlesXML(candles))
			})
		if err != nil {
			return failure(err.Error()), nil
		}
		return success(), nil
	case "neworder":
		var req struct {
			SecID     int    `xml:"secid"`
			Client    string `xml:"client"`
			Union     string `xml:"union"`
			Price     string `xml:"price"`
			Quantity  int64  `xml:"quantity"`
			BuySell   string `xml:"buysell"`
			Unfilled  string `xml:"unfilled"`
			Hidden    int64  `xml:"hidden"`
			BrokerRef string `xml:"brokerref"`
		}
		if err := xml.Unmarshal([]byte(command), &req); err != nil {
			return failure(err.Error()), nil
		}
		c.mu.Lock()
		client := c.client
		c.mu.Unlock()
		if client == nil {
			return failure(msgNeedsConnection), nil
		}
		if req.SecID == 0 {
			return failure("Не указан элемент secid"), nil
		}
		if req.Client == "" && req.Union == "" {
			return failure("Должен быть указан клиент и/или юнион"), nil
		}
		if req.Price == "" {
			return failure("Price not found."), nil
		}
		symbol, ok := client.SymbolBySecID(uint16(req.SecID))
		if !ok {
			return failure(fmt.Sprintf("unknown secid %d", req.SecID)), nil
		}
		price, err := model.ParseDecimal(req.Price)
		if err != nil {
			return failure(err.Error()), nil
		}
		buySell := byte(0)
		if len(req.BuySell) == 1 {
			buySell = req.BuySell[0]
		}
		if buySell != 'B' && buySell != 'S' {
			return failure("Wrong buysell."), nil
		}
		order := model.NewOrder{
			Client: req.Client, Union: req.Union, Symbol: symbol, Price: price,
			Hidden: req.Hidden, Quantity: req.Quantity, BuySell: buySell,
			Unfilled: req.Unfilled, BrokerRef: req.BrokerRef,
		}
		id, err := client.NewOrder(ctx, order)
		if err != nil {
			return failure(err.Error()), nil
		}
		return "<result success=\"true\" transactionid=\"" + strconv.FormatUint(uint64(id), 10) + "\"/>", nil
	case "cancelorder":
		var req struct {
			TransactionID uint64 `xml:"transactionid"`
		}
		if err := xml.Unmarshal([]byte(command), &req); err != nil {
			return failure(err.Error()), nil
		}
		c.mu.Lock()
		client := c.client
		c.mu.Unlock()
		if client == nil {
			return failure(msgNeedsConnection), nil
		}
		if req.TransactionID == 0 {
			return failure("You have to specify transactionid."), nil
		}
		if err := client.CancelOrder(ctx, model.TransactionID(req.TransactionID)); err != nil {
			return failure(err.Error()), nil
		}
		return "<result success=\"true\" transactionid=\"" + strconv.FormatUint(req.TransactionID, 10) + "\"/>", nil
	case "get_securities_info":
		var req struct {
			Security []struct {
				Market  int    `xml:"market"`
				SecCode string `xml:"seccode"`
			} `xml:"security"`
			SecIDs []int `xml:"secid"`
		}
		if err := xml.Unmarshal([]byte(command), &req); err != nil {
			return failure(err.Error()), nil
		}
		c.mu.Lock()
		client := c.client
		c.mu.Unlock()
		if client == nil {
			return failure(msgNeedsConnection), nil
		}
		var symbols []model.Symbol
		for _, s := range req.Security {
			symbol, ok := client.SymbolByMarket(s.Market, s.SecCode)
			if !ok {
				return failure("unknown security " + s.SecCode), nil
			}
			symbols = append(symbols, symbol)
		}
		for _, id := range req.SecIDs {
			if id < 0 || id > 0xffff {
				return failure(fmt.Sprintf("secid %d is out of range", id)), nil
			}
			symbol, ok := client.SymbolBySecID(uint16(id))
			if !ok {
				return failure(fmt.Sprintf("unknown secid %d", id)), nil
			}
			symbols = append(symbols, symbol)
		}
		// The DLL renders the callbacks from the local catalog/spec cache and
		// sends no request to the server (S, r11 capture).
		infos, err := client.SecuritiesInfo(ctx, symbols...)
		if err != nil {
			return failure(err.Error()), nil
		}
		for _, info := range infos {
			c.emit(xmlcompat.SecInfoXML(info))
		}
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
			case *model.ClientAccount:
				c.emit(xmlcompat.ClientXML(*e))
			case *model.MoneyPosition:
				c.emit(xmlcompat.PositionsXML([]model.MoneyPosition{*e}, c.micexRegisters))
			case *model.Union:
				c.emit(xmlcompat.UnionXML(*e))
			case *model.SecInfoUpdate:
				c.emit(xmlcompat.SecInfoUpdateXML(*e))
			case *model.Quotation:
				c.emit(xmlcompat.QuotationXML(*e))
			case *model.Quotes:
				c.emit(xmlcompat.QuotesXML(*e))
			case *model.Order:
				c.emit(xmlcompat.OrdersXML([]model.Order{*e}))
			case *model.ClientTrade:
				c.emit(xmlcompat.TradesXML([]model.ClientTrade{*e}))
			case model.ServerStatus:
				switch {
				case e.Recover:
					c.emit(c.recoveryStatusXML())
				case e.Connected:
					// The DLL does not put an overnight line before the
					// recovery statuses (E05 fixtures): S wins over the D-only
					// "before every status" claim.
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
		OnLoadEvent: func(events []model.Event) {
			if mErr != nil {
				return
			}
			for _, ev := range events {
				// Accounts, positions and unions are emitted from the state
				// after the load (catalog/client store below), so only the
				// updates the DLL reports per RQ_MAIN reply go from here.
				if update, ok := ev.(*model.SecInfoUpdate); ok {
					c.emit(xmlcompat.SecInfoUpdateXML(*update))
				}
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

	// The DLL reports the end-of-load callbacks just before server_status:
	// <client>, <positions>, <overnight>, <server_status connected="true"/>.
	for _, account := range client.Accounts() {
		c.emit(xmlcompat.ClientXML(account))
	}
	if positions := client.MoneyPositions(); len(positions) > 0 {
		c.emit(xmlcompat.PositionsXML(positions, req.MicexRegisters))
	}
	// A non-empty union is not in the live capture (the demo union is empty):
	// the DLL position is inferred, so the callbacks go right after
	// <positions> (I) and are not lost silently.
	for _, union := range client.Unions() {
		c.emit(xmlcompat.UnionXML(union))
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
	overnight := c.overnightXML(client)
	status := c.statusLocked(true)
	c.mu.Unlock()
	c.emit(overnight)
	c.emit(status)
	return client, nil
}

// subscribe handles <alltrades> of subscribe/unsubscribe, in either the
// secid or the board+seccode form.
func (c *Connector) subscribe(ctx context.Context, command string, on bool) (string, error) {
	type feed struct {
		SecIDs     []int `xml:"secid"`
		Securities []struct {
			Board   string `xml:"board"`
			SecCode string `xml:"seccode"`
		} `xml:"security"`
	}
	var req struct {
		AllTrades  feed  `xml:"alltrades"`
		Quotations *feed `xml:"quotations"`
		Quotes     *feed `xml:"quotes"`
	}
	if err := xml.Unmarshal([]byte(command), &req); err != nil {
		return failure(err.Error()), nil
	}
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	resolve := func(f feed) ([]model.Symbol, error) {
		var symbols []model.Symbol
		for _, s := range f.Securities {
			symbols = append(symbols, model.Symbol{Board: s.Board, SecCode: s.SecCode})
		}
		for _, id := range f.SecIDs {
			sec, ok := client.Catalog().Security(uint16(id))
			p, hasPit := sec.DefaultPit()
			board, hasBoard := client.Catalog().Board(p.Board)
			if !ok || !hasPit || !hasBoard {
				return nil, fmt.Errorf("unknown secid %d", id)
			}
			symbols = append(symbols, model.Symbol{Board: board.Code, SecCode: sec.SecCode})
		}
		return symbols, nil
	}
	var sub Subscription
	symbols, err := resolve(req.AllTrades)
	if err != nil {
		return failure(err.Error()), nil
	}
	sub.AllTrades = symbols
	// <quotations> needs document order: encoding/xml merges repeated elements,
	// so the first block/child is picked by a streaming scan (S r8: a command
	// with four <quotations> blocks subscribed only the first security).
	if quotations, err := c.firstFeed(client, command, "quotations"); err != nil {
		return failure(err.Error()), nil
	} else if len(quotations) > 0 {
		sub.Quotations = quotations
	}
	// <quotes>: the first-block rule is used by analogy with <quotations>; the
	// live r10 capture subscribed one security per command, so the "only first
	// element" behaviour for several <quotes> blocks is I, not S.
	if quotes, err := c.firstFeed(client, command, "quotes"); err != nil {
		return failure(err.Error()), nil
	} else if len(quotes) > 0 {
		sub.Quotes = quotes
	}
	var runErr error
	if on {
		runErr = client.Subscribe(ctx, sub)
	} else {
		runErr = client.Unsubscribe(ctx, sub)
	}
	if runErr != nil {
		return failure(runErr.Error()), nil
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
	// The DLL reports connected="false" without an overnight line before
	// disconnect returns (E07 snapshot-tail-live.xml).
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

// overnightXML renders the <overnight status="..."/> line the DLL puts before
// every server_status callback; the session default is false.
func (c *Connector) overnightXML(client *Client) string {
	status := false
	if client != nil {
		status = client.Overnight().Status
	}
	return xmlcompat.OvernightXML(status)
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

// firstFeed returns the first <feedName> child of the command in XML document
// order (encoding/xml would merge repeated blocks and mix <secid> with
// <security> children). The S rule "only the first element" is proven for
// <quotations> (r8); <quotes> uses it by analogy (I).
func (c *Connector) firstFeed(client *Client, command, feedName string) ([]model.Symbol, error) {
	dec := xml.NewDecoder(strings.NewReader(command))
	depth := 0
	feedDepth := -1
	type item struct {
		secID   int
		market  int
		secCode string
		isSec   bool
	}
	var first *item
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if feedDepth < 0 {
				if t.Name.Local == feedName {
					feedDepth = depth
				}
				continue
			}
			if depth != feedDepth+1 {
				continue
			}
			switch t.Name.Local {
			case "secid":
				var id int
				if err := dec.DecodeElement(&id, &t); err != nil {
					return nil, err
				}
				depth-- // DecodeElement consumed the whole element
				if first == nil {
					first = &item{secID: id, isSec: true}
				}
			case "security":
				var sec struct {
					Market  int    `xml:"market"`
					SecCode string `xml:"seccode"`
				}
				if err := dec.DecodeElement(&sec, &t); err != nil {
					return nil, err
				}
				depth-- // DecodeElement consumed the whole element
				if first == nil {
					first = &item{market: sec.Market, secCode: sec.SecCode}
				}
			}
		case xml.EndElement:
			if feedDepth > 0 && depth == feedDepth {
				// End of the first <quotations> block.
				if first == nil {
					return nil, nil
				}
				if client == nil {
					return nil, fmt.Errorf(msgNeedsConnection)
				}
				if first.isSec {
					symbol, ok := client.SymbolBySecID(uint16(first.secID))
					if !ok {
						return nil, fmt.Errorf("unknown secid %d", first.secID)
					}
					return []model.Symbol{symbol}, nil
				}
				symbol, ok := client.SymbolByMarket(first.market, first.secCode)
				if !ok {
					return nil, fmt.Errorf("unknown security %s", first.secCode)
				}
				return []model.Symbol{symbol}, nil
			}
			depth--
		}
	}
	return nil, nil
}
