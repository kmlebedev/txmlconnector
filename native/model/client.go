package model

// ClientAccount is a <client> callback of the end of the load: one R_CLN_NEW
// record (tag 0x02). On the demo account the client code is TESTCLIEN.
type ClientAccount struct {
	ID       string // client code, the DLL's <client id>
	Type     string // spot, risk, leverage, margin_level or mct
	Currency string // RUB for the demo (currency id 1)
	Union    string // Z7 union of the record; empty on the demo
	FortsAcc string // FORTS account, not recovered yet (task 20)
	Market   int    // the client market from the record
	Removed  bool   // the record updates an existing client: remove="true"
}

func (*ClientAccount) isEvent() {}

// MoneyPosition is one <money_position> of the end-of-load <positions> batch.
// Task 7 implements only the money positions (symbol id 0xffff); paper and
// FORTS positions belong to task 20.
type MoneyPosition struct {
	Client     string
	Union      string
	Currency   string
	Market     int
	Asset      string
	ShortName  string
	Register   string // rendered only with <micex_registers> (task 7: options)
	SaldoIn    Decimal
	Bought     Decimal
	Sold       Decimal
	Saldo      Decimal
	OrdBuy     Decimal
	OrdBuyCond Decimal
	Comission  Decimal
}

func (*MoneyPosition) isEvent() {}

// Overnight is the session overnight flag from R_OVERNIGHT (tag 0x18): the DLL
// renders it as <overnight status="true|false"/> before every server_status.
type Overnight struct {
	Status bool
}

// Union is a <union> callback from R_UNION (tag 0x68). The live case is not
// captured (the demo union is empty), so the wire format is decompiler-backed
// only; the XML shape is inferred from the DLL string literal.
type Union struct {
	ID      string // Z20 name of the union
	Removed bool
}

func (*Union) isEvent() {}
