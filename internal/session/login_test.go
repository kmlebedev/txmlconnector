package session

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/internal/cryptoprofile"
	"github.com/kmlebedev/txmlconnector/internal/wire"
)

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Exchange recorded with the DLL against a local stub. The server bytes are
// those the DLL accepted; with them it logged "SID# 7 UID# 4242" and moved to
// the LOADING state. The stub issued session key 40..5f and sync 70..77.
const (
	dllLoginRequest = "e920e526b0d5603b9021ab352b9268fca36d964c861698e8f82e56"
	stubLoginReply  = "d7c6c64985ed4716d5cfc74138835a3ee034"
	stubPwdReply    = "d7e9c612a09a7307d5f98ddfc1df238d566b3ff5080d5f55670c23c7d7de9710" +
		"f4d9ab2fdbab9822915b38db0d1f444e1a69a6c3226093aa73d7658dad201e26" +
		"8547ea33eeac5f82e32a10596fbbae695562c9f7a972a1119c"
	// Header of the first post-login request (RQ_TIME, empty payload) as the
	// DLL sent it under the session key.
	dllTimeRequest = "7a451ae0505f09e4"
)

func TestLoginPayloadMatchesDLL(t *testing.T) {
	if got := hex.EncodeToString(LoginPayload("TESTLOGIN")); got != "78004e0001021c544553544c4f47494e00ffe8" {
		t.Fatalf("payload = %s", got)
	}
}

// RQ_PWD body traced by the DLL with host strings replaced by values of the
// same length; proof, addresses and trailer are unchanged.
func TestPwdPayloadLayout(t *testing.T) {
	c, err := cryptoprofile.NewCipher(cryptoprofile.PasswordKey("TESTPASS"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := PwdPayload(c, []byte{1, 2, 3, 4, 5, 6, 7, 8}, ClientInfo{
		ComputerName: "TESTHOST-MAC-01",
		UserName:     "testuser",
		OS:           "Windows 10  (Bld 19045) ",
		HardwareID:   0,
		Addresses:    []int32{16782022, -2130466624},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "211cf3ef536a73bf" +
		hex.EncodeToString([]byte("TESTHOST-MAC-01\x00testuser\x00Windows 10  (Bld 19045) \x00")) +
		"00" + "632c0110" + "0c74c5ef07" + "00" + "00"
	if hex.EncodeToString(got) != want {
		t.Fatalf("payload\n got %x\nwant %s", got, want)
	}
}

func TestLoginAgainstRecordedServer(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	errc := make(chan error, 1)
	go func() { errc <- replayServer(t, server) }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, info, err := Login(ctx, client, Config{Login: "TESTLOGIN", Password: "TESTPASS"})
	if err != nil {
		t.Fatal(err)
	}
	if info.UID != 4242 || info.SID != 7 || info.SessionTimeout != 120 || info.Text1 != "SRVNAME" {
		t.Fatalf("info = %+v", info)
	}
	if _, err := conn.Request(ctx, RqTime, nil); !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("RQ_TIME: %v", err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

// replayServer checks the client's packets against the DLL's and answers with
// the replies the DLL accepted.
func replayServer(t *testing.T, nc net.Conn) error {
	defer nc.Close()
	expect := func(want []byte) error {
		got := make([]byte, len(want))
		if _, err := io.ReadFull(nc, got); err != nil {
			return err
		}
		if !bytes.Equal(got, want) {
			return errors.New("unexpected client bytes: " + hex.EncodeToString(got))
		}
		return nil
	}
	if err := expect(unhex(t, dllLoginRequest)); err != nil {
		return err
	}
	if _, err := nc.Write(unhex(t, stubLoginReply)); err != nil {
		return err
	}
	f, err := wire.DefaultKeys().Decode(wire.ClientToServer, nc)
	if err != nil {
		return err
	}
	if f.Type != RqPwd || !bytes.HasPrefix(f.Payload, unhex(t, "211cf3ef536a73bf")) {
		return errors.New("unexpected RQ_PWD: " + hex.EncodeToString(f.Payload))
	}
	if _, err := nc.Write(unhex(t, stubPwdReply)); err != nil {
		return err
	}
	return expect(unhex(t, dllTimeRequest))
}

func TestLoginErrorWithMessage(t *testing.T) {
	c, _ := cryptoprofile.NewCipher(cryptoprofile.PasswordKey("x"))
	_, _, _, err := parsePwdReply(c, append([]byte{pwdStatusMessage}, "bad password\x00"...))
	var le *LoginError
	if !errors.As(err, &le) || le.Code != 0x0e || le.Message != "bad password" {
		t.Fatalf("err = %v", err)
	}
}
