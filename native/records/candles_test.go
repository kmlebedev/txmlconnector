package records_test

import (
	"compress/gzip"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/internal/oracle"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/wire"
	"github.com/kmlebedev/txmlconnector/native/xmlcompat"
)

func readCandlesHex(t *testing.T, name string) []byte {
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

func readCandlesText(t *testing.T, name string) string {
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
	return strings.TrimSpace(string(raw))
}

// TestCandlesMatchDLL parses the live 0x09 record (10 SBER one-minute candles)
// and compares the rendered <candles> with the DLL callback byte for byte.
func TestCandlesMatchDLL(t *testing.T) {
	payload := readCandlesHex(t, "candles-live.hex.gz")
	recs, err := records.Records(payload)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	if len(recs) != 1 || recs[0].Tag != records.TagCandles {
		t.Fatalf("records = %+v", recs)
	}
	part, err := records.ParseCandles(recs[0].Body)
	if err != nil {
		t.Fatalf("ParseCandles: %v", err)
	}
	if len(part.Items) != 10 || part.Status != 1 {
		t.Fatalf("items/status = %d/%d, want 10/1", len(part.Items), part.Status)
	}
	catalog := oracle.LoadCatalog(t, "main1-demo.hex.gz")
	ev := part.Event(catalog, 0)
	got, ok := xmlcompat.Render(catalog, &ev, xmlcompat.Options{})
	if !ok {
		t.Fatal("no XML form")
	}
	want := readCandlesText(t, "candles-dll.xml.gz")
	if i := strings.Index(want, "</candles>"); i >= 0 {
		want = want[:i+len("</candles>")]
	}
	if got != want {
		t.Fatalf("candles\n got %s\nwant %s", got, want)
	}
}

// TestCandlesMultipart checks that two parts (status 2 then 1) render as the
// single chronological DLL callback of the oracle multipart fixture.
func TestCandlesMultipart(t *testing.T) {
	catalog := oracle.LoadCatalog(t, "main1-demo.hex.gz")
	var parts []records.Candles
	for _, name := range []string{"candles-part1-inject.hex", "candles-part2-inject.hex"} {
		payload := readPlainHex(t, name)
		recs, err := records.Records(payload)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		part, err := records.ParseCandles(recs[0].Body)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		parts = append(parts, part)
	}
	if parts[0].Status != 2 || parts[1].Status != 1 {
		t.Fatalf("statuses = %d/%d, want 2/1", parts[0].Status, parts[1].Status)
	}
	ev := records.CombineCandles(parts, catalog, 0)
	got, ok := xmlcompat.Render(catalog, &ev, xmlcompat.Options{})
	if !ok {
		t.Fatal("no XML form")
	}
	want := strings.TrimSpace(readPlainText(t, "candles-multipart-dll.xml"))
	if got != want {
		t.Fatalf("multipart candles\n got %s\nwant %s", got, want)
	}
}

// candleRecord builds one synthetic R_HISTORY byte body for the given wire
// items (newest first, minutes/deltas as the wire format).
func candleRecord(t *testing.T, secID, board uint16, period, flag byte, items []records.Candle) []byte {
	t.Helper()
	var w wire.Writer
	w.Uint16(secID)
	w.Uint16(board)
	w.Byte(period)
	w.Byte(flag)
	for _, item := range items {
		w.Int32(int32(item.Time))
		w.Int32(int32(item.Open))
		w.Int32(int32(item.Close))
		w.Int32(int32(item.High))
		w.Int32(int32(item.Low))
		w.Int32(int32(item.Field6))
		w.Int32(int32(item.Volume))
	}
	w.Int32(1) // final status
	return append([]byte{records.TagCandles}, w.Bytes()...)
}

// TestCombineCandlesSinglePartOrder is the P1 regression: CombineCandles on one
// 10-candle part keeps the DLL chronological order (18:35 -> 18:44) and its XML
// matches the live callback.
func TestCombineCandlesSinglePartOrder(t *testing.T) {
	payload := readCandlesHex(t, "candles-live.hex.gz")
	recs, err := records.Records(payload)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	part, err := records.ParseCandles(recs[0].Body)
	if err != nil {
		t.Fatalf("ParseCandles: %v", err)
	}
	catalog := oracle.LoadCatalog(t, "main1-demo.hex.gz")
	combined := records.CombineCandles([]records.Candles{part}, catalog, 0)
	if len(combined.Items) != 10 {
		t.Fatalf("items = %d, want 10", len(combined.Items))
	}
	if got := combined.Items[0].Time.Format("15:04"); got != "18:35" {
		t.Fatalf("first time = %s, want 18:35 (double reverse regression)", got)
	}
	if got := combined.Items[9].Time.Format("15:04"); got != "18:44" {
		t.Fatalf("last time = %s, want 18:44", got)
	}
	got, ok := xmlcompat.Render(catalog, &combined, xmlcompat.Options{})
	if !ok {
		t.Fatal("no XML form")
	}
	want := readCandlesText(t, "candles-dll.xml.gz")
	if i := strings.Index(want, "</candles>"); i >= 0 {
		want = want[:i+len("</candles>")]
	}
	if got != want {
		t.Fatalf("combined candles\n got %s\nwant %s", got, want)
	}
}

// TestCombineCandlesMultipartOrder is the P1 multipart regression with more
// than one candle per part: parts arrive newest first and each part is already
// chronological; the result must be 18:41..18:44.
func TestCombineCandlesMultipartOrder(t *testing.T) {
	part1 := candleRecord(t, 26, 37, 1, 0, []records.Candle{
		{Time: 29896964, Open: 2914600}, // 18:44 absolute
		{Time: -1, Open: 2914200},       // 18:43 delta
	})
	part2 := candleRecord(t, 26, 37, 1, 0, []records.Candle{
		{Time: 29896962, Open: 2914100}, // 18:42 absolute
		{Time: -1, Open: 2914000},       // 18:41 delta
	})
	var parts []records.Candles
	for _, payload := range [][]byte{part1, part2} {
		recs, err := records.Records(payload)
		if err != nil {
			t.Fatalf("Records: %v", err)
		}
		part, err := records.ParseCandles(recs[0].Body)
		if err != nil {
			t.Fatalf("ParseCandles: %v", err)
		}
		parts = append(parts, part)
	}
	catalog := oracle.LoadCatalog(t, "main1-demo.hex.gz")
	combined := records.CombineCandles(parts, catalog, 0)
	if len(combined.Items) != 4 {
		t.Fatalf("items = %d, want 4", len(combined.Items))
	}
	want := []string{"18:41", "18:42", "18:43", "18:44"}
	for i, item := range combined.Items {
		if got := item.Time.Format("15:04"); got != want[i] {
			t.Fatalf("item %d = %s, want %s (parts must be reversed, items kept)", i, got, want[i])
		}
	}
}

// TestCandlesTimeBias is the P2.1 regression: the wire minutes are the server
// wall clock, the event Time is UTC (15:44Z) and the DLL XML keeps 18:44.
func TestCandlesTimeBias(t *testing.T) {
	payload := readCandlesHex(t, "candles-live.hex.gz")
	recs, err := records.Records(payload)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	part, err := records.ParseCandles(recs[0].Body)
	if err != nil {
		t.Fatalf("ParseCandles: %v", err)
	}
	catalog := oracle.LoadCatalog(t, "main1-demo.hex.gz")
	bias := -180 * time.Minute
	ev := part.Event(catalog, bias)
	last := ev.Items[len(ev.Items)-1]
	if got := last.Time.UTC().Format("15:04"); got != "15:44" {
		t.Fatalf("event time = %s, want 15:44Z", got)
	}
	got, ok := xmlcompat.Render(catalog, &ev, xmlcompat.Options{})
	if !ok {
		t.Fatal("no XML form")
	}
	if !strings.Contains(got, "04.11.2026 18:44:00") {
		t.Fatalf("XML must keep the wall clock 18:44: %s", got)
	}
}

// TestHistorySplitterRejectsTrailing pins the P3.3 diagnostic: an unsized
// R_HISTORY followed by more bytes is an explicit error, not a silent swallow.
func TestHistorySplitterRejectsTrailing(t *testing.T) {
	concat := append(readPlainHex(t, "candles-part1-inject.hex"), readPlainHex(t, "candles-part2-inject.hex")...)
	if _, err := records.Records(concat); err == nil || !strings.Contains(err.Error(), "unexpected data after the record status") {
		t.Fatalf("err = %v, want the trailing-bytes diagnostic", err)
	}
}

func readPlainHex(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return payload
}

func readPlainText(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestCandlesTimeUnitFollowsPeriod pins the P1 fix of the live demo run of
// 05.11.2026: the wire time of a candle record is expressed in units of the
// candle period, not always in Unix minutes. With period kind 2 (300 s) the
// absolute value 5979680 is 05.11.2026 18:40 MSK, not a 1981 date; the 1-minute
// fixtures keep matching Unix minutes because their period is 60 s.
func TestCandlesTimeUnitFollowsPeriod(t *testing.T) {
	payload := candleRecord(t, 26, 37, 2, 0, []records.Candle{
		{Time: 5979680, Open: 2932200}, // newest: 18:40 MSK 05.11.2026
		{Time: -1, Open: 2931300},      // 18:35 MSK
	})
	recs, err := records.Records(payload)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	part, err := records.ParseCandles(recs[0].Body)
	if err != nil {
		t.Fatalf("ParseCandles: %v", err)
	}
	catalog := oracle.LoadCatalog(t, "main1-demo.hex.gz")
	bias := -180 * time.Minute
	ev := part.Event(catalog, bias)
	if len(ev.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(ev.Items))
	}
	if got := ev.Items[0].Time.UTC().Format("2006-01-02 15:04"); got != "2026-11-05 15:35" {
		t.Fatalf("first candle = %s, want 2026-11-05 15:35Z (18:35 MSK)", got)
	}
	if got := ev.Items[1].Time.UTC().Format("2006-01-02 15:04"); got != "2026-11-05 15:40" {
		t.Fatalf("last candle = %s, want 2026-11-05 15:40Z (18:40 MSK)", got)
	}
	got, ok := xmlcompat.Render(catalog, &ev, xmlcompat.Options{})
	if !ok {
		t.Fatal("no XML form")
	}
	if !strings.Contains(got, `date="05.11.2026 18:40:00"`) {
		t.Fatalf("XML must render 05.11.2026 18:40:00, got %s", got)
	}
}
