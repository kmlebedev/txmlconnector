package session

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// Transaction requests (PDF 3.8): RQ_TRANSACT is a direct reply frame; the
// release bookkeeping requests are D-only (their reply formats are I).
const (
	RqTransact          byte = 14
	RqTrnsInProgress    byte = 156
	RqEnforceTrnRelease byte = 157
)

// TxKind is the transaction kind byte the DLL puts in front of the request body
// (RE-1 FUN_18039f490; the fixtures start with 01 neworder / 02 cancelorder).
type TxKind byte

const (
	// TxNewOrder is TRN_NEWORDER.
	TxNewOrder TxKind = 1
	// TxCancelOrder is TRN_CANCELORDER.
	TxCancelOrder TxKind = 2
	// TxNewRpsRepo is TRN_NEWRPSREPO (O payload prefix 0x04).
	TxNewRpsRepo TxKind = 0x04
	// TxNewRepoOrder is TRN_NEWREPOORDER (O payload prefix 0x0e).
	TxNewRepoOrder TxKind = 0x0e
	// TxCancelNegDeal is the cancel-negdeal TRN code (O payload prefix 0x08).
	TxCancelNegDeal TxKind = 0x08
	// TxCancelReport4 shares the O prefix 0x08 with cancelnegdeal; the exact TRN
	// code difference is not recovered (I, rps.md).
	TxCancelReport4 TxKind = 0x08
	// TxNewMRepoOrder is TRN_NEWMREPOORDER: the payload layout is not recovered
	// (I), so no constant is fabricated.
	// TxNewStopOrder is TRN_NEWSTOPORDER (O payload prefix 0x14).
	TxNewStopOrder TxKind = 0x14
	// TxNewCondOrder is TRN_NEWORDER (0x01) with the cond fields appended (O).
	TxNewCondOrder TxKind = 0x01
	// TxCancelStopOrder is TRN_CANCELSTOPORDER (O payload 17 <packed L id>).
	TxCancelStopOrder TxKind = 0x17
)

// ErrOutcomeUnknown reports a transaction whose fate is unknown: the request
// may have reached the server, but no valid reply was received. It must never
// be retried automatically (Review Focus 2).
var ErrOutcomeUnknown = errors.New("transaq: transaction outcome is unknown")

// OutcomeError wraps an unknown outcome with the local transaction number.
type OutcomeError struct {
	TransactionID model.TransactionID
	Err           error
}

func (e *OutcomeError) Error() string {
	return fmt.Sprintf("transaq: transaction %d outcome is unknown: %v", e.TransactionID, e.Err)
}

func (e *OutcomeError) Unwrap() error { return e.Err }

func (e *OutcomeError) Is(target error) bool {
	return target == ErrOutcomeUnknown
}

// TransactionError is a known rejection: the server answered with a non-zero
// RSM code (the DLL maps the codes to the <message> texts, docs/protocol/
// orders.md; the full table is I).
type TransactionError struct {
	Code    byte
	Message string
}

func (e *TransactionError) Error() string {
	return fmt.Sprintf("transaq: transaction rejected (RSM code %d): %s", e.Code, e.Message)
}

// transactionMessages maps the O-confirmed RQ_TRANSACT server codes 1..6 to the
// DLL resource texts (errors.md). Codes 7..66 are not recovered (I) and render
// as Unknown error.

// transactionSeq is the local transaction number source: the DLL assigns the
// numbers itself (docs/protocol/orders.md), so an unknown outcome still has one.
var transactionSeq atomic.Uint64

