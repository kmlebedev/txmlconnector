package records_test

import (
	"testing"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
)

// A trimmed template in the production layout (S, RQ_INQUIRE_UT 2026-11-05;
// values of SBER/GAZP/SiZ6 as the DLL printed them).
const testTemplate = `<template id="18" name="TEST" W="100" Rshort="0" Rlong="0" short="100" long="100" shortable="0" longable="1" marketable="0" tradeable="1" min_ratio="0.50">
	<market-defaults>
		<market id="1" W="0" Rlong="0.0000" Rshort="0.0000" long="100.0000" short="100.0000" tradeable="1" longable="1" shortable="0" marketable="1"/>
	</market-defaults>
	<instrclass-defaults>
		<class letter="Y" mkt="14" tradeable="1" longable="1" shortable="0" marketable="1" long="100.0000" short="100.0000" Rlong="0.0000" Rshort="0.0000"/>
	</instrclass-defaults>
	<instruments>
		<instr seccode="SBER" W="100" mkt="1" tradeable="1" longable="1" shortable="1" marketable="1" long="15.00" short="15.00" BA="SBER"/>
		<instr seccode="SBER" W="100" mkt="7" tradeable="1" longable="1" shortable="0" marketable="1" long="15.00" short="15.00" BA="SBER"/>
		<instr seccode="SiZ6" W="100" mkt="4" tradeable="1" longable="1" shortable="1" marketable="1" long="12.00" short="12.00" BA="USD_FUT"/>
	</instruments>
</template>`

func TestMarginTemplateLookup(t *testing.T) {
	tpl, err := records.ParseMarginTemplate([]byte(testTemplate))
	if err != nil {
		t.Fatal(err)
	}
	sber := tpl.Rule("SBER", "E", 1)
	if !sber.Shortable || sber.Long != (model.Decimal{Units: 1500, Scale: 2}) || sber.RLong.Units != 0 {
		t.Fatalf("SBER = %+v", sber)
	}
	if spb := tpl.Rule("SBER", "E", 7); spb.Shortable {
		t.Fatalf("SBER on market 7 = %+v", spb)
	}
	if fut := tpl.Rule("SiZ6", "F", 4); fut.Long != (model.Decimal{Units: 1200, Scale: 2}) {
		t.Fatalf("SiZ6 = %+v", fut)
	}
	if cls := tpl.Rule("AAPL", "Y", 14); !cls.Tradeable || cls.Shortable {
		t.Fatalf("class Y = %+v", cls)
	}
	if mkt := tpl.Rule("GAZP", "E", 1); mkt.Shortable || mkt.Long != (model.Decimal{Units: 1000000, Scale: 4}) {
		t.Fatalf("market 1 default = %+v", mkt)
	}
	if def := tpl.Rule("X", "Z", 99); !def.Tradeable || def.Marketable {
		t.Fatalf("template default = %+v", def)
	}
}
