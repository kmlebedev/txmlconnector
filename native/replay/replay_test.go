package replay

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native"
	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

const (
	testLogin    = "TESTLOGIN"
	testPassword = "TESTPASS"
	// fixturesDir is where the binary fixtures and the DLL reference callbacks
	// live, shared with the records and xmlcompat tests.
	fixturesDir = "../records/testdata/"
	// stubPwdReply is the recorded RQ_PWD reply frame from
	// native/session/login_test.go: it issues session key 40..5f and sync
	// 70..77, sealed with TESTPASS's password key.
	stubPwdReply = "d7e9c612a09a7307d5f98ddfc1df238d566b3ff5080d5f55670c23c7d7de9710" +
		"f4d9ab2fdbab9822915b38db0d1f444e1a69a6c3226093aa73d7658dad201e26" +
		"8547ea33eeac5f82e32a10596fbbae695562c9f7a972a1119c"
)

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// readFixture loads a hex fixture (optionally gzipped) from fixturesDir.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	f, err := os.Open(fixturesDir + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(name, ".gz") {
		zr, err := gzip.NewReader(f)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		defer zr.Close()
		r = zr
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	b, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return b
}

// dumpLine renders one server reply in the format of wire-decode -payload -1.
func dumpLine(b *bytes.Buffer, n int, typ, flags byte, payload []byte) {
	fmt.Fprintf(b, "0.%03d 1 S>C #%d flags=%#02x type=%d len=%d %s\n",
		n, n, flags, typ, len(payload), hex.EncodeToString(payload))
}

// TestReplayServesRecordedSession loads a dump assembled from the recorded
// login, dictionary and snapshot payloads, lets native.Dial load the session
// through it and checks that the injected RQ_MAIN records reach the client.
func TestReplayServesRecordedSession(t *testing.T) {
	pwd, err := wire.DefaultKeys().Decode(wire.ServerToClient, bytes.NewReader(unhex(t, stubPwdReply)))
	if err != nil {
		t.Fatalf("decode recorded RQ_PWD reply: %v", err)
	}

	var dump bytes.Buffer
	dumpLine(&dump, 1, session.RqLogin, wire.KindReply, unhex(t, "00080102030405060708"))
	dumpLine(&dump, 2, session.RqPwd, wire.KindReply, pwd.Payload)
	dumpLine(&dump, 3, session.RqTime, wire.KindReply, unhex(t, "74c0a4ae064cff0000c4ff00000000000000000000"))
	dumpLine(&dump, 4, session.RqStartup, wire.KindReply, readFixture(t, "startup-demo.hex"))
	dumpLine(&dump, 5, session.RqMain, wire.KindReply, readFixture(t, "main1-demo.hex.gz"))

	sess, err := Load(&dump, testPassword)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	inject := readFixture(t, "alltrades-inject1.hex")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	errc := make(chan error, 1)
	go func() {
		nc, err := ln.Accept()
		if err != nil {
			errc <- err
			return
		}
		errc <- sess.Serve(nc, Injection{AfterRequest: session.RqSubscribe, Payload: inject})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := native.Dial(ctx, native.ClientConfig{
		Address: ln.Addr().String(), Login: testLogin, Password: testPassword,
	})
	if err != nil {
		t.Fatalf("native.Dial: %v", err)
	}
	if got := c.Catalog().Len(); got != 499 {
		c.Close()
		t.Fatalf("catalog = %d securities, want 499", got)
	}
	if err := c.SubscribeAllTrades(ctx, model.Symbol{Board: "TQBR", SecCode: "SBER"}); err != nil {
		c.Close()
		t.Fatalf("subscribe: %v", err)
	}
	trades, _, err := c.Poll(ctx)
	if err != nil {
		c.Close()
		t.Fatalf("poll: %v", err)
	}
	if len(trades) != 5 {
		c.Close()
		t.Fatalf("first poll after subscribe returned %d trades, want 5", len(trades))
	}
	if trades[0].SecCode != "SBER" {
		c.Close()
		t.Fatalf("first trade security = %q, want SBER", trades[0].SecCode)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after the client disconnected")
	}
}
