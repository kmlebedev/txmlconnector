package native

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// DLL local validation texts of change_pass (changepass.md D+O, verbatim).
const (
	msgChangeOldMissing = "You have to specify old password"
	msgChangeNewMissing = "You have to specify new password"
	msgChangeTooLong    = "Password cannot be longer than 20 characters."
	msgChangeForbiden   = "Forbiden character: ';'"
	msgChangeSame       = "The same passwords specified"
)

// ChangePassword sends change_pass (RQ_CHANGE_PWD 3). On success the session
// keys are NOT changed (D: the DLL keeps the old keys until the next login), but
// the saved password is updated for the next reconnect/recover. The server
// security policy (conn+0x279) is I and is not implemented.
func (c *Client) ChangePassword(ctx context.Context, oldPassword, newPassword string) error {
	if oldPassword == "" {
		return errors.New(msgChangeOldMissing)
	}
	if newPassword == "" {
		return errors.New(msgChangeNewMissing)
	}
	if len(oldPassword) > 20 || len(newPassword) > 20 {
		return errors.New(msgChangeTooLong)
	}
	if strings.Contains(newPassword, ";") {
		return errors.New(msgChangeForbiden)
	}
	if oldPassword == newPassword {
		return errors.New(msgChangeSame)
	}
	conn := c.connection()
	if conn == nil {
		return errors.New("transaq: client is not connected")
	}
	reply, err := conn.ChangePassword(ctx, oldPassword, newPassword)
	if err != nil {
		// The fatal empty-reply path already closed the session connection; the
		// Connector reports connected="false" like the DLL.
		return err
	}
	switch reply.Status {
	case 0:
		c.mu.Lock()
		c.cfg.Password = newPassword
		c.mu.Unlock()
		return nil
	case 1:
		return errors.New("Wrong oldpass specified.")
	case 2:
		return errors.New(reply.Message)
	default:
		return fmt.Errorf("RQ_CHANGE_PWD: unknown status %d (I)", reply.Status)
	}
}
