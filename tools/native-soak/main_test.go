package main

import "testing"

func TestParseSymbols(t *testing.T) {
	got, err := parseSymbols("TQBR:SBER, TQBR:GAZP")
	if err != nil || len(got) != 2 || got[0].SecCode != "SBER" || got[1].SecCode != "GAZP" {
		t.Fatalf("parseSymbols = %+v, %v", got, err)
	}
	if _, err := parseSymbols("SBER"); err == nil {
		t.Fatal("bad symbol accepted")
	}
	def, err := parseSymbols("")
	if err != nil || len(def) != 20 {
		t.Fatalf("default symbols = %d, %v; want 20", len(def), err)
	}
}

func TestRSSBytes(t *testing.T) {
	if got := rssBytes(); got == 0 {
		t.Fatal("rssBytes = 0")
	}
}
