package native

import (
	"encoding/hex"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/testserver"
	"github.com/kmlebedev/txmlconnector/native/wire"
	"github.com/kmlebedev/txmlconnector/native/xmlcompat"
)

func newsFixture(t *testing.T) []byte { return readOrderFixture(t, "news-inject.hex") }

func newsDLL(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("records/testdata/news-dll.xml")
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, strings.TrimSpace(line))
		}
	}
	return lines
}

// captureNewsGet registers the RQ_GETNEWS fake success reply and returns the
// captured payload getter.
func captureNewsGet(t *testing.T, srv *testserver.Server) func() []byte {
	t.Helper()
	var mu sync.Mutex
	var captured []byte
	srv.Handle(session.RqGetNews, func(payload []byte) (byte, []byte) {
		mu.Lock()
		captured = append([]byte(nil), payload...)
		mu.Unlock()
		return wire.KindReply | wire.FlagPlain, nil
	})
	return func() []byte {
		mu.Lock()
		defer mu.Unlock()
		return captured
	}
}

// TestOldNewsPayloadMatchesDLL checks both RQ_GETNEWS payloads from news.md:
// mixed store (0x35 1001 + 0x36 2002) -> 999,1001,2,1 and the 0x36-only store
// (2002) -> 2000,2002,2,1.
func TestOldNewsPayloadMatchesDLL(t *testing.T) {
	recs, err := records.Records(newsFixture(t))
	if err != nil || len(recs) != 2 {
		t.Fatalf("news fixture: %v", err)
	}
	// 0x36 is unsized: tag + body, no size prefix.
	uploadOnly := append([]byte{recs[1].Tag}, recs[1].Body...)
	for _, tc := range []struct {
		name    string
		store   []byte
		payload string
	}{
		{"mixed", append(clientSnapshot(t), newsFixture(t)...), "713e913e0201"},
		{"uploadOnly", append(clientSnapshot(t), uploadOnly...), "017d217d0201"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newRunServer(t)
			srv.Snapshot = tc.store
			get := captureNewsGet(t, srv)
			c := New(Config{Dial: srv.Dial})
			rec := newRecorder()
			if err := c.Start(rec.handle); err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			send(t, c, connectCommand())
			rec.next(t, "<server_status")
			if got := send(t, c, "<command id="+quote+"get_old_news"+quote+" count="+quote+"2"+quote+"/>"); got != "<result success="+quote+"true"+quote+"/>" {
				t.Fatalf("get_old_news = %s", got)
			}
			got := get()
			if hex.EncodeToString(got) != tc.payload {
				t.Fatalf("RQ_GETNEWS payload = %x, want %s", got, tc.payload)
			}
		})
	}
}

// TestNewsHeadersMatchDLL: the 0x35/0x36 headers arriving in the load stream are
// rendered byte-exactly against the O fixture.
func TestNewsHeadersMatchDLL(t *testing.T) {
	want := newsDLL(t)
	if len(want) != 2 {
		t.Fatalf("news-dll.xml lines = %d", len(want))
	}
	srv := newRunServer(t)
	srv.Snapshot = append(clientSnapshot(t), newsFixture(t)...)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	// Load callbacks precede the first <server_status>.
	if got := rec.next(t, "<news_header"); got != want[0] {
		t.Fatalf("news_header 1\n got %s\nwant %s", got, want[0])
	}
	if got := rec.next(t, "<news_header"); got != want[1] {
		t.Fatalf("news_header 2\n got %s\nwant %s", got, want[1])
	}
	rec.next(t, "<server_status")
}

