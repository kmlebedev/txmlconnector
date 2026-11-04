package records_test

import (
	"encoding/hex"
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
	w.Int32(4)                // package length
	w.Raw([]byte{1, 2, 3, 4}) // not a zlib stream
	upload, err := records.ParseNewsUpload(w.Bytes())
	if err != nil {
		t.Fatalf("ParseNewsUpload: %v", err)
	}
	if _, err := records.UnpackNewsBody(upload.Package); err == nil {
		t.Fatal("bad package accepted")
	}
}
