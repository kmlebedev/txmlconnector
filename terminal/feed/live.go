package feed

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kmlebedev/txmlconnector/native"
	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/terminal/backtest"
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
	OldNews(ctx context.Context, count int) error
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
	// newsPollInterval is how often the live feed asks get_old_news: the news
	// feed has no subscription on the wire (docs/protocol/news.md).
	newsPollInterval = 30 * time.Second
	// newsPollCount is the get_old_news count (the protocol maximum is 100).
	newsPollCount = 100
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
	// A new session owns a fresh order book: after a reconnect the exchange
	// sends a new snapshot, and applying deltas to the stale ladder would
	// resurrect levels that are gone.
	s.resetBooks()
	if err := s.subscribeAll(s.ctx, client); err != nil {
		return err
	}
	s.setSessionState(true, "подключено к серверу биржи")
	// The news feed is request-driven, so it is polled for this session only.
	newsCtx, cancelNews := context.WithCancel(s.ctx)
	defer cancelNews()
	s.wg.Add(1)
	go s.newsLoop(newsCtx, client)
	return client.Run(s.ctx, native.RunOptions{}, func(events []model.Event) error {
		s.applyEvents(events)
		return nil
	})
}

// newsLoop requests the news headers of the live session: the headers arrive
// through the Run callback, the loop only sends get_old_news.
func (s *Service) newsLoop(ctx context.Context, client marketClient) {
	defer s.wg.Done()
	fetch := func() {
		if ctx.Err() != nil {
			return
		}
		// Best effort: a news failure must not break the market feed.
		_ = client.OldNews(ctx, newsPollCount)
	}
	fetch()
	ticker := time.NewTicker(newsPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fetch()
		}
	}
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
		case *model.NewsHeader:
			s.applyNewsHeader(ev)
		case *model.NewsBody:
			s.applyNewsBody(ev)
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

// maxBookDepth bounds one side of the persistent live ladder: deeper levels are
// dropped so a symbol's book cannot grow without bound. The API still reports at
// most maxBookLevels (feed.go), the best levels of the side.
const maxBookDepth = 4 * maxBookLevels

// applyQuotes merges one <quotes> callback into the persistent per-symbol ladder
// and publishes the resulting snapshot. The demo server sends one full snapshot
// and then incremental deltas (PR 67 live acceptance): a positive quantity
// upserts a level. A negative quantity (-1) is a level deletion, but its price
// cannot be recovered from the pinned wire encoding yet (see mergeQuotes), so it
// is ignored until RE resolves the deletion encoding. Replacing the whole book
// with every callback left it one-sided.
func (s *Service) applyQuotes(ev *model.Quotes) {
	key := ev.Symbol.String()
	now := time.Now().UTC()
	s.mu.Lock()
	inst := s.instruments[key]
	if inst == nil {
		s.mu.Unlock()
		return
	}
	book := mergeQuotes(s.books[key], ev, inst.Last, now)
	s.books[key] = book
	inst.UpdatedAt = now
	s.status.LastEventAt = now
	s.mu.Unlock()
	s.publish(Event{Type: "orderbook", Data: orderBookSnapshot(book)})
}

// mergeQuotes applies the deltas of one <quotes> callback to the previous
// ladder and returns a new best-first snapshot. The previous ladder is not
// mutated, so snapshots already handed to readers and subscribers stay valid.
func mergeQuotes(prev OrderBook, ev *model.Quotes, last float64, now time.Time) OrderBook {
	bids := ladderSide(prev.Bids)
	asks := ladderSide(prev.Asks)
	for _, level := range ev.Levels {
		// Deletions (-1) are not applied: native/records/quote.go stores the
		// accumulated wire price in model.Quote.Price, and for the pinned
		// deletion encoding that accumulated value is < 1 (Price: 0 in
		// native/records/quote_test.go). Deleting by such a price is a no-op,
		// and the real level price is not recoverable without a live capture /
		// RE of the deletion encoding. Live deletions are I; the demo server
		// sent none during the 2026-11-06 acceptance probe.
		price := level.Price.Float64()
		if level.Buy > 0 {
			bids[price] = level.Buy
		}
		if level.Sell > 0 {
			asks[price] = level.Sell
		}
	}
	return OrderBook{
		Symbol: ev.Symbol.String(),
		Last:   last,
		Time:   now,
		Bids:   sortedLevels(bids, true),
		Asks:   sortedLevels(asks, false),
		Source: "live",
	}
}

// orderBookFromQuotes maps one <quotes> payload to a fresh price ladder (the
// empty-ladder form of mergeQuotes). Kept as the mapper used by the feed tests;
// production code merges into the persistent ladder via mergeQuotes.
func orderBookFromQuotes(ev *model.Quotes, last float64, now time.Time) OrderBook {
	return mergeQuotes(OrderBook{}, ev, last, now)
}

// ladderSide converts a stored ladder side to a price -> quantity map.
func ladderSide(levels []Level) map[float64]int64 {
	side := make(map[float64]int64, len(levels))
	for _, level := range levels {
		side[level.Price] = int64(level.Qty)
	}
	return side
}

