package native

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// TestNewsBodyCallbackS: get_news_body sends RQ_GETNEWS (reply empty) and the
// body arrives as RQ_MAIN 0x36 flags=2, rendered byte-exactly as <news_body>.
func TestNewsBodyCallbackS(t *testing.T) {
	headerRaw, err := os.ReadFile("records/testdata/news-header-live-inject.hex")
	if err != nil {
		t.Fatal(err)
	}
	headerPayload, err := hex.DecodeString(strings.TrimSpace(string(headerRaw)))
	if err != nil {
		t.Fatal(err)
	}
	headerRecs, err := records.Records(headerPayload)
	if err != nil || len(headerRecs) != 1 {
		t.Fatalf("header fixture: %v", err)
	}
	header, err := records.ParseNewsUpload(headerRecs[0].Body)
	if err != nil || header.Header == nil {
		t.Fatalf("header: %+v, %v", header, err)
	}
	bodyRaw, err := os.ReadFile("records/testdata/news-body-live-inject.hex")
	if err != nil {
		t.Fatal(err)
	}
	bodyPayload, err := hex.DecodeString(strings.TrimSpace(string(bodyRaw)))
	if err != nil {
		t.Fatal(err)
	}
	bodyRecs, err := records.Records(bodyPayload)
	if err != nil || len(bodyRecs) != 1 {
		t.Fatalf("body fixture: %v", err)
	}
	bodyRecord, err := records.ParseNewsUpload(bodyRecs[0].Body)
	if err != nil || len(bodyRecord.Package) == 0 {
		t.Fatalf("body record: %+v, %v", bodyRecord, err)
	}
	bodyID := bodyRecord.NewsID
	// Synthesize a header with the id the body record carries (the S header
	// fixture id differs; DLL had the matching header in its store from
	// get_old_news).
	var hw wire.Writer
	hw.Byte(1)
	hw.Int32(int32(bodyID))
	hw.Int32(int32(header.Header.Timestamp))
	hw.String(header.Header.Source, 16)
	hw.String(header.Header.Title, 128)
	hw.Byte(header.Header.Flag)
	patchedHeader := append([]byte{records.TagNewsUpload}, hw.Bytes()...)

	srv := newRunServer(t)
	srv.Snapshot = append(clientSnapshot(t), patchedHeader...)
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
	cmd := "<command id=" + quote + "get_news_body" + quote + " news_id=" + quote + "15979131" + quote + "/>"
	if got := send(t, c, cmd); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("get_news_body = %s", got)
	}
	srv.QueueMain(bodyPayload)
	wantRaw, err := os.ReadFile("records/testdata/news-body-live-dll.xml")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSpace(string(wantRaw))
	if got := rec.next(t, "<news_body"); got != want {
		t.Fatalf("news_body\n got %s\nwant %s", got, want)
	}
}
