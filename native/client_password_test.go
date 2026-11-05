package native

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

func changeFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("session/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	out, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

// TestChangePasswordCommand: the O payload is byte-exact, success updates the
// saved password, session keys stay unchanged (D).
func TestChangePasswordCommand(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	var mu sync.Mutex
	var captured []byte
	srv.Handle(session.RqChangePwd, func(payload []byte) (byte, []byte) {
		mu.Lock()
		captured = append([]byte(nil), payload...)
		mu.Unlock()
		return wire.KindReply | wire.FlagPlain, changeFixture(t, "changepwd-reply-ok.hex")
	})
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	cmd := "<command id=" + quote + "change_pass" + quote + " oldpass=" + quote + "OldPass2!" + quote + " newpass=" + quote + "TestPass1!" + quote + "/>"
	if got := send(t, c, cmd); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("change_pass = %s", got)
	}
	mu.Lock()
	got := captured
	mu.Unlock()
	want := changeFixture(t, "changepwd-request.hex")
	if hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Fatalf("payload = %x, want %x", got, want)
	}
	c.mu.Lock()
	saved := c.client.cfg.Password
	c.mu.Unlock()
	if saved != "TestPass1!" {
		t.Fatalf("saved password = %q, want the new one", saved)
	}
}

// TestChangePasswordReplyErrors: status 1 and status 2 + C-string become the
// DLL <message> texts.
func TestChangePasswordReplyErrors(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	var mu sync.Mutex
	replies := [][]byte{
		changeFixture(t, "changepwd-reply-wrong-old.hex"),
		changeFixture(t, "changepwd-reply-error.hex"),
	}
	call := 0
	srv.Handle(session.RqChangePwd, func(payload []byte) (byte, []byte) {
		mu.Lock()
		defer mu.Unlock()
		reply := replies[call]
		call++
		return wire.KindReply | wire.FlagPlain, reply
	})
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	base := "<command id=" + quote + "change_pass" + quote + " oldpass=" + quote + "OldPass2!" + quote + " newpass=" + quote + "TestPass1!" + quote + "/>"
	if got := send(t, c, base); !strings.Contains(got, "Wrong oldpass specified.") {
		t.Fatalf("status 1 = %s", got)
	}
	if got := send(t, c, base); !strings.Contains(got, "Server says: bad password") {
		t.Fatalf("status 2 = %s", got)
	}
}

// TestChangePasswordLocalErrors checks the DLL local validation texts.
func TestChangePasswordLocalErrors(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	srv.Handle(session.RqChangePwd, func([]byte) (byte, []byte) {
		return wire.KindReply | wire.FlagPlain, changeFixture(t, "changepwd-reply-ok.hex")
	})
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	cases := []struct {
		name string
		cmd  string
		want string
	}{
		{"no old", "<command id=" + quote + "change_pass" + quote + " newpass=" + quote + "X" + quote + "/>", "You have to specify old password"},
		{"no new", "<command id=" + quote + "change_pass" + quote + " oldpass=" + quote + "X" + quote + "/>", "You have to specify new password"},
		{"too long", "<command id=" + quote + "change_pass" + quote + " oldpass=" + quote + strings.Repeat("a", 21) + quote + " newpass=" + quote + "b" + quote + "/>", "Password cannot be longer than 20 characters."},
		{"forbiden", "<command id=" + quote + "change_pass" + quote + " oldpass=" + quote + "OldPass2!" + quote + " newpass=" + quote + "a;b" + quote + "/>", "Forbiden character:"},
		{"same", "<command id=" + quote + "change_pass" + quote + " oldpass=" + quote + "SamePass" + quote + " newpass=" + quote + "SamePass" + quote + "/>", "The same passwords specified"},
	}
	for _, tc := range cases {
		if got := send(t, c, tc.cmd); !strings.Contains(got, tc.want) {
			t.Fatalf("%s: %s, want %q", tc.name, got, tc.want)
		}
	}
}

