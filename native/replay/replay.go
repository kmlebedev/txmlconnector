// Package replay replays a recorded TRANSAQ session to a client, so the client
// can be exercised against the traffic a real server produced. It is the Go
// port of tools/re/py/replay_server.py, the reference implementation of
// procedure RE-3 in tools/re/README.md.
//
// The package is public rather than under native/internal because the
// tools/replay-server command lives outside the native/ tree, and Go forbids
// importing an internal package from there; see the same trade-off for the
// shared test server in docs/deepseek/task-index.md §8 п.2.
//
// The recorded session is the output of tools/wire-decode -payload -1: one
// frame per line, "<seconds> <conn> C>S|S>C #<n> flags=0x<f> type=<t>
// len=<n> <hex payload>"; lines starting with '#' are comments and a zero
// payload has no hex field. Only the server replies are replayed, the requests
// are decoded from the live client.
package replay

import (
	"bufio"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// Injection replaces the recorded RQ_MAIN stream after the client sends the
// request of type AfterRequest. It is the RE-3 oracle hook: the replayed login,
// dictionaries and snapshot keep the client working, and the injected records
// then show what the client (or the DLL) makes of them.
type Injection struct {
	// AfterRequest is the request type that arms the injection; the replay
	// server defaults it to RQ_SUBSCRIBE, because the DLL subscribes before the
	// records of interest arrive. Zero disables the injection.
	AfterRequest byte
	// Payload is the RQ_MAIN reply body to inject: a sequence of tagged
	// records, as stored in the .hex fixtures. The first RQ_MAIN request after
	// AfterRequest gets it, later ones get an empty reply.
	Payload []byte
}

// reply is one recorded server reply.
type reply struct {
	flags   byte
	payload []byte
}

// Session is the server side of one recorded TRANSAQ session.
type Session struct {
	replies map[byte][]reply
	keys    *wire.Keys
}

// Load parses a capture decoded by wire-decode -payload -1 and unlocks the
// session key with the password of the recorded account. The dump must contain
// the RQ_PWD reply: the session key is wrapped with the password key, so a
// session recorded with another password cannot be replayed.
func Load(r io.Reader, password string) (*Session, error) {
	if password == "" {
		return nil, errors.New("replay: the account password is required to unwrap the session key")
	}
	replies, err := parseDump(r)
	if err != nil {
		return nil, err
	}
	pwd := replies[session.RqPwd]
	if len(pwd) == 0 {
		return nil, errors.New("replay: dump has no RQ_PWD reply; is it wire-decode -payload -1 output?")
	}
	_, keys, err := session.SessionKeys(password, pwd[0].payload)
	if err != nil {
		return nil, fmt.Errorf("replay: session key: %w", err)
	}
	return &Session{replies: replies, keys: keys}, nil
}

// parseDump groups the recorded server replies by request type, keeping the
// recorded order within a type.
func parseDump(r io.Reader) (map[byte][]reply, error) {
	replies := make(map[byte][]reply)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<22)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue // a comment or a raw capture line, not a wire-decode frame
		}
		if fields[2] != "S>C" {
			continue // only server replies are replayed
		}
		flags, err := parseHexField(fields[4], "flags")
		if err != nil {
			return nil, fmt.Errorf("replay: line %d: %w", n, err)
		}
		typ, err := parseDecField(fields[5], "type")
		if err != nil {
			return nil, fmt.Errorf("replay: line %d: %w", n, err)
		}
		if typ > 0xff {
			return nil, fmt.Errorf("replay: line %d: request type %d does not fit a byte", n, typ)
		}
		size, err := parseDecField(fields[6], "len")
		if err != nil {
			return nil, fmt.Errorf("replay: line %d: %w", n, err)
		}
		var payload []byte
		if len(fields) > 7 {
			if strings.HasSuffix(fields[7], "…") {
				return nil, fmt.Errorf("replay: line %d: payload is truncated; decode the capture with wire-decode -payload -1", n)
			}
			if payload, err = hex.DecodeString(fields[7]); err != nil {
				return nil, fmt.Errorf("replay: line %d: bad payload hex: %w", n, err)
			}
		}
		if uint64(len(payload)) != size {
			return nil, fmt.Errorf("replay: line %d: payload is %d bytes, len says %d", n, len(payload), size)
		}
		replies[byte(typ)] = append(replies[byte(typ)], reply{flags: byte(flags), payload: payload})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("replay: read dump: %w", err)
	}
	return replies, nil
}

// parseHexField parses "<name>=0x<hex>", the format wire-decode uses for flags.
func parseHexField(s, name string) (uint64, error) {
	v, ok := strings.CutPrefix(s, name+"=")
	if !ok {
		return 0, fmt.Errorf("want %s=0x<hex>, got %q", name, s)
	}
	v = strings.TrimPrefix(v, "0x")
	return strconv.ParseUint(v, 16, 8)
}

// parseDecField parses "<name>=<decimal>", the format wire-decode uses for the
// request type and payload length.
func parseDecField(s, name string) (uint64, error) {
	v, ok := strings.CutPrefix(s, name+"=")
	if !ok {
		return 0, fmt.Errorf("want %s=<number>, got %q", name, s)
	}
	return strconv.ParseUint(v, 10, 64)
}

// Serve plays the session over nc until the client disconnects or the
// connection fails. Requests are decoded with the default keys until RQ_PWD,
// then with the recorded session keys. Replies recorded for a request type are
// sent in their recorded order; once they are exhausted the client gets an
// empty reply. RQ_LOGOUT is answered with a close, as the server does.
//
// Serve keeps its state per call, so the same Session may serve several
// connections, each time from the start of the recording.
func (s *Session) Serve(nc net.Conn, inject Injection) error {
	defer nc.Close()
	keys := wire.DefaultKeys()
	next := make(map[byte]int, len(s.replies))
	armed, injected := false, false
	for {
		req, err := keys.Decode(wire.ClientToServer, nc)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		if req.Type == session.RqLogout {
			return nil
		}
		if inject.AfterRequest != 0 && req.Type == inject.AfterRequest {
			armed = true
		}
		var flags byte
		var payload []byte
		switch {
		case req.Type == session.RqMain && armed:
			flags = wire.KindReply | wire.FlagPlain
			if !injected {
				payload, injected = inject.Payload, true
			}
		case next[req.Type] < len(s.replies[req.Type]):
			rec := s.replies[req.Type][next[req.Type]]
			next[req.Type]++
			flags, payload = rec.flags, rec.payload
		default:
			flags = wire.KindReply | wire.FlagPlain
		}
		out, err := keys.Encode(wire.ServerToClient, wire.Frame{Flags: flags, Type: req.Type, Payload: payload})
		if err != nil {
			return err
		}
		if _, err := nc.Write(out); err != nil {
			return err
		}
		if req.Type == session.RqPwd {
			keys = s.keys
		}
	}
}
