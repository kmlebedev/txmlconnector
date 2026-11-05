// Command pe-repro makes a Go c-shared Windows DLL linked by zig/lld
// reproducible: it zeroes the PE time stamps (file header, export and debug
// directories) and the CodeView (RSDS) GUID, which differ between otherwise
// identical builds. Run `go tool buildid -w` afterwards so the Go build ID is
// recomputed from the normalized content (C9, docs/COMPATIBILITY.md).
//
// usage: go run ./tools/cabi/pe-repro FILE.dll
//
//	go run ./tools/cabi/pe-repro -exports FILE.dll   (prints "ordinal name")
package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "-exports" {
		data, err := os.ReadFile(os.Args[2])
		var exports []Export
		if err == nil {
			exports, err = Exports(data)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "pe-repro:", err)
			os.Exit(1)
		}
		for _, e := range exports {
			fmt.Println(e.Ordinal, e.Name)
		}
		return
	}
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: pe-repro [-exports] FILE.dll")
		os.Exit(2)
	}
	data, err := os.ReadFile(os.Args[1])
	if err == nil {
		err = Normalize(data)
	}
	if err == nil {
		err = os.WriteFile(os.Args[1], data, 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "pe-repro:", err)
		os.Exit(1)
	}
}

const (
	dirExport = 0
	dirDebug  = 6
	debugSize = 28 // IMAGE_DEBUG_DIRECTORY
	codeView  = 2  // IMAGE_DEBUG_TYPE_CODEVIEW
)

var le = binary.LittleEndian

type section struct{ vsize, va, rawSize, rawPtr uint32 }

// image is the parsed header layout of a PE file.
type image struct {
	pe, dirs int
	secs     []section
}

func parse(d []byte) (*image, error) {
	if len(d) < 0x40 || string(d[:2]) != "MZ" {
		return nil, errors.New("not a PE image")
	}
	pe := int(le.Uint32(d[0x3c:]))
	if pe+24 > len(d) || string(d[pe:pe+4]) != "PE\x00\x00" {
		return nil, errors.New("no PE signature")
	}
	sections := int(le.Uint16(d[pe+6:]))
	optSize := int(le.Uint16(d[pe+20:]))
	opt := pe + 24
	img := &image{pe: pe}
	switch le.Uint16(d[opt:]) {
	case 0x20b: // PE32+
		img.dirs = opt + 112
	case 0x10b: // PE32
		img.dirs = opt + 96
	default:
		return nil, errors.New("unknown optional header")
	}
	for i := 0; i < sections; i++ {
		s := opt + optSize + 40*i
		if s+40 > len(d) {
			return nil, errors.New("truncated section table")
		}
		img.secs = append(img.secs, section{le.Uint32(d[s+8:]), le.Uint32(d[s+12:]), le.Uint32(d[s+16:]), le.Uint32(d[s+20:])})
	}
	return img, nil
}

func (img *image) offset(rva uint32) (int, bool) {
	for _, s := range img.secs {
		size := max(s.vsize, s.rawSize)
		if rva >= s.va && rva < s.va+size {
			return int(s.rawPtr + rva - s.va), true
		}
	}
	return 0, false
}

// Export is one named export with its ordinal (base + index).
type Export struct {
	Ordinal int
	Name    string
}

// Exports lists the named exports in the order of the export name table.
func Exports(d []byte) ([]Export, error) {
	img, err := parse(d)
	if err != nil {
		return nil, err
	}
	rva := le.Uint32(d[img.dirs+8*dirExport:])
	if rva == 0 {
		return nil, nil
	}
	o, ok := img.offset(rva)
	if !ok || o+40 > len(d) {
		return nil, errors.New("export directory outside sections")
	}
	base := int(le.Uint32(d[o+16:]))
	names := int(le.Uint32(d[o+24:]))
	namePtr, ok1 := img.offset(le.Uint32(d[o+32:]))
	ordPtr, ok2 := img.offset(le.Uint32(d[o+36:]))
	if !ok1 || !ok2 {
		return nil, errors.New("export tables outside sections")
	}
	var out []Export
	for i := 0; i < names; i++ {
		if namePtr+4*i+4 > len(d) || ordPtr+2*i+2 > len(d) {
			return nil, errors.New("truncated export tables")
		}
		no, ok := img.offset(le.Uint32(d[namePtr+4*i:]))
		if !ok {
			return nil, errors.New("export name outside sections")
		}
		end := no
		for end < len(d) && d[end] != 0 {
			end++
		}
		out = append(out, Export{Ordinal: base + int(le.Uint16(d[ordPtr+2*i:])), Name: string(d[no:end])})
	}
	return out, nil
}

// Normalize rewrites the non-deterministic fields of a PE image in place.
func Normalize(d []byte) error {
	img, err := parse(d)
	if err != nil {
		return err
	}
	le.PutUint32(d[img.pe+8:], 0) // FileHeader.TimeDateStamp
	dirs, offset := img.dirs, img.offset
	if rva := le.Uint32(d[dirs+8*dirExport:]); rva != 0 {
		if o, ok := offset(rva); ok && o+8 <= len(d) {
			le.PutUint32(d[o+4:], 0) // export TimeDateStamp
		}
	}
	rva, size := le.Uint32(d[dirs+8*dirDebug:]), le.Uint32(d[dirs+8*dirDebug+4:])
	if rva == 0 {
		return nil
	}
	o, ok := offset(rva)
	if !ok {
		return errors.New("debug directory outside sections")
	}
	for e := o; e+debugSize <= o+int(size) && e+debugSize <= len(d); e += debugSize {
		le.PutUint32(d[e+4:], 0) // debug TimeDateStamp
		ptr := int(le.Uint32(d[e+24:]))
		if le.Uint32(d[e+12:]) == codeView && ptr+20 <= len(d) && string(d[ptr:ptr+4]) == "RSDS" {
			clear(d[ptr+4 : ptr+20]) // CodeView GUID
		}
	}
	return nil
}
