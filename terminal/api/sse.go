package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sync"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/agent"
	"github.com/kmlebedev/txmlconnector/terminal/feed"
	"github.com/kmlebedev/txmlconnector/terminal/paper"
)

// event is one SSE message: {"type":"...","data":...}.
type event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// hub fans events out to the SSE clients. Publishing is non-blocking: a client
// whose 256-slot queue is full is dropped instead of stalling the feed.
type hub struct {
	mu   sync.Mutex
	subs map[chan event]struct{}
}

// newHub builds an empty event hub.
func newHub() *hub {
	return &hub{subs: map[chan event]struct{}{}}
}

// subscribe registers one client and returns its channel plus a cancel func.
func (h *hub) subscribe() (chan event, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan event, hubBuffer)
	h.subs[ch] = struct{}{}
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			h.mu.Lock()
			if _, ok := h.subs[ch]; ok {
				delete(h.subs, ch)
				close(ch)
			}
			h.mu.Unlock()
		})
	}
	return ch, cancel
}

// publish delivers ev to every subscriber, dropping slow clients.
func (h *hub) publish(ev event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- ev:
		default:
			// The client cannot keep up: drop it (its channel closes, the SSE
			// handler returns, the browser reconnects).
			delete(h.subs, ch)
			close(ch)
		}
	}
}

// closeAll closes every client channel (server shutdown).
func (h *hub) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		delete(h.subs, ch)
		close(ch)
	}
}

// size reports the number of connected clients (tests).
func (h *hub) size() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// eventLoop publishes the periodic SSE state (§10.3): quotes from the feed
// snapshot every second, portfolio every second, session status every 5s and a
// heartbeat every 15s. Feed and agent events are forwarded as they arrive.
func (s *Server) eventLoop() {
	defer s.wg.Done()
	quoteTick := time.NewTicker(s.quoteInterval)
	statusTick := time.NewTicker(s.statusInterval)
	heartbeatTick := time.NewTicker(s.heartbeatInterval)
	defer quoteTick.Stop()
	defer statusTick.Stop()
	defer heartbeatTick.Stop()
	if cancels := s.subscribeBackends(); len(cancels) > 0 {
		defer func() {
			for _, cancel := range cancels {
				cancel()
			}
		}()
	}
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-quoteTick.C:
			s.tickQuotes()
		case <-statusTick.C:
			s.tickStatus()
		case <-heartbeatTick.C:
			s.hub.publish(event{Type: "heartbeat", Data: struct{}{}})
		}
	}
}

// subscribeBackends hooks the feed and agent event streams into the SSE hub.
func (s *Server) subscribeBackends() []func() {
	var cancels []func()
	if s.deps.Feed != nil {
		cancels = append(cancels, s.deps.Feed.Subscribe(s.onFeedEvent))
	}
	if s.deps.Agent != nil {
		cancels = append(cancels, s.deps.Agent.Subscribe(s.onAgentEvent))
	}
	return cancels
}

// tickQuotes publishes the current quotes and drives resting paper limit orders
// against them.
func (s *Server) tickQuotes() {
	changed := false
	for _, inst := range s.deps.Feed.Instruments() {
		quote, ok := s.deps.Feed.Quote(inst.Symbol)
		if !ok || quote.Last == 0 {
			continue
		}
		s.hub.publish(event{Type: "quote", Data: mkQuoteEventData(quote)})
		if s.deps.Paper != nil {
			events := s.deps.Paper.OnQuote(paper.QuoteView{
				Symbol: quote.Symbol,
				Last:   quote.Last,
				Bid:    quote.Bid,
				Ask:    quote.Ask,
				Time:   quote.Time,
			})
			if len(events) > 0 {
				changed = true
			}
		}
	}
	if changed {
		s.publishPaperState()
		return
	}
	s.publishPortfolio()
}

// tickStatus publishes the session status every statusInterval.
func (s *Server) tickStatus() {
	s.hub.publish(event{Type: "status", Data: s.statusEventData()})
}

// statusEventData builds the SSE status payload.
func (s *Server) statusEventData() statusEventData {
	st := s.feedStatus()
	return statusEventData{
		OK:         true,
		Mode:       string(st.Mode),
		Connected:  st.Connected,
		Message:    st.Message,
		ServerTime: utc(st.ServerTime),
	}
}

// publishPortfolio publishes the current paper portfolio snapshot.
func (s *Server) publishPortfolio() {
	portfolio, ok := s.portfolio()
	if !ok {
		return
	}
	s.hub.publish(event{Type: "portfolio", Data: portfolio})
}

// publishPaperState publishes both order and portfolio changes after a paper
// order state transition.
func (s *Server) publishPaperState() {
	snapshot, ok := s.paperSnapshot()
	if !ok {
		return
	}
	orders := make([]orderDTO, 0, len(snapshot.Orders))
	for _, order := range snapshot.Orders {
		orders = append(orders, orderToDTO(order))
	}
	s.hub.publish(event{Type: "orders", Data: ordersResponse{Orders: orders}})
	s.publishPortfolio()
}

