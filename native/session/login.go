// Package session implements the TRANSAQ connect sequence of the built-in
// crypto profile (connect mode 0), recovered from
// txmlconnector64-6.47.2.26.4.dll (FUN_1803895c0). See docs/protocol/connect.md.
package session

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/kmlebedev/txmlconnector/native/cryptoprofile"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// Request types: the constant passed with each name to FUN_180355d50.
const (
	RqLogin          byte = 1   // RQ_LOGIN
	RqLogout         byte = 2   // RQ_LOGOUT, sent without waiting for a reply
	RqMain           byte = 5   // RQ_MAIN, polls queued server events
	RqStartup        byte = 9   // RQ_STARTUP
	RqSubscribe      byte = 10  // RQ_SUBSCRIBE
	RqClientHoldings byte = 42  // RQ_CLIENT_HOLDINGS
	RqPwd            byte = 46  // RQ_PWD
	RqTime           byte = 50  // RQ_TIME
	RqRelogin        byte = 63  // RQ_RELOGIN (short recovery path)
	RqRecover        byte = 64  // RQ_RECOVER (short recovery path)
	RqHandshake      byte = 106 // RQ_HANDSHAKE, external crypto providers only
)

// Constant RQ_LOGIN fields written by FUN_1803895c0.
const (
	loginMagic      = 'x'
	ProtocolVersion = 78    // logged as "PRO# x/78"
	loginWord2      = 1     // meaning unknown
	loginWord3      = 0x21c // used while DAT_1809dd9c0 is 0
)

const (
	maxLogin        = 12
	maxComputerName = 16
	maxUserName     = 32
	maxOSName       = 80
	maxAddresses    = 4
	// DefaultRequestTimeout matches the DLL default of 20 s.
	DefaultRequestTimeout = 20 * time.Second
)

// Login response flag bits (byte 0 of the RQ_LOGIN reply).
const (
	loginHasVersion = 0x01
	loginRejected4  = 0x04 // DLL returns code 0xc; meaning unknown
	loginRejected8  = 0x08 // DLL returns code 0xd; meaning unknown
)

const pwdStatusMessage = 0x0e // RQ_PWD status followed by a text message

// ClientInfo is the host description sent in RQ_PWD. The DLL fills it from
// GetComputerNameA, GetUserNameA, GetVersionExA and network adapter data.
type ClientInfo struct {
	ComputerName string
	UserName     string
	OS           string
	// HardwareID is the 64-bit value the DLL sends after the OS string.
	HardwareID int64
	// Addresses are up to four non-zero 32-bit values (adapter addresses).
	Addresses []int32
	// Trailer is the final byte (global DAT_1809d4064, observed 0).
	Trailer byte
}

type Config struct {
	Login    string
	Password string
	Client   ClientInfo
	// RequestTimeout bounds each request/response exchange.
	RequestTimeout time.Duration
}

// VersionInfo is present in the RQ_LOGIN reply when flag bit 0 is set.
type VersionInfo struct {
	Text string
	A    int32
	B    int64
}

// Info holds the RQ_PWD reply fields in wire order. Names are given only where
// the DLL log identifies them; the rest are named by order.
type Info struct {
	Version *VersionInfo

	Byte1, Byte2, Byte3 byte
	// SessionTimeout is the server's value, used when the client sets none
	// (values below 300 are seconds, otherwise milliseconds).
	SessionTimeout             int32
	Byte4, Byte5, Byte6, Byte7 byte
	Int1                       int32

	// The following fields are optional in the reply.
	Flags      byte // bit 0 and bit 1 are stored separately by the DLL
	Byte8      byte
	SubBroker  byte // non-zero logs "incorrect SUBBRK value"
	Text1      string
	Byte9      byte
	Text2      string
	Text3      string
	Byte10     byte
	UID        int32 // "UID#"
	Byte11     byte
	SID        byte // "SID#", reported as server_status id
	Byte12     byte
	Byte13     byte
	SysVersion int32 // "SYS#"
	Build      int32 // "BLD#"
	Byte14     byte
	Int2       int32
	Text4      string
	Text5      string
	Text6      string
	Protocol   uint16 // "PRO#", 62 when absent
	FortsDate  int32  // days, "FORTS Date:"
	Text7      string
}

