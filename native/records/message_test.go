package records_test

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/records"
)

func messageFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/message-inject.hex")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

// TestMessageRecordsSplit: R_MSG is unsized with variable C-strings; both
// records of the fixture are split and decoded field by field.
func TestMessageRecordsSplit(t *testing.T) {
	recs, err := records.Records(messageFixture(t))
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	if len(recs) != 2 || recs[0].Tag != records.TagMessage || recs[1].Tag != records.TagMessage {
		t.Fatalf("records = %+v", recs)
	}
	for i, rec := range recs {
		msg, err := records.ParseRMsg(rec.Body)
		if err != nil {
			t.Fatalf("ParseRMsg[%d]: %v", i, err)
		}
		wantFrom := []string{"TESTUSER", "TESTURGENT"}[i]
		wantText := []string{"Test message text", "Urgent message text"}[i]
		if msg.From != wantFrom || msg.Text != wantText || msg.Date == 0 {
			t.Fatalf("msg[%d] = %+v", i, msg)
		}
		if i == 0 && msg.Flags != 0 {
			t.Fatalf("msg[0] flags = %#x", msg.Flags)
		}
		if i == 1 && msg.Flags != 1 {
			t.Fatalf("msg[1] flags = %#x", msg.Flags)
		}
	}
}

// TestMessageParseNegatives: short body, C-string without NUL and trailing data
// are explicit errors.
func TestMessageParseNegatives(t *testing.T) {
	if _, err := records.ParseRMsg(nil); err == nil {
		t.Fatal("nil body accepted")
	}
	// flags + "AB" without NUL.
	if _, err := records.ParseRMsg([]byte{0, 'A', 'B'}); err == nil {
		t.Fatal("unterminated C-string accepted")
	}
	body := append(messageFixture(t), 0xff)
	recs, err := records.Records(body)
	if err != nil {
		// The splitter sees the trailing byte as the next tag: it must not
		// silently swallow it either way.
		return
	}
	for _, rec := range recs {
		if rec.Tag == records.TagMessage {
			if _, err := records.ParseRMsg(rec.Body); err != nil {
				continue
			}
		}
	}
	t.Fatal("trailing byte accepted")
}
