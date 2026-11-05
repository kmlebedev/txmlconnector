package native

import (
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/testserver"
	"github.com/kmlebedev/txmlconnector/native/wire"
	"github.com/kmlebedev/txmlconnector/native/xmlcompat"
)

// messageRqTime overrides RQ_TIME so the message wall time has a known bias:
// bias 0 = UTC, +180 minutes = the same wall clock +3h (the DLL convention
// UTC = wall + bias).
func messageRqTime(t *testing.T, srv *testserver.Server, wall int64, biasMinutes int16) {
	t.Helper()
	zone := [16]byte{byte(uint16(biasMinutes)), byte(uint16(biasMinutes) >> 8)}
	srv.Handle(session.RqTime, func([]byte) (byte, []byte) {
		var w wire.Writer
		w.Int32(int32(wall))
		w.Raw(zone[:])
		return wire.KindReply, w.Bytes()
	})
}

func messageDLL(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("records/testdata/message-dll.xml")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(raw))
}

// TestMessagesMatchDLL: two R_MSG records in the load stream become ONE
// <messages> callback, byte-exact against the O fixture (bias 0 -> UTC date).
func TestMessagesMatchDLL(t *testing.T) {
	want := messageDLL(t)
	srv := newRunServer(t)
	// Real demo server time zone: Russian Standard Time (-180 min). The DLL
	// still prints the WALL time (23:46:40), so this must match the O fixture.
	messageRqTime(t, srv, 1762300000, -180)
	srv.Snapshot = append(clientSnapshot(t), readOrderFixture(t, "message-inject.hex")...)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	if got := rec.next(t, "<messages"); got != want {
		t.Fatalf("messages\n got %s\nwant %s", got, want)
	}
}

// TestMessageBatchSingleCallback: several R_MSG records of one RQ_MAIN reply
// produce exactly one <messages> callback (O).
func TestMessageBatchSingleCallback(t *testing.T) {
	srv := newRunServer(t)
	messageRqTime(t, srv, 1762300000, 0)
	srv.Snapshot = clientSnapshot(t)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	srv.QueueMain(readOrderFixture(t, "message-inject.hex"))
	rec.next(t, "<messages")
	// Flush via server_status and count the callbacks since connect.
	send(t, c, "<command id="+quote+"server_status"+quote+"/>")
	rec.next(t, "<server_status")
	rec.mu.Lock()
	count := 0
	for _, msg := range rec.msgs {
		if strings.HasPrefix(msg, "<messages") {
			count++
		}
	}
	rec.mu.Unlock()
	if count != 1 {
		t.Fatalf("<messages> callbacks = %d, want 1", count)
	}
}

// TestMessageUrgentAndTimeBias: flags 0x01/0x08 -> Y, 0x00/0x02 -> N; the XML
// date is the WALL time and does not depend on the RQ_TIME bias (DLL prints the
// wall clock; the UTC instant in model.Message differs, but is not rendered).
func TestMessageUrgentAndTimeBias(t *testing.T) {
	srv := newRunServer(t)
	messageRqTime(t, srv, 1762300000, 180)
	srv.Snapshot = clientSnapshot(t)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	mk := func(flags byte, text string) []byte {
		var w wire.Writer
		w.Byte(flags)
		w.String("TESTUSER", 20)
		w.Int32(1762300000)
		w.String(text, 1024)
		return append([]byte{records.TagMessage}, w.Bytes()...)
	}
	payload := mk(0x00, "n0")
	payload = append(payload, mk(0x01, "y1")...)
	payload = append(payload, mk(0x08, "y8")...)
	payload = append(payload, mk(0x02, "n2")...)
	srv.QueueMain(payload)
	got := rec.next(t, "<messages")
	want := "<messages>" +
		"<message><date>23:46:40</date><urgent>N</urgent><from>TESTUSER</from><text><![CDATA[n0]]></text></message>" +
		"<message><date>23:46:40</date><urgent>Y</urgent><from>TESTUSER</from><text><![CDATA[y1]]></text></message>" +
		"<message><date>23:46:40</date><urgent>Y</urgent><from>TESTUSER</from><text><![CDATA[y8]]></text></message>" +
		"<message><date>23:46:40</date><urgent>N</urgent><from>TESTUSER</from><text><![CDATA[n2]]></text></message>" +
		"</messages>"
	if got != want {
		t.Fatalf("messages\n got %s\nwant %s", got, want)
	}
}