// LoginError is a non-zero RQ_PWD status or a rejecting RQ_LOGIN flag.
type LoginError struct {
	Code    int
	Message string
}

func (e *LoginError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("transaq login failed: code %d: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("transaq login failed: code %d", e.Code)
}

var ErrBye = errors.New("transaq: connection closed by server (BYE)")

// Conn is an authenticated TRANSAQ connection. Requests are serialized.
type Conn struct {
	nc      net.Conn
	timeout time.Duration

	mu   sync.Mutex
	keys *wire.Keys

	// sid is the server id from the RQ_PWD reply, compared on RQ_RELOGIN.
	sid byte
	// seq counts the ordinary requests sent (63/64 excluded): RQ_RECOVER
	// carries it as lastSeq.
	seq int32
	// lastType is the type of the last ordinary request; the RQ_RECOVER reply
	// may carry it instead of type 64.
	lastType byte
}

// Dial connects to addr and performs the login sequence.
func Dial(ctx context.Context, addr string, cfg Config) (*Conn, *Info, error) {
	var d net.Dialer
	nc, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, nil, err
	}
	c, info, err := Login(ctx, nc, cfg)
	if err != nil {
		nc.Close()
		return nil, nil, err
	}
	return c, info, nil
}

// Login runs RQ_LOGIN and RQ_PWD over an established stream and switches the
// connection to the issued session key.
func Login(ctx context.Context, nc net.Conn, cfg Config) (*Conn, *Info, error) {
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = DefaultRequestTimeout
	}
	c := &Conn{nc: nc, timeout: cfg.RequestTimeout, keys: wire.DefaultKeys()}

	reply, err := c.Request(ctx, RqLogin, LoginPayload(cfg.Login))
	if err != nil {
		return nil, nil, fmt.Errorf("RQ_LOGIN: %w", err)
	}
	challenge, version, err := parseLoginReply(reply)
	if err != nil {
		return nil, nil, fmt.Errorf("RQ_LOGIN: %w", err)
	}

	pwKey, err := cryptoprofile.NewCipher(cryptoprofile.PasswordKey(cfg.Password))
	if err != nil {
		return nil, nil, err
	}
	payload, err := PwdPayload(pwKey, challenge, cfg.Client)
	if err != nil {
		return nil, nil, err
	}
	reply, err = c.Request(ctx, RqPwd, payload)
	if err != nil {
		return nil, nil, fmt.Errorf("RQ_PWD: %w", err)
	}
	info, key, sync, err := parsePwdReply(pwKey, reply)
	if err != nil {
		return nil, nil, fmt.Errorf("RQ_PWD: %w", err)
	}
	info.Version = version

	keys, err := wire.NewKeys(key, sync)
	if err != nil {
		return nil, nil, err
	}
	c.mu.Lock()
	c.keys = keys
	c.sid = info.SID
	c.mu.Unlock()
	return c, info, nil
}

// SessionKeys parses an RQ_PWD reply payload with the account password and
// returns its fields and the session keys. It is used to decode captures.
func SessionKeys(password string, reply []byte) (*Info, *wire.Keys, error) {
	pwKey, err := cryptoprofile.NewCipher(cryptoprofile.PasswordKey(password))
	if err != nil {
		return nil, nil, err
	}
	info, key, sync, err := parsePwdReply(pwKey, reply)
	if err != nil {
		return nil, nil, err
	}
	keys, err := wire.NewKeys(key, sync)
	return info, keys, err
}

// LoginPayload builds RQ_LOGIN: C:'x' W:78 W:1 W:540 Z12:login W:65512.
func LoginPayload(login string) []byte {
	var w wire.Writer
	w.Byte(loginMagic)
	w.Uint16(ProtocolVersion)
	w.Uint16(loginWord2)
	w.Uint16(loginWord3)
	w.String(login, maxLogin)
	w.Uint16(wire.ClientBufferSize)
	return w.Bytes()
}

