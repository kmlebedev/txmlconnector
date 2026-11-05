package native

import (
	"strconv"
	"time"

	"github.com/kmlebedev/txmlconnector/native/session"
)

// TraceKind is a connection-lifecycle step of the Connector. The steps mirror
// the DLL _dsp.log lines (docs/protocol/logs.md) so a log writer can render the
// same sequence; the Connector itself does not format log text.
type TraceKind uint8

const (
	TraceStateChange     TraceKind = iota + 1 // "State <From> changed to <To>"
	TraceLoginStart                           // "Initializing dsp...", "Preparing for login...", "Packet size", "Logging in..."
	TraceLoggedIn                             // "Logged in" (Info, RequestTimeout, SessionTimeout set)
	TraceLoginFailed                          // "Failed to login" (Err set)
	TraceLoadingStatic                        // "Loading static data..."
	TraceLoadingSnapshot                      // "Loading snapshot..."
	TraceReady                                // "Main thread OK. Data refresh period: N" (RqDelay set)
	TraceDisconnecting                        // "Disconnecting..."
	TraceShutdown                             // "Shutdown event received"
	TraceWaitWorker                           // "Waiting for WorkUp..."
	TraceLoggingOut                           // "Logging out..."
	TraceDisconnectOK                         // "Disconnect OK"
)

var traceNames = [...]string{
	TraceStateChange: "StateChange", TraceLoginStart: "LoginStart", TraceLoggedIn: "LoggedIn",
	TraceLoginFailed: "LoginFailed", TraceLoadingStatic: "LoadingStatic",
	TraceLoadingSnapshot: "LoadingSnapshot", TraceReady: "Ready", TraceDisconnecting: "Disconnecting",
	TraceShutdown: "Shutdown", TraceWaitWorker: "WaitWorker", TraceLoggingOut: "LoggingOut",
	TraceDisconnectOK: "DisconnectOK",
}

func (k TraceKind) String() string {
	if int(k) < len(traceNames) && traceNames[k] != "" {
		return traceNames[k]
	}
	return "TraceKind(" + strconv.Itoa(int(k)) + ")"
}

// DLL connection state names of the "State <A> changed to <B>" lines (O).
const (
	TraceStateDisconnected  = "DISCONNECTED"
	TraceStateConnecting    = "CONNECTING"
	TraceStateLoading       = "LOADING"
	TraceStateReady         = "READY"
	TraceStateDisconnecting = "DISCONNECTING"
)

// TraceEvent is one lifecycle step. Worker is true for steps the DLL logs from
// its connection thread ("[main]"); the others run on the caller's thread.
type TraceEvent struct {
	Kind     TraceKind
	Worker   bool
	From, To string // TraceStateChange

	Info           *session.Info // TraceLoggedIn
	RequestTimeout time.Duration // TraceLoggedIn: effective request timeout
	SessionTimeout time.Duration // TraceLoggedIn: effective session window
	RqDelay        time.Duration // TraceReady
	Err            error         // TraceLoginFailed
}

// trace reports ev to Config.Trace; it must not be called with c.mu held.
func (c *Connector) trace(ev TraceEvent) {
	if c.cfg.Trace != nil {
		c.cfg.Trace(ev)
	}
}

// traceState reports a state change and remembers the new state. Worker is
// the thread the DLL logs it from.
func (c *Connector) traceState(to string, worker bool) {
	c.mu.Lock()
	from := c.traceName
	c.traceName = to
	c.mu.Unlock()
	if from == "" {
		from = TraceStateDisconnected
	}
	if from == to {
		return
	}
	c.trace(TraceEvent{Kind: TraceStateChange, Worker: worker, From: from, To: to})
}
