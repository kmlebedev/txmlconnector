package native

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// quote spares raw strings in the expected XML snippets.
var quote = string('"')

// russianZone is the 16-byte time zone block of the demo server (bias -180
// minutes, daylight -60), the value RQ_TIME carries after the timestamp.
var russianZone = [16]byte{0x4c, 0xff, 0x00, 0x00, 0xc4, 0xff}

// TestServerTimeDifference uses a testserver clock shifted by -2 s: the DLL gets
// the server clock minus the local one, so the result must be about -2 s.
func TestServerTimeDifference(t *testing.T) {
	srv := newTestServer(t)
	srv.Handle(session.RqTime, func([]byte) (byte, []byte) {
		var w wire.Writer
		w.Int32(int32(time.Now().Add(-2 * time.Second).Unix()))
		w.Raw(russianZone[:])
		return wire.KindReply, w.Bytes()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Dial(ctx, ClientConfig{
		Address: "testserver:0", Login: testLogin, Password: testPassword, Dial: srv.Dial,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	if tz := c.ServerTZ(); tz != "Russian Standard Time" {
		t.Fatalf("server tz = %q", tz)
	}
	diff, err := c.ServerTimeDifference(ctx)
	if err != nil {
		t.Fatalf("ServerTimeDifference: %v", err)
	}
	if diff > -1*time.Second || diff < -3*time.Second {
		t.Fatalf("ServerTimeDifference = %v, want -2s ±1s", diff)
	}
}

// TestServiceCommands checks the cached service data: the SID from RQ_PWD and
// the market dictionary from RQ_STARTUP.
func TestServiceCommands(t *testing.T) {
	srv := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Dial(ctx, ClientConfig{
		Address: "testserver:0", Login: testLogin, Password: testPassword, Dial: srv.Dial,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	if id := c.ServerID(); id != 7 {
		t.Fatalf("ServerID = %d, want 7", id)
	}
	markets := c.Markets()
	if len(markets) != 9 {
		t.Fatalf("Markets = %d entries, want 9", len(markets))
	}
	if m := markets[0]; m.ID != 15 || m.Code != "ETS" {
		t.Fatalf("first market = %+v", m)
	}
	// The dictionary is returned as a copy.
	markets[0].Code = "CHANGED"
	if m := c.Markets()[0]; m.Code != "ETS" {
		t.Fatalf("Markets exposes the internal slice: %+v", m)
	}
}

// serviceCallbacks parses native/testdata/service-dll.xml into the command
// results and the asynchronous callbacks, in file order (comments removed).
func serviceCallbacks(t *testing.T) (results, callbacks []string) {
	t.Helper()
	data, err := os.ReadFile("testdata/service-dll.xml")
	if err != nil {
		t.Fatal(err)
	}
	inComment := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "<!--") {
			inComment = true
		}
		if inComment {
			if strings.HasSuffix(line, "-->") {
				inComment = false
			}
			continue
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "<result") {
			results = append(results, line)
		} else {
			callbacks = append(callbacks, line)
		}
	}
	if len(results) != 3 || len(callbacks) != 2 {
		t.Fatalf("fixture: %d results, %d callbacks, want 3 and 2", len(results), len(callbacks))
	}
	return results, callbacks
}

// TestServiceCommandsMatchDLL compares the three service commands with the DLL
// output fixture (native/testdata/service-dll.xml): deterministic lines byte for
// byte, the difference attribute numerically (it depends on the local time zone;
// the fixture was taken on UTC+5 against the MSK server).
func TestServiceCommandsMatchDLL(t *testing.T) {
	results, callbacks := serviceCallbacks(t)
	srv := newTestServer(t)
	srv.SID = 0 // the fixture was captured on the demo server (SID 0)
	srv.Handle(session.RqTime, func([]byte) (byte, []byte) {
		var w wire.Writer
		w.Int32(int32(time.Now().Add(-7200 * time.Second).Unix()))
		w.Raw(russianZone[:])
		return wire.KindReply, w.Bytes()
	})
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")

	// get_server_id: the result and the cached SID callback, byte for byte.
	cmd := "<command id=" + quote + "get_server_id" + quote + "/>"
	if got := send(t, c, cmd); got != results[0] {
		t.Fatalf("get_server_id result = %s, want %s", got, results[0])
	}
	if got := rec.next(t, "<current_server"); got != callbacks[0] {
		t.Fatalf("current_server = %s, want %s", got, callbacks[0])
	}

	// get_servtime_difference: one RQ_TIME on the wire and the diff attribute.
	before := len(srv.Requests())
	cmd = "<command id=" + quote + "get_servtime_difference" + quote + "/>"
	got := send(t, c, cmd)
	if !strings.HasPrefix(got, "<result success="+quote+"true"+quote+" diff=") {
		t.Fatalf("get_servtime_difference result = %s", got)
	}
	if actual := diffAttr(t, got); actual < -7201 || actual > -7199 {
		t.Fatalf("diff = %d, want -7200 ±1 (fixture %s)", actual, results[1])
	}
	requests := srv.Requests()
	if len(requests)-before != 1 || requests[len(requests)-1].Type != session.RqTime {
		t.Fatalf("get_servtime_difference must send exactly one RQ_TIME, got %d new request(s)", len(requests)-before)
	}

	// get_markets: cached RQ_STARTUP, no network request.
	before = len(srv.Requests())
	cmd = "<command id=" + quote + "get_markets" + quote + "/>"
	if got := send(t, c, cmd); got != results[2] {
		t.Fatalf("get_markets result = %s, want %s", got, results[2])
	}
	if got := rec.next(t, "<markets"); got != callbacks[1] {
		t.Fatalf("markets = %s, want %s", got, callbacks[1])
	}
	if delta := len(srv.Requests()) - before; delta != 0 {
		t.Fatalf("get_markets sent %d network request(s), want 0", delta)
	}
}

// diffAttr parses the diff attribute of the get_servtime_difference result.
func diffAttr(t *testing.T, result string) int {
	t.Helper()
	prefix := "diff=" + quote
	i := strings.Index(result, prefix)
	if i < 0 {
		t.Fatalf("no diff attribute in %s", result)
	}
	rest := result[i+len(prefix):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		t.Fatalf("unterminated diff attribute in %s", result)
	}
	v, err := strconv.Atoi(rest[:j])
	if err != nil {
		t.Fatalf("diff attribute %q: %v", rest[:j], err)
	}
	return v
}

// TestConnectLanguageTrailer checks the <language> connect option: it is the
// trailer byte of RQ_PWD ("ru"=0, "en"=1, RE-2); an unknown language is rejected
// with the DLL message.
func TestConnectLanguageTrailer(t *testing.T) {
	srv := newTestServer(t)
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	connect := strings.Replace(connectCommand(), "</rqdelay>", "</rqdelay><language>en</language>", 1)
	if got := send(t, c, connect); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("connect = %s", got)
	}
	rec.next(t, "<server_status")
	var pwd []byte
	for _, f := range srv.Requests() {
		if f.Type == session.RqPwd {
			pwd = f.Payload
		}
	}
	if len(pwd) == 0 {
		t.Fatal("the server saw no RQ_PWD request")
	}
	if got := pwd[len(pwd)-1]; got != 1 {
		t.Fatalf("RQ_PWD trailer = %d for language=en, want 1", got)
	}

	unknown := New(Config{Dial: srv.Dial})
	rec2 := newRecorder()
	if err := unknown.Start(rec2.handle); err != nil {
		t.Fatal(err)
	}
	defer unknown.Close()
	bad := strings.Replace(connectCommand(), "</rqdelay>", "</rqdelay><language>de</language>", 1)
	want := "<result success=" + quote + "false" + quote + "><message>Unknown language.</message></result>"
	if got := send(t, unknown, bad); got != want {
		t.Fatalf("unknown language = %s", got)
	}
	if got := send(t, unknown, connectCommand()); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("connect after a rejected language = %s", got)
	}
}

// TestConnectUTCTime checks the <utc_time> connect option: <alltrades> carries
// the UTC instant instead of the server wall clock (RE-1: XML-only option).
func TestConnectUTCTime(t *testing.T) {
	srv := marketServer(t, "alltrades-inject1.hex")
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	connect := strings.Replace(connectCommand(), "</rqdelay>",
		"</rqdelay><milliseconds>true</milliseconds><utc_time>true</utc_time>", 1)
	send(t, c, connect)
	rec.next(t, "<server_status")
	sub := "<command id=" + quote + "subscribe" + quote + "><alltrades>"
	sub += "<security><board>TQBR</board><seccode>SBER</seccode></security></alltrades></command>"
	if got := send(t, c, sub); got != "<result success="+quote+"true"+quote+"/>" {
		t.Fatalf("subscribe = %s", got)
	}
	alltrades := rec.next(t, "<alltrades>")
	// 23:53:20 MSK is 20:53:20 UTC on the demo server (bias -180 minutes).
	if !strings.Contains(alltrades, "<time>03.11.2026 20:53:20.000</time>") {
		t.Fatalf("utc_time <alltrades> has no UTC time: %s", alltrades)
	}
	if strings.Contains(alltrades, "23:53:20") {
		t.Fatalf("utc_time <alltrades> still has the server wall clock: %s", alltrades)
	}
}

// TestConnectOptionsMatchDLL checks that the Go connect requests are byte-equal
// to the DLL capture for the same <connect> parameters: RQ_LOGIN, the RQ_PWD tail
// after the proof for ru/en and the frame-type set. The fixture is the task-11
// RE-2 capture; until it is committed the test is skipped.
func TestConnectOptionsMatchDLL(t *testing.T) {
	t.Skip("connect options fixture (R6 RE-2 runs, task-11) is not in the repository yet; enable the byte comparison when it lands")
}
