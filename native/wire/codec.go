// Package wire implements TRANSAQ framing and the field encodings used inside
// request and response payloads.
package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

var ErrShortBuffer = errors.New("wire: short buffer")

// Writer builds a payload. Field formats follow the DLL writers named in the
// comments; its debug trace prints them as C/W/Z/L/G/B.
type Writer struct {
	buf []byte
}

func (w *Writer) Bytes() []byte { return w.buf }

// Byte writes one byte (FUN_1801be680 "C", FUN_1801be4f0).
func (w *Writer) Byte(v byte) { w.buf = append(w.buf, v) }

// Uint16 writes a big-endian word (FUN_1801be710 "W").
func (w *Writer) Uint16(v uint16) { w.buf = binary.BigEndian.AppendUint16(w.buf, v) }

// String writes at most max bytes of s followed by NUL (FUN_1801be3e0 "Zn").
func (w *Writer) String(s string, max int) {
	if i := indexNUL(s); i >= 0 {
		s = s[:i]
	}
	if len(s) > max {
		s = s[:max]
	}
	w.buf = append(append(w.buf, s...), 0)
}

// Raw writes b without a length prefix (FUN_1801be5d0 "Bn").
func (w *Writer) Raw(b []byte) { w.buf = append(w.buf, b...) }

// Int32 writes a packed signed integer (FUN_1801be7d0 "L").
func (w *Writer) Int32(v int32) { w.buf = appendPacked(w.buf, int64(v)) }

// Int64 writes a packed signed integer. Up to six extra bytes it matches
// FUN_1801be970; longer values use the count 7 = eight extra bytes form that
// FUN_1801bd9b0 reads, which covers the full int64 range.
func (w *Writer) Int64(v int64) {
	w.buf = appendPacked(w.buf, v)
}

// appendPacked encodes v as: first byte = low nibble of |v| << 4 | sign << 3 |
// count of extra bytes; extra bytes hold |v| >> 4 little-endian. Count 7
// stands for eight extra bytes. The minimum 32-bit value is written as
// negative zero.
func appendPacked(buf []byte, v int64) []byte {
	var head byte
	m := uint64(v)
	if v < 0 {
		head = 8
		m = uint64(-v)
		if v == math.MinInt32 || v == math.MinInt64 {
			m = 0
		}
	}
	head |= byte(m&0xf) << 4
	m >>= 4
	var ext [8]byte
	n := 0
	for ; m != 0; m >>= 8 {
		ext[n] = byte(m)
		n++
	}
	count := n
	if n == 7 {
		n, count = 8, 7
	}
	if n == 8 {
		count = 7
	}
	buf = append(buf, head|byte(count))
	return append(buf, ext[:n]...)
}

func indexNUL(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			return i
		}
	}
	return -1
}

// Reader consumes a payload.
type Reader struct {
	buf []byte
	off int
}

func NewReader(b []byte) *Reader { return &Reader{buf: b} }

func (r *Reader) Len() int { return len(r.buf) - r.off }

// Remaining returns the unread bytes without consuming them (splitter helpers).
func (r *Reader) Remaining() []byte { return r.buf[r.off:] }

// Skip consumes n unread bytes (splitter helpers).
func (r *Reader) Skip(n int) error {
	if n < 0 || n > r.Len() {
		return fmt.Errorf("wire: skip %d with %d bytes left", n, r.Len())
	}
	r.off += n
	return nil
}

// Byte reads one byte (FUN_1801bd290, FUN_1801bd510).
func (r *Reader) Byte() (byte, error) {
	if r.Len() < 1 {
		return 0, ErrShortBuffer
	}
	r.off++
	return r.buf[r.off-1], nil
}

// Uint16 reads a big-endian word (FUN_1801bd5d0).
func (r *Reader) Uint16() (uint16, error) {
	if r.Len() < 2 {
		return 0, ErrShortBuffer
	}
	r.off += 2
	return binary.BigEndian.Uint16(r.buf[r.off-2:]), nil
}

// Raw reads exactly n bytes (FUN_1801bd420).
func (r *Reader) Raw(n int) ([]byte, error) {
	if n < 0 || r.Len() < n {
		return nil, ErrShortBuffer
	}
	r.off += n
	return r.buf[r.off-n : r.off], nil
}

// String reads a NUL-terminated string of at most max bytes before the NUL
// (FUN_1801bd170). On failure the read position is unchanged, matching the DLL.
func (r *Reader) String(max int) (string, error) {
	for i := r.off; i < len(r.buf) && i-r.off <= max; i++ {
		if r.buf[i] == 0 {
			s := string(r.buf[r.off:i])
			r.off = i + 1
			return s, nil
		}
	}
	return "", ErrShortBuffer
}

// Int32 reads a packed signed integer (FUN_1801bd7b0). Arithmetic wraps at
// 32 bits like the DLL.
func (r *Reader) Int32() (int32, error) {
	v, err := r.packed(false)
	if err == nil && v == math.MinInt64 {
		v = math.MinInt32
	}
	return int32(v), err
}

// Int64 reads a packed 64-bit integer (FUN_1801bd9b0): count 7 means eight
// extra bytes.
func (r *Reader) Int64() (int64, error) {
	return r.packed(true)
}

func (r *Reader) packed(wide bool) (int64, error) {
	if r.Len() < 1 {
		return 0, ErrShortBuffer
	}
	head := r.buf[r.off]
	n := int(head & 7)
	if wide && n == 7 {
		n = 8
	}
	if r.Len() < n+1 {
		return 0, ErrShortBuffer
	}
	var m uint64
	for i := n; i >= 1; i-- {
		m = m<<8 | uint64(r.buf[r.off+i])
	}
	m = m<<4 | uint64(head>>4)
	r.off += n + 1
	if head&8 != 0 {
		if m == 0 {
			return math.MinInt64, nil
		}
		return -int64(m), nil
	}
	return int64(m), nil
}
