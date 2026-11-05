package native

import (
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// TestNewsBadBodyDoesNotAbortRun: a 0x36 package with broken zlib (and a body
// whose header is absent) is silently dropped, like the DLL log-and-skip path;
// the following RQ_MAIN event is still delivered.
func TestNewsBadBodyDoesNotAbortRun(t *testing.T) {
	srv := newRunServer(t)
	srv.Snapshot = clientSnapshot(t)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")
	badPackage := []byte{4, 0, 0, 0, 0xde, 0xad, 0xbe, 0xef} // expected len 4, invalid zlib
	var bad wire.Writer
	bad.Byte(2)
	bad.Int32(7)
	bad.Int32(int32(len(badPackage)))
	bad.Raw(badPackage)
	srv.QueueMain(append([]byte{records.TagNewsUpload}, bad.Bytes()...))
	validNoHeader := []byte{0x13, 0x00, 0x00, 0x00} // a valid-looking package, header 9999 absent
	var orphan wire.Writer
	orphan.Byte(2)
	orphan.Int32(9999)
	orphan.Int32(int32(len(validNoHeader)))
	orphan.Raw(validNoHeader)
	srv.QueueMain(append([]byte{records.TagNewsUpload}, orphan.Bytes()...))
	// The stream must still process the next event.
	srv.QueueMain([]byte{0x0e, 0x00, 0x1a, 0x01})
	if got := rec.next(t, "<marketord"); !strings.Contains(got, "permit="+quote+"yes"+quote) {
		t.Fatalf("stream after bad news body = %s", got)
	}
}