// TestChangePasswordEmptyReplyFatal: an empty reply closes the session
// connection (client-level fatal error, DLL connected="false").
func TestChangePasswordEmptyReplyFatal(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	srv.Handle(session.RqChangePwd, func([]byte) (byte, []byte) {
		return wire.KindReply | wire.FlagPlain, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Dial(ctx, ClientConfig{Address: "testserver:0", Login: testLogin, Password: testPassword, Dial: srv.Dial})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.ChangePassword(ctx, "OldPass2!", "TestPass1!"); !errors.Is(err, session.ErrChangePasswordFatal) {
		t.Fatalf("empty reply err = %v, want fatal", err)
	}
	if err := c.ChangePassword(ctx, "OldPass2!", "TestPass1!"); err == nil {
		t.Fatal("session connection was not closed by the fatal reply")
	}
}

// TestChangePasswordConnectorFatal: the Connector maps the fatal empty reply to
// the DLL failure text and reports connected="false" (changepass.md O/D).
func TestChangePasswordConnectorFatal(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	srv.Handle(session.RqChangePwd, func([]byte) (byte, []byte) {
		return wire.KindReply | wire.FlagPlain, nil
	})
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	cmd := "<command id=" + quote + "change_pass" + quote + " oldpass=" + quote + "OldPass2!" + quote + " newpass=" + quote + "TestPass1!" + quote + "/>"
	if got := send(t, c, cmd); !strings.Contains(got, "RQ_CHANGE_PWD incorrect server buffer") {
		t.Fatalf("fatal change_pass = %s", got)
	}
	if got := rec.next(t, "<server_status"); !strings.Contains(got, "connected="+quote+"false"+quote) {
		t.Fatalf("fatal status = %s, want connected=false", got)
	}
	// Fatal must be terminal: exactly one connected=false, no recover=true and
	// no subsequent connected=true.
	// Wait until the Run loop has exited and no further status arrives.
	waitFor(t, func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		falseSeen := false
		for _, msg := range rec.msgs {
			if strings.Contains(msg, "connected="+quote+"false"+quote) {
				falseSeen = true
			}
			if falseSeen && strings.Contains(msg, "connected="+quote+"true"+quote) {
				return false
			}
		}
		return falseSeen
	})
	rec.mu.Lock()
	falseCount, recoverCount, trueCount := 0, 0, 0
	for _, msg := range rec.msgs {
		switch {
		case strings.Contains(msg, "recover="+quote+"true"+quote):
			recoverCount++
		case strings.Contains(msg, "connected="+quote+"false"+quote):
			falseCount++
		case strings.Contains(msg, "connected="+quote+"true"+quote):
			trueCount++
		}
	}
	rec.mu.Unlock()
	if falseCount != 1 {
		t.Fatalf("connected=false callbacks = %d, want exactly 1", falseCount)
	}
	if recoverCount != 0 {
		t.Fatalf("recover=true callbacks = %d, want 0 for fatal change_pass", recoverCount)
	}
	// The initial connected=true before the command is expected; no new true after
	// the false status. Count true statuses before the first false.
	rec.mu.Lock()
	afterFalse := false
	lateTrue := 0
	for _, msg := range rec.msgs {
		if strings.Contains(msg, "connected="+quote+"false"+quote) {
			afterFalse = true
			continue
		}
		if afterFalse && strings.Contains(msg, "connected="+quote+"true"+quote) {
			lateTrue++
		}
	}
	rec.mu.Unlock()
	if lateTrue != 0 {
		t.Fatalf("connected=true after false = %d, want 0", lateTrue)
	}
}

// TestConnChangePasswordMalformedEndToEnd: status 3 and status-2 trailing bytes
// are rejected by Conn.ChangePassword itself (not only by the parser) and are
// terminal for the connector.
func TestConnChangePasswordMalformedEndToEnd(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply []byte
	}{
		{"status3", []byte{3}},
		{"status2-trailing", []byte{2, 'x', 0, 0}},
		{"status0-trailing", []byte{0, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newRunServer(t)
			srv.Snapshot = clientSnapshot(t)
			srv.Handle(session.RqChangePwd, func([]byte) (byte, []byte) {
				return wire.KindReply | wire.FlagPlain, tc.reply
			})
			c := New(Config{Dial: srv.Dial})
			rec := newRecorder()
			if err := c.Start(rec.handle); err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			send(t, c, connectCommand())
			rec.next(t, "<server_status")
			cmd := "<command id=" + quote + "change_pass" + quote + " oldpass=" + quote + "OldPass2!" + quote + " newpass=" + quote + "TestPass1!" + quote + "/>"
			if got := send(t, c, cmd); !strings.Contains(got, "RQ_CHANGE_PWD incorrect server buffer") {
				t.Fatalf("malformed reply = %s", got)
			}
			if got := rec.next(t, "<server_status"); !strings.Contains(got, "connected="+quote+"false"+quote) {
				t.Fatalf("fatal status = %s", got)
			}
		})
	}
}
