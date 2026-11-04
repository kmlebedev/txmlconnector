package xmlcompat_test

import (
	"strings"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/internal/oracle"
	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/xmlcompat"
)

// TestRenderAllTradesThroughRender checks the single Render entry point against
// the DLL <alltrades> callback: all R_ALLTRADE records of one RQ_MAIN reply
// become one batched message (testdata/alltrades-dll1.xml, milliseconds=true).
//
// VTBR 889 is not in the 499-instrument main1-demo snapshot, but the DLL knew
// it in the recorded session (the fixture prints seccode VTBR and price 0.098,
// i.e. decimals 3), so the test seeds it explicitly - the knowledge the old
// oracleCatalog helper held.
func TestRenderAllTradesThroughRender(t *testing.T) {
	oracle.CheckDLL(t, "main1-demo.hex.gz", "alltrades-inject1.hex", "alltrades-dll1.xml",
		records.SecNew{SecID: 889, SecCode: "VTBR", Pits: []records.Pit{{Board: 37, Decimals: 3, Default: true}}})
}

// TestRenderAllTradesUTCTime checks the utc_time XML option: <time> carries the
// UTC instant instead of the server wall clock (RE-1: an XML-only connect
// option).
func TestRenderAllTradesUTCTime(t *testing.T) {
	catalog := records.NewCatalog(&model.Startup{})
	trade := model.Trade{
		SecID: 26, Board: "TQBR", SecCode: "SBER", TradeNo: 1,
		Time:     time.Date(2026, 11, 3, 20, 53, 20, 250*int(time.Millisecond), time.UTC),
		WallTime: time.Date(2026, 11, 3, 23, 53, 20, 0, time.UTC).Unix(),
		Millis:   250, Price: model.Decimal{Units: 30512, Scale: 2}, Quantity: 3,
		BuySell: 'B', Period: 'B',
	}
	got, ok := xmlcompat.Render(catalog, &trade, xmlcompat.Options{Milliseconds: true, UTCTime: true})
	if !ok {
		t.Fatal("Render does not accept a trade")
	}
	if !strings.Contains(got, "<time>03.11.2026 20:53:20.250</time>") {
		t.Fatalf("utc_time rendering = %s", got)
	}
	got, ok = xmlcompat.Render(catalog, &trade, xmlcompat.Options{Milliseconds: true})
	if !ok || !strings.Contains(got, "<time>03.11.2026 23:53:20.250</time>") {
		t.Fatalf("default rendering = %s", got)
	}
}

// TestRenderClientAndPositions checks the end-of-load renders against the DLL
// callback shapes of native/testdata/snapshot-tail-live.xml.
func TestRenderClientAndPositions(t *testing.T) {
	account := model.ClientAccount{
		ID: "TESTCLIEN", Type: "leverage", Currency: "RUB", Market: 1,
	}
	if got, want := xmlcompat.ClientXML(account), `<client id="TESTCLIEN" remove="false"><market>1</market><currency>RUB</currency><type>leverage</type></client>`; got != want {
		t.Fatalf("ClientXML = %s, want %s", got, want)
	}
	if got, want := xmlcompat.ClientXML(model.ClientAccount{ID: "TESTCLIEN", Removed: true}),
		`<client id="TESTCLIEN" remove="true"/>`; got != want {
		t.Fatalf("removed ClientXML = %s, want %s", got, want)
	}
	position := model.MoneyPosition{
		Client: "TESTCLIEN", Currency: "RUR", Market: 1,
		Asset: "FOND_MICEX", ShortName: "Деньги КЦБ ММВБ (RUR)",
		SaldoIn:    model.Decimal{Units: 30000000, Scale: 2},
		Saldo:      model.Decimal{Units: 30000000, Scale: 2},
		Bought:     model.Decimal{Scale: 2},
		Sold:       model.Decimal{Scale: 2},
		OrdBuy:     model.Decimal{Scale: 2},
		OrdBuyCond: model.Decimal{Scale: 2},
		Comission:  model.Decimal{Scale: 2},
	}
	want := `<positions><money_position><currency>RUR</currency><client>TESTCLIEN</client><union></union><markets><market>1</market></markets><asset>FOND_MICEX</asset><shortname>Деньги КЦБ ММВБ (RUR)</shortname><saldoin>300000.0</saldoin><bought>0.0</bought><sold>0.0</sold><saldo>300000.0</saldo><ordbuy>0.0</ordbuy><ordbuycond>0.0</ordbuycond><comission>0.0</comission></money_position></positions>`
	if got := xmlcompat.PositionsXML([]model.MoneyPosition{position}, false); got != want {
		t.Fatalf("PositionsXML\n got %s\nwant %s", got, want)
	}
	// micex_registers adds the register name (RE-1 option).
	position.Register = "T0"
	if got := xmlcompat.PositionsXML([]model.MoneyPosition{position}, true); !strings.Contains(got, "<register>T0</register>") {
		t.Fatalf("micex_registers PositionsXML = %s", got)
	}
	if got, want := xmlcompat.OvernightXML(false), `<overnight status="false"/>`; got != want {
		t.Fatalf("OvernightXML = %s, want %s", got, want)
	}
	// The non-empty union is decompiler-backed only (docs/protocol/connect.md).
	if got, want := xmlcompat.UnionXML(model.Union{ID: "TESTUNION"}), `<union id="TESTUNION" remove="false"/>`; got != want {
		t.Fatalf("UnionXML = %s, want %s", got, want)
	}
}
