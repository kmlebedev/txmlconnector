package records

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/kmlebedev/txmlconnector/native/wire"
)

// R_NEWS (0x35) and R_NEWS_UPLOAD (0x36) are unsized records with variable
// C-strings (no zero padding): the splitter walks the fields to find the end
// (news.md, D/O).
const (
	TagNews       byte = 0x35
	TagNewsUpload byte = 0x36
)

// NewsHeaderRecord is one 0x35/0x36 header body (news.md, D/O).
type NewsHeaderRecord struct {
	ID        int64
	Timestamp int64 // Unix seconds; the DLL prints it as local machine time
	Source    string
	Title     string
	Flag      byte // 0 in the O runs; meaning I
}

// NewsUpload is the 0x36 record: an optional header and/or a packed body.
type NewsUpload struct {
	Flags   byte
	Header  *NewsHeaderRecord
	NewsID  int64 // id of the body (flags==2 form)
	HasID   bool
	Package []byte // int32 uncompressed size + zlib stream (flags&2)
}

// readCString reads a NUL-terminated C-string of at most max bytes. The wire
// field is not padded, so the NUL is part of the stream and the next field
// starts right after it (news-repro.md).
func readCString(r *wire.Reader, max int) (string, error) {
	// A C-string may hold exactly max characters; the NUL is the (max+1)-th
	// byte of the field. Reading one extra byte distinguishes "exactly max" from
	// "longer than max" (news/messages repro notes).
	out := make([]byte, 0, max)
	for i := 0; i <= max; i++ {
		b, err := r.Byte()
		if err != nil {
			return "", fmt.Errorf("C-string: %w", err)
		}
		if b == 0 {
			return string(out), nil
		}
		if i == max {
			return "", fmt.Errorf("C-string longer than %d bytes without NUL", max)
		}
		out = append(out, b)
	}
	return "", fmt.Errorf("C-string longer than %d bytes without NUL", max)
}

func readNewsHeaderFields(r *wire.Reader) (NewsHeaderRecord, error) {
	var h NewsHeaderRecord
	id, err := r.Int32()
	if err != nil {
		return h, fmt.Errorf("R_NEWS id: %w", err)
	}
	ts, err := r.Int32()
	if err != nil {
		return h, fmt.Errorf("R_NEWS timestamp: %w", err)
	}
	source, err := readCString(r, 16)
	if err != nil {
		return h, fmt.Errorf("R_NEWS source: %w", err)
	}
	title, err := readCString(r, 128)
	if err != nil {
		return h, fmt.Errorf("R_NEWS title: %w", err)
	}
	flag, err := r.Byte()
	if err != nil {
		return h, fmt.Errorf("R_NEWS flag: %w", err)
	}
	h.ID, h.Timestamp, h.Source, h.Title, h.Flag = int64(id), int64(ts), source, title, flag
	return h, nil
}

// newsBodyPackageLen returns the length of an S flags=2 package (int32
// uncompressed size + zlib stream) after the fixed unknown 4-byte field. The
// zlib stream end is located by decoding it; an invalid stream is an explicit
// ErrUnknownRecord, never a silent "consume the rest" fallback.
func newsBodyPackageLen(rest []byte) (int, error) {
	if len(rest) < 8 {
		return 0, fmt.Errorf("%w: R_NEWS_UPLOAD package is %d bytes", ErrUnknownRecord, len(rest))
	}
	counter := &byteCounter{data: rest[4:]}
	zr, err := zlib.NewReader(counter)
	if err != nil {
		return 0, fmt.Errorf("%w: R_NEWS_UPLOAD zlib: %v", ErrUnknownRecord, err)
	}
	_, readErr := io.Copy(io.Discard, zr)
	closeErr := zr.Close()
	if readErr != nil {
		return 0, fmt.Errorf("%w: R_NEWS_UPLOAD zlib: %v", ErrUnknownRecord, readErr)
	}
	if closeErr != nil {
		return 0, fmt.Errorf("%w: R_NEWS_UPLOAD zlib: %v", ErrUnknownRecord, closeErr)
	}
	return 4 + counter.off, nil
}

// byteCounter reads exactly as many bytes as the zlib decoder asks for and
// implements io.ByteReader, so compress/zlib does not wrap it in a buffered
// reader that would over-read into the next RQ_MAIN record.
type byteCounter struct {
	data []byte
	off  int
}

func (b *byteCounter) Read(p []byte) (int, error) {
	if b.off >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.off:])
	b.off += n
	return n, nil
}

func (b *byteCounter) ReadByte() (byte, error) {
	if b.off >= len(b.data) {
		return 0, io.EOF
	}
	c := b.data[b.off]
	b.off++
	return c, nil
}

