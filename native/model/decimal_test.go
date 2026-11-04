package model

import "testing"

func TestDecimalString(t *testing.T) {
	for _, c := range []struct {
		d    Decimal
		want string
	}{
		{Decimal{30512, 2}, "305.12"},
		{Decimal{30500, 2}, "305.0"},
		{Decimal{98, 3}, "0.098"},
		{Decimal{-30512, 2}, "-305.12"},
		{Decimal{7, 0}, "7"},
		{Decimal{5, 4}, "0.0005"},
	} {
		if got := c.d.String(); got != c.want {
			t.Errorf("Decimal{%d,%d}.String() = %q, want %q", c.d.Units, c.d.Scale, got, c.want)
		}
	}
}

func TestDecimalFloat64(t *testing.T) {
	if got := (Decimal{30512, 2}).Float64(); got != 305.12 {
		t.Errorf("Float64() = %v, want 305.12", got)
	}
}