// Transact sends one RQ_TRANSACT and parses its direct reply frame. A transport
// failure after the frame was written (and every invalid/empty reply) is an
// OutcomeError; errors before the write are ordinary. The request is never
// retried.
func (c *Conn) Transact(ctx context.Context, kind TxKind, body []byte) (model.TransactionID, error) {
	local := model.TransactionID(transactionSeq.Add(1))
	payload := make([]byte, 0, 1+len(body))
	payload = append(payload, byte(kind))
	payload = append(payload, body...)
	reply, err := c.transactExchange(ctx, payload)
	if err != nil {
		return local, err
	}
	if len(reply) == 0 {
		// The DLL logs "RQ_TRANSACT ... return incorrect buffer" and renders
		// <result success="false"><message>Unknown error</message></result>.
		return local, &OutcomeError{TransactionID: local, Err: errors.New("empty RQ_TRANSACT reply")}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if reply[0] != 0 {
		message, ok := TransactionErrorText(reply[0])
		if !ok || message == "" {
			message = "Unknown error"
		}
		return local, &TransactionError{Code: reply[0], Message: message}
	}
	r := wire.NewReader(reply[1:])
	tid, err := r.Int32()
	if err != nil {
		return local, &OutcomeError{TransactionID: local, Err: fmt.Errorf("RQ_TRANSACT reply: %w", err)}
	}
	return model.TransactionID(tid), nil
}

// transactExchange is exchange() with the write tracked: an error after the
// frame left the process means the transaction may have been accepted.
func (c *Conn) transactExchange(ctx context.Context, payload []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	nc := c.nc
	if nc == nil {
		return nil, net.ErrClosed
	}
	deadline := time.Now().Add(c.timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := nc.SetDeadline(deadline); err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { nc.SetDeadline(time.Unix(1, 0)) })
	defer stop()
	c.seq++
	c.lastType = RqTransact
	out, err := c.keys.Encode(wire.ClientToServer, wire.Frame{Flags: wire.KindRequest, Type: RqTransact, Payload: payload})
	if err != nil {
		return nil, err // before the write: an ordinary error
	}
	if _, err := nc.Write(out); err != nil {
		return nil, &OutcomeError{TransactionID: model.TransactionID(transactionSeq.Load()), Err: ctxErr(ctx, err)}
	}
	f, err := c.keys.Decode(wire.ServerToClient, nc)
	if err != nil {
		return nil, &OutcomeError{TransactionID: model.TransactionID(transactionSeq.Load()), Err: ctxErr(ctx, err)}
	}
	switch {
	case f.Kind() == wire.KindBye:
		return nil, &OutcomeError{TransactionID: model.TransactionID(transactionSeq.Load()), Err: ErrBye}
	case f.Kind() != wire.KindReply || f.Type != RqTransact:
		return nil, &OutcomeError{TransactionID: model.TransactionID(transactionSeq.Load()),
			Err: fmt.Errorf("incorrect RQ_TRANSACT response: flags %#x type %#x", f.Flags, f.Type)}
	}
	return f.Payload, nil
}

// TransactionsInProgress sends RQ_TRNS_IN_PROGRESS (156) and returns the
// transaction numbers of its reply stream. The record layout is D-only
// (L, L, B, G, Z12, B, Z25, B, L, Z15, Z15) and only the first L is
// interpreted as the transaction number; the rest is I. The stream ends at the
// first failed read, as in the DLL.
func (c *Conn) TransactionsInProgress(ctx context.Context) ([]model.TransactionID, error) {
	payload, err := c.Request(ctx, RqTrnsInProgress, nil)
	if err != nil {
		return nil, err
	}
	r := wire.NewReader(payload)
	ids := make([]model.TransactionID, 0)
	for r.Len() > 0 {
		number, err := r.Int32()
		if err != nil {
			break // the DLL stops at the first failed read
		}
		ids = append(ids, model.TransactionID(number))
		// Skip the rest of the record; a short body ends the stream (I).
		if _, err := r.Int32(); err != nil {
			break
		}
		if _, err := r.Byte(); err != nil {
			break
		}
		if _, err := r.Int64(); err != nil {
			break
		}
		if _, err := r.String(12); err != nil {
			break
		}
		if _, err := r.Byte(); err != nil {
			break
		}
		if _, err := r.String(25); err != nil {
			break
		}
		if _, err := r.Byte(); err != nil {
			break
		}
		if _, err := r.Int32(); err != nil {
			break
		}
		if _, err := r.String(15); err != nil {
			break
		}
		if _, err := r.String(15); err != nil {
			break
		}
	}
	return ids, nil
}
