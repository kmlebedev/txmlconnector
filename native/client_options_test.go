package native

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

func optionFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("records/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	out, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

// TestConnectorOptionFamilies: get_option_families sends RQ_OPTION_FAMILIES 149
// with the O payload and maps the empty/text reply (O).
func TestConnectorOptionFamilies(t *testing.T) {
	for _, tc := range []struct {
		name    string
		reply   []byte
		success bool
		message string
	}{
		{"ok", optionFixture(t, "option-families-reply-ok.hex"), true, ""},
		{"error", optionFixture(t, "option-families-reply-error.hex"), false, "No families"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newRunServer(t)
			srv.Snapshot = clientSnapshot(t)
			var captured []byte
			srv.Handle(session.RqOptionFamilies, func(payload []byte) (byte, []byte) {
				captured = append([]byte(nil), payload...)
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
			cmd := "<command id=" + quote + "get_option_families" + quote + "><security><board>TQBR</board><seccode>SBER</seccode></security></command>"
			got := send(t, c, cmd)
			if tc.success {
				if got != "<result success="+quote+"true"+quote+"/>" {
					t.Fatalf("get_option_families = %s", got)
				}
			} else if !strings.Contains(got, tc.message) {
				t.Fatalf("get_option_families = %s, want %q", got, tc.message)
			}
			if hex.EncodeToString(captured) != "001a" {
				t.Fatalf("149 payload = %x, want 001a", captured)
			}
		})
	}
}

// TestConnectorGetOptions: get_options sends the Z25 code list (151) and maps
// the reply; an empty list is the DLL local error.
func TestConnectorGetOptions(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	var captured []byte
	srv.Handle(session.RqCertainOptions, func(payload []byte) (byte, []byte) {
		captured = append([]byte(nil), payload...)
		return wire.KindReply | wire.FlagPlain, optionFixture(t, "option-families-reply-ok.hex")
	})
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	cmd := "<command id=" + quote + "get_options" + quote + "><opt_code>OPT1</opt_code><opt_code>OPT2</opt_code></command>"
	if got := send(t, c, cmd); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("get_options = %s", got)
	}
	var w wire.Writer
	w.String("OPT1", 25)
	w.String("OPT2", 25)
	if hex.EncodeToString(captured) != hex.EncodeToString(w.Bytes()) {
		t.Fatalf("151 payload = %x, want %x", captured, w.Bytes())
	}
	if got := send(t, c, "<command id="+quote+"get_options"+quote+"/>"); !strings.Contains(got, "No seccodes found") {
		t.Fatalf("empty get_options = %s", got)
	}
}

// TestConnectorFamilyStrikesErrors: mat_date local errors (D+O) and the honest
// I-error for the not-recovered family mapping.
func TestConnectorFamilyStrikesErrors(t *testing.T) {
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
	cases := []struct {
		name string
		cmd  string
		want string
	}{
		{"no mat_date", "<command id=" + quote + "get_family_strikes" + quote + "/>", "No mat_date found"},
		{"bad mat_date", "<command id=" + quote + "get_family_strikes" + quote + "><mat_date>2026-02-01</mat_date></command>", "Failed to parse mat_date=2026-02-01"},
		{"valid mat_date", "<command id=" + quote + "get_family_strikes" + quote + "><mat_date>01.02.2026</mat_date></command>", "family id mapping is not recovered (I)"},
	}
	for _, tc := range cases {
		if got := send(t, c, tc.cmd); !strings.Contains(got, tc.want) {
			t.Fatalf("%s: %s, want %q", tc.name, got, tc.want)
		}
	}
}

// TestOptionFamilyEvent: 0x6f R_FAMILY from RQ_MAIN renders the D literal
// <option_families> (form is I until the S series).
func TestOptionFamilyEvent(t *testing.T) {
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
	var w wire.Writer
	w.Uint16(26)
	w.String("SBER", 6)
	w.Int32(0)
	w.Int32(1)
	srv.QueueMain(append([]byte{records.TagFamily}, w.Bytes()...))
	got := rec.next(t, "<option_families")
	if !strings.Contains(got, "<lot_size>1</lot_size>") || !strings.Contains(got, "<mat_date></mat_date>") {
		t.Fatalf("option_families = %s", got)
	}
	_ = model.OptionFamily{}
}
