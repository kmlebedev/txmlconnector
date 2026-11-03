package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/kmlebedev/txmlconnector/internal/cryptoprofile"
)

const (
	HeaderSize = 8
	// MaxPayload is the largest payload length accepted by FUN_1801bfef0.
	MaxPayload = 0xfff0
	// ClientBufferSize is the receive size the DLL advertises in RQ_LOGIN
	// (packet size 65520 minus the header).
	ClientBufferSize = 65512
)

// Header flag bits (byte 0).
const (
	KindMask    = 0x03
	KindRequest = 0x01
	KindReply   = 0x02
	KindBye     = 0x03
	// FlagPlain marks a payload sent without encryption; the receiver skips
	// payload decryption but still checks its CRC.
	FlagPlain = 0x80
)

// Direction selects the cipher mode: the client encrypts with GOST gamma and
// the server with GOST CFB.
type Direction int

const (
	ClientToServer Direction = iota
	ServerToClient
)

var (
	ErrHeaderCRC  = errors.New("wire: header checksum mismatch")
	ErrPayloadCRC = errors.New("wire: payload checksum mismatch")
	ErrTooLarge   = errors.New("wire: payload too large")
)

// Frame is one decrypted packet.
//
// Plain header layout (FUN_1801bfb20): flags, type, payload length (BE16),
// CRC-16 of the plain payload (BE16), CRC-16 of the first six header bytes
// (BE16). Header and payload are encrypted separately, each restarting from
// the same key and synchronization value.
type Frame struct {
	Flags   byte
	Type    byte
	Payload []byte
}

func (f Frame) Kind() byte { return f.Flags & KindMask }

// Keys is the key material for both directions. The built-in profile uses the
// same key and sync for header and payload in both directions.
type Keys struct {
	cipher *cryptoprofile.Cipher
	sync   [cryptoprofile.SyncSize]byte
}

func NewKeys(key []byte, sync [cryptoprofile.SyncSize]byte) (*Keys, error) {
	c, err := cryptoprofile.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return &Keys{cipher: c, sync: sync}, nil
}

// DefaultKeys returns the keys in effect before the session key is issued.
func DefaultKeys() *Keys {
	k, err := NewKeys(cryptoprofile.DefaultKey, cryptoprofile.DefaultSync)
	if err != nil {
		panic(err)
	}
	return k
}

func (k *Keys) encrypt(dir Direction, b []byte) {
	if dir == ClientToServer {
		k.cipher.Gamma(k.sync, b, b)
	} else {
		k.cipher.CFBEncrypt(k.sync, b, b)
	}
}

func (k *Keys) decrypt(dir Direction, b []byte) {
	if dir == ClientToServer {
		k.cipher.Gamma(k.sync, b, b)
	} else {
		k.cipher.CFBDecrypt(k.sync, b, b)
	}
}

// Encode serializes and encrypts f for sending in direction dir.
func (k *Keys) Encode(dir Direction, f Frame) ([]byte, error) {
	if len(f.Payload) > MaxPayload {
		return nil, ErrTooLarge
	}
	out := make([]byte, HeaderSize+len(f.Payload))
	out[0] = f.Flags & 0x7f
	out[1] = f.Type
	binary.BigEndian.PutUint16(out[2:], uint16(len(f.Payload)))
	binary.BigEndian.PutUint16(out[4:], CRC16(f.Payload))
	binary.BigEndian.PutUint16(out[6:], CRC16(out[:6]))
	copy(out[HeaderSize:], f.Payload)
	k.encrypt(dir, out[:HeaderSize])
	if len(f.Payload) > 0 {
		k.encrypt(dir, out[HeaderSize:])
	}
	return out, nil
}

// Decode reads and decrypts one frame sent in direction dir.
func (k *Keys) Decode(dir Direction, r io.Reader) (Frame, error) {
	var hdr [HeaderSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Frame{}, err
	}
	k.decrypt(dir, hdr[:])
	if binary.BigEndian.Uint16(hdr[6:]) != CRC16(hdr[:6]) {
		return Frame{}, ErrHeaderCRC
	}
	n := int(binary.BigEndian.Uint16(hdr[2:]))
	if n > MaxPayload {
		return Frame{}, fmt.Errorf("%w: %d", ErrTooLarge, n)
	}
	f := Frame{Flags: hdr[0], Type: hdr[1], Payload: make([]byte, n)}
	if _, err := io.ReadFull(r, f.Payload); err != nil {
		return Frame{}, err
	}
	if n > 0 && f.Flags&FlagPlain == 0 {
		k.decrypt(dir, f.Payload)
	}
	if binary.BigEndian.Uint16(hdr[4:]) != CRC16(f.Payload) {
		return Frame{}, ErrPayloadCRC
	}
	return f, nil
}

// CRC16 is CRC-16/CCITT-FALSE (poly 0x1021, init 0xffff), the checksum
// computed by FUN_18043d7c0 with a byte-swapped table.
func CRC16(b []byte) uint16 {
	crc := uint16(0xffff)
	for _, v := range b {
		crc ^= uint16(v) << 8
		for range 8 {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}
