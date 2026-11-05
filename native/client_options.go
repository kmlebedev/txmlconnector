package native

import (
	"context"
	"errors"
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/session"
)

// Options API (task 27). The request payloads are D+O; the data/XML forms are
// D-only (options.md I): the client returns the state collected from RQ_MAIN
// 0x6f/0x78 and marks mappings that are not recovered.
func (c *Client) optionSecID(underlying model.Symbol) (uint16, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.catalog == nil {
		return 0, fmt.Errorf("transaq: client is not connected")
	}
	sec, _, ok := c.catalog.Lookup(underlying.Board, underlying.SecCode)
	if !ok {
		return 0, fmt.Errorf("Инструмент не найден ('seccode'=%s, 'board'=%s)", underlying.SecCode, underlying.Board)
	}
	return sec.SecID, nil
}

// OptionFamilies is get_option_families: RQ_OPTION_FAMILIES 149 (O payload);
// the families arrive as 0x6f RQ_MAIN records (D, XML I).
func (c *Client) OptionFamilies(ctx context.Context, underlying model.Symbol) ([]model.OptionFamily, error) {
	secID, err := c.optionSecID(underlying)
	if err != nil {
		return nil, err
	}
	conn := c.connection()
	if conn == nil {
		return nil, fmt.Errorf("transaq: client is not connected")
	}
	if err := conn.OptionFamilies(ctx, secID); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := append([]model.OptionFamily(nil), c.optionFamilies[secID]...)
	return out, nil
}

// FamilyStrikes is get_family_strikes: the family-name -> (secid, family_id)
// mapping is not recovered (the DLL derives it from its internal family store,
// options.md I), so this returns the explicit I error instead of sending a
// guessed request.
func (c *Client) FamilyStrikes(ctx context.Context, family string) ([]model.Strike, error) {
	_ = ctx
	_ = family
	return nil, fmt.Errorf("family id mapping is not recovered (I)")
}

// Options is get_options: RQ_CERTAIN_OPTIONS 151 with the Z25 opt_code list
// (D). The option data records are D/I, so only the request/result is modelled;
// the returned list is empty until RE/S fills it (I, not dead code).
func (c *Client) Options(ctx context.Context, req model.OptionsRequest) ([]model.Option, error) {
	if len(req.OptCodes) == 0 {
		return nil, fmt.Errorf("No seccodes found")
	}
	conn := c.connection()
	if conn == nil {
		return nil, fmt.Errorf("transaq: client is not connected")
	}
	if err := conn.CertainOptions(ctx, req.OptCodes); err != nil {
		return nil, err
	}
	return nil, nil
}

// optionsServerMessage extracts the DLL <message> text from an options reply
// error, unwrapping the session request context ("RQ_OPTION_FAMILIES: ...").
func optionsServerMessage(err error) (string, bool) {
	var reply *session.OptionsServerError
	if errors.As(err, &reply) {
		return reply.Message, true
	}
	return "", false
}
