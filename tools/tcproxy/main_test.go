package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestProxyForwardsAndLogs puts the proxy between a client and an echo server
// on loopback: the echo must come back unchanged and both directions must be
// logged in the capture format of tools/wire-decode.
func TestProxyForwardsAndLogs(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}()
		}
	}()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- proxy(ctx, ln, echo.Addr().String(), &log) }()

	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 4)
	if _, err := io.ReadFull(client, got); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(got) != "ping" {
		t.Fatalf("echo = %q, want %q", got, "ping")
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("proxy: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("proxy did not stop after context cancellation")
	}

	lines := strings.Split(strings.TrimSpace(log.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("capture has %d lines, want 2:\n%s", len(lines), log.String())
	}
	pattern := regexp.MustCompile(`^1 [0-9]+\.[0-9]{3} (C>S|S>C) 4 70696e67$`)
	for i, dir := range []string{"C>S", "S>C"} {
		m := pattern.FindStringSubmatch(lines[i])
		if m == nil || m[1] != dir {
			t.Fatalf("line %d = %q, want a %s frame with ping payload", i+1, lines[i], dir)
		}
	}
}
