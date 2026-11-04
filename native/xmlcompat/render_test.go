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
	// The single Render entry point passes Options.MicexRegisters through for a
	// money position event (Connector uses the same option for load-end batches).
	catalog := records.NewCatalog(&model.Startup{})
	if got, ok := xmlcompat.Render(catalog, &position, xmlcompat.Options{MicexRegisters: true}); !ok || !strings.Contains(got, "<register>T0</register>") {
		t.Fatalf("Render(MicexRegisters) = %q, %v", got, ok)
	}
	if got, want := xmlcompat.OvernightXML(false), `<overnight status="false"/>`; got != want {
		t.Fatalf("OvernightXML = %s, want %s", got, want)
	}
	// The non-empty union is decompiler-backed only (docs/protocol/connect.md).
	if got, want := xmlcompat.UnionXML(model.Union{ID: "TESTUNION"}), `<union id="TESTUNION" remove="false"/>`; got != want {
		t.Fatalf("UnionXML = %s, want %s", got, want)
	}
}

// TestQuotationFixedMoney checks the fixed formats of the <quotation> money
// fields: valtoday keeps three decimals, the deposits two (the DLL emits
// 1.000/7601.40/7516.00, while plain Decimal.String trims the zeros).
func TestQuotationFixedMoney(t *testing.T) {
	q := model.Quotation{
		SecID: 26, Board: "TQBR",
		Symbol: model.Symbol{Board: "TQBR", SecCode: "SBER"},
		Fields: model.QuotationFields{
			Status:      ptrString("A"),
			ValToday:    &model.Decimal{Units: 1000, Scale: 3},
			BuyDeposit:  &model.Decimal{Units: 760140, Scale: 2},
			SellDeposit: &model.Decimal{Units: 751600, Scale: 2},
			Last:        &model.Decimal{Units: 4968, Scale: 4},
		},
	}
	got := xmlcompat.QuotationXML(q)
	for _, want := range []string{
		"<valtoday>1.000</valtoday>",
		"<buydeposit>7601.40</buydeposit>",
		"<selldeposit>7516.00</selldeposit>",
		"<last>0.4968</last>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("QuotationXML = %s, want substring %s", got, want)
		}
	}
}

func ptrString(s string) *string { return &s }

// TestQuotesDeletionRendersPrice pins the DLL order: <price> is built before the
// deletion branch, so a deleted level still has its price (asm 0x1800faa27).
func TestQuotesDeletionRendersPrice(t *testing.T) {
	q := model.Quotes{SecID: 26, Symbol: model.Symbol{Board: "TQBR", SecCode: "SBER"}, Levels: []model.Quote{
		{SecID: 26, Price: model.Decimal{Units: 0}, Buy: -1},
		{SecID: 26, Price: model.Decimal{Units: 50, Scale: 2}, Sell: -1},
	}}
	got := xmlcompat.QuotesXML(q)
	for _, want := range []string{
		"<price>0</price><yield>0</yield><buy>-1</buy>",
		"<price>0.5</price><yield>0</yield><sell>-1</sell>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("QuotesXML = %s, want substring %s", got, want)
		}
	}
}

// TestSecInfoXMLSkipsEmptyFields: the DLL drops empty/zero fields (market 0,
// zero decimals), so a SecInfo with only identity must render those fields as
// absent instead of zero values.
func TestSecInfoXMLSkipsEmptyFields(t *testing.T) {
	zero := model.Decimal{Units: 0, Scale: 2}
	info := model.SecInfo{SecID: 26, SecCode: "SBER", Market: 0, PointCost: &zero, BuyDeposit: &zero, ISIN: "RU0009029540"}
	got := xmlcompat.SecInfoXML(info)
	for _, absent := range []string{"<market>", "<point_cost>", "<buy_deposit>"} {
		if strings.Contains(got, absent) {
			t.Fatalf("SecInfoXML = %s, must not contain %s", got, absent)
		}
	}
	if !strings.Contains(got, "<seccode>SBER</seccode>") || !strings.Contains(got, "<isin>RU0009029540</isin>") {
		t.Fatalf("SecInfoXML = %s", got)
	}
}
