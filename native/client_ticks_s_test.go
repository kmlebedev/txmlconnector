package native

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// TestTickMatchDLL: S tick arrives as RQ_MAIN 0x5a and renders the S <ticks>
// form. The fixture identity (secid 2412/board 75) is patched to the synthetic
// catalog (26/37) to keep the XML byte-exact.
func TestTickMatchDLL(t *testing.T) {
	raw, err := os.ReadFile("records/testdata/ticks-live-inject.hex")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	recs, err := records.Records(payload)
	if err != nil || len(recs) != 1 {
		t.Fatalf("tick fixture: %v", err)
	}
	body := append([]byte(nil), recs[0].Body...)
	body[0], body[1] = 0, 26 // secid
	body[2], body[3] = 0, 37 // board
	patched := append([]byte{records.TagTick}, body...)
	wantRaw, err := os.ReadFile("records/testdata/ticks-live-dll.xml")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.ReplaceAll(strings.TrimSpace(string(wantRaw)), "<secid>2412</secid>", "<secid>26</secid>")
	srv := newRunServer(t)
	srv.Snapshot = append(clientSnapshot(t), patched...)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	if got := rec.next(t, "<ticks"); got != want {
		t.Fatalf("ticks\n got %s\nwant %s", got, want)
	}
}

// TestSubscribeTicksSForm: the S subscribe_ticks payload is B filter then
// {W secid, W board, G tradeno} (board/secid from the catalog).
func TestSubscribeTicksSForm(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	var captured []byte
	srv.Handle(120, func(payload []byte) (byte, []byte) {
		captured = append([]byte(nil), payload...)
		return wire.KindReply | wire.FlagPlain, nil
	})
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	cmd := "<command id=" + quote + "subscribe_ticks" + quote + "><filter>true</filter>"
	cmd += "<security><board>TQBR</board><seccode>SBER</seccode><tradeno>0</tradeno></security></command>"
	if got := send(t, c, cmd); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("subscribe_ticks = %s", got)
	}
	var w wire.Writer
	w.Byte(1)
	w.Uint16(26)
	w.Uint16(37)
	w.Int64(0)
	if hex.EncodeToString(captured) != hex.EncodeToString(w.Bytes()) {
		t.Fatalf("subscribe_ticks payload = %x, want %x", captured, w.Bytes())
	}
}