func parseLoginReply(p []byte) (challenge []byte, version *VersionInfo, err error) {
	r := wire.NewReader(p)
	flags, err := r.Byte()
	if err != nil {
		return nil, nil, err
	}
	switch {
	case flags&loginRejected4 != 0:
		return nil, nil, &LoginError{Code: 0xc}
	case flags&loginRejected8 != 0:
		return nil, nil, &LoginError{Code: 0xd}
	}
	n, err := r.Byte()
	if err != nil {
		return nil, nil, err
	}
	raw, err := r.Raw(int(n))
	if err != nil {
		return nil, nil, err
	}
	challenge = append([]byte(nil), raw...)
	if flags&loginHasVersion != 0 {
		v := &VersionInfo{}
		if v.Text, err = r.String(0x20); err == nil {
			if v.A, err = r.Int32(); err == nil {
				var b int32
				b, err = r.Int32()
				v.B = int64(b)
			}
		}
		if err != nil {
			return nil, nil, fmt.Errorf("version block: %w", err)
		}
		version = v
	}
	return challenge, version, nil
}

// PwdPayload builds RQ_PWD: the challenge encrypted with GOST gamma under the
// password key, then host strings, hardware id, addresses, 0 and a trailer.
func PwdPayload(pwKey *cryptoprofile.Cipher, challenge []byte, ci ClientInfo) ([]byte, error) {
	if len(ci.Addresses) > maxAddresses {
		return nil, fmt.Errorf("at most %d addresses", maxAddresses)
	}
	proof := make([]byte, len(challenge))
	pwKey.Gamma(cryptoprofile.DefaultSync, proof, challenge)

	var w wire.Writer
	w.Raw(proof)
	w.String(ci.ComputerName, maxComputerName)
	w.String(ci.UserName, maxUserName)
	w.String(ci.OS, maxOSName)
	w.Int64(ci.HardwareID)
	for _, a := range ci.Addresses {
		if a == 0 {
			break
		}
		w.Int32(a)
	}
	w.Int32(0)
	w.Byte(ci.Trailer)
	return w.Bytes(), nil
}

func parsePwdReply(pwKey *cryptoprofile.Cipher, p []byte) (*Info, []byte, [8]byte, error) {
	var sync [8]byte
	r := wire.NewReader(p)
	status, err := r.Byte()
	if err != nil {
		return nil, nil, sync, err
	}
	if status != 0 {
		le := &LoginError{Code: int(status)}
		if status == pwdStatusMessage {
			le.Message, _ = r.String(0x103)
		}
		return nil, nil, sync, le
	}

	encKey, err := r.Raw(cryptoprofile.KeySize)
	if err != nil {
		return nil, nil, sync, err
	}
	encSync, err := r.Raw(cryptoprofile.SyncSize)
	if err != nil {
		return nil, nil, sync, err
	}
	key := make([]byte, cryptoprofile.KeySize)
	pwKey.CFBDecrypt(cryptoprofile.DefaultSync, key, encKey)
	pwKey.CFBDecrypt(cryptoprofile.DefaultSync, sync[:], encSync)

	info := &Info{}
	required := []func() error{
		readByte(r, &info.Byte1), readByte(r, &info.Byte2), readByte(r, &info.Byte3),
		readInt(r, &info.SessionTimeout),
		readByte(r, &info.Byte4), readByte(r, &info.Byte5), readByte(r, &info.Byte6), readByte(r, &info.Byte7),
		readInt(r, &info.Int1),
	}
	for _, read := range required {
		if err := read(); err != nil {
			return nil, nil, sync, fmt.Errorf("reply body: %w", err)
		}
	}
	// The DLL ignores failures from here on and keeps defaults; a failed
	// string read does not advance the position.
	info.Protocol = 62
	optional := []func() error{
		readByte(r, &info.Flags), readByte(r, &info.Byte8), readByte(r, &info.SubBroker),
		readString(r, 0x81, &info.Text1), readByte(r, &info.Byte9),
		readString(r, 0x41, &info.Text2), readString(r, 0xd, &info.Text3),
		readByte(r, &info.Byte10), readInt(r, &info.UID), readByte(r, &info.Byte11),
		readByte(r, &info.SID), readByte(r, &info.Byte12), readByte(r, &info.Byte13),
		readInt(r, &info.SysVersion), readInt(r, &info.Build), readByte(r, &info.Byte14),
		readInt(r, &info.Int2), readString(r, 0xf, &info.Text4),
		readString(r, 0x80, &info.Text5), readString(r, 0x40, &info.Text6),
		func() error {
			v, err := r.Uint16()
			if err == nil {
				info.Protocol = v
			}
			return err
		},
		readInt(r, &info.FortsDate), readString(r, 0xf, &info.Text7),
	}
	for _, read := range optional {
		_ = read()
	}
	return info, key, sync, nil
}

