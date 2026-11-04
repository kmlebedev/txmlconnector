// Command tcproxy is a logging TCP forwarder between a TRANSAQ client (the DLL
// under Wine or a Go client) and the server. It writes the capture format read
// by tools/wire-decode:
//
//	<conn> <seconds> C>S|S>C <len> <hex>
//
// It is the Go port of tools/re/py/proxy.py, which stays the reference
// implementation for procedure RE-2 in tools/re/README.md. Captures of real
// sessions carry account data: keep them out of git (*.cap is ignored).
package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:39100", "listen address the client connects to")
	upstream := flag.String("upstream", "", "upstream host:port of the TRANSAQ server")
	out := flag.String("out", "", "capture file; frame lines are appended")
	flag.Parse()
	if *upstream == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: tcproxy -listen 127.0.0.1:39100 -upstream host:port -out capture.log")
		flag.PrintDefaults()
		os.Exit(2)
	}

	capture, err := os.OpenFile(*out, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tcproxy:", err)
		os.Exit(1)
	}
	defer capture.Close()
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tcproxy:", err)
		os.Exit(1)
	}
	defer ln.Close()
	fmt.Fprintf(os.Stderr, "listening on %s -> %s, capture %s\n", ln.Addr(), *upstream, *out)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := proxy(ctx, ln, *upstream, capture); err != nil {
		fmt.Fprintln(os.Stderr, "tcproxy:", err)
		os.Exit(1)
	}
}

// proxy forwards connections accepted on ln to upstream until ctx is done or
// the listener fails. Every chunk read in either direction is appended to logw.
// Connections are numbered from 1; timestamps are seconds since proxy started.
func proxy(ctx context.Context, ln net.Listener, upstream string, logw io.Writer) error {
	locked := &syncWriter{w: logw}
	logw = locked
	start := time.Now()

	var (
		mu    sync.Mutex
		wg    sync.WaitGroup
		conns = make(map[net.Conn]struct{})
		seq   int
	)
	track := func(nc ...net.Conn) {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range nc {
			conns[c] = struct{}{}
		}
	}
	untrack := func(nc ...net.Conn) {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range nc {
			delete(conns, c)
		}
	}
	closeAll := func() {
		mu.Lock()
		defer mu.Unlock()
		for c := range conns {
			c.Close()
		}
	}

	// A cancelled proxy stops accepting; closeAll below unblocks the pumps.
	stop := context.AfterFunc(ctx, func() { ln.Close() })
	defer stop()

	var runErr error
	for {
		client, err := ln.Accept()
		if err != nil {
			if ctx.Err() == nil {
				runErr = err
			}
			break
		}
		var d net.Dialer
		d.Timeout = 15 * time.Second // py proxy: create_connection(timeout=15)
		server, err := d.DialContext(ctx, "tcp", upstream)
		if err != nil {
			client.Close()
			if ctx.Err() == nil {
				runErr = fmt.Errorf("dial %s: %w", upstream, err)
			}
			break
		}
		mu.Lock()
		seq++
		n := seq
		mu.Unlock()
		track(client, server)
		wg.Add(1)
		go func(n int, client, server net.Conn) {
			defer wg.Done()
			handle(n, client, server, logw, start)
			untrack(client, server)
		}(n, client, server)
	}
	closeAll()
	wg.Wait()
	if err := locked.Err(); err != nil {
		return fmt.Errorf("write capture: %w", err)
	}
	if ctx.Err() != nil {
		return nil
	}
	return runErr
}

// handle pumps both directions of one proxied connection and closes the pair
// when both directions end.
func handle(n int, client, server net.Conn, logw io.Writer, start time.Time) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		pump(n, "C>S", client, server, logw, start)
	}()
	go func() {
		defer wg.Done()
		pump(n, "S>C", server, client, logw, start)
	}()
	wg.Wait()
	client.Close()
	server.Close()
}

// pump copies one direction. Each chunk is logged before it is forwarded; when
// the source ends, the write side of the destination is closed so the peer sees
// the end of the stream.
func pump(n int, dir string, src, dst net.Conn, logw io.Writer, start time.Time) {
	buf := make([]byte, 64*1024)
	for {
		k, err := src.Read(buf)
		if k > 0 {
			if cerr := writeCapture(logw, n, dir, start, buf[:k]); cerr != nil {
				return // a lost capture is worth stopping for; proxy reports it
			}
			if _, werr := dst.Write(buf[:k]); werr != nil {
				return
			}
		}
		if err != nil {
			if cw, ok := dst.(interface{ CloseWrite() error }); ok {
				_ = cw.CloseWrite()
			}
			return
		}
	}
}

// writeCapture appends one "<conn> <seconds> <dir> <len> <hex>" line.
func writeCapture(w io.Writer, n int, dir string, start time.Time, p []byte) error {
	_, err := fmt.Fprintf(w, "%d %.3f %s %d %s\n", n, time.Since(start).Seconds(), dir, len(p), hex.EncodeToString(p))
	return err
}

// syncWriter serializes capture lines: the two pumps of a connection and
// connections with each other write to one shared capture file. It keeps the
// first error so proxy can report a capture that silently stopped working.
type syncWriter struct {
	mu  sync.Mutex
	w   io.Writer
	err error
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return 0, s.err
	}
	n, err := s.w.Write(p)
	if err != nil {
		s.err = err
	}
	return n, err
}

// Err returns the first capture write error, if any.
func (s *syncWriter) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}
