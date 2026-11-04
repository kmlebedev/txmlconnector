// Command replay-server replays a recorded TRANSAQ session to a client (the
// DLL under Wine or a Go client) and can inject crafted RQ_MAIN records; that
// is procedure RE-3 in tools/re/README.md. It is the Go port of
// tools/re/py/replay_server.py, which stays the reference implementation.
//
//	TC_PASSWORD=... replay-server -dump session.dump -listen 127.0.0.1:39170 \
//		-inject inject.hex -after 10
//
// The dump is the output of tools/wire-decode -payload -1. TC_PASSWORD is the
// password of the recorded account: it unwraps the session key from the RQ_PWD
// reply, the challenge proof of the replayed client is not checked. A capture
// of a real session carries account data: keep it out of git.
package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/kmlebedev/txmlconnector/native/replay"
	"github.com/kmlebedev/txmlconnector/native/session"
)

func main() {
	dump := flag.String("dump", "", "session dump: output of wire-decode -payload -1")
	listen := flag.String("listen", "127.0.0.1:39170", "listen address for the replayed client")
	inject := flag.String("inject", "", "hex file with RQ_MAIN records to inject after -after")
	after := flag.Int("after", int(session.RqSubscribe), "request type that arms the injection; 0 disables injection and cuts at RQ_SUBSCRIBE")
	flag.Parse()
	if *dump == "" {
		fmt.Fprintln(os.Stderr, "usage: TC_PASSWORD=... replay-server -dump session.dump [-listen 127.0.0.1:39170] [-inject inject.hex -after 10]")
		flag.PrintDefaults()
		os.Exit(2)
	}
	if *after < 0 || *after > 0xff {
		fmt.Fprintln(os.Stderr, "replay-server: -after must be a request type byte")
		os.Exit(2)
	}
	password := os.Getenv("TC_PASSWORD")
	if password == "" {
		fmt.Fprintln(os.Stderr, "replay-server: TC_PASSWORD is required to unwrap the recorded session key")
		os.Exit(2)
	}

	f, err := os.Open(*dump)
	if err != nil {
		fmt.Fprintln(os.Stderr, "replay-server:", err)
		os.Exit(1)
	}
	defer f.Close()
	sess, err := replay.Load(f, password)
	if err != nil {
		fmt.Fprintln(os.Stderr, "replay-server:", err)
		os.Exit(1)
	}

	var payload []byte
	if *inject != "" {
		raw, err := os.ReadFile(*inject)
		if err != nil {
			fmt.Fprintln(os.Stderr, "replay-server:", err)
			os.Exit(1)
		}
		payload, err = hex.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil {
			fmt.Fprintf(os.Stderr, "replay-server: %s: %v\n", *inject, err)
			os.Exit(1)
		}
	}

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, "replay-server:", err)
		os.Exit(1)
	}
	defer ln.Close()
	fmt.Printf("listening on %s, dump %s, after request type %d, injecting %d bytes\n",
		ln.Addr(), *dump, *after, len(payload))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var (
		mu     sync.Mutex
		active net.Conn
	)
	go func() {
		<-ctx.Done()
		ln.Close()
		mu.Lock()
		if active != nil {
			active.Close() // unblock Serve's read, not just the accept loop
		}
		mu.Unlock()
	}()
	injection := replay.Injection{AfterRequest: byte(*after), Payload: payload}
	for {
		nc, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			fmt.Fprintln(os.Stderr, "replay-server:", err)
			os.Exit(1)
		}
		mu.Lock()
		active = nc
		mu.Unlock()
		fmt.Printf("connection from %s\n", nc.RemoteAddr())
		err = sess.Serve(nc, injection)
		mu.Lock()
		active = nil
		mu.Unlock()
		if err != nil {
			fmt.Fprintln(os.Stderr, "replay-server:", err)
		} else {
			fmt.Println("session finished")
		}
	}
}