// TestMessageRenderBatch checks the batch envelope and single-item form.
func TestMessageRenderBatch(t *testing.T) {
	item := model.Message{Time: time.Unix(1762289200, 0).UTC(), WallTime: time.Unix(1762300000, 0).UTC(), Urgent: true, From: "A", Text: "T"}
	want := "<messages><message><date>23:46:40</date><urgent>Y</urgent><from>A</from><text><![CDATA[T]]></text></message></messages>"
	if got := xmlcompat.MessagesXML([]model.Message{item}); got != want {
		t.Fatalf("MessagesXML = %s", got)
	}
}

// TestMessageTimeBiasTable: the RQ_TIME bias shifts the UTC instant used for
// the HH:MM:SS part (wall 1762300000 = 23:46:40 UTC).
func TestMessageTimeBiasTable(t *testing.T) {
	for _, tc := range []struct {
		name string
		bias int16
		date string
	}{
		{"utc", 0, "23:46:40"},
		{"plus3h", 180, "23:46:40"},
		{"minus2h", -120, "23:46:40"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newRunServer(t)
			messageRqTime(t, srv, 1762300000, tc.bias)
			srv.Snapshot = clientSnapshot(t)
			c := New(Config{Dial: srv.Dial})
			rec := newRecorder()
			if err := c.Start(rec.handle); err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			send(t, c, connectCommand())
			rec.next(t, "<server_status")
			srv.QueueMain(readOrderFixture(t, "message-inject.hex"))
			got := rec.next(t, "<messages")
			if !strings.Contains(got, "<date>"+tc.date+"</date>") {
				t.Fatalf("date = %s, want %s", got, tc.date)
			}
		})
	}
}

// TestMessageTextCDATA: text is wrapped in CDATA; we escape ]]> (DLL likely
// inserts as-is - I) and check the escaped form.
func TestMessageTextCDATA(t *testing.T) {
	item := model.Message{Time: time.Unix(1762289200, 0).UTC(), WallTime: time.Unix(1762300000, 0).UTC(), From: "A", Text: "a]]>b"}
	want := "<messages><message><date>23:46:40</date><urgent>N</urgent><from>A</from><text><![CDATA[a]]]]><![CDATA[>b]]></text></message></messages>"
	if got := xmlcompat.MessagesXML([]model.Message{item}); got != want {
		t.Fatalf("MessagesXML = %s, want %s", got, want)
	}
}

// TestMessageCP1251Text: R_MSG strings are CP1251 on the wire (S: production
// 2026-11-05, a TradeServer notice); the callback must be UTF-8 or the gRPC
// server cannot send it ("string field contains invalid UTF-8").
func TestMessageCP1251Text(t *testing.T) {
	srv := newRunServer(t)
	messageRqTime(t, srv, 1762300000, -180)
	var w wire.Writer
	w.Byte(records.TagMessage)
	w.Byte(1)
	w.String("TradeServer", 20)
	w.Int32(43755)
	w.Raw([]byte{0xc2, 0xf0, 0xe5, 0xec, 0xff, 0}) // "Время"
	srv.Snapshot = append(clientSnapshot(t), w.Bytes()...)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	got := rec.next(t, "<messages")
	if !strings.Contains(got, "<![CDATA[Время]]>") || !utf8.ValidString(got) {
		t.Fatalf("messages = %q", got)
	}
}
