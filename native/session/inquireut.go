package session

import (
	"bytes"
	"compress/zlib"
	"context"
	"fmt"
	"io"

	"github.com/kmlebedev/txmlconnector/native/wire"
)

// RqInquireUT is RQ_INQUIRE_UT, the margin template request (D:
// FUN_1803dc4e0; S on production 2026-11-05).
const RqInquireUT byte = 152

// InquireUTPayload is B category, L offset into the compressed document.
func InquireUTPayload(category byte, offset int32) []byte {
	var w wire.Writer
	w.Byte(category)
	w.Int32(offset)
	return w.Bytes()
}

// InquireUT fetches the margin template of a union category: the reply is
// B, L inflated size, L compressed size, L chunk size, chunk; the client asks
// again from the next offset until the compressed document is complete, then
// inflates it (zlib). S: category 18, chunks of 65496 and 40186 bytes, 991306
// bytes of CP1251 XML.
func (c *Conn) InquireUT(ctx context.Context, category byte) ([]byte, error) {
	var compressed []byte
	var full, total int32
	for {
		reply, err := c.Request(ctx, RqInquireUT, InquireUTPayload(category, int32(len(compressed))))
		if err != nil {
			return nil, fmt.Errorf("RQ_INQUIRE_UT: %w", err)
		}
		r := wire.NewReader(reply)
		if _, err := r.Byte(); err != nil {
			return nil, fmt.Errorf("RQ_INQUIRE_UT header: %w", err)
		}
		var n int32
		for _, dst := range []*int32{&full, &total, &n} {
			if *dst, err = r.Int32(); err != nil {
				return nil, fmt.Errorf("RQ_INQUIRE_UT header: %w", err)
			}
		}
		if n <= 0 || total <= 0 {
			return nil, fmt.Errorf("RQ_INQUIRE_UT: empty chunk (total %d)", total)
		}
		chunk, err := r.Raw(int(n))
		if err != nil {
			return nil, fmt.Errorf("RQ_INQUIRE_UT chunk: %w", err)
		}
		compressed = append(compressed, chunk...)
		if int32(len(compressed)) >= total {
			break
		}
	}
	zr, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, fmt.Errorf("RQ_INQUIRE_UT inflate: %w", err)
	}
	doc, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("RQ_INQUIRE_UT inflate: %w", err)
	}
	if int32(len(doc)) != full {
		return nil, fmt.Errorf("RQ_INQUIRE_UT: inflated %d bytes, header says %d", len(doc), full)
	}
	return doc, nil
}
