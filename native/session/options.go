package session

import (
	"context"
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/wire"
)

// Option request types (options.md, D+O). The 153 and 151 senders have the
// server-version gate 0x45 ("Unsupported request."); 150 has no gate (D). The
// field location symbolStore+0x694 is not recovered (I), so the Go client cannot
// apply the gate up front and relies on the server reply.
const (
	RqOptionFamilies  byte = 149 // RQ_OPTION_FAMILIES
	RqFamiliyOptions  byte = 150 // RQ_FAMILIY_OPTIONS (DLL spelling)
	RqCertainOptions  byte = 151 // RQ_CERTAIN_OPTIONS
	RqFamilyStrikes   byte = 153 // RQ_FAMILY_STRIKES
	ServerVersionGate      = 0x45
)

// OptionFamiliesPayload is W secid (O: 001a for SBER).
func OptionFamiliesPayload(secID uint16) []byte {
	var w wire.Writer
	w.Uint16(secID)
	return w.Bytes()
}

// FamilyStrikesPayload is W secid + packed L family id (D).
func FamilyStrikesPayload(secID uint16, familyID int64) []byte {
	var w wire.Writer
	w.Uint16(secID)
	w.Int32(int32(familyID))
	return w.Bytes()
}

// FamilyOptionsPayload is W secid + Z6 string + two packed L values (D).
func FamilyOptionsPayload(secID uint16, text string, first, second int64) []byte {
	var w wire.Writer
	w.Uint16(secID)
	w.String(text, 6)
	w.Int32(int32(first))
	w.Int32(int32(second))
	return w.Bytes()
}

// CertainOptionsPayload is the Z25 opt_code stream of RQ_CERTAIN_OPTIONS (D:
// one C-string per requested option, no separators observed).
func CertainOptionsPayload(optCodes []string) []byte {
	var w wire.Writer
	for _, code := range optCodes {
		w.String(code, 25)
	}
	return w.Bytes()
}

// OptionsReply decodes the C-string reply (up to 0x200 bytes): payload 0x00 is
// an empty C-string = success; a non-empty one is the DLL <message> text (O for
// 149: 00 -> success, "No families" -> error). A zero-length reply is the DLL
// "incorrect buffer" failure, not success.
func OptionsReply(payload []byte) (string, error) {
	if len(payload) == 0 {
		return "", fmt.Errorf("incorrect server buffer")
	}
	r := wire.NewReader(payload)
	message, err := readCStringLimit(r, 0x200)
	if err != nil {
		return "", fmt.Errorf("options reply: %w", err)
	}
	if r.Len() > 0 {
		return "", fmt.Errorf("options reply: %d trailing bytes", r.Len())
	}
	return message, nil
}

func optionsReplyError(payload []byte) error {
	message, err := OptionsReply(payload)
	if err != nil {
		return err
	}
	if message != "" {
		return &OptionsServerError{Message: message}
	}
	return nil
}

// OptionsServerError is the non-empty C-string reply (the DLL <message> text).
type OptionsServerError struct{ Message string }

func (e *OptionsServerError) Error() string { return e.Message }

// OptionFamilies sends RQ_OPTION_FAMILIES (149, no version gate).
func (c *Conn) OptionFamilies(ctx context.Context, secID uint16) error {
	reply, err := c.Request(ctx, RqOptionFamilies, OptionFamiliesPayload(secID))
	if err != nil {
		return fmt.Errorf("RQ_OPTION_FAMILIES: %w", err)
	}
	return optionsReplyError(reply)
}

// FamilyStrikes sends RQ_FAMILY_STRIKES (153, gate 0x45 on the DLL side).
func (c *Conn) FamilyStrikes(ctx context.Context, secID uint16, familyID int64) error {
	reply, err := c.Request(ctx, RqFamilyStrikes, FamilyStrikesPayload(secID, familyID))
	if err != nil {
		return fmt.Errorf("RQ_FAMILY_STRIKES: %w", err)
	}
	return optionsReplyError(reply)
}

// FamilyOptions sends RQ_FAMILIY_OPTIONS (150, gate 0x45).
func (c *Conn) FamilyOptions(ctx context.Context, secID uint16, text string, first, second int64) error {
	reply, err := c.Request(ctx, RqFamiliyOptions, FamilyOptionsPayload(secID, text, first, second))
	if err != nil {
		return fmt.Errorf("RQ_FAMILIY_OPTIONS: %w", err)
	}
	return optionsReplyError(reply)
}

// CertainOptions sends RQ_CERTAIN_OPTIONS (151, gate 0x45).
func (c *Conn) CertainOptions(ctx context.Context, optCodes []string) error {
	reply, err := c.Request(ctx, RqCertainOptions, CertainOptionsPayload(optCodes))
	if err != nil {
		return fmt.Errorf("RQ_CERTAIN_OPTIONS: %w", err)
	}
	return optionsReplyError(reply)
}
