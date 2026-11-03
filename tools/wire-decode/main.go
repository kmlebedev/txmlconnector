// Command wire-decode decrypts a TRANSAQ TCP capture and prints one line per
// frame. The capture format is the one written by a logging proxy:
//
//	<conn> <seconds> C>S|S>C <len> <hex>
//
// Lines starting with '#' are ignored. The account password (TC_PASSWORD) is
// needed to unwrap the session key from the RQ_PWD reply.
//
//	TC_PASSWORD=... wire-decode [-payload 64] capture.log
package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

type stream struct {
	buf  bytes.Buffer
	keys *wire.Keys
	dir  wire.Direction
	n    int
}

func main() {
	payloadBytes := flag.Int("payload", 48, "payload bytes to print in hex (-1 for all)")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: TC_PASSWORD=... wire-decode [-payload n] capture.log")
		os.Exit(2)
	}
	f, err := os.Open(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	if err := decode(f, os.Getenv("TC_PASSWORD"), *payloadBytes, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func decode(r io.Reader, password string, payloadBytes int, out io.Writer) error {
	type connDir struct{ conn, dir string }
	streams := map[connDir]*stream{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 5 {
			return fmt.Errorf("bad line: %.60s", line)
		}
		key := connDir{fields[0], fields[2]}
		s := streams[key]
		if s == nil {
			s = &stream{keys: wire.DefaultKeys(), dir: wire.ClientToServer}
			if fields[2] == "S>C" {
				s.dir = wire.ServerToClient
			}
			streams[key] = s
		}
		chunk, err := hex.DecodeString(fields[4])
		if err != nil {
			return err
		}
		s.buf.Write(chunk)
		for s.buf.Len() >= wire.HeaderSize {
			frame, err := tryFrame(s)
			if errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			if err != nil {
				return fmt.Errorf("conn %s %s frame %d: %w", fields[0], fields[2], s.n, err)
			}
			s.n++
			fmt.Fprintf(out, "%s %s %s #%d flags=%#02x type=%d len=%d %s\n",
				fields[1], fields[0], fields[2], s.n, frame.Flags, frame.Type, len(frame.Payload),
				clip(frame.Payload, payloadBytes))
			if s.dir == wire.ServerToClient && frame.Type == session.RqPwd && frame.Kind() == wire.KindReply {
				info, keys, err := session.SessionKeys(password, frame.Payload)
				if err != nil {
					return fmt.Errorf("RQ_PWD reply: %w", err)
				}
				fmt.Fprintf(out, "# session: %+v\n", *info)
				s.keys = keys
				if c := streams[connDir{fields[0], "C>S"}]; c != nil {
					c.keys = keys
				}
			}
		}
	}
	return sc.Err()
}

// tryFrame decodes one frame if the buffer holds it completely.
func tryFrame(s *stream) (wire.Frame, error) {
	data := s.buf.Bytes()
	f, err := s.keys.Decode(s.dir, bytes.NewReader(data))
	if err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return f, err
	}
	s.buf.Next(wire.HeaderSize + len(f.Payload))
	return f, nil
}

func clip(b []byte, n int) string {
	if n >= 0 && len(b) > n {
		return hex.EncodeToString(b[:n]) + "…"
	}
	return hex.EncodeToString(b)
}
