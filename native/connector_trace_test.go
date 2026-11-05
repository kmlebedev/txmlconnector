package native

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

type traceRecorder struct {
	mu     sync.Mutex
	events []TraceEvent
}

func (r *traceRecorder) add(ev TraceEvent) {
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.mu.Unlock()
}

// lines renders the events compactly: kind name, the state change and the
// worker flag, so the order can be compared with the DLL _dsp.log sequence.
func (r *traceRecorder) lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.events))
	for _, ev := range r.events {
		s := ev.Kind.String()
		if ev.Kind == TraceStateChange {
			s += " " + ev.From + "->" + ev.To
		}
		if ev.Worker {
			s = "main " + s
		}
		out = append(out, s)
	}
	return out
}

func (r *traceRecorder) waitFor(t *testing.T, kind TraceKind) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		for _, ev := range r.events {
			if ev.Kind == kind {
				r.mu.Unlock()
				return
			}
		}
		r.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no trace event %v; got %v", kind, r.lines())
}

// The order follows the DLL _dsp.log of a demo session (O: RE-2 2026-11-05,
// docs/protocol/logs.md): connect, login, load, READY, then disconnect.
func TestConnectorTraceMatchesDLLSessionOrder(t *testing.T) {
	srv := newTestServer(t)
	tr := &traceRecorder{}
	c := New(Config{Dial: srv.Dial, Trace: tr.add})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	send(t, c, connectCommand())
	rec.next(t, `<server_status`)
	send(t, c, `<command id="disconnect"/>`)

	want := []string{
		"StateChange DISCONNECTED->CONNECTING",
		"main LoginStart",
		"main LoggedIn",
		"main StateChange CONNECTING->LOADING",
		"main LoadingStatic",
		"main LoadingSnapshot",
		"main StateChange LOADING->READY",
		"main Ready",
		"Disconnecting",
		"main Shutdown",
		"WaitWorker",
		"main StateChange READY->DISCONNECTING",
		"main LoggingOut",
		"main StateChange DISCONNECTING->DISCONNECTED",
		"DisconnectOK",
	}
	if got := tr.lines(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("trace:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	for _, ev := range tr.events {
		switch ev.Kind {
		case TraceLoggedIn:
			if ev.Info == nil || ev.RequestTimeout <= 0 || ev.SessionTimeout <= 0 {
				t.Fatalf("LoggedIn without session data: %+v", ev)
			}
		case TraceReady:
			if ev.RqDelay != 10*time.Millisecond {
				t.Fatalf("Ready rqdelay = %v, want 10ms", ev.RqDelay)
			}
		}
	}
}

// A refused TCP connection is a failed login in the DLL log (O: "Failed to
// login", CONNECTING -> DISCONNECTED), and disconnect without a connection
// still logs "Disconnecting..."/"Disconnect OK".
func TestConnectorTraceFailedLogin(t *testing.T) {
	refused := errors.New("connection refused")
	tr := &traceRecorder{}
	c := New(Config{Trace: tr.add, Dial: func(context.Context, string) (net.Conn, error) { return nil, refused }})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	send(t, c, connectCommand())
	rec.next(t, `<server_status connected="error"`)
	tr.waitFor(t, TraceStateChange) // CONNECTING
	send(t, c, `<command id="disconnect"/>`)

	want := []string{
		"StateChange DISCONNECTED->CONNECTING",
		"main LoginStart",
		"main LoginFailed",
		"main StateChange CONNECTING->DISCONNECTED",
		"Disconnecting",
		"DisconnectOK",
	}
	if got := tr.lines(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("trace:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	for _, ev := range tr.events {
		if ev.Kind == TraceLoginFailed && !errors.Is(ev.Err, refused) {
			t.Fatalf("LoginFailed err = %v", ev.Err)
		}
	}
}
