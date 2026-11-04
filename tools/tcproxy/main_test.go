package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// capture is a concurrency-safe io.Writer for the proxy log.
type capture struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (c *capture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.b.Write(p)
}

func (c *capture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.b.String()
}

// captureLine matches "<conn> <seconds> C>S|S>C <len> <hex>".
var captureLine = regexp.MustCompile("^([0-9]+) [0-9]+\\.[0-9]{3} (C>S|S>C) ([0-9]+) ([0-9a-f]*)$")

// capturePayloads returns the payload logged per direction, concatenated in
// wire order, and whether both directions have been logged yet. TCP can split
// one write into several chunks, so a direction may span several lines.
func capturePayloads(log string) (map[string]string, bool) {
	out := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
		if line == "" {
			continue
		}
		m := captureLine.FindStringSubmatch(line)
		if m == nil {
			return nil, false
		}
		if _, err := hex.DecodeString(m[4]); err != nil {
			return nil, false
		}
		if n, err := strconv.Atoi(m[3]); err != nil || n != len(m[4])/2 {
			return nil, false
		}
		out[m[2]] += m[4]
	}
	_, cs := out["C>S"]
	_, sc := out["S>C"]
	return out, cs && sc
}

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
	var log capture
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

	// TCP can split the write into several chunks: wait for both directions
	// instead of assuming exactly one chunk per direction.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := capturePayloads(log.String()); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("capture did not log both directions: %q", log.String())
		}
		time.Sleep(5 * time.Millisecond)
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

	payloads, ok := capturePayloads(log.String())
	if !ok {
		t.Fatalf("capture has malformed lines: %q", log.String())
	}
	for _, dir := range []string{"C>S", "S>C"} {
		if got := payloads[dir]; got != "70696e67" {
			t.Fatalf("%s payload = %q (%s), want the ping bytes", dir, got, log.String())
		}
	}
	if first := strings.Split(strings.TrimSpace(log.String()), "\n")[0]; !strings.HasPrefix(first, "1 ") {
		t.Fatalf("connection numbers must start at 1: %q", first)
	}
}
