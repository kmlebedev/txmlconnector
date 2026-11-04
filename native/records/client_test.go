package records

import (
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/wire"
)

// readSnapshotTail decodes the E07 end-of-load fixture: a single hex string of
// the decrypted RQ_MAIN tail (task 7).
func readSnapshotTail(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("../testdata/snapshot-tail-live.hex")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(strings.Fields(string(data)), "")
	payload, err := hex.DecodeString(text)
	if err != nil {
		t.Fatalf("fixture hex: %v", err)
	}
	return payload
}

// TestParseEndOfLoadRecords checks the R_CLN_NEW and R_CLIENT_ASSET values of
// the live fixture against the RE-1 tables (docs/protocol/connect.md).
func TestParseEndOfLoadRecords(t *testing.T) {
	payload := readSnapshotTail(t)
	recs, err := Records(payload)
	var unknown *UnknownRecordError
	if err != nil && !errors.As(err, &unknown) {
		t.Fatalf("Records: %v", err)
	}
	if unknown != nil {
		t.Logf("the rest of the E07 tail is not mapped yet (task 12): tag %#02x at offset %d", unknown.Tag, unknown.Offset)
	}
	var client *Record
	var asset *Record
	for i := range recs {
		switch recs[i].Tag {
		case TagClientNew:
			client = &recs[i]
		case TagClientAsset:
			asset = &recs[i]
		}
	}
	if client == nil || asset == nil {
		t.Fatalf("fixture has no R_CLN_NEW/R_CLIENT_ASSET: %v", tags(recs))
	}
	cn, err := ParseClientNew(client.Body)
	if err != nil {
		t.Fatalf("ParseClientNew: %v", err)
	}
	if cn.Code != "TESTCLIEN" || cn.Login != "TESTLOGI" {
		t.Fatalf("client strings = %q/%q", cn.Code, cn.Login)
	}
	if typ, ok := ClientTypeName(cn.Type); !ok || typ != "leverage" {
		t.Fatalf("client type = %d (%q), want leverage", cn.Type, typ)
	}
	if cn.Market != 1 || cn.Union != "" || cn.CurrencyID != 1 || cn.Word != 100 {
		t.Fatalf("client fields = %+v", cn)
	}
	if got := CurrencyName(cn.CurrencyID); got != "RUB" {
		t.Fatalf("currency name = %q, want RUB", got)
	}
	ca, err := ParseClientAsset(asset.Body)
	if err != nil {
		t.Fatalf("ParseClientAsset: %v", err)
	}
	if ca.ClientID != cn.ID || ca.SymID != 0xffff {
		t.Fatalf("asset ids = %d/%#04x", ca.ClientID, ca.SymID)
	}
	if len(ca.Regs) != 1 || ca.Regs[0].OpenBalance != 30000000 || ca.Regs[0].Currency != "RUR" {
		t.Fatalf("asset regs = %+v", ca.Regs)
	}
}

// tags returns the record tags for diagnostics.
func tags(recs []Record) []byte {
	out := make([]byte, len(recs))
	for i, r := range recs {
		out[i] = r.Tag
	}
	return out
}

// TestParseOvernightAndUnion covers the records that are not in the demo
// fixture: R_OVERNIGHT and the decompiler-only R_UNION.
func TestParseOvernightAndUnion(t *testing.T) {
	if status, err := ParseOvernight([]byte{0}); err != nil || status {
		t.Fatalf("overnight false = %v, %v", status, err)
	}
	if status, err := ParseOvernight([]byte{1}); err != nil || !status {
		t.Fatalf("overnight true = %v, %v", status, err)
	}
	var w wire.Writer
	w.Int64(42)
	w.String("TESTUNION", 20)
	w.String("TESTCAT", 32)
	w.Byte(7)
	name, err := ParseUnion(w.Bytes())
	if err != nil || name != "TESTUNION" {
		t.Fatalf("union name = %q, %v", name, err)
	}
}

// TestParseShortBodies covers the negative paths of the end-of-load parsers.
func TestParseShortBodies(t *testing.T) {
	if _, err := ParseClientNew(nil); !errors.Is(err, wire.ErrShortBuffer) {
		t.Fatalf("ParseClientNew(nil) = %v, want ErrShortBuffer", err)
	}
	if _, err := ParseClientNew([]byte{0x01}); !errors.Is(err, wire.ErrShortBuffer) {
		t.Fatalf("ParseClientNew(short) = %v, want ErrShortBuffer", err)
	}
	if _, err := ParseClientAsset(nil); !errors.Is(err, wire.ErrShortBuffer) {
		t.Fatalf("ParseClientAsset(nil) = %v, want ErrShortBuffer", err)
	}
	if _, err := ParseUnion(nil); !errors.Is(err, wire.ErrShortBuffer) {
		t.Fatalf("ParseUnion(nil) = %v, want ErrShortBuffer", err)
	}
	if _, err := ParseOvernight(nil); !errors.Is(err, wire.ErrShortBuffer) {
		t.Fatalf("ParseOvernight(nil) = %v, want ErrShortBuffer", err)
	}
}

// TestStateOnlyRecords checks the tail records that must yield no events:
// 0x62/0x67/0x6b/0x69 are state updates, and 0x69 must be terminated by 0xff.
func TestStateOnlyRecords(t *testing.T) {
	payload := []byte{
		0x62, 0x00, 0x00,
		0x67, 0x01, 0x02,
		0x6b, 0x00, // R_FORTS_SESSION: packed L session = 0
		0x69, 0x01, 0x02, 0xff,
	}
	recs, err := Records(payload)
	if err != nil || len(recs) != 4 {
		t.Fatalf("records = %v, %v", tags(recs), err)
	}
	for _, rec := range recs {
		if rec.Tag != 0x62 && rec.Tag != 0x67 && rec.Tag != 0x6b && rec.Tag != 0x69 {
			t.Fatalf("unexpected tag %#02x", rec.Tag)
		}
	}
	// Without the 0xff terminator the rest of the frame cannot be split.
	_, err = Records([]byte{0x69, 0x01, 0x02})
	var unknown *UnknownRecordError
	if !errors.As(err, &unknown) || unknown.Tag != 0x69 {
		t.Fatalf("unterminated 0x69: err = %v, want UnknownRecordError(0x69)", err)
	}
}
