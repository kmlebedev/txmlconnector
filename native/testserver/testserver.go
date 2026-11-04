// Package testserver speaks the server side of the TRANSAQ protocol to a
// native client: it accepts RQ_LOGIN/RQ_PWD, serves the dictionaries and the
// snapshot and answers RQ_MAIN with the queued payload. It is the shared test
// double of the native packages; it is public rather than native/internal so
// packages outside the native tree (server) can use it too, and it does not
// import native itself (that would close an import cycle through the native
// test package).
package testserver

import (
	"context"
	"encoding/hex"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/cryptoprofile"
	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// Login and Password are the credentials the server expects; the password also
// wraps the session key it issues in the RQ_PWD reply.
const (
	Login    = "TESTLOGIN"
	Password = "TESTPASS"
)

// rqTimeReply is the demo-server RQ_TIME reply: 2026-11-03 21:03:03, Russian
// Standard Time (bias -180 minutes).
var rqTimeReply = mustHex("74c0a4ae064cff0000c4ff00000000000000000000")

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// replyHandler overrides the default answer for one request type.
type replyHandler func(payload []byte) (flags byte, reply []byte)

// Server is the server side of one native session. Startup, Snapshot,
// AfterSubscribe and MainDelay are read when the session starts (Dial copies
// them into the serving goroutine), so set them before assigning Dial to a
// ClientConfig; use SetMainDelay to change the poll delay later.
type Server struct {
	// Startup is the RQ_STARTUP reply payload; the native client needs a valid
	// one, so tests pass a dictionary fixture.
	Startup []byte
	// Snapshot is the first RQ_MAIN reply; nil means an empty snapshot.
	Snapshot []byte
	// AfterSubscribe is queued for the first RQ_MAIN reply after RQ_SUBSCRIBE.
	AfterSubscribe []byte
	// MainDelay delays every RQ_MAIN reply.
	MainDelay time.Duration
	// SessionTimeout is the RQ_PWD session timeout in seconds; 0 means the
	// demo-server value of 120. The client uses it as the recovery limit.
	SessionTimeout int32
	// Password wraps the session key; New sets it to Password.
	Password string

	t testing.TB

	mu       sync.Mutex
	requests []wire.Frame
	handlers map[byte]replyHandler
	conns    []net.Conn
	logouts  int
	pending  []byte
	delay    time.Duration
}

// New returns a server that reports protocol errors through t.
func New(t testing.TB) *Server {
	return &Server{t: t, Password: Password, handlers: map[byte]replyHandler{}}
}

// Dial opens a loopback TCP connection to a fresh server-side session and
// serves it in the background. Assign it to native.ClientConfig.Dial.
func (s *Server) Dial(ctx context.Context, address string) (net.Conn, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	accepted := make(chan struct{})
	go func() {
		defer ln.Close()
		server, err := ln.Accept()
		if err != nil {
			close(accepted)
			return
		}
		// Register under the mutex before Dial returns: a Drop called right
		// after Dial then closes this connection too.
		s.mu.Lock()
		s.conns = append(s.conns, server)
		s.delay = s.MainDelay
		s.mu.Unlock()
		close(accepted)
		s.serve(server)
	}()
	var d net.Dialer
	client, err := d.DialContext(ctx, "tcp", ln.Addr().String())
	if err != nil {
		ln.Close() // unblock Accept and let the goroutine finish
		<-accepted
		return nil, err
	}
	<-accepted
	return client, nil
}

// Drop closes every live server connection, simulating a lost link.
func (s *Server) Drop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conns {
		c.Close()
	}
}

// SetMainDelay changes the delay applied to RQ_MAIN replies.
func (s *Server) SetMainDelay(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delay = d
}

// Handle sets a custom reply for a request type; fn gets the request payload
// and returns the reply flags and payload.
func (s *Server) Handle(reqType byte, fn func(payload []byte) (flags byte, reply []byte)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[reqType] = fn
}

// Requests returns the requests received so far, in wire order.
func (s *Server) Requests() []wire.Frame {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]wire.Frame, len(s.requests))
	for i, f := range s.requests {
		out[i] = wire.Frame{Flags: f.Flags, Type: f.Type, Payload: append([]byte(nil), f.Payload...)}
	}
	return out
}

// Logouts returns the number of RQ_LOGOUT requests seen.
func (s *Server) Logouts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logouts
}

