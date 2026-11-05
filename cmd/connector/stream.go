package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/kmlebedev/txmlconnector/cmd/connector/internal/dlllog"
	"github.com/kmlebedev/txmlconnector/native"
)

// tradeStreams writes the _ts.log records of the TCP connections (O: header,
// "Connecting to host:port buf=65512...", "Connected.", "Connection error #8,
// WSAE:N.", "Trade Stream Closed..."). The DLL counters are S:bytes/requests
// and R:bytes/replies; here they are bytes and Write/Read calls (I).
type tradeStreams struct {
	log      *dlllog.Logger
	mu       sync.Mutex
	loggedIn bool // the current stream passed RQ_PWD
}

const streamBuffer = 65512 // O: "buf=65512"

func (s *tradeStreams) setLoggedIn(v bool) {
	s.mu.Lock()
	s.loggedIn = v
	s.mu.Unlock()
}

func (s *tradeStreams) dial(ctx context.Context, address string) (net.Conn, error) {
	s.setLoggedIn(false)
	s.log.TSStart()
	s.log.TS(fmt.Sprintf("Connecting to %s buf=%d...", address, streamBuffer))
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", address)
	if err != nil {
		s.log.TS(fmt.Sprintf("Connection error #8, WSAE:%d.", wsaCode(err)))
		s.log.TS("Trade Stream Closed. Reason: 'Connect/Login failed'. S:0/0, R:0/0, E:0, Rl:0")
		return nil, err
	}
	s.log.TS("Connected.")
	return &countingConn{Conn: conn, streams: s}, nil
}

// wsaCode maps a dial error to the Winsock code the DLL prints (O: refused is
// 10061; the other codes are the Winsock equivalents, I).
func wsaCode(err error) int {
	var errno syscall.Errno
	if runtime.GOOS == "windows" && errors.As(err, &errno) && errno >= 10000 {
		return int(errno) // already the Winsock code
	}
	var dnsErr *net.DNSError
	switch {
	case errors.As(err, &dnsErr):
		return 11001 // WSAHOST_NOT_FOUND
	case errors.Is(err, syscall.ECONNREFUSED):
		return 10061
	case errors.Is(err, os.ErrDeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
		return 10060
	case errors.Is(err, syscall.EHOSTUNREACH):
		return 10065
	case errors.Is(err, syscall.ENETUNREACH):
		return 10051
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return 10060
	}
	return 10061
}

type countingConn struct {
	net.Conn
	streams        *tradeStreams
	sent, received atomic.Int64
	writes, reads  atomic.Int64
	closeOnce      sync.Once
}

func (c *countingConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.sent.Add(int64(n))
	c.writes.Add(1)
	return n, err
}

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.received.Add(int64(n))
		c.reads.Add(1)
	}
	return n, err
}

func (c *countingConn) Close() error {
	err := c.Conn.Close()
	c.closeOnce.Do(func() {
		c.streams.mu.Lock()
		loggedIn := c.streams.loggedIn
		c.streams.mu.Unlock()
		reason := ""
		if !loggedIn {
			reason = "Reason: 'Connect/Login failed'. "
		}
		c.streams.log.TS(fmt.Sprintf("Trade Stream Closed. %sS:%d/%d, R:%d/%d, E:0, Rl:0",
			reason, c.sent.Load(), c.writes.Load(), c.received.Load(), c.reads.Load()))
	})
	return err
}

// traceToLog renders a Connector lifecycle step as the DLL _dsp.log/_ts.log
// records (docs/protocol/logs.md).
func traceToLog(log *dlllog.Logger, streams *tradeStreams, ev native.TraceEvent) {
	thread := ""
	if ev.Worker {
		thread = "main"
	}
	switch ev.Kind {
	case native.TraceStateChange:
		log.Info(thread, "State <"+ev.From+"> changed to <"+ev.To+">")
	case native.TraceLoginStart:
		log.Info(thread, "Initializing dsp...")
		log.Info(thread, "Preparing for login...")
		log.Info(thread, "Packet size: 65520")
		log.Info(thread, "Logging in...")
	case native.TraceLoggedIn:
		streams.setLoggedIn(true)
		log.Info(thread, "Logged in")
		log.TS(fmt.Sprintf("Session timeout %d ms.", ev.SessionTimeout.Milliseconds()))
		log.TS(fmt.Sprintf("Request timeout %d ms.", ev.RequestTimeout.Milliseconds()))
		if info := ev.Info; info != nil {
			// SYS#/PRO#/BLD#: server value / connector value (O: 647, 78,
			// 2.26.4); the "=0" flags are I.
			log.TS(fmt.Sprintf("SYS# %d/647 PRO# %d/78=0 BLD# %d/2.26.4=0 SID# %d UID# %d",
				info.SysVersion, info.Protocol, info.Build, info.SID, info.UID))
			log.TS(fmt.Sprintf("FORTS Date:%d Current Date:%d", info.FortsDate, epochDays(time.Now())))
		}
	case native.TraceLoginFailed:
		log.Warning(thread, "Failed to login")
	case native.TraceLoadingStatic:
		log.Info(thread, "Loading static data...")
	case native.TraceLoadingSnapshot:
		log.Info(thread, "Loading snapshot...")
	case native.TraceReady:
		log.Info(thread, fmt.Sprintf("Main thread OK. Data refresh period: %d", ev.RqDelay.Milliseconds()))
	case native.TraceDisconnecting:
		log.Info(thread, "Disconnecting...")
	case native.TraceShutdown:
		log.Info(thread, "Shutdown event received")
	case native.TraceWaitWorker:
		log.Info(thread, "Waiting for WorkUp...")
	case native.TraceLoggingOut:
		log.Info(thread, "Logging out...")
	case native.TraceDisconnectOK:
		log.Info(thread, "Disconnect OK")
	}
}

// epochDays is the local date as days since 1970-01-01 (O: "Current
// Date:20762" on 2026-11-05).
func epochDays(t time.Time) int64 {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400
}
