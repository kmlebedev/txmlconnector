package feed

import (
	"context"
	"strconv"
	"time"

	"github.com/kmlebedev/txmlconnector/native"
	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
)

// marketClient is the subset of *native.Client the live feed uses. It is an
// interface so the live path is unit-testable with fakes: the tests never dial
// the demo server.
type marketClient interface {
	Subscribe(ctx context.Context, sub native.Subscription) error
	Run(ctx context.Context, opt native.RunOptions, h func([]model.Event) error) error
	History(ctx context.Context, req native.HistoryRequest) (model.Candles, error)
	ServerTimeDifference(ctx context.Context) (time.Duration, error)
	ServerTZ() string
	Catalog() *records.Catalog
	Close() error
}

// liveDial opens a live market session.
type liveDial func(ctx context.Context, cfg native.ClientConfig) (marketClient, error)

// dialNative is the production dialer.
func dialNative(ctx context.Context, cfg native.ClientConfig) (marketClient, error) {
	return native.Dial(ctx, cfg)
}

// Live-feed tuning.
const (
	// subscribeBatch is the maximum number of symbols per subscribe request.
	subscribeBatch = 50
	// liveHistoryTimeout bounds one exchange history request.
	liveHistoryTimeout = 25 * time.Second
	// liveDialTimeout bounds one reconnect attempt.
	liveDialTimeout = 15 * time.Second
	// liveTradeRing is how many recent ticks are kept per symbol.
	liveTradeRing = 500
)

// clientConfig builds the native session config from the feed config.
func (s *Service) clientConfig() native.ClientConfig {
	return native.ClientConfig{
		Address:  s.cfg.Address,
		Login:    s.cfg.Login,
		Password: s.cfg.Password,
		RqDelay:  s.cfg.RqDelay,
	}
}

// runLoop owns the live session: subscribe, poll, and reconnect with an
// exponential backoff (2s -> 30s) keeping the last known data.
func (s *Service) runLoop(client marketClient) {
	defer s.wg.Done()
	backoff := s.backoffMin
	if backoff <= 0 {
		backoff = 2 * time.Second
	}
	for {
		if s.ctx.Err() != nil {
			return
		}
		err := s.runOnce(client)
		if s.ctx.Err() != nil {
			return
		}
		s.setSessionState(false, reconnectMessage(err))
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-time.After(backoff):
			}
			next := backoff * 2
			if max := s.backoffMax; max > 0 && next > max {
				next = max
			}
			backoff = next
			s.setSessionState(false, "переподключение к серверу биржи")
			newClient, dialErr := s.dialWithTimeout()
			if dialErr != nil {
				if s.ctx.Err() != nil {
					return
				}
				s.setSessionState(false, "нет соединения с сервером биржи: "+dialErr.Error())
				continue
			}
			// A fresh session owns a fresh catalog: refresh the static
			// metadata before Run starts polling.
			old := client
			client = newClient
			s.mu.Lock()
			s.client = client
			s.mu.Unlock()
			if old != nil {
				_ = old.Close()
			}
			s.applyCatalog(client)
			s.refreshServerTime(s.ctx, client)
			backoff = s.backoffMin
			if backoff <= 0 {
				backoff = 2 * time.Second
			}
			break
		}
	}
}

// runOnce subscribes the universe and polls until the session fails.
func (s *Service) runOnce(client marketClient) error {
	if err := s.subscribeAll(s.ctx, client); err != nil {
		return err
	}
	s.setSessionState(true, "подключено к серверу биржи")
	return client.Run(s.ctx, native.RunOptions{}, func(events []model.Event) error {
		s.applyEvents(events)
		return nil
	})
}

// subscribeAll subscribes quotations, order books and the tick tape for the
// whole universe, in batches of subscribeBatch.
func (s *Service) subscribeAll(ctx context.Context, client marketClient) error {
	for start := 0; start < len(s.symbols); start += subscribeBatch {
		end := start + subscribeBatch
		if end > len(s.symbols) {
			end = len(s.symbols)
		}
		chunk := make([]model.Symbol, 0, end-start)
		for _, ref := range s.symbols[start:end] {
			chunk = append(chunk, model.Symbol{Board: ref.Board, SecCode: ref.SecCode})
		}
		sub := native.Subscription{Quotations: chunk, Quotes: chunk, AllTrades: chunk}
		if err := client.Subscribe(ctx, sub); err != nil {
			return err
		}
	}
	return nil
}