// sortedLevels converts a ladder side to the contract order (bids by descending
// price, asks by ascending) and clamps it to maxBookDepth.
func sortedLevels(side map[float64]int64, descending bool) []Level {
	levels := make([]Level, 0, len(side))
	for price, qty := range side {
		levels = append(levels, Level{Price: price, Qty: float64(qty)})
	}
	sort.Slice(levels, func(i, j int) bool {
		if descending {
			return levels[i].Price > levels[j].Price
		}
		return levels[i].Price < levels[j].Price
	})
	if len(levels) > maxBookDepth {
		levels = levels[:maxBookDepth]
	}
	return levels
}

// orderBookSnapshot deep-copies a stored ladder and clamps it to the API depth,
// so the published/returned book shares no memory with the live state.
func orderBookSnapshot(book OrderBook) OrderBook {
	out := copyOrderBook(book)
	out.Bids = limitLevels(out.Bids)
	out.Asks = limitLevels(out.Asks)
	return out
}

// resetBooks drops the live ladders of the finished session. The next session
// starts from its own full snapshot.
func (s *Service) resetBooks() {
	s.mu.Lock()
	s.books = map[string]OrderBook{}
	s.mu.Unlock()
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

// applyNewsHeader stores one news header and publishes the news lenta event.
// get_old_news is polled repeatedly, so an unchanged story is not re-published;
// a correction keeps the original FirstSeen.
func (s *Service) applyNewsHeader(ev *model.NewsHeader) {
	if ev == nil || ev.ID == 0 {
		return
	}
	now := time.Now().UTC()
	s.mu.Lock()
	item, known := s.news[ev.ID]
	changed := !known
	if !known {
		item = News{ID: ev.ID, FirstSeen: now}
		s.newsOrder = append(s.newsOrder, ev.ID)
		if len(s.newsOrder) > maxNews {
			delete(s.news, s.newsOrder[0])
			s.newsOrder = append([]int64(nil), s.newsOrder[1:]...)
		}
	}
	published := ev.Time.UTC()
	if item.Published != published || item.Source != ev.Source || item.Title != ev.Title {
		changed = true
	}
	item.Published, item.Source, item.Title = published, ev.Source, ev.Title
	s.news[ev.ID] = item
	s.status.LastEventAt = now
	s.mu.Unlock()
	if changed {
		s.publish(Event{Type: "news", Data: item})
	}
}

// applyNewsBody attaches the body of a known news and publishes the update; the
// FirstSeen of the story never moves.
func (s *Service) applyNewsBody(ev *model.NewsBody) {
	if ev == nil {
		return
	}
	now := time.Now().UTC()
	s.mu.Lock()
	item, ok := s.news[ev.ID]
	if !ok {
		s.mu.Unlock()
		return
	}
	item.Body = ev.Text
	s.news[ev.ID] = item
	s.status.LastEventAt = now
	s.mu.Unlock()
	s.publish(Event{Type: "news", Data: item})
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
	kindsLoaded := len(s.candleKinds) > 0
	s.mu.RUnlock()
	if !ok {
		if !kindsLoaded {
			// <candlekinds> of the session has not arrived yet (the catalog is
			// refreshed after connect/reconnect): the period may well be valid,
			// so this is a temporary unavailability, not a client bad_period.
			return History{}, &Error{
				Code:    CodeUnavailable,
				Message: "справочник периодов ещё не загружен: повторите запрос после загрузки словарей сессии",
			}
		}
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
	return orderBookSnapshot(book), nil
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
	if len(candles.Warnings) > 0 {
		history.Warnings = append([]string(nil), candles.Warnings...)
	}
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

// ---- bot closed-bar stream (plan Task 22) --------------------------------

// barCloseGrace is the "interval boundary + 2s" grace of the closed-bar timer:
// the exchange can publish the last fill of an interval slightly late, so the
// bar is requested only after the grace period (status I of the plan).
const barCloseGrace = 2 * time.Second

// History implements bot.BarStream: the closed candle series of one instrument
// as backtest bars. It works in both modes (offline serves the seed).
func (s *Service) History(ctx context.Context, symbol string, periodSec, count int) ([]backtest.Bar, error) {
	history, err := s.Candles(ctx, symbol, periodSec, count)
	if err != nil {
		return nil, err
	}
	return barsFromHistory(history), nil
}

// SubscribeClosedBars implements bot.BarStream: it streams the closed candles of
// one series exactly once, in order. A bar is emitted by the first quote of the
// next interval or by the boundary timer (+2s), whichever comes first; after a
// reconnect the missed bars are backfilled from the history without duplicates.
func (s *Service) SubscribeClosedBars(ctx context.Context, symbol string, periodSec int) (<-chan backtest.Bar, error) {
	if _, ok := s.Instrument(symbol); !ok {
		return nil, notFound(symbol)
	}
	if !ValidPeriod(periodSec) {
		return nil, periodError(periodSec)
	}
	if s.mode != ModeLive {
		return nil, &Error{Code: CodeUnavailable, Message: "поток закрытых свечей доступен только в live-режиме"}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	out := make(chan backtest.Bar, 16)
	go s.streamClosedBars(ctx, symbol, periodSec, out)
	return out, nil
}

// streamClosedBars is the closed-bar producer of one subscription.
func (s *Service) streamClosedBars(ctx context.Context, symbol string, periodSec int, out chan<- backtest.Bar) {
	defer close(out)
	events := make(chan Event, 64)
	cancel := s.Subscribe(func(ev Event) {
		if ev.Type != "quote" && ev.Type != "status" {
			return
		}
		select {
		case events <- ev:
		default:
			// The consumer is slower than the feed: the next tick/timer still
			// triggers the history fetch, so dropping a tick is safe.
		}
	})
	defer cancel()

	// Seed the last emitted bar from the history, so the subscription starts
	// with the current interval instead of replaying the whole series.
	var last time.Time
	if bars, err := s.closedBacktestBars(ctx, symbol, periodSec, 3); err == nil {
		cutoff := barIntervalStart(time.Now().UTC(), periodSec)
		for _, bar := range bars {
			if !bar.Partial && bar.Time.Before(cutoff) && bar.Time.After(last) {
				last = bar.Time
			}
		}
	}
	timer := time.NewTimer(nextBarCloseDelay(time.Now().UTC(), periodSec))
	defer timer.Stop()

	emit := func(now time.Time) {
		cutoff := barIntervalStart(now, periodSec)
		count := 4
		if !last.IsZero() && cutoff.After(last) {
			missed := int(cutoff.Sub(last)/(time.Duration(periodSec)*time.Second)) + 2
			if missed > count {
				count = missed
			}
		}
		if count > 500 {
			count = 500
		}
		bars, err := s.closedBacktestBars(ctx, symbol, periodSec, count)
		if err != nil {
			return
		}
		closed := make([]backtest.Bar, 0, len(bars))
		for _, bar := range bars {
			if !bar.Partial && bar.Time.Before(cutoff) {
				closed = append(closed, bar)
			}
		}
		if last.IsZero() {
			// No seed history: start from the newest closed bar instead of
			// replaying the series.
			if len(closed) > 0 {
				last = closed[len(closed)-1].Time
			}
			return
		}
		for _, bar := range closed {
			if !bar.Time.After(last) {
				continue
			}
			select {
			case out <- bar:
				last = bar.Time
			case <-ctx.Done():
				return
			}
		}
	}

	connected := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			emit(time.Now().UTC())
			timer.Reset(nextBarCloseDelay(time.Now().UTC(), periodSec))
		case ev := <-events:
			switch ev.Type {
			case "status":
				status, ok := ev.Data.(Status)
				if !ok {
					continue
				}
				if !status.Connected {
					connected = false
					continue
				}
				if !connected {
					// A (re)connection: backfill the bars missed while offline.
					connected = true
					emit(time.Now().UTC())
				}
			case "quote":
				quote, ok := ev.Data.(Quote)
				if !ok || !strings.EqualFold(quote.Symbol, symbol) {
					continue
				}
				tickAt := quote.Time
				if tickAt.IsZero() {
					tickAt = time.Now().UTC()
				}
				if !last.IsZero() && !barIntervalStart(tickAt, periodSec).After(barIntervalStart(last, periodSec)) {
					continue
				}
				emit(tickAt)
			}
		}
	}
}

// closedBacktestBars fetches the last closed bars directly from the exchange
// connection, bypassing the TTL history cache: a bot must see a closed bar as
// soon as the exchange publishes it.
func (s *Service) closedBacktestBars(ctx context.Context, symbol string, periodSec, count int) ([]backtest.Bar, error) {
	client := s.currentClient()
	if client == nil {
		return nil, &Error{Code: CodeUnavailable, Message: "нет соединения с сервером биржи"}
	}
	history, err := s.fetchHistory(ctx, client, symbol, periodSec, count)
	if err != nil {
		return nil, err
	}
	return barsFromHistory(history), nil
}

// barsFromHistory converts the feed series into backtest bars.
func barsFromHistory(history History) []backtest.Bar {
	bars := make([]backtest.Bar, 0, len(history.Bars))
	for _, bar := range history.Bars {
		bars = append(bars, backtest.Bar{
			Time:   bar.Time.UTC(),
			Open:   bar.Open,
			High:   bar.High,
			Low:    bar.Low,
			Close:  bar.Close,
			Volume: bar.Volume,
		})
	}
	return bars
}

// barIntervalStart returns the UTC start of the candle interval containing t.
func barIntervalStart(t time.Time, periodSec int) time.Time {
	if periodSec <= 0 {
		return t.UTC()
	}
	secs := int64(periodSec)
	return time.Unix((t.UTC().Unix()/secs)*secs, 0).UTC()
}

// nextBarCloseDelay returns the duration until the next interval boundary plus
// the close grace period.
func nextBarCloseDelay(now time.Time, periodSec int) time.Duration {
	period := time.Duration(periodSec) * time.Second
	next := barIntervalStart(now, periodSec).Add(period + barCloseGrace)
	if !next.After(now) {
		next = next.Add(period)
	}
	return time.Until(next)
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