// newsEventData is the SSE "news" payload: the whole recorded story with the
// point-in-time (firstSeen) and the exchange publication instant, both RFC3339
// UTC like every API time (§10.2).
type newsEventData struct {
	ID        int64     `json:"id"`
	Published time.Time `json:"published"`
	FirstSeen time.Time `json:"firstSeen"`
	Source    string    `json:"source"`
	Title     string    `json:"title"`
	Body      string    `json:"body,omitempty"`
}

// mkNewsEventData maps a feed news story to the SSE payload.
func mkNewsEventData(news feed.News) newsEventData {
	return newsEventData{
		ID:        news.ID,
		Published: utc(news.Published),
		FirstSeen: utc(news.FirstSeen),
		Source:    news.Source,
		Title:     news.Title,
		Body:      news.Body,
	}
}

// onFeedEvent forwards feed events to the SSE clients. It runs on the feed
// goroutine, so it only performs non-blocking hub publications.
func (s *Server) onFeedEvent(ev feed.Event) {
	switch ev.Type {
	case "quote":
		if quote, ok := ev.Data.(feed.Quote); ok {
			s.hub.publish(event{Type: "quote", Data: mkQuoteEventData(quote)})
		}
	case "news":
		if news, ok := ev.Data.(feed.News); ok {
			s.hub.publish(event{Type: "news", Data: mkNewsEventData(news)})
		}
	case "status":
		s.hub.publish(event{Type: "status", Data: s.statusEventData()})
	}
}

// onAgentEvent forwards agent events to the SSE clients with the timestamps
// rendered as RFC3339 UTC (§10.2).
func (s *Server) onAgentEvent(ev agent.Event) {
	ev.Data = normalizeEventTimes(ev.Data)
	s.hub.publish(event{Type: "agent", Data: ev})
}

// timeType is the reflect type of time.Time used by normalizeEventTimes.
var timeType = reflect.TypeOf(time.Time{})

// normalizeEventTimes returns a copy of an agent event payload with every
// time.Time rendered in UTC. The agent engine uses the local clock
// (time.Now()), while every JSON time in the API must be RFC3339 UTC (§10.2);
// SSE "log"/"autonomy" events carry such times. Unknown/complex payloads are
// copied structurally, a deeply nested value is left as is at depth 8.
func normalizeEventTimes(value any) (out any) {
	if value == nil {
		return nil
	}
	// Defensive: this runs on the SSE event-loop goroutine, a reflection panic
	// must never crash the server — fall back to the original payload.
	defer func() {
		if recover() != nil {
			out = value
		}
	}()
	normalized := normalizeTimeValue(reflect.ValueOf(value), 0)
	if !normalized.IsValid() {
		return value
	}
	return normalized.Interface()
}

// normalizeTimeValue copies v and normalizes every reachable time.Time to UTC.
func normalizeTimeValue(v reflect.Value, depth int) reflect.Value {
	if !v.IsValid() || depth > 8 {
		return v
	}
	if v.Type() == timeType {
		return reflect.ValueOf(v.Interface().(time.Time).UTC())
	}
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(normalizeTimeValue(v.Elem(), depth+1))
		return out
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(normalizeTimeValue(v.Elem(), depth+1))
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for i := 0; i < v.NumField(); i++ {
			field := out.Field(i)
			if !field.CanSet() {
				continue
			}
			field.Set(normalizeTimeValue(field, depth+1))
		}
		return out
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(normalizeTimeValue(v.Index(i), depth+1))
		}
		return out
	case reflect.Array:
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(normalizeTimeValue(v.Index(i), depth+1))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(iter.Key(), normalizeTimeValue(iter.Value(), depth+1))
		}
		return out
	}
	return v
}

// handleEvents serves the /api/events server-sent event stream. Every write
// carries a fresh write deadline: a client that stopped reading must not pin
// the handler goroutine (and its connection) forever.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	header := w.Header()
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	header.Set("Connection", "keep-alive")
	header.Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	deadline := s.writeDeadline
	if deadline <= 0 {
		deadline = sseWriteTimeout
	}
	// write sends one already-encoded SSE frame under the write deadline.
	// It reports false when the connection is dead or the client is too slow.
	write := func(frame []byte) bool {
		_ = controller.SetWriteDeadline(time.Now().Add(deadline))
		if len(frame) > 0 {
			if _, err := w.Write(frame); err != nil {
				return false
			}
		}
		if err := controller.Flush(); err != nil {
			return false
		}
		return true
	}
	w.WriteHeader(http.StatusOK)
	if !write(nil) {
		return
	}
	ch, cancel := s.hub.subscribe()
	defer cancel()
	for {
		select {
		case <-r.Context().Done():
			return
		case message, open := <-ch:
			if !open {
				return
			}
			data, err := json.Marshal(message)
			if err != nil {
				continue
			}
			if !write([]byte(fmt.Sprintf("event: message\ndata: %s\n\n", data))) {
				return
			}
		}
	}
}
