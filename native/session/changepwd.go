package session

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/wire"
)

// RqChangePwd is RQ_CHANGE_PWD (type 3, changepass.md D+O).
const RqChangePwd byte = 3

// ErrChangePasswordFatal reports the DLL's fatal empty/incorrect reply: the
// server buffer is unusable and the DLL closes the connection
// ("RQ_CHANGE_PWD incorrect server buffer"; changepass.md O/D).
var ErrChangePasswordFatal = errors.New("RQ_CHANGE_PWD: empty/incorrect server reply (fatal, connection closed)")

// ChangePasswordReply is the type-3 reply: B status (0 ok, 1 wrong old, 2 text),
// and the C-string message for status 2.
type ChangePasswordReply struct {
	Status  byte
	Message string
}

// ChangePasswordPayload is the O 56-byte RQ_CHANGE_PWD body:
// SHA-1(oldpass)[20] + 8 zero bytes + SHA-1(newpass)[20] + 8 zero bytes.
//
// SECURITY: the DLL sends UNSALTED SHA-1 of the password (no random, no salt).
// This is required for DLL compatibility and is an explicit I-risk, matching
// docs/protocol/changepass.md.
func ChangePasswordPayload(oldPassword, newPassword string) []byte {
	oldHash := sha1.Sum([]byte(oldPassword))
	newHash := sha1.Sum([]byte(newPassword))
	out := make([]byte, 0, 56)
	out = append(out, oldHash[:]...)
	out = append(out, make([]byte, 8)...)
	out = append(out, newHash[:]...)
	out = append(out, make([]byte, 8)...)
	return out
}

// ParseChangePasswordReply decodes the type-3 reply payload strictly:
//   - status 0/1: exactly one byte;
//   - status 2: a non-empty NUL-terminated C-string message, no trailing bytes;
//   - zero-length/other statuses/extra bytes: ErrChangePasswordFatal.
func ParseChangePasswordReply(payload []byte) (ChangePasswordReply, error) {
	var out ChangePasswordReply
	if len(payload) == 0 {
		return out, ErrChangePasswordFatal
	}
	out.Status = payload[0]
	switch out.Status {
	case 0, 1:
		if len(payload) != 1 {
			return ChangePasswordReply{}, ErrChangePasswordFatal
		}
		return out, nil
	case 2:
		if len(payload) < 3 {
			return ChangePasswordReply{}, ErrChangePasswordFatal
		}
		r := wire.NewReader(payload[1:])
		message, err := readCStringLimit(r, 0xfff0)
		if err != nil || message == "" || r.Len() > 0 {
			return ChangePasswordReply{}, ErrChangePasswordFatal
		}
		out.Message = message
		return out, nil
	default:
		return ChangePasswordReply{}, ErrChangePasswordFatal
	}
}

// readCStringLimit reads a NUL-terminated string with at most max characters
// (the reply text is a C-string, changepass.md D).
func readCStringLimit(r *wire.Reader, max int) (string, error) {
	out := make([]byte, 0, 32)
	for i := 0; i <= max; i++ {
		b, err := r.Byte()
		if err != nil {
			return "", fmt.Errorf("C-string: %w", err)
		}
		if b == 0 {
			return string(out), nil
		}
		if i == max {
			return "", fmt.Errorf("C-string longer than %d bytes", max)
		}
		out = append(out, b)
	}
	return "", fmt.Errorf("C-string longer than %d bytes", max)
}

// ChangePassword sends RQ_CHANGE_PWD and parses the reply. An empty reply is
// fatal: the session connection is closed here and ErrChangePasswordFatal is
// returned (the DLL reports connected="false"); session keys are not changed
// (D: the DLL keeps the old keys until the next login).
func (c *Conn) ChangePassword(ctx context.Context, oldPassword, newPassword string) (ChangePasswordReply, error) {
	reply, err := c.Request(ctx, RqChangePwd, ChangePasswordPayload(oldPassword, newPassword))
	if err != nil {
		return ChangePasswordReply{}, err
	}
	parsed, err := ParseChangePasswordReply(reply)
	if err != nil {
		// Any malformed reply is the DLL fatal "incorrect server buffer" path.
		_ = c.Close()
		return ChangePasswordReply{}, ErrChangePasswordFatal
	}
	return parsed, nil
}