// dialWithTimeout runs one reconnect dial bounded by liveDialTimeout.
func (s *Service) dialWithTimeout() (marketClient, error) {
	ctx, cancel := context.WithTimeout(s.ctx, liveDialTimeout)
	defer cancel()
	return s.dial(ctx, s.clientConfig())
}

// setSessionState updates the live connection status and publishes it.
func (s *Service) setSessionState(connected bool, message string) {
	s.mu.Lock()
	s.status.Connected = connected
	s.status.Message = message
	if connected {
		s.status.LastEventAt = time.Now().UTC()
	}
	st := s.status
	s.mu.Unlock()
	s.publish(Event{Type: "status", Data: st})
}

// reconnectMessage renders a Russian session-failure message; it never includes
// credentials (the error comes from the transport/login layer).
func reconnectMessage(err error) string {
	if err == nil {
		return "обрыв связи с сервером биржи"
	}
	return "обрыв связи с сервером биржи: " + err.Error()
}

// applyCatalog refreshes the static instrument metadata from the session
// catalog and the candle-kind ids. It must be called only while Run is not
// polling: the catalog is otherwise mutated under the native client lock.
func (s *Service) applyCatalog(client marketClient) {
	catalog := client.Catalog()
	if catalog == nil {
		return
	}
	kinds := map[int]int{}
	if startup := catalog.Startup(); startup != nil {
		for _, kind := range startup.CandleKinds {
			period := int(kind.Period)
			if ValidPeriod(period) {
				kinds[period] = int(kind.ID)
			}
		}
	}
	s.mu.Lock()
	if len(kinds) > 0 {
		s.candleKinds = kinds
	}
	for _, ref := range s.symbols {
		key := ref.Board + ":" + ref.SecCode
		inst := s.instruments[key]
		if inst == nil {
			continue
		}
		sec, board, ok := catalog.Lookup(ref.Board, ref.SecCode)
		if !ok {
			continue
		}
		if sec.ShortName != "" {
			inst.ShortName = sec.ShortName
			if inst.Name == "" {
				inst.Name = sec.ShortName
			}
		}
		if sec.Currency != "" {
			inst.Currency = normalizeCurrency(sec.Currency)
		}
		if decimals, ok := catalog.Decimals(sec.SecID, board.Num); ok {
			inst.Decimals = int(decimals)
		}
	}
	exchangeTZ := client.ServerTZ()
	if exchangeTZ != "" {
		s.exchangeTZ = exchangeTZ
		s.status.ExchangeTZ = exchangeTZ
	}
	s.mu.Unlock()
}

// refreshServerTime stores the exchange clock offset (UTC instant minus local
// now) so Status can render the exchange time without another request.
func (s *Service) refreshServerTime(ctx context.Context, client marketClient) {
	diff, err := client.ServerTimeDifference(ctx)
	if err != nil {
		return
	}
	now := time.Now()
	localWall := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), now.Second(), 0, time.UTC)
	serverWall := localWall.Add(diff).UTC()
	instant := serverWall
	s.mu.RLock()
	zone := s.exchangeTZ
	s.mu.RUnlock()
	if zone != "" {
		if loc, err := time.LoadLocation(zone); err == nil {
			instant = time.Date(serverWall.Year(), serverWall.Month(), serverWall.Day(),
				serverWall.Hour(), serverWall.Minute(), serverWall.Second(), 0, loc).UTC()
		}
	}
	s.mu.Lock()
	s.serverOffset = instant.Sub(now)
	s.status.ServerTime = instant
	s.mu.Unlock()
}

// applyEvents dispatches one RQ_MAIN event batch into the live caches.
func (s *Service) applyEvents(events []model.Event) {
	for _, event := range events {
		switch ev := event.(type) {
		case *model.Quotation:
			s.applyQuotation(ev)
		case *model.Quotes:
			s.applyQuotes(ev)
		case *model.Trade:
			s.applyTrade(ev)
		case model.ServerStatus:
			s.applyServerStatus(ev)
		default:
			// Other record families are not subscribed by the terminal feed.
		}
	}
}

// applyQuotation merges a partial <quotation> update into the instrument cache
// and publishes the new quote.
func (s *Service) applyQuotation(ev *model.Quotation) {
	key := ev.Symbol.String()
	now := time.Now().UTC()
	s.mu.Lock()
	inst := s.instruments[key]
	if inst == nil {
		s.mu.Unlock()
		return
	}
	applyQuotationFields(inst, ev.Fields)
	inst.UpdatedAt = now
	quote := quoteOf(*inst, now)
	s.quotes[key] = quote
	s.status.LastEventAt = now
	s.mu.Unlock()
	s.publish(Event{Type: "quote", Data: quote})
}

