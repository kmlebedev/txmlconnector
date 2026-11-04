package records_test

import (
	"compress/gzip"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/internal/oracle"
	"github.com/kmlebedev/txmlconnector/native/records"
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
	ev := part.Event(catalog)
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
	ev := records.CombineCandles(parts, catalog)
	got, ok := xmlcompat.Render(catalog, &ev, xmlcompat.Options{})
	if !ok {
		t.Fatal("no XML form")
	}
	want := strings.TrimSpace(readPlainText(t, "candles-multipart-dll.xml"))
	if got != want {
		t.Fatalf("multipart candles\n got %s\nwant %s", got, want)
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
