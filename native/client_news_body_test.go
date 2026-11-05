package native

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// TestNewsHeaderMatchDLL: the S 0x36 flags=1 header is rendered byte-exactly
// with CP1251 source/title converted to UTF-8.
func TestNewsHeaderMatchDLL(t *testing.T) {
	headerPayload, err := os.ReadFile("records/testdata/news-header-live-inject.hex")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := hex.DecodeString(strings.TrimSpace(string(headerPayload)))
	if err != nil {
		t.Fatal(err)
	}
	wantRaw, err := os.ReadFile("records/testdata/news-header-live-dll.xml")
	if err != nil {
		t.Fatal(err)
	}
	srv := newRunServer(t)
	srv.Snapshot = append(clientSnapshot(t), payload...)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	if got := rec.next(t, "<news_header"); got != strings.TrimSpace(string(wantRaw)) {
		t.Fatalf("news_header\n got %s\nwant %s", got, strings.TrimSpace(string(wantRaw)))
	}
}

// TestNewsBodyCallbackS: get_news_body sends RQ_GETNEWS (reply empty) and the
// body arrives as S RQ_MAIN 0x36 flags=2; the callback uses the real header
// fixture (no synthesized header), and a following 0x18 record is not lost.
func TestNewsBodyCallbackS(t *testing.T) {
	headerPayload, err := os.ReadFile("records/testdata/news-header-live-inject.hex")
	if err != nil {
		t.Fatal(err)
	}
	header, err := hex.DecodeString(strings.TrimSpace(string(headerPayload)))
	if err != nil {
		t.Fatal(err)
	}
	bodyRaw, err := os.ReadFile("records/testdata/news-body-live-inject.hex")
	if err != nil {
		t.Fatal(err)
	}
	body, err := hex.DecodeString(strings.TrimSpace(string(bodyRaw)))
	if err != nil {
		t.Fatal(err)
	}
	srv := newRunServer(t)
	srv.Snapshot = append(clientSnapshot(t), header...)
	srv.Handle(session.RqGetNews, func([]byte) (byte, []byte) {
		return wire.KindReply | wire.FlagPlain, nil // S: reply 73 is empty
	})
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	cmd := "<command id=" + quote + "get_news_body" + quote + " news_id=" + quote + "15979127" + quote + "/>"
	if got := send(t, c, cmd); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("get_news_body = %s", got)
	}
	wantRaw, err := os.ReadFile("records/testdata/news-body-live-dll.xml")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSpace(string(wantRaw))
	// The body frame is followed by 0x18 (overnight): its byte must not be
	// swallowed as part of the package.
	srv.QueueMain(append(body, 0x18, 0x01))
	if got := rec.next(t, "<news_body"); got != want {
		t.Fatalf("news_body\n got %s\nwant %s", got, want)
	}
	// The 0x18 byte after the body must not be swallowed: the stream is still
	// healthy and answers the next command.
	if got := send(t, c, "<command id="+quote+"server_status"+quote+"/>"); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("server_status after news body = %s", got)
	}
}