func readByte(r *wire.Reader, dst *byte) func() error {
	return func() (err error) { *dst, err = r.Byte(); return }
}

func readInt(r *wire.Reader, dst *int32) func() error {
	return func() (err error) { *dst, err = r.Int32(); return }
}

func readString(r *wire.Reader, max int, dst *string) func() error {
	return func() (err error) { *dst, err = r.String(max); return }
}

// Request sends one request and waits for its reply. A reply must carry kind
// 2 and the request type (FUN_180355d50); kind 3 is BYE.
func (c *Conn) Request(ctx context.Context, typ byte, payload []byte) ([]byte, error) {
	f, err := c.exchange(ctx, typ, payload, true)
	if err != nil {
		return nil, err
	}
	return f.Payload, nil
}

// exchange sends a request and, if wait is set, reads and validates the reply.
func (c *Conn) exchange(ctx context.Context, typ byte, payload []byte, wait bool) (wire.Frame, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	nc := c.nc
	if nc == nil {
		return wire.Frame{}, net.ErrClosed
	}
	deadline := time.Now().Add(c.timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := nc.SetDeadline(deadline); err != nil {
		return wire.Frame{}, err
	}
	stop := context.AfterFunc(ctx, func() { nc.SetDeadline(time.Unix(1, 0)) })
	defer stop()

	// The DLL increments its send counter before every ordinary request; the
	// recovery requests 63/64 do not count (docs/protocol/connect.md).
	if typ != RqRelogin && typ != RqRecover {
		c.seq++
		c.lastType = typ
	}
	out, err := c.keys.Encode(wire.ClientToServer, wire.Frame{Flags: wire.KindRequest, Type: typ, Payload: payload})
	if err != nil {
		return wire.Frame{}, err
	}
	if _, err := nc.Write(out); err != nil {
		return wire.Frame{}, ctxErr(ctx, err)
	}
	if !wait {
		return wire.Frame{}, nil
	}
	f, err := c.keys.Decode(wire.ServerToClient, nc)
	if err != nil {
		return wire.Frame{}, ctxErr(ctx, err)
	}
	switch {
	case f.Kind() == wire.KindBye:
		return wire.Frame{}, ErrBye
	case f.Kind() != wire.KindReply || f.Type != typ:
		return wire.Frame{}, fmt.Errorf("incorrect server response: flags %#x type %#x", f.Flags, f.Type)
	}
	return f, nil
}

func ctxErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// Close closes the connection. The socket is read under the same lock Restore
// swaps it with, so a concurrent short recovery cannot leave the new socket open
// or race on the field.
func (c *Conn) Close() error {
	c.mu.Lock()
	nc := c.nc
	c.nc = nil
	c.mu.Unlock()
	if nc == nil {
		return nil
	}
	return nc.Close()
}

// LastSeq returns the send counter value of the last ordinary request, the
// lastSeq RQ_RECOVER carries.
func (c *Conn) LastSeq() int32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.seq
}

// SID returns the server id from the RQ_PWD reply.
func (c *Conn) SID() byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sid
}
