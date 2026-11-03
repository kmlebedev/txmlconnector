// Package cryptoprofile implements the built-in TRANSAQ crypto profile
// (connect mode 0): GOST 28147-89 with vendor 8-bit S-boxes, counter-mode
// gamma for client→server traffic and CFB for server→client traffic.
//
// Recovered from txmlconnector64-6.47.2.26.4.dll; see docs/protocol/connect.md.
package cryptoprofile

import (
	"crypto/sha1"
	"encoding/binary"
	"errors"
)

const (
	KeySize  = 32
	SyncSize = 8

	gammaC1 = 0x01010104 // added modulo 2^32-1 to the high counter word
	gammaC2 = 0x01010101 // added modulo 2^32 to the low counter word
)

// keyOrder is the standard GOST 28147-89 encryption key schedule.
var keyOrder = [32]int{
	0, 1, 2, 3, 4, 5, 6, 7,
	0, 1, 2, 3, 4, 5, 6, 7,
	0, 1, 2, 3, 4, 5, 6, 7,
	7, 6, 5, 4, 3, 2, 1, 0,
}

// Cipher is a GOST 28147-89 block cipher keyed with a 256-bit key.
type Cipher struct {
	k [8]uint32
}

func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != KeySize {
		return nil, errors.New("cryptoprofile: key must be 32 bytes")
	}
	c := &Cipher{}
	for i := range c.k {
		c.k[i] = binary.LittleEndian.Uint32(key[i*4:])
	}
	return c, nil
}

func (c *Cipher) f(x uint32) uint32 {
	y := uint32(sboxTable[0x300+int(x>>24)])<<24 |
		uint32(sboxTable[0x200+int(x>>16&0xff)])<<16 |
		uint32(sboxTable[0x100+int(x>>8&0xff)])<<8 |
		uint32(sboxTable[x&0xff])
	return y<<11 | y>>21
}

// encrypt runs the 32-round GOST encryption cycle (FUN_180439d90).
func (c *Cipher) encrypt(n1, n2 uint32) (uint32, uint32) {
	for _, i := range keyOrder {
		n1, n2 = n2^c.f(n1+c.k[i]), n1
	}
	return n2, n1
}

func (c *Cipher) encryptBlock(dst *[8]byte, n1, n2 uint32) {
	a, b := c.encrypt(n1, n2)
	binary.LittleEndian.PutUint32(dst[0:], a)
	binary.LittleEndian.PutUint32(dst[4:], b)
}

// Gamma applies GOST counter mode (FUN_18043a520, modes ±1). It is its own
// inverse. dst and src may overlap exactly.
func (c *Cipher) Gamma(sync [SyncSize]byte, dst, src []byte) {
	n1, n2 := c.encrypt(binary.LittleEndian.Uint32(sync[0:]), binary.LittleEndian.Uint32(sync[4:]))
	var g [8]byte
	for off := 0; off < len(src); off += 8 {
		carry := uint32(0)
		if n2 > 0xfefefefb {
			carry = 1
		}
		n2 += gammaC1 + carry
		n1 += gammaC2
		c.encryptBlock(&g, n1, n2)
		xorBlock(dst[off:], src[off:], g[:])
	}
}

// CFBEncrypt applies GOST cipher feedback encryption (FUN_18043a5d0, mode 2).
func (c *Cipher) CFBEncrypt(sync [SyncSize]byte, dst, src []byte) {
	fb := sync
	var g [8]byte
	for off := 0; off < len(src); off += 8 {
		c.encryptBlock(&g, binary.LittleEndian.Uint32(fb[0:]), binary.LittleEndian.Uint32(fb[4:]))
		n := xorBlock(dst[off:], src[off:], g[:])
		fb = g
		copy(fb[:], dst[off:off+n])
	}
}

// CFBDecrypt reverses CFBEncrypt (FUN_18043a660, mode -2).
func (c *Cipher) CFBDecrypt(sync [SyncSize]byte, dst, src []byte) {
	fb := sync
	var g, in [8]byte
	for off := 0; off < len(src); off += 8 {
		c.encryptBlock(&g, binary.LittleEndian.Uint32(fb[0:]), binary.LittleEndian.Uint32(fb[4:]))
		in = g
		n := copy(in[:], src[off:])
		xorBlock(dst[off:], in[:n], g[:])
		fb = in
	}
}

func xorBlock(dst, src, g []byte) int {
	n := min(len(src), 8)
	for i := 0; i < n; i++ {
		dst[i] = src[i] ^ g[i]
	}
	return n
}

// PasswordKey derives the 32-byte key used to answer the login challenge and
// to unwrap the session key: SHA-1 of the password expanded by FUN_18043d370.
func PasswordKey(password string) []byte {
	sum := sha1.Sum([]byte(password))
	return expandKey(sum[:], KeySize)
}

func expandKey(src []byte, size int) []byte {
	out := make([]byte, size)
	n := copy(out, src)
	for i := n; i < size; i++ {
		b := out[i-n] - byte(i) + byte(size)
		out[i] = (b>>5 | b<<3) ^ 0x97
	}
	return out
}
