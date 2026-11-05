package session

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/hex"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/wire"
)

// S (production 2026-11-05): category 18 asks "1200", then "1282fd0f" for the
// offset 65496 after the first 65496-byte chunk.
func TestInquireUTPayloadMatchesS(t *testing.T) {
	if got := hex.EncodeToString(InquireUTPayload(0x12, 0)); got != "1200" {
		t.Fatalf("first = %s", got)
	}
	if got := hex.EncodeToString(InquireUTPayload(0x12, 65496)); got != "1282fd0f" {
		t.Fatalf("next = %s", got)
	}
}

// The document comes in chunks that the client requests by offset and then
// inflates.
func TestInquireUTAssemblesChunks(t *testing.T) {
	doc := bytes.Repeat([]byte(`<instr seccode="SBER" long="15.00"/>`), 4000)
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	zw.Write(doc)
	zw.Close()
	compressed := z.Bytes()
	half := len(compressed) / 2

	client, server := pipeTCP(t)
	c := &Conn{nc: client, timeout: 5 * time.Second, keys: wire.DefaultKeys()}
	var requests []string
	go func() {
		keys := wire.DefaultKeys()
		for {
			f, err := keys.Decode(wire.ClientToServer, server)
			if err != nil {
				return
			}
			requests = append(requests, hex.EncodeToString(f.Payload))
			r := wire.NewReader(f.Payload)
			r.Byte()
			off, _ := r.Int32()
			end := min(int(off)+half, len(compressed))
			var w wire.Writer
			w.Byte(0x0f)
			w.Int32(int32(len(doc)))
			w.Int32(int32(len(compressed)))
			w.Int32(int32(end - int(off)))
			w.Raw(compressed[off:end])
			out, _ := keys.Encode(wire.ServerToClient, wire.Frame{Flags: wire.KindReply, Type: f.Type, Payload: w.Bytes()})
			server.Write(out)
		}
	}()
	got, err := c.InquireUT(context.Background(), 0x12)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, doc) {
		t.Fatalf("document: %d bytes, want %d", len(got), len(doc))
	}
	if len(requests) < 2 || requests[0] != "1200" {
		t.Fatalf("requests = %v", requests)
	}
}