// TestNewsErrors checks the DLL validation texts of both commands.
func TestNewsErrors(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = append(clientSnapshot(t), newsFixture(t)...)
	captureNewsGet(t, srv)
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
		{"no count", "<command id=" + quote + "get_old_news" + quote + "/>", "attribute not found"},
		{"count zero", "<command id=" + quote + "get_old_news" + quote + " count=" + quote + "0" + quote + "/>", "News count must be in interval from 1 to 100. Received value is 0"},
		{"count 101", "<command id=" + quote + "get_old_news" + quote + " count=" + quote + "101" + quote + "/>", "News count must be in interval from 1 to 100. Received value is 101"},
		{"count non-int", "<command id=" + quote + "get_old_news" + quote + " count=" + quote + "x" + quote + "/>", "attribute must be an integer"},
		{"no news_id", "<command id=" + quote + "get_news_body" + quote + "/>", "attribute not found"},
		{"news_id negative", "<command id=" + quote + "get_news_body" + quote + " news_id=" + quote + "-1" + quote + "/>", "must be in interval from 0 to 2147483647"},
		{"unknown header", "<command id=" + quote + "get_news_body" + quote + " news_id=" + quote + "999999" + quote + "/>", "Header with id = 999999 not found"},
	}
	for _, tc := range cases {
		if got := send(t, c, tc.cmd); !strings.Contains(got, tc.want) {
			t.Fatalf("%s: %s, want %q", tc.name, got, tc.want)
		}
	}
}

// TestGetNewsBodyRequest: the command sends the D+O payload (2002,2002,1,2) and
// reports success; the <news_body> callback is I (news.md).
func TestGetNewsBodyRequest(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = append(clientSnapshot(t), newsFixture(t)...)
	get := captureNewsGet(t, srv)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	cmd := "<command id=" + quote + "get_news_body" + quote + " news_id=" + quote + "2002" + quote + "/>"
	if got := send(t, c, cmd); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("get_news_body = %s", got)
	}
	if got := hex.EncodeToString(get()); got != "217d217d0102" {
		t.Fatalf("RQ_GETNEWS body payload = %s, want 217d217d0102", got)
	}
}

// TestNewsBodyRender checks the D XML form and CDATA escaping.
func TestNewsBodyRender(t *testing.T) {
	got := xmlcompat.NewsBodyXML(model.NewsBody{ID: 7, Text: "a]]>b"})
	want := "<news_body><id>7</id><text><![CDATA[a]]]]><![CDATA[>b]]></text></news_body>"
	if got != want {
		t.Fatalf("NewsBodyXML = %s, want %s", got, want)
	}
}

// TestOldNewsHeadersAfterCommand: get_old_news sends RQ_GETNEWS and the 0x35/
// 0x36 records of the following RQ_MAIN batch render as <news_header>.
func TestOldNewsHeadersAfterCommand(t *testing.T) {
	want := newsDLL(t)
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	captureNewsGet(t, srv)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	if got := send(t, c, "<command id="+quote+"get_old_news"+quote+" count="+quote+"2"+quote+"/>"); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("get_old_news = %s", got)
	}
	srv.QueueMain(newsFixture(t))
	if got := rec.next(t, "<news_header"); got != want[0] {
		t.Fatalf("news_header 1 = %s", got)
	}
	if got := rec.next(t, "<news_header"); got != want[1] {
		t.Fatalf("news_header 2 = %s", got)
	}
}

// TestNewsHeaderCDATA: the DLL wraps source/title in CDATA and (per the code)
// does not escape ]]>; we escape it to keep the XML valid (I) and check it.
func TestNewsHeaderCDATA(t *testing.T) {
	got := xmlcompat.NewsHeaderXML(model.NewsHeader{ID: 7, Time: time.Unix(1762300000, 0).UTC(), Source: "s]]>x", Title: "t]]>y"})
	want := "<news_header><id>7</id><timestamp>04.11.2025 23:46:40</timestamp><source><![CDATA[s]]]]><![CDATA[>x]]></source><title><![CDATA[t]]]]><![CDATA[>y]]></title></news_header>"
	if got != want {
		t.Fatalf("NewsHeaderXML = %s, want %s", got, want)
	}
}

// TestOldNewsEmptyStorePayload: an empty news store sends 0..0x7fffffff
// (news.md D; the exact mixed-store rule is I).
func TestOldNewsEmptyStorePayload(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	get := captureNewsGet(t, srv)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	send(t, c, "<command id="+quote+"get_old_news"+quote+" count="+quote+"2"+quote+"/>")
	if got := hex.EncodeToString(get()); got != "00f4ffffff070201" {
		t.Fatalf("empty-store payload = %s, want 00f4ffffff070201", got)
	}
}
