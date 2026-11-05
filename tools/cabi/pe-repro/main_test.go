package main

import (
	"bytes"
	"testing"
)

// minimalPE builds a PE32+ image with one section holding an export directory
// and a CodeView debug entry, stamped with the given time and GUID byte.
func minimalPE(stamp uint32, guid byte) []byte {
	d := make([]byte, 0x400)
	copy(d, "MZ")
	const pe = 0x80
	le.PutUint32(d[0x3c:], pe)
	copy(d[pe:], "PE\x00\x00")
	le.PutUint16(d[pe+6:], 1)     // sections
	le.PutUint32(d[pe+8:], stamp) // TimeDateStamp
	le.PutUint16(d[pe+20:], 240)  // optional header size
	opt := pe + 24
	le.PutUint16(d[opt:], 0x20b)
	dirs := opt + 112
	le.PutUint32(d[dirs+8*dirExport:], 0x1000) // export at RVA 0x1000
	le.PutUint32(d[dirs+8*dirDebug:], 0x1040)  // debug at RVA 0x1040
	le.PutUint32(d[dirs+8*dirDebug+4:], debugSize)
	sec := opt + 240
	le.PutUint32(d[sec+8:], 0x100)   // vsize
	le.PutUint32(d[sec+12:], 0x1000) // va
	le.PutUint32(d[sec+16:], 0x100)  // raw size
	le.PutUint32(d[sec+20:], 0x200)  // raw ptr
	le.PutUint32(d[0x200+4:], stamp) // export TimeDateStamp
	dbg := 0x240
	le.PutUint32(d[dbg+4:], stamp)
	le.PutUint32(d[dbg+12:], codeView)
	le.PutUint32(d[dbg+24:], 0x280) // file pointer of RSDS
	copy(d[0x280:], "RSDS")
	for i := 0; i < 16; i++ {
		d[0x284+i] = guid
	}
	return d
}

func TestNormalizeMakesBuildsIdentical(t *testing.T) {
	a, b := minimalPE(0x12345678, 0xaa), minimalPE(0x0badf00d, 0x55)
	if bytes.Equal(a, b) {
		t.Fatal("fixtures must differ")
	}
	for _, img := range [][]byte{a, b} {
		if err := Normalize(img); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(a, b) {
		t.Fatal("normalized images differ")
	}
	if le.Uint32(a[0x80+8:]) != 0 || le.Uint32(a[0x204:]) != 0 || le.Uint32(a[0x244:]) != 0 || a[0x284] != 0 {
		t.Fatal("stamps or GUID not cleared")
	}
	if string(a[0x280:0x284]) != "RSDS" {
		t.Fatal("RSDS signature must stay")
	}
}

func TestNormalizeRejectsNonPE(t *testing.T) {
	if err := Normalize([]byte("not a dll at all, just some bytes padding padding padding padding")); err == nil {
		t.Fatal("want error")
	}
}
