package records

import (
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/wire"
)

// Option records (unsized, D layouts; options.md).
const (
	TagFamily      byte = 0x6f // R_FAMILY
	TagEndOfFamily byte = 0x71 // R_END_OF_FAMILY
	// TagEndOfFamilies closes the R_FAMILY list of one underlying (D:
	// FUN_180396720, "R_END_OF_FAMILIES"; S: production 5x 0x6f then 70 395c 00).
	TagEndOfFamilies byte = 0x70
	TagStrikes       byte = 0x78 // R_STRIKES
)

// FamilyRecord is one R_FAMILY body: W secid, Z6 opt_code, L, L.
type FamilyRecord struct {
	SecID   uint16
	OptCode string
	First   int64
	Second  int64
}

// EndOfFamilyRecord is one R_END_OF_FAMILY body: W secid, Z6 opt_code, L, Z512.
type EndOfFamilyRecord struct {
	SecID   uint16
	OptCode string
	Code    int64
	Text    string
}

// StrikesRecord is one R_STRIKES body: W secid, L family_id, {Z26 opt_code,
// B cp, L price}* terminated by an empty opt_code, then B final.
// EndOfFamiliesRecord is R_END_OF_FAMILIES: W secid, Z512 text (empty on
// success; a non-empty text is I).
type EndOfFamiliesRecord struct {
	SecID uint16
	Text  string
}

func readEndOfFamilies(r *wire.Reader) (EndOfFamiliesRecord, error) {
	var out EndOfFamiliesRecord
	secID, err := r.Uint16()
	if err != nil {
		return out, fmt.Errorf("R_END_OF_FAMILIES secid: %w", err)
	}
	text, err := readCString(r, 0x200)
	if err != nil {
		return out, fmt.Errorf("R_END_OF_FAMILIES text: %w", err)
	}
	return EndOfFamiliesRecord{SecID: secID, Text: text}, nil
}

// ParseEndOfFamilies decodes R_END_OF_FAMILIES (0x70).
func ParseEndOfFamilies(body []byte) (EndOfFamiliesRecord, error) {
	r := wire.NewReader(body)
	out, err := readEndOfFamilies(r)
	if err == nil && r.Len() > 0 {
		err = fmt.Errorf("R_END_OF_FAMILIES: %d trailing bytes", r.Len())
	}
	return out, err
}

type StrikesRecord struct {
	SecID    uint16
	FamilyID int64
	Items    []StrikeItem
	Final    byte
}

type StrikeItem struct {
	OptCode string
	CP      byte
	Price   int64
}

func readFamily(r *wire.Reader) (FamilyRecord, error) {
	var out FamilyRecord
	secID, err := r.Uint16()
	if err != nil {
		return out, fmt.Errorf("R_FAMILY secid: %w", err)
	}
	code, err := readCString(r, 6)
	if err != nil {
		return out, fmt.Errorf("R_FAMILY opt_code: %w", err)
	}
	first, err := r.Int32()
	if err != nil {
		return out, fmt.Errorf("R_FAMILY first: %w", err)
	}
	second, err := r.Int32()
	if err != nil {
		return out, fmt.Errorf("R_FAMILY second: %w", err)
	}
	out = FamilyRecord{SecID: secID, OptCode: code, First: int64(first), Second: int64(second)}
	return out, nil
}

func readEndOfFamily(r *wire.Reader) (EndOfFamilyRecord, error) {
	var out EndOfFamilyRecord
	secID, err := r.Uint16()
	if err != nil {
		return out, fmt.Errorf("R_END_OF_FAMILY secid: %w", err)
	}
	code, err := readCString(r, 6)
	if err != nil {
		return out, fmt.Errorf("R_END_OF_FAMILY opt_code: %w", err)
	}
	value, err := r.Int32()
	if err != nil {
		return out, fmt.Errorf("R_END_OF_FAMILY value: %w", err)
	}
	text, err := readCString(r, 512)
	if err != nil {
		return out, fmt.Errorf("R_END_OF_FAMILY text: %w", err)
	}
	out = EndOfFamilyRecord{SecID: secID, OptCode: code, Code: int64(value), Text: text}
	return out, nil
}

func readStrikes(r *wire.Reader) (StrikesRecord, error) {
	var out StrikesRecord
	secID, err := r.Uint16()
	if err != nil {
		return out, fmt.Errorf("R_STRIKES secid: %w", err)
	}
	family, err := r.Int32()
	if err != nil {
		return out, fmt.Errorf("R_STRIKES family: %w", err)
	}
	out.SecID, out.FamilyID = secID, int64(family)
	for {
		code, err := readCString(r, 26)
		if err != nil {
			return out, fmt.Errorf("R_STRIKES opt_code: %w", err)
		}
		if code == "" {
			break
		}
		cp, err := r.Byte()
		if err != nil {
			return out, fmt.Errorf("R_STRIKES cp: %w", err)
		}
		price, err := r.Int32()
		if err != nil {
			return out, fmt.Errorf("R_STRIKES price: %w", err)
		}
		out.Items = append(out.Items, StrikeItem{OptCode: code, CP: cp, Price: int64(price)})
	}
	final, err := r.Byte()
	if err != nil {
		return out, fmt.Errorf("R_STRIKES final: %w", err)
	}
	out.Final = final
	return out, nil
}

// SkipOptionRecord consumes one 0x6f/0x71/0x78 body from the splitter reader.
func SkipOptionRecord(tag byte, r *wire.Reader) error {
	switch tag {
	case TagFamily:
		_, err := readFamily(r)
		return err
	case TagEndOfFamily:
		_, err := readEndOfFamily(r)
		return err
	case TagEndOfFamilies:
		_, err := readEndOfFamilies(r)
		return err
	case TagStrikes:
		_, err := readStrikes(r)
		return err
	default:
		return fmt.Errorf("options: unexpected tag %#x", tag)
	}
}

// ParseFamily decodes one 0x6f body.
func ParseFamily(body []byte) (FamilyRecord, error) {
	r := wire.NewReader(body)
	out, err := readFamily(r)
	if err != nil {
		return out, err
	}
	if r.Len() > 0 {
		return out, fmt.Errorf("R_FAMILY: %d trailing bytes", r.Len())
	}
	return out, nil
}

// ParseEndOfFamily decodes one 0x71 body.
func ParseEndOfFamily(body []byte) (EndOfFamilyRecord, error) {
	r := wire.NewReader(body)
	out, err := readEndOfFamily(r)
	if err != nil {
		return out, err
	}
	if r.Len() > 0 {
		return out, fmt.Errorf("R_END_OF_FAMILY: %d trailing bytes", r.Len())
	}
	return out, nil
}

// ParseStrikes decodes one 0x78 body.
func ParseStrikes(body []byte) (StrikesRecord, error) {
	r := wire.NewReader(body)
	out, err := readStrikes(r)
	if err != nil {
		return out, err
	}
	if r.Len() > 0 {
		return out, fmt.Errorf("R_STRIKES: %d trailing bytes", r.Len())
	}
	return out, nil
}
