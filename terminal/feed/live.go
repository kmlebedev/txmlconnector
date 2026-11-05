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

// stableSessionDuration is how long a live session must survive to count as
// stable and reset the reconnect backoff.
const stableSessionDuration = 30 * time.Second

// backoffSchedule escalates the reconnect wait (min -> max) while sessions keep
// failing; only a stable session resets it, so a flapping server is not retried
// every two seconds forever.
type backoffSchedule struct {
	min, max time.Duration
	current  time.Duration
}

// newBackoffSchedule builds the retry schedule.
func newBackoffSchedule(min, max time.Duration) backoffSchedule {
	if min <= 0 {
		min = 2 * time.Second
	}
	if max < min {
		max = min
	}
	return backoffSchedule{min: min, max: max, current: min}
}

// observeSession resets the escalation when the session lasted at least stable.
func (b *backoffSchedule) observeSession(d, stable time.Duration) {
	if stable > 0 && d >= stable {
		b.current = b.min
	}
}

// next returns the wait before the next dial attempt and escalates.
func (b *backoffSchedule) next() time.Duration {
	wait := b.current
	next := b.current * 2
	if next > b.max {
		next = b.max
	}
	b.current = next
	return wait
}

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
// exponential backoff (2s -> 30s) keeping the last known data. The backoff
// escalates while sessions keep failing and resets only after a stable one.
func (s *Service) runLoop(client marketClient) {
	defer s.wg.Done()
	schedule := newBackoffSchedule(s.backoffMin, s.backoffMax)
	stableThreshold := s.stableSession
	if stableThreshold <= 0 {
		stableThreshold = stableSessionDuration
	}
	for {
		if s.ctx.Err() != nil {
			return
		}
		sessionStart := time.Now()
		err := s.runOnce(client)
		if s.ctx.Err() != nil {
			return
		}
		s.setSessionState(false, reconnectMessage(err))
		schedule.observeSession(time.Since(sessionStart), stableThreshold)
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-time.After(schedule.next()):
			}
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
	// The server reports a Windows zone name; the contract exposes IANA (§6).
	// Normalize it before the catalog work so the exchange time zone is right
	// even when the dictionaries are not ready yet.
	if zone := exchangeZoneName(client.ServerTZ()); zone != "" {
		s.mu.Lock()
		s.exchangeTZ = zone
		s.status.ExchangeTZ = zone
		s.mu.Unlock()
	}
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
	s.mu.RLock()
	zone := s.exchangeTZ
	s.mu.RUnlock()
	instant, ok := serverTimeFromWall(serverWall, zone)
	if !ok {
		// Unknown exchange zone: fall back to the local UTC clock instead of
		// mislabelling the server wall clock as UTC (a wrong instant is worse
		// than a small local-clock skew).
		instant = time.Now().UTC()
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

// historyFlight is one in-flight live history request shared by every waiter of
// the same symbol+period. native.Client rejects a second concurrent History for
// the same series ("history request is already in flight"), so the feed must
// deduplicate: the graph and a backtest/agent call often ask concurrently.
type historyFlight struct {
	done    chan struct{}
	history History
	count   int
	err     error
}

// liveCandles serves a candle series from native.Client with a TTL cache and
// single-flight deduplication of concurrent requests.
func (s *Service) liveCandles(ctx context.Context, symbol string, periodSec, count int) (History, error) {
	if _, ok := s.Instrument(symbol); !ok {
		return History{}, notFound(symbol)
	}
	key := symbol + "|" + strconv.Itoa(periodSec)
	for {
		if history, ok := s.cachedHistory(key, count); ok {
			return history, nil
		}
		client := s.currentClient()
		if client == nil {
			return History{}, &Error{Code: CodeUnavailable, Message: "нет соединения с сервером биржи"}
		}
		flight, leader := s.beginHistoryFlight(key)
		if !leader {
			select {
			case <-flight.done:
			case <-ctx.Done():
				return History{}, &Error{Code: CodeUnavailable, Message: "история: превышено время ожидания ответа биржи"}
			}
			if flight.err != nil {
				return History{}, flight.err
			}
			if flight.count >= count {
				return trimHistory(flight.history, count), nil
			}
			// The shared fetch is shorter than this caller needs: retry as the
			// next leader with the larger count.
			continue
		}
		history, err := s.fetchHistory(ctx, client, symbol, periodSec, count)
		if err != nil {
			s.endHistoryFlight(key, flight, History{}, 0, err)
			return History{}, err
		}
		s.mu.Lock()
		s.historyCache[key] = &historyEntry{history: history, expireAt: time.Now().Add(s.cfg.HistoryTTL), count: count}
		s.mu.Unlock()
		s.endHistoryFlight(key, flight, history, count, nil)
		return trimHistory(history, count), nil
	}
}

// cachedHistory returns a cached series that satisfies count.
func (s *Service) cachedHistory(key string, count int) (History, bool) {
	now := time.Now()
	s.mu.RLock()
	entry := s.historyCache[key]
	s.mu.RUnlock()
	// A cached series satisfies requests up to the count it was fetched with;
	// a larger request must re-fetch, otherwise the backtest would silently get
	// the short series of an earlier endpoint call.
	if entry != nil && now.Before(entry.expireAt) && count <= entry.count {
		return trimHistory(entry.history, count), true
	}
	return History{}, false
}

// fetchHistory requests one candle series from the exchange.
func (s *Service) fetchHistory(ctx context.Context, client marketClient, symbol string, periodSec, count int) (History, error) {
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
	return historyFromCandles(candles, symbol, periodSec, "с сервера биржи"), nil
}

// beginHistoryFlight joins the in-flight request of a series or becomes its
// leader.
func (s *Service) beginHistoryFlight(key string) (*historyFlight, bool) {
	s.flightMu.Lock()
	defer s.flightMu.Unlock()
	if s.flights == nil {
		s.flights = map[string]*historyFlight{}
	}
	if flight, ok := s.flights[key]; ok {
		return flight, false
	}
	flight := &historyFlight{done: make(chan struct{})}
	s.flights[key] = flight
	return flight, true
}

// endHistoryFlight publishes the shared result and wakes every waiter.
func (s *Service) endHistoryFlight(key string, flight *historyFlight, history History, count int, err error) {
	s.flightMu.Lock()
	defer s.flightMu.Unlock()
	flight.history, flight.count, flight.err = history, count, err
	delete(s.flights, key)
	close(flight.done)
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
