package session

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/kmlebedev/txmlconnector/native/wire"
)

// ErrNotSameServer reports a reconnect to a server with another SID: the DLL
// aborts the recovery with -0xe in that case (docs/protocol/connect.md).
var ErrNotSameServer = errors.New("transaq: reconnect to a different server")

// ReloginPayload builds RQ_RELOGIN: Z12: the login up to 12 bytes plus NUL. The
// request is sent on the default keys (RE-1).
func ReloginPayload(login string) []byte {
	var w wire.Writer
	w.String(login, maxLogin)
	return w.Bytes()
}

// RecoverPayload builds RQ_RECOVER: Bn: the session token from the RQ_RELOGIN
// reply (exactly its bytes, no length prefix) followed by L: lastSeq, the send
// counter of the last ordinary request. The request is sent on the saved
// session keys (RE-1).
func RecoverPayload(token []byte, lastSeq int32) []byte {
	var w wire.Writer
	w.Raw(token)
	w.Int32(lastSeq)
	return w.Bytes()
}

// ReloginReply is the RQ_RELOGIN response.
type ReloginReply struct {
	Token []byte
	SID   byte
}

// ParseReloginReply decodes the RQ_RELOGIN response: B:len, Bn:token, B:sid.
func ParseReloginReply(p []byte) (ReloginReply, error) {
	r := wire.NewReader(p)
	n, err := r.Byte()
	if err != nil {
		return ReloginReply{}, fmt.Errorf("RQ_RELOGIN reply: %w", err)
	}
	token, err := r.Raw(int(n))
	if err != nil {
		return ReloginReply{}, fmt.Errorf("RQ_RELOGIN token: %w", err)
	}
	sid, err := r.Byte()
	if err != nil {
		return ReloginReply{}, fmt.Errorf("RQ_RELOGIN sid: %w", err)
	}
	return ReloginReply{Token: append([]byte(nil), token...), SID: sid}, nil
}

// Restore reconnects the session on nc, the DLL short recovery path: RQ_RELOGIN
// is sent on the default keys (63), the token and SID are parsed and another
// server is refused (ErrNotSameServer), then RQ_RECOVER is sent on the saved
// session keys (64) with lastSeq and the reply frame is checked. On success the
// connection is replaced; the keys, the catalog and the subscriptions stay valid
// because the server keeps the session state (docs/protocol/connect.md).
func (c *Conn) Restore(ctx context.Context, nc net.Conn, login string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	savedKeys := c.keys
	seq := c.seq
	lastType := c.lastType

	deadline := time.Now().Add(c.timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := nc.SetDeadline(deadline); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { nc.SetDeadline(time.Unix(1, 0)) })
	defer stop()

	defaultKeys := wire.DefaultKeys()
	relogin, err := defaultKeys.Encode(wire.ClientToServer, wire.Frame{
		Flags: wire.KindRequest, Type: RqRelogin, Payload: ReloginPayload(login),
	})
	if err != nil {
		return err
	}
	if _, err := nc.Write(relogin); err != nil {
		return ctxErr(ctx, err)
	}
	f, err := defaultKeys.Decode(wire.ServerToClient, nc)
	if err != nil {
		return ctxErr(ctx, err)
	}
	switch {
	case f.Kind() == wire.KindBye:
		return ErrBye
	case f.Kind() != wire.KindReply || f.Type != RqRelogin:
		return fmt.Errorf("incorrect server RELOGIN response: flags %#x type %#x", f.Flags, f.Type)
	}
	reply, err := ParseReloginReply(f.Payload)
	if err != nil {
		return err
	}
	if reply.SID != c.sid {
		// The DLL compares the SID unconditionally (including a recorded SID of
		// 0) and aborts the recovery with -0xe on a mismatch.
		return fmt.Errorf("%w: SID %d instead of %d", ErrNotSameServer, reply.SID, c.sid)
	}

	recover, err := savedKeys.Encode(wire.ClientToServer, wire.Frame{
		Flags: wire.KindRequest, Type: RqRecover, Payload: RecoverPayload(reply.Token, seq),
	})
	if err != nil {
		return err
	}
	if _, err := nc.Write(recover); err != nil {
		return ctxErr(ctx, err)
	}
	f, err = savedKeys.Decode(wire.ServerToClient, nc)
	if err != nil {
		return ctxErr(ctx, err)
	}
	if f.Kind() != wire.KindReply || (f.Type != RqRecover && f.Type != lastType) {
		return fmt.Errorf("incorrect server RECOVER response: flags %#x type %#x", f.Flags, f.Type)
	}

	old := c.nc
	c.nc = nc
	if old != nil && old != nc {
		old.Close()
	}
	return nil
}