// SkipNews consumes one 0x35/0x36 record body from the splitter reader.
func SkipNews(tag byte, r *wire.Reader) error {
	switch tag {
	case TagNews:
		_, err := readNewsHeaderFields(r)
		return err
	case TagNewsUpload:
		flags, err := r.Byte()
		if err != nil {
			return fmt.Errorf("R_NEWS_UPLOAD flags: %w", err)
		}
		if flags&1 != 0 {
			if _, err := readNewsHeaderFields(r); err != nil {
				return err
			}
		}
		if flags&2 != 0 {
			if flags&1 == 0 {
				if _, err := r.Int32(); err != nil {
					return fmt.Errorf("R_NEWS_UPLOAD news id: %w", err)
				}
			}
			if flags&1 != 0 {
				// Header+body (flags=3): legacy explicit package length; it has
				// no S capture and stays on the previous D/O layout.
				size, err := r.Int32()
				if err != nil {
					return fmt.Errorf("R_NEWS_UPLOAD package length: %w", err)
				}
				if size < 0 || int(size) > r.Len() {
					return &UnknownRecordError{Tag: TagNewsUpload, Offset: 0}
				}
				return r.Skip(int(size))
			}
			// S flags=2: L id, 4 unknown bytes, then int32+zlib package. Find the
			// zlib stream end instead of guessing the unknown field as a length.
			if err := r.Skip(4); err != nil {
				return &UnknownRecordError{Tag: TagNewsUpload, Offset: 0}
			}
			n, err := newsBodyPackageLen(r.Remaining())
			if err != nil {
				return err
			}
			return r.Skip(n)
		}
		return nil
	default:
		return fmt.Errorf("R_NEWS: unexpected tag %#x", tag)
	}
}

// ParseNewsHeader decodes one 0x35 body.
func ParseNewsHeader(body []byte) (NewsHeaderRecord, error) {
	r := wire.NewReader(body)
	h, err := readNewsHeaderFields(r)
	if err != nil {
		return h, err
	}
	if r.Len() > 0 {
		return h, fmt.Errorf("RNEWS: %d trailing bytes", r.Len())
	}
	return h, nil
}

// ParseNewsUpload decodes one 0x36 body.
func ParseNewsUpload(body []byte) (NewsUpload, error) {
	var out NewsUpload
	r := wire.NewReader(body)
	flags, err := r.Byte()
	if err != nil {
		return out, fmt.Errorf("R_NEWS_UPLOAD flags: %w", err)
	}
	out.Flags = flags
	if flags&1 != 0 {
		header, err := readNewsHeaderFields(r)
		if err != nil {
			return out, err
		}
		out.Header = &header
	}
	if flags&2 != 0 {
		if flags&1 == 0 {
			id, err := r.Int32()
			if err != nil {
				return out, fmt.Errorf("R_NEWS_UPLOAD news id: %w", err)
			}
			out.NewsID, out.HasID = int64(id), true
		}
		if flags&1 != 0 {
			size, err := r.Int32()
			if err != nil {
				return out, fmt.Errorf("R_NEWS_UPLOAD package length: %w", err)
			}
			if size < 0 || int(size) > r.Len() {
				return out, fmt.Errorf("%w: R_NEWS_UPLOAD package length %d", ErrUnknownRecord, size)
			}
			if out.Package, err = r.Raw(int(size)); err != nil {
				return out, fmt.Errorf("R_NEWS_UPLOAD package: %w", err)
			}
		} else {
			// S flags=2: L id, unknown 4 bytes, then int32+zlib package; take
			// exactly the zlib stream, do not guess the unknown field as a length.
			if err := r.Skip(4); err != nil {
				return out, fmt.Errorf("%w: R_NEWS_UPLOAD body", ErrUnknownRecord)
			}
			n, err := newsBodyPackageLen(r.Remaining())
			if err != nil {
				return out, err
			}
			if out.Package, err = r.Raw(n); err != nil {
				return out, fmt.Errorf("R_NEWS_UPLOAD package: %w", err)
			}
		}
	}
	if r.Len() > 0 {
		return out, fmt.Errorf("R_NEWS_UPLOAD: %d trailing bytes", r.Len())
	}
	return out, nil
}

// UnpackNewsBody decodes the packed body: int32 little-endian uncompressed size
// followed by a zlib stream (D: FUN_180451c10). The byte order is the one the
// fixture demonstrates; the DLL code path is I beyond that.
func UnpackNewsBody(pkg []byte) ([]byte, error) {
	if len(pkg) < 8 {
		return nil, fmt.Errorf("news package: %d bytes, need at least 8", len(pkg))
	}
	expected := binary.LittleEndian.Uint32(pkg[:4])
	if int(expected) > 16<<20 {
		return nil, fmt.Errorf("news package: suspicious uncompressed size %d", expected)
	}
	zr, err := zlib.NewReader(bytes.NewReader(pkg[4:]))
	if err != nil {
		return nil, fmt.Errorf("news package zlib: %w", err)
	}
	defer zr.Close()
	body, err := io.ReadAll(io.LimitReader(zr, int64(expected)+1))
	if err != nil {
		return nil, fmt.Errorf("news package inflate: %w", err)
	}
	if uint32(len(body)) != expected {
		return nil, fmt.Errorf("news package: uncompressed %d bytes, header says %d", len(body), expected)
	}
	return body, nil
}
