package session_test

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/testserver"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

func transactConn(t *testing.T, srv *testserver.Server) (*session.Conn, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	nc, err := srv.Dial(ctx, "testserver:0")
	if err != nil {
		t.Fatal(err)
	}
	conn, _, err := session.Login(ctx, nc, session.Config{Login: testserver.Login, Password: testserver.Password})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn, ctx
}

func replyHandler(payload []byte) func([]byte) (byte, []byte) {
	return func([]byte) (byte, []byte) { return wire.KindReply | wire.FlagPlain, payload }
}

// TestNewOrderLostReplyIsUncertain is Review Focus 2: the server takes the
// RQ_TRANSACT request and closes the connection without a reply. Transact must
// report ErrOutcomeUnknown with the local transaction number and must not retry.
func TestNewOrderLostReplyIsUncertain(t *testing.T) {
	srv := testserver.New(t)
	got := make(chan struct{})
	release := make(chan struct{})
	srv.Handle(session.RqTransact, func([]byte) (byte, []byte) {
		close(got)
		<-release
		return wire.KindReply | wire.FlagPlain, nil
	})
	conn, ctx := transactConn(t, srv)
	go func() {
		<-got
		srv.Drop()
		close(release)
	}()
	id, err := conn.Transact(ctx, session.TxNewOrder, []byte{0x00})
	var outcome *session.OutcomeError
	if !errors.As(err, &outcome) || !errors.Is(err, session.ErrOutcomeUnknown) {
		t.Fatalf("err = %v, want OutcomeError/ErrOutcomeUnknown", err)
	}
	if id == 0 || outcome.TransactionID != id {
		t.Fatalf("transaction id = %d (outcome %d), want a non-zero local number", id, outcome.TransactionID)
	}
	count := 0
	for _, f := range srv.Requests() {
		if f.Type == session.RqTransact {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("RQ_TRANSACT requests = %d, want exactly 1 (no retry)", count)
	}
}

// TestTransactSuccess: the O payload 00 10 00 10 yields transactionid 1 (the
// first packed-L after the RSM_OK code).
func TestTransactSuccess(t *testing.T) {
	srv := testserver.New(t)
	payload, err := hex.DecodeString("00100010")
	if err != nil {
		t.Fatal(err)
	}
	srv.Handle(session.RqTransact, replyHandler(payload))
	conn, ctx := transactConn(t, srv)
	id, err := conn.Transact(ctx, session.TxNewOrder, nil)
	if err != nil || id != 1 {
		t.Fatalf("Transact = %d, %v; want 1, nil", id, err)
	}
}

// TestTransactServerError: RSM code 1 is the DLL text "Клиент не допущен к
// торгам."; the outcome is known (rejected), not unknown.
func TestTransactServerError(t *testing.T) {
	srv := testserver.New(t)
	srv.Handle(session.RqTransact, replyHandler([]byte{1}))
	conn, ctx := transactConn(t, srv)
	id, err := conn.Transact(ctx, session.TxNewOrder, nil)
	var rejected *session.TransactionError
	if !errors.As(err, &rejected) || rejected.Message != "Клиент не допущен к торгам." {
		t.Fatalf("err = %v, want the RSM code 1 rejection", err)
	}
	if id == 0 {
		t.Fatalf("transaction id = 0, want the local number even for a rejection")
	}
}

// TestTransactEmptyReply: an empty reply is an unknown outcome (never success).
func TestTransactEmptyReply(t *testing.T) {
	srv := testserver.New(t)
	srv.Handle(session.RqTransact, replyHandler(nil))
	conn, ctx := transactConn(t, srv)
	_, err := conn.Transact(ctx, session.TxNewOrder, nil)
	if !errors.Is(err, session.ErrOutcomeUnknown) {
		t.Fatalf("err = %v, want ErrOutcomeUnknown", err)
	}
}

// TestTransactionsInProgress: the 156 reply is a stream of records until the
// first failed read (D layout: L,L,B,G,Z12,B,Z25,B,L,Z15,Z15); only the first L
// (transaction number) is interpreted, the rest is I.
func TestTransactionsInProgress(t *testing.T) {
	srv := testserver.New(t)
	var w wire.Writer
	w.Int32(7)
	w.Int32(0)
	w.Byte(0)
	w.Int64(0)
	w.String("TESTCLIENT", 12)
	w.Byte(0)
	w.String("SBER", 25)
	w.Byte(1)
	w.Int32(10)
	w.String("", 15)
	w.String("", 15)
	srv.Handle(session.RqTrnsInProgress, replyHandler(w.Bytes()))
	conn, ctx := transactConn(t, srv)
	ids, err := conn.TransactionsInProgress(ctx)
	if err != nil || len(ids) != 1 || ids[0] != model.TransactionID(7) {
		t.Fatalf("ids = %v, %v; want [7]", ids, err)
	}
}
