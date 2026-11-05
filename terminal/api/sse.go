package api

import (
	"encoding/json"
	"fmt"
	"net/http"
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

// onFeedEvent forwards feed events to the SSE clients. It runs on the feed
// goroutine, so it only performs non-blocking hub publications.
func (s *Server) onFeedEvent(ev feed.Event) {
	switch ev.Type {
	case "quote":
		if quote, ok := ev.Data.(feed.Quote); ok {
			s.hub.publish(event{Type: "quote", Data: mkQuoteEventData(quote)})
		}
	case "status":
		s.hub.publish(event{Type: "status", Data: s.statusEventData()})
	}
}

// onAgentEvent forwards agent events to the SSE clients.
func (s *Server) onAgentEvent(ev agent.Event) {
	s.hub.publish(event{Type: "agent", Data: ev})
}

// handleEvents serves the /api/events server-sent event stream.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, codeInternal, "SSE не поддерживается транспортом")
		return
	}
	header := w.Header()
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	header.Set("Connection", "keep-alive")
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

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
			if _, err := fmt.Fprintf(w, "event: message\ndata: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