func (s *Server) serve(nc net.Conn) {
	defer nc.Close()
	s.mu.Lock()
	snapshot, afterSubscribe, password := s.Snapshot, s.AfterSubscribe, s.Password
	sessionTimeout := s.SessionTimeout
	s.mu.Unlock()
	if sessionTimeout <= 0 {
		sessionTimeout = 120
	}

	keys := wire.DefaultKeys()
	pwKey, err := cryptoprofile.NewCipher(cryptoprofile.PasswordKey(password))
	if err != nil {
		s.t.Errorf("testserver: password key: %v", err)
		return
	}
	sessionKey := make([]byte, cryptoprofile.KeySize)
	for i := range sessionKey {
		sessionKey[i] = byte(0x40 + i)
	}
	sessionSync := [8]byte{0x70, 0x71, 0x72, 0x73, 0x74, 0x75, 0x76, 0x77}

	reply := func(flags, typ byte, payload []byte) error {
		out, err := keys.Encode(wire.ServerToClient, wire.Frame{Flags: flags, Type: typ, Payload: payload})
		if err != nil {
			return err
		}
		_, err = nc.Write(out)
		return err
	}
	for {
		f, err := keys.Decode(wire.ClientToServer, nc)
		if err != nil {
			return
		}
		s.mu.Lock()
		s.requests = append(s.requests, wire.Frame{Flags: f.Flags, Type: f.Type, Payload: append([]byte(nil), f.Payload...)})
		handler := s.handlers[f.Type]
		s.mu.Unlock()

		if handler != nil {
			flags, payload := handler(f.Payload)
			if err := reply(flags, f.Type, payload); err != nil {
				return
			}
			if f.Type == session.RqPwd {
				if keys, err = wire.NewKeys(sessionKey, sessionSync); err != nil {
					s.t.Errorf("testserver: session keys: %v", err)
					return
				}
			}
			continue
		}
		switch f.Type {
		case session.RqLogin:
			err = reply(wire.KindReply, f.Type, []byte{0, 8, 1, 2, 3, 4, 5, 6, 7, 8})
		case session.RqPwd:
			var w wire.Writer
			w.Byte(0)
			encKey := make([]byte, len(sessionKey))
			pwKey.CFBEncrypt(cryptoprofile.DefaultSync, encKey, sessionKey)
			encSync := make([]byte, 8)
			pwKey.CFBEncrypt(cryptoprofile.DefaultSync, encSync, sessionSync[:])
			w.Raw(encKey)
			w.Raw(encSync)
			// Byte1..3, SessionTimeout, Byte4..7, Int1
			w.Raw([]byte{0, 0, 0})
			w.Int32(sessionTimeout)
			w.Raw([]byte{0, 0, 0, 0})
			w.Int32(0)
			// Flags, Byte8, SubBroker, Text1, Byte9, Text2, Text3, Byte10
			w.Raw([]byte{0, 0, 0, 0, 0, 0, 0, 0})
			w.Int32(4242) // UID
			w.Byte(0)     // Byte11
			w.Byte(7)     // SID
			w.Raw([]byte{0, 0})
			w.Int32(626) // SYS#
			w.Int32(4)   // BLD#
			if err = reply(wire.KindReply, f.Type, w.Bytes()); err == nil {
				keys, err = wire.NewKeys(sessionKey, sessionSync)
			}
		case session.RqTime:
			err = reply(wire.KindReply, f.Type, rqTimeReply)
		case session.RqStartup:
			s.mu.Lock()
			payload := s.Startup
			s.mu.Unlock()
			err = reply(wire.KindReply, f.Type, payload)
		case session.RqMain:
			s.mu.Lock()
			payload := snapshot
			snapshot = nil
			if payload == nil {
				payload, s.pending = s.pending, nil
			}
			delay := s.delay
			s.mu.Unlock()
			time.Sleep(delay)
			err = reply(wire.KindReply, f.Type, payload)
		case session.RqSubscribe:
			s.mu.Lock()
			s.pending, afterSubscribe = afterSubscribe, nil
			s.mu.Unlock()
			err = reply(wire.KindReply, f.Type, nil)
		case session.RqLogout:
			s.mu.Lock()
			s.logouts++
			s.mu.Unlock()
			return
		default:
			s.t.Errorf("testserver: unexpected request type %d", f.Type)
			return
		}
		if err != nil {
			return
		}
	}
}
