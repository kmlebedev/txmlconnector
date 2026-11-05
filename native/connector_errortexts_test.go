package native

import (
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// TestServerErrorTexts checks the DLL rendering of RQ_TRANSACT server codes
// (errors.md): codes 1..6 have O RU texts (with <message>), an empty/unknown
// reply is "Unknown error"; internal library codes are formatted as "{code} text".
func TestServerErrorTexts(t *testing.T) {
	serverTexts := map[byte]string{
		1: "Клиент не допущен к торгам.",
		2: "Трейдер не допущен к торгам.",
		3: "Клиент не обслуживается данным Трейдером.",
		4: "Сделки с собой запрещены.",
		5: "Попытка снять несуществующую заявку.",
		6: "Транзакции не разрешены.",
	}
	for code, text := range serverTexts {
		t.Run("transact-code-"+string(rune('0'+code)), func(t *testing.T) {
			srv := newRunServer(t)
			srv.Snapshot = clientSnapshot(t)
			srv.Handle(session.RqTransact, func([]byte) (byte, []byte) {
				return wire.KindReply | wire.FlagPlain, []byte{code}
			})
			c := New(Config{Dial: srv.Dial})
			rec := newRecorder()
			if err := c.Start(rec.handle); err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			send(t, c, connectCommand())
			rec.next(t, "<server_status")
			cmd := "<command id=" + quote + "neworder" + quote + "><secid>26</secid><client>TESTCLIEN</client><price>100</price><quantity>1</quantity><buysell>B</buysell><unfilled>PutInQueue</unfilled></command>"
			got := send(t, c, cmd)
			want := "<result success=" + quote + "false" + quote + "><message>" + text + "</message></result>"
			if got != want {
				t.Fatalf("code %d = %s, want %s", code, got, want)
			}
		})
	}
	// Empty and unknown replies render Unknown error (O; 7..66 are I).
	for _, tc := range []struct {
		name  string
		reply []byte
	}{
		{"empty", nil},
		{"unknown-code", []byte{34}},
		{"i-code-7", []byte{7}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newRunServer(t)
			srv.Snapshot = clientSnapshot(t)
			srv.Handle(session.RqTransact, func([]byte) (byte, []byte) {
				return wire.KindReply | wire.FlagPlain, tc.reply
			})
			c := New(Config{Dial: srv.Dial})
			rec := newRecorder()
			if err := c.Start(rec.handle); err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			send(t, c, connectCommand())
			rec.next(t, "<server_status")
			cmd := "<command id=" + quote + "neworder" + quote + "><secid>26</secid><client>TESTCLIEN</client><price>100</price><quantity>1</quantity><buysell>B</buysell><unfilled>PutInQueue</unfilled></command>"
			if got := send(t, c, cmd); !strings.Contains(got, "Unknown error") {
				t.Fatalf("%s = %s, want Unknown error", tc.name, got)
			}
		})
	}
	// General internal-error formatter: "{code} <RU>" and the 67-code table.
	if text, ok := session.FormatInternalError(8); !ok || text != "{8} Неверное значение параметра '%s'." {
		t.Fatalf("format 8 = %q, %v", text, ok)
	}
	if text, ok := session.FormatInternalError(37); !ok || text != "{37} Заявка отменена" {
		t.Fatalf("format 37 = %q, %v", text, ok)
	}
	// I: RQ_HISTORY has no error texts (status 0..3 only). RQ_PWD: only 0x0e+text
	// is confirmed (docs/protocol/errors.md) — not asserted here.
}
