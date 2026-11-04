package records_test

import (
	"compress/gzip"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// readMiscFixture loads the compact live-session slice of the task 12 records
// (native/records/testdata/misc-live.hex.gz): 8x R_FORTS_SESSION, one AmBOOK and
// three R_WRAPPER records with nested R_EDP_RATE.
func readMiscFixture(t *testing.T) []byte {
	t.Helper()
	f, err := os.Open("testdata/misc-live.hex.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("fixture hex: %v", err)
	}
	return payload
}

// TestMiscLiveRecords checks that the records of the live dumps are split and
// their parsers accept the real bodies: none of them produces events.
func TestMiscLiveRecords(t *testing.T) {
	payload := readMiscFixture(t)
	recs, err := records.Records(payload)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	counts := map[byte]int{}
	var edp *records.Record
	var book *records.Record
	var session *records.Record
	for i := range recs {
		rec := &recs[i]
		counts[rec.Tag]++
		switch rec.Tag {
		case records.TagEdpRate:
			edp = rec
		case records.TagAmBook:
			book = rec
		case records.TagFortsSession:
			session = rec
		}
	}
	if counts[records.TagFortsSession] != 8 || counts[records.TagEdpRate] != 3 ||
		counts[records.TagAmBook] != 1 || counts[records.TagSysParams] != 1 || counts[records.TagState] != 1 {
		t.Fatalf("tag counts = %v, want 8x0x6b, 3x0x6e, 1x0x5c, 1x0x5b, 1x0x05", counts)
	}
	if _, err := records.ParseFortsSession(session.Body); err != nil {
		t.Fatalf("R_FORTS_SESSION: %v", err)
	}
	if rate, err := records.ParseEdpRate(edp.Body); err != nil {
		t.Fatalf("R_EDP_RATE: %v", err)
	} else if rate.Name == "" || rate.Pair == "" {
		t.Fatalf("R_EDP_RATE = %+v", rate)
	}
	// The live r10 AmBOOK record is an empty book (body 20 86 ff).
	bookRec, err := records.ParseAmBook(book.Body)
	if err != nil || bookRec.SecID != 0x2086 || len(bookRec.Levels) != 0 {
		t.Fatalf("live AmBOOK = %+v, %v", bookRec, err)
	}
	// A synthetic level exercises the flags-driven conditional fields.
	var w wire.Writer
	w.Uint16(26)
	w.Byte(0x0f) // byte+int1+int2+text
	w.Byte(7)
	w.Int32(100)
	w.Int32(200)
	w.String("RUR", 4)
	w.Byte(0xff)
	bookRec, err = records.ParseAmBook(w.Bytes())
	if err != nil || bookRec.SecID != 26 || len(bookRec.Levels) != 1 {
		t.Fatalf("AmBOOK synthetic = %+v, %v", bookRec, err)
	}
	if level := bookRec.Levels[0]; level.Byte != 7 || level.Int1 != 100 || level.Int2 != 200 || level.Text != "RUR" {
		t.Fatalf("AmBOOK level = %+v", level)
	}
}

// TestFullLiveSessionHasNoUnknownRecords is the task 12 goal test. The live
// dump is env-gated: TXML_DEMO_MAIN holds one hex RQ_MAIN payload per line.
//
// The E07 tail carries 0x5b R_SYSPARAMS and the empty 0x05 state record; both
// are handled now, so the full dump must split with no unknown record.
func TestFullLiveSessionHasNoUnknownRecords(t *testing.T) {
	path := os.Getenv("TXML_DEMO_MAIN")
	if path == "" {
		t.Skip("TXML_DEMO_MAIN is not set")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	payloads := 0
	for n, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		payload, err := hex.DecodeString(line)
		if err != nil {
			t.Fatalf("line %d: %v", n+1, err)
		}
		payloads++
		if _, err := records.Records(payload); err != nil {
			t.Fatalf("line %d: %v", n+1, err)
		}
	}
	if payloads == 0 {
		t.Fatal("the dump has no payloads")
	}
}

// TestSizedMiscRecords checks the sized task 12 records (R_BOARD 0x34 and
// ReadICA 0x7c): the splitter yields them and stays aligned.
func TestSizedMiscRecords(t *testing.T) {
	payload := []byte{
		records.TagBoard, 0, 5, 1, 2, 3, 4, 5,
		records.TagReadICA, 0, 3, 7, 8, 9,
	}
	recs, err := records.Records(payload)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	if len(recs) != 2 || recs[0].Tag != records.TagBoard || recs[1].Tag != records.TagReadICA {
		t.Fatalf("records = %+v", recs)
	}
	if len(recs[0].Body) != 5 || len(recs[1].Body) != 3 {
		t.Fatalf("bodies = %d/%d", len(recs[0].Body), len(recs[1].Body))
	}
}

// readGzHexFixture loads a hex .gz fixture of native/records/testdata.
func readGzHexFixture(t *testing.T, name string) []byte {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return payload
}

// TestE07TailHasNoUnknownRecords is the compact full-session oracle: the E07
// RQ_MAIN tail (41070 bytes, r13) containing the 0x5b R_SYSPARAMS blob, the
// empty 0x05 state record, R_WRAPPER records and the AmBOOK must split without
// an unknown record. A Client.Poll turns a Records error into
// Stats().UnknownRecords, so the zero here is the same contract.
func TestE07TailHasNoUnknownRecords(t *testing.T) {
	payload := readGzHexFixture(t, "e07-tail-live.hex.gz")
	recs, err := records.Records(payload)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	if len(recs) < 600 {
		t.Fatalf("records = %d, want the full tail (600+)", len(recs))
	}
	hasSysParams := false
	for _, rec := range recs {
		if rec.Tag == records.TagSysParams {
			hasSysParams = true
			if len(rec.Body) == 0 || rec.Body[0] == 0 {
				t.Fatalf("R_SYSPARAMS body = %x", rec.Body)
			}
		}
	}
	if !hasSysParams {
		t.Fatal("no R_SYSPARAMS record in the E07 tail")
	}
}

// TestMiscParsersNegative covers the short bodies of the task 12 parsers.
func TestMiscParsersNegative(t *testing.T) {
	if _, err := records.ParseAmBook(nil); err == nil {
		t.Fatal("ParseAmBook(nil) did not fail")
	}
	if _, err := records.ParseAmBook([]byte{0x20}); err == nil {
		t.Fatal("ParseAmBook(short secid) did not fail")
	}
	if _, err := records.ParseAmBook([]byte{0x20, 0x86, 0x01}); err == nil {
		t.Fatal("ParseAmBook(no 0xff) did not fail")
	}
	if _, err := records.ParseFortsSession(nil); err == nil {
		t.Fatal("ParseFortsSession(nil) did not fail")
	}
	if _, err := records.ParseEdpRate([]byte{0x45}); err == nil {
		t.Fatal("ParseEdpRate(short) did not fail")
	}
}

// TestKnownUnseenTagReturnsUnknownRecord pins the task 12 boundary: tags whose
// layouts are not recovered (0x38/0x39/0x42/0x43/0x4f/0x5d/0x74/0x79) surface a
// clear UnknownRecordError instead of being dropped or panicking. The option
// tags 0x6f/0x71/0x78 are implemented by task 27.
func TestKnownUnseenTagReturnsUnknownRecord(t *testing.T) {
	for _, tag := range []byte{0x38, 0x39, 0x42, 0x43, 0x4f, 0x5d, 0x74, 0x79} {
		_, err := records.Records([]byte{tag, 0x00, 0x00})
		var unknown *records.UnknownRecordError
		if !errors.As(err, &unknown) || unknown.Tag != tag {
			t.Fatalf("tag %#02x: err = %v, want UnknownRecordError for the tag", tag, err)
		}
	}
}
