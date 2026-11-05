package native

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

func writeTick(w *wire.Writer, secID, board uint16, tradeNo, tm, price, volume int64, period, buySell byte) {
	w.Uint16(secID)
	w.Uint16(board)
	w.Int64(tradeNo)
	w.Int32(int32(tm))
	w.Int32(int32(price))
	w.Int32(int32(volume))
	w.Byte(period)
	w.Byte(buySell)
	w.Int32(0)
	w.Int32(0)
}

func tickMain(payload []byte) []byte { return append([]byte{records.TagTick}, payload...) }

// TestTickAccumulationBatch: two ticks in one 0x5a record; tradeno/time are
// cumulative deltas and the connector renders one <ticks> with both ticks.
func TestTickAccumulationBatch(t *testing.T) {
	var w wire.Writer
	writeTick(&w, 26, 37, 100, 1793865395, 29045, 45, 'N', 'B')
	writeTick(&w, 26, 37, 1, 1, 29050, 10, 'N', 'S')
	w.Uint16(0xffff)
	srv := newRunServer(t)
	srv.Snapshot = append(clientSnapshot(t), tickMain(w.Bytes())...)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	got := rec.next(t, "<ticks")
	if strings.Count(got, "<tick>") != 2 {
		t.Fatalf("ticks batch = %s", got)
	}
	for _, want := range []string{
		"<tradeno>100</tradeno><tradetime>05.11.2026 07:56:35</tradetime>",
		"<tradeno>101</tradeno><tradetime>05.11.2026 07:56:36</tradetime>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("batch lacks %q: %s", want, got)
		}
	}
}

// TestTickZeroTimeFallback: zero accumulated time is replaced by the start of
// the current UTC day (D: FUN_18017c360), not 1970.
func TestTickZeroTimeFallback(t *testing.T) {
	var w wire.Writer
	writeTick(&w, 26, 37, 100, 0, 29045, 1, 'N', 'B')
	w.Uint16(0xffff)
	srv := newRunServer(t)
	srv.Snapshot = append(clientSnapshot(t), tickMain(w.Bytes())...)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	got := rec.next(t, "<ticks")
	wantDate := time.Now().UTC().Format("02.01.2006") + " 00:00:00"
	if !strings.Contains(got, "<tradetime>"+wantDate+"</tradetime>") {
		t.Fatalf("zero-time tick = %s, want %s", got, wantDate)
	}
}

// TestTickParseNegatives: a missing 0xffff terminator and trailing bytes are
// explicit errors, not a silently swallowed stream.
func TestTickParseNegatives(t *testing.T) {
	var noTerm wire.Writer
	writeTick(&noTerm, 26, 37, 1, 1, 1, 1, 'N', 'B')
	if _, err := records.ParseTickRecord(noTerm.Bytes()); err == nil {
		t.Fatal("missing 0xffff accepted")
	}
	var trailing wire.Writer
	writeTick(&trailing, 26, 37, 1, 1, 1, 1, 'N', 'B')
	trailing.Uint16(0xffff)
	trailing.Byte(0xaa)
	if _, err := records.ParseTickRecord(trailing.Bytes()); err == nil {
		t.Fatal("trailing bytes accepted")
	}
	if _, err := records.ParseTickRecord([]byte{0x00}); err == nil {
		t.Fatal("short body accepted")
	}
}

// TestSubscribeTicksWrongSecID: S rejects <secid> in the board/seccode form.
func TestSubscribeTicksWrongSecID(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	cmd := "<command id=" + quote + "subscribe_ticks" + quote + "><filter>true</filter><secid>26</secid></command>"
	want := "<result success=" + quote + "false" + quote + "><message>Wrong security format.</message></result>"
	if got := send(t, c, cmd); got != want {
		t.Fatalf("subscribe_ticks <secid> = %s", got)
	}
}

// TestSubscribeTicksNoRQ_TICKS: the connector never sends the legacy RQ_TICKS
// (13) polling request.
func TestSubscribeTicksNoRQ_TICKS(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	polls := 0
	srv.Handle(13, func([]byte) (byte, []byte) {
		polls++
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
	if got := send(t, c, cmd); !strings.Contains(got, "success="+quote+"true"+quote) {
		t.Fatalf("subscribe_ticks = %s", got)
	}
	time.Sleep(150 * time.Millisecond)
	if polls != 0 {
		t.Fatalf("RQ_TICKS (13) requests = %d, want 0", polls)
	}
}

var _ = session.RqSubscribe

// TestSubscribeTicksMultiSecurity: the S payload mirrors the XML list in reverse
// order: filter then {W secid, W board, G tradeno} for each security.
func TestSubscribeTicksMultiSecurity(t *testing.T) {
	srv := newRunServer(t)
	snapshot := clientSnapshot(t)
	gazpRecs, err := records.Records(secNewRecord(t, "GAZP", 27))
	if err != nil || len(gazpRecs) != 1 {
		t.Fatalf("gazp sec record: %v", err)
	}
	rec := gazpRecs[0]
	snapshot = append(snapshot, rec.Tag, byte(len(rec.Body)>>8), byte(len(rec.Body)))
	snapshot = append(snapshot, rec.Body...)
	srv.Snapshot = snapshot
	var captured []byte
	srv.Handle(120, func(payload []byte) (byte, []byte) {
		captured = append([]byte(nil), payload...)
		return wire.KindReply | wire.FlagPlain, nil
	})
	c := New(Config{Dial: srv.Dial})
	recorder := newRecorder()
	if err := c.Start(recorder.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	recorder.next(t, "<server_status")
	cmd := "<command id=" + quote + "subscribe_ticks" + quote + "><filter>true</filter>"
	cmd += "<security><board>TQBR</board><seccode>SBER</seccode><tradeno>0</tradeno></security>"
	cmd += "<security><board>TQBR</board><seccode>GAZP</seccode><tradeno>0</tradeno></security></command>"
	if got := send(t, c, cmd); !strings.Contains(got, "success="+quote+"true"+quote) {
		t.Fatalf("subscribe_ticks multi = %s", got)
	}
	var w wire.Writer
	w.Byte(1)
	w.Uint16(27)
	w.Uint16(37)
	w.Int64(0)
	w.Uint16(26)
	w.Uint16(37)
	w.Int64(0)
	if hex.EncodeToString(captured) != hex.EncodeToString(w.Bytes()) {
		t.Fatalf("multi subscribe payload = %x, want %x", captured, w.Bytes())
	}
}
