package records_test

import (
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

func newsFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/news-inject.hex")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

// TestNewsRecordsSplit: the unsized 0x35/0x36 records split by their variable
// C-strings (no padding), not by a size prefix.
func TestNewsRecordsSplit(t *testing.T) {
	recs, err := records.Records(newsFixture(t))
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	if len(recs) != 2 || recs[0].Tag != records.TagNews || recs[1].Tag != records.TagNewsUpload {
		t.Fatalf("records = %+v", recs)
	}
	header, err := records.ParseNewsHeader(recs[0].Body)
	if err != nil {
		t.Fatalf("ParseNewsHeader: %v", err)
	}
	if header.ID != 1001 || header.Source != "TESTNEWSSOURCE" || header.Title != "Test news title" {
		t.Fatalf("header = %+v", header)
	}
	upload, err := records.ParseNewsUpload(recs[1].Body)
	if err != nil {
		t.Fatalf("ParseNewsUpload: %v", err)
	}
	if upload.Header == nil || upload.Header.ID != 2002 || upload.Header.Title != "Upload news title" {
		t.Fatalf("upload = %+v", upload)
	}
	body, err := records.UnpackNewsBody(upload.Package)
	if err != nil {
		t.Fatalf("UnpackNewsBody: %v", err)
	}
	if string(body) != "Test news body text" {
		t.Fatalf("body = %q", body)
	}
}

// TestNewsParseNegatives: a C-string without NUL, a bad package and a wrong
// uncompressed length are explicit errors.
func TestNewsParseNegatives(t *testing.T) {
	// 0x35 body truncated in the source C-string.
	body := []byte{0x91, 0x3e, 0x06, 0xa9, 0x90, 0x06, 'A', 'B'}
	if _, err := records.ParseNewsHeader(body); err == nil {
		t.Fatal("truncated C-string accepted")
	}
	var w wire.Writer
	w.Byte(2)                 // flags: body only
	w.Int32(7)                // news id
	w.Raw(make([]byte, 4))    // S: unknown 4-byte field
	w.Raw([]byte{1, 2, 3, 4}) // not a zlib stream
	if _, err := records.ParseNewsUpload(w.Bytes()); !errors.Is(err, records.ErrUnknownRecord) {
		t.Fatalf("bad S package err = %v, want ErrUnknownRecord", err)
	}
}

// TestCStringBoundaries: exactly max characters are valid (NUL is the next
// byte), one more is an explicit error (news.md/messages.md: length edge is I).
func TestCStringBoundaries(t *testing.T) {
	mkNews := func(source, title string) []byte {
		var w wire.Writer
		w.Int32(1)
		w.Int32(1762300000)
		w.Raw([]byte(source))
		w.Byte(0)
		w.Raw([]byte(title))
		w.Byte(0)
		w.Byte(0)
		return w.Bytes()
	}
	if _, err := records.ParseNewsHeader(mkNews(strings.Repeat("S", 16), strings.Repeat("T", 128))); err != nil {
		t.Fatalf("max-length header: %v", err)
	}
	if _, err := records.ParseNewsHeader(mkNews(strings.Repeat("S", 17), "T")); err == nil {
		t.Fatal("17-byte source accepted")
	}
	if _, err := records.ParseNewsHeader(mkNews("S", strings.Repeat("T", 129))); err == nil {
		t.Fatal("129-byte title accepted")
	}
	var msg wire.Writer
	msg.Byte(0)
	msg.String(strings.Repeat("F", 20), 20)
	msg.Int32(1762300000)
	msg.String(strings.Repeat("X", 1024), 1024)
	if _, err := records.ParseRMsg(msg.Bytes()); err != nil {
		t.Fatalf("max-length message: %v", err)
	}
	var tooLong wire.Writer
	tooLong.Byte(0)
	tooLong.Raw([]byte(strings.Repeat("F", 21)))
	tooLong.Byte(0)
	tooLong.Int32(1762300000)
	tooLong.String("X", 1024)
	if _, err := records.ParseRMsg(tooLong.Bytes()); err == nil {
		t.Fatal("21-byte from accepted")
	}
}

// TestNewsMixedStream: unsized news records are followed by the next record
// without losing alignment (0x18 overnight after 0x35+0x36).
func TestNewsMixedStream(t *testing.T) {
	stream := append(newsFixture(t), byte(0x18), 0x01)
	recs, err := records.Records(stream)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	if len(recs) != 3 || recs[2].Tag != 0x18 {
		t.Fatalf("records = %+v", recs)
	}
}

// TestNewsTwoHeaders: two 0x35 records back to back split correctly.
func TestNewsTwoHeaders(t *testing.T) {
	recs, err := records.Records(newsFixture(t))
	if err != nil || len(recs) != 2 {
		t.Fatalf("fixture: %v", err)
	}
	stream := append(append([]byte{recs[0].Tag}, recs[0].Body...), append([]byte{recs[0].Tag}, recs[0].Body...)...)
	got, err := records.Records(stream)
	if err != nil || len(got) != 2 {
		t.Fatalf("two headers: %v, %+v", err, got)
	}
}

// TestNewsUploadFlags2: the body-only form (flags=2) has its own news id.
func TestNewsUploadFlags2(t *testing.T) {
	recs, err := records.Records(newsFixture(t))
	if err != nil || len(recs) != 2 {
		t.Fatalf("fixture: %v", err)
	}
	upload, err := records.ParseNewsUpload(recs[1].Body)
	if err != nil || upload.Header == nil {
		t.Fatalf("upload: %v", err)
	}
	var w wire.Writer
	w.Byte(2)
	w.Int32(int32(upload.Header.ID))
	w.Raw(make([]byte, 4)) // S: unknown 4-byte field, then package
	w.Raw(upload.Package)
	parsed, err := records.ParseNewsUpload(w.Bytes())
	if err != nil {
		t.Fatalf("flags=2 parse: %v", err)
	}
	if !parsed.HasID || parsed.NewsID != upload.Header.ID || len(parsed.Package) != len(upload.Package) {
		t.Fatalf("parsed = %+v", parsed)
	}
	if body, err := records.UnpackNewsBody(parsed.Package); err != nil || string(body) != "Test news body text" {
		t.Fatalf("body = %q, %v", body, err)
	}
}

// TestNewsUploadFlagForms: flags=1 (header only) and flags=2 (body only) are
// both unsized walkers and keep the stream aligned when concatenated.
func TestNewsUploadFlagForms(t *testing.T) {
	recs, err := records.Records(newsFixture(t))
	if err != nil || len(recs) != 2 {
		t.Fatalf("fixture: %v", err)
	}
	upload, err := records.ParseNewsUpload(recs[1].Body)
	if err != nil || upload.Header == nil {
		t.Fatalf("upload: %v", err)
	}
	var header wire.Writer
	header.Byte(1)
	header.Int32(1500)
	header.Int32(1762300000)
	header.String("SRC", 16)
	header.String("Title", 128)
	header.Byte(0)
	var body wire.Writer
	body.Byte(2)
	body.Int32(2002)
	body.Raw(make([]byte, 4)) // S: unknown 4-byte field, then int32+zlib package
	body.Raw(upload.Package)
	stream := append([]byte{records.TagNewsUpload}, header.Bytes()...)
	stream = append(stream, records.TagNewsUpload)
	stream = append(stream, body.Bytes()...)
	got, err := records.Records(stream)
	if err != nil || len(got) != 2 {
		t.Fatalf("flags forms: %v, %+v", err, got)
	}
	if parsed, err := records.ParseNewsUpload(got[0].Body); err != nil || parsed.Header == nil || parsed.Header.ID != 1500 {
		t.Fatalf("flags=1: %+v, %v", parsed, err)
	}
	if parsed, err := records.ParseNewsUpload(got[1].Body); err != nil || !parsed.HasID || parsed.NewsID != 2002 {
		t.Fatalf("flags=2: %+v, %v", parsed, err)
	}
}