// applyQuotationFields merges the changed fields of one <quotation> callback
// into an instrument (nil pointers are fields absent from the update).
func applyQuotationFields(inst *Instrument, f model.QuotationFields) {
	if f.Last != nil {
		inst.Last = f.Last.Float64()
	}
	if f.Change != nil {
		inst.Change = f.Change.Float64()
	}
	if f.Bid != nil {
		inst.Bid = f.Bid.Float64()
	}
	if f.Offer != nil {
		inst.Ask = f.Offer.Float64()
	}
	if f.VolToday != nil {
		inst.Volume = *f.VolToday
	}
	if f.Open != nil {
		inst.Open = f.Open.Float64()
	}
	if f.High != nil {
		inst.High = f.High.Float64()
	}
	if f.Low != nil {
		inst.Low = f.Low.Float64()
	}
	if f.ClosePrice != nil {
		inst.PrevClose = f.ClosePrice.Float64()
	}
	inst.ChangePct = changePct(inst.Last, inst.Change)
}

// quoteOf builds the quote view of an instrument.
func quoteOf(inst Instrument, now time.Time) Quote {
	return Quote{
		Symbol:    inst.Symbol,
		Last:      inst.Last,
		Change:    inst.Change,
		ChangePct: inst.ChangePct,
		Bid:       inst.Bid,
		Ask:       inst.Ask,
		Volume:    inst.Volume,
		Time:      now,
	}
}

// applyQuotes stores a full order-book snapshot and publishes it.
func (s *Service) applyQuotes(ev *model.Quotes) {
	key := ev.Symbol.String()
	now := time.Now().UTC()
	s.mu.Lock()
	inst := s.instruments[key]
	if inst == nil {
		s.mu.Unlock()
		return
	}
	book := orderBookFromQuotes(ev, inst.Last, now)
	s.books[key] = book
	inst.UpdatedAt = now
	s.status.LastEventAt = now
	s.mu.Unlock()
	s.publish(Event{Type: "orderbook", Data: book})
}

// orderBookFromQuotes maps a full <quotes> snapshot to a price ladder. Levels
// with a negative quantity are deletions (-1) and are skipped: the snapshot
// always replaces the whole book.
func orderBookFromQuotes(ev *model.Quotes, last float64, now time.Time) OrderBook {
	book := OrderBook{Symbol: ev.Symbol.String(), Last: last, Time: now, Source: "live"}
	for _, level := range ev.Levels {
		price := level.Price.Float64()
		switch {
		case level.Buy > 0:
			book.Bids = append(book.Bids, Level{Price: price, Qty: float64(level.Buy)})
		case level.Sell > 0:
			book.Asks = append(book.Asks, Level{Price: price, Qty: float64(level.Sell)})
		}
	}
	book.Bids = limitLevels(book.Bids)
	book.Asks = limitLevels(book.Asks)
	return book
}

// applyTrade appends one anonymous trade to the per-symbol tick-tape ring.
func (s *Service) applyTrade(ev *model.Trade) {
	key := ev.Board + ":" + ev.SecCode
	now := time.Now().UTC()
	trade := Trade{Time: ev.Time.UTC(), Price: ev.Price.Float64(), Qty: ev.Quantity}
	switch ev.BuySell {
	case 1:
		trade.Side = "buy"
	case 2:
		trade.Side = "sell"
	}
	if ev.Time.IsZero() {
		trade.Time = now
	}
	s.mu.Lock()
	if _, ok := s.instruments[key]; !ok {
		s.mu.Unlock()
		return
	}
	ring := append(s.trades[key], trade)
	if len(ring) > liveTradeRing {
		ring = append([]Trade(nil), ring[len(ring)-liveTradeRing:]...)
	}
	s.trades[key] = ring
	s.status.LastEventAt = now
	s.mu.Unlock()
}

// applyServerStatus updates the session status and publishes it.
func (s *Service) applyServerStatus(st model.ServerStatus) {
	switch {
	case st.Err != "":
		s.setSessionState(false, "ошибка сессии: "+st.Err)
	case st.Recover:
		s.setSessionState(true, "восстановление соединения с сервером биржи")
	case st.Connected:
		s.setSessionState(true, "подключено к серверу биржи")
	default:
		s.setSessionState(false, "соединение с сервером биржи потеряно")
	}
}

// currentClient returns the active live client (nil when disconnected).
func (s *Service) currentClient() marketClient {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.client
}

// liveCandles serves a candle series from native.Client with a TTL cache.
func (s *Service) liveCandles(ctx context.Context, symbol string, periodSec, count int) (History, error) {
	if _, ok := s.Instrument(symbol); !ok {
		return History{}, notFound(symbol)
	}
	client := s.currentClient()
	if client == nil {
		return History{}, &Error{Code: CodeUnavailable, Message: "нет соединения с сервером биржи"}
	}
	key := symbol + "|" + strconv.Itoa(periodSec)
	now := time.Now()
	s.mu.RLock()
	entry := s.historyCache[key]
	s.mu.RUnlock()
	if entry != nil && now.Before(entry.expireAt) {
		return trimHistory(entry.history, count), nil
	}
	s.mu.RLock()
	kind, ok := s.candleKinds[periodSec]
	s.mu.RUnlock()
	if !ok {
		return History{}, &Error{
			Code:    CodeBadPeriod,
			Message: "сервер не поддерживает период свечей: " + strconv.Itoa(periodSec) + " с",
		}
	}
	board, secCode, _ := cutSymbol(symbol)
	callCtx, cancel := context.WithTimeout(ctx, liveHistoryTimeout)
	defer cancel()
	candles, err := client.History(callCtx, native.HistoryRequest{
		Symbol: model.Symbol{Board: board, SecCode: secCode},
		Period: kind,
		Count:  count,
	})
	if err != nil {
		return History{}, &Error{Code: CodeUnavailable, Message: "не удалось получить историю с сервера биржи: " + err.Error()}
	}
	history := historyFromCandles(candles, symbol, periodSec, "с сервера биржи")
	s.mu.Lock()
	s.historyCache[key] = &historyEntry{history: history, expireAt: now.Add(s.cfg.HistoryTTL)}
	s.mu.Unlock()
	return trimHistory(history, count), nil
}

// liveOrderBook serves the cached price ladder of a live instrument.
func (s *Service) liveOrderBook(symbol string) (OrderBook, error) {
	if _, ok := s.Instrument(symbol); !ok {
		return OrderBook{}, notFound(symbol)
	}
	s.mu.RLock()
	book, ok := s.books[symbol]
	s.mu.RUnlock()
	if !ok {
		return OrderBook{}, &Error{Code: CodeNotFound, Message: "стакан ещё не получен с сервера: " + symbol}
	}
	return copyOrderBook(book), nil
}

// liveTrades serves the cached tick tape of a live instrument.
func (s *Service) liveTrades(symbol string, limit int) ([]Trade, error) {
	if _, ok := s.Instrument(symbol); !ok {
		return nil, notFound(symbol)
	}
	s.mu.RLock()
	trades := s.trades[symbol]
	s.mu.RUnlock()
	if len(trades) > limit {
		trades = trades[len(trades)-limit:]
	}
	return append([]Trade(nil), trades...), nil
}

// trimHistory keeps only the last count bars of a series.
func trimHistory(history History, count int) History {
	if len(history.Bars) > count {
		history.Bars = history.Bars[len(history.Bars)-count:]
	}
	return history
}

// historyFromCandles maps the native candle series to the feed history. Only
// the decimal-to-float boundary conversion happens here: the wire stays
// model.Decimal.
func historyFromCandles(candles model.Candles, symbol string, periodSec int, note string) History {
	history := History{Symbol: symbol, PeriodSec: periodSec, Source: "live", Note: note}
	history.Bars = make([]Bar, 0, len(candles.Items))
	for _, item := range candles.Items {
		history.Bars = append(history.Bars, Bar{
			Time:   item.Time.UTC(),
			Open:   item.Open.Float64(),
			High:   item.High.Float64(),
			Low:    item.Low.Float64(),
			Close:  item.Close.Float64(),
			Volume: item.Volume,
		})
	}
	return history
}

// cutSymbol splits "BOARD:SECCODE".
func cutSymbol(symbol string) (board, secCode string, ok bool) {
	for i := 0; i < len(symbol); i++ {
		if symbol[i] == ':' {
			return symbol[:i], symbol[i+1:], true
		}
	}
	return "", "", false
}
