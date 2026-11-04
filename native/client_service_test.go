package native

import (
	"bytes"
	"context"
	"encoding/hex"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/cryptoprofile"
	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/testserver"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// quote spares raw strings in the expected XML snippets.
var quote = string('"')

// russianZone is the 16-byte time zone block of the demo server (bias -180
// minutes, daylight -60), the value RQ_TIME carries after the timestamp.
var russianZone = [16]byte{0x4c, 0xff, 0x00, 0x00, 0xc4, 0xff}

// fakeUTCWall returns the local wall clock encoded as a fake UTC instant: the
// calendar components of t in the local zone taken as UTC. The DLL compares wall
// clocks this way (Windows wall time carries no zone), so tests built on it do
// not depend on the host time zone.
func fakeUTCWall(t time.Time) time.Time {
	y, mo, d := t.Date()
	h, mi, sec := t.Clock()
	return time.Date(y, mo, d, h, mi, sec, 0, time.UTC)
}

// TestServerTimeDifference uses a testserver clock shifted by -2 s: the DLL gets
// the server clock minus the local one, so the result must be about -2 s.
func TestServerTimeDifference(t *testing.T) {
	srv := newTestServer(t)
	srv.Handle(session.RqTime, func([]byte) (byte, []byte) {
		var w wire.Writer
		w.Int32(int32(fakeUTCWall(time.Now()).Add(-2 * time.Second).Unix()))
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
		w.Int32(int32(fakeUTCWall(time.Now()).Add(-7200 * time.Second).Unix()))
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
	before := countNonMain(srv.Requests())
	cmd := "<command id=" + quote + "get_server_id" + quote + "/>"
	if got := send(t, c, cmd); got != results[0] {
		t.Fatalf("get_server_id result = %s, want %s", got, results[0])
	}
	if got := rec.next(t, "<current_server"); got != callbacks[0] {
		t.Fatalf("current_server = %s, want %s", got, callbacks[0])
	}
	if delta := countNonMain(srv.Requests()) - before; delta != 0 {
		t.Fatalf("get_server_id sent %d non-RQ_MAIN request(s), want 0", delta)
	}

	// get_servtime_difference: one RQ_TIME (50) on the wire and the diff
	// attribute. RQ_MAIN polling runs in parallel, so only non-RQ_MAIN frames
	// are counted (deterministic).
	before = countNonMain(srv.Requests())
	beforeTime := countType(srv.Requests(), session.RqTime)
	cmd = "<command id=" + quote + "get_servtime_difference" + quote + "/>"
	got := send(t, c, cmd)
	if !strings.HasPrefix(got, "<result success="+quote+"true"+quote+" diff=") {
		t.Fatalf("get_servtime_difference result = %s", got)
	}
	if actual := diffAttr(t, got); actual < -7201 || actual > -7199 {
		t.Fatalf("diff = %d, want -7200 ±1 (fixture %s)", actual, results[1])
	}
	if delta := countNonMain(srv.Requests()) - before; delta != 1 {
		t.Fatalf("get_servtime_difference sent %d non-RQ_MAIN request(s), want 1", delta)
	}
	if delta := countType(srv.Requests(), session.RqTime) - beforeTime; delta != 1 {
		t.Fatalf("get_servtime_difference sent %d RQ_TIME request(s), want 1", delta)
	}

	// get_markets: cached RQ_STARTUP, no network request.
	before = countNonMain(srv.Requests())
	cmd = "<command id=" + quote + "get_markets" + quote + "/>"
	if got := send(t, c, cmd); got != results[2] {
		t.Fatalf("get_markets result = %s, want %s", got, results[2])
	}
	if got := rec.next(t, "<markets"); got != callbacks[1] {
		t.Fatalf("markets = %s, want %s", got, callbacks[1])
	}
	if delta := countNonMain(srv.Requests()) - before; delta != 0 {
		t.Fatalf("get_markets sent %d non-RQ_MAIN request(s), want 0", delta)
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

// connectFixture parses native/testdata/connect-options-live.txt: "key = value"
// lines, '#' comments, surrounding quotes removed.
func connectFixture(t *testing.T) map[string]string {
	t.Helper()
	data, err := os.ReadFile("testdata/connect-options-live.txt")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("bad fixture line %q", line)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			value = value[1 : len(value)-1]
		}
		out[key] = value
	}
	return out
}

// fixtureInt parses a decimal or 0x-prefixed fixture value.
func fixtureInt(t *testing.T, fx map[string]string, key string) int64 {
	t.Helper()
	value, ok := fx[key]
	if !ok {
		t.Fatalf("fixture has no %s", key)
	}
	base := 10
	text := value
	if strings.HasPrefix(text, "0x") {
		base, text = 16, text[2:]
	}
	n, err := strconv.ParseInt(text, base, 64)
	if err != nil {
		t.Fatalf("%s = %q: %v", key, value, err)
	}
	return n
}

// TestConnectOptionsMatchDLL compares the connect requests with the R6 live
// capture (native/testdata/connect-options-live.txt): the RQ_LOGIN payload byte
// for byte, the RQ_PWD tail after the proof for default/options (ru) and
// language=en, and the frame-type set: <connect> options do not change the wire,
// only language changes the RQ_PWD trailer.
func TestConnectOptionsMatchDLL(t *testing.T) {
	fx := connectFixture(t)
	if got := hex.EncodeToString(session.LoginPayload("TESTLOGIN")); got != fx["rq_login"] {
		t.Fatalf("LoginPayload(TESTLOGIN) = %s, want %s", got, fx["rq_login"])
	}

	ci := session.ClientInfo{
		ComputerName: fx["client.computer_name"],
		UserName:     fx["client.user_name"],
		OS:           fx["client.os"],
		HardwareID:   fixtureInt(t, fx, "client.hardware_id"),
	}
	for _, address := range strings.Split(fx["client.addresses"], ",") {
		ci.Addresses = append(ci.Addresses, int32(parseHexOrDecimal(t, address)))
	}
	// RQ_PWD is encrypted with the password key; TESTPASS is the testserver
	// credential, the fixture itself contains no password.
	pwKey, err := cryptoprofile.NewCipher(cryptoprofile.PasswordKey(testserver.Password))
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range []string{"default", "options", "lang_en"} {
		proofLen := int(fixtureInt(t, fx, run+".rq_pwd_proof_len_excluded"))
		ci.Trailer = byte(fixtureInt(t, fx, run+".rq_pwd_trailer"))
		payload, err := session.PwdPayload(pwKey, make([]byte, proofLen), ci)
		if err != nil {
			t.Fatalf("%s: PwdPayload: %v", run, err)
		}
		if wantLen := int(fixtureInt(t, fx, run+".rq_pwd_payload_len")); len(payload) != wantLen {
			t.Fatalf("%s: RQ_PWD payload = %d bytes, want %d", run, len(payload), wantLen)
		}
		got := hex.EncodeToString(payload[proofLen:])
		if want := fx[run+".rq_pwd_tail"]; got != want {
			t.Fatalf("%s: RQ_PWD tail = %s, want %s", run, got, want)
		}
	}

	// End-to-end check: the Connector must actually send these bytes. It gets the
	// dummy ClientInfo from the fixture, and the testserver records the decrypted
	// requests: compare the sent RQ_LOGIN payload and the RQ_PWD tail after the
	// server challenge (a suffix comparison, so it is proof-length agnostic) and
	// the sent frame-type sequence with the RLE list of the capture.
	optionsXML := "<autopos>true</autopos><micex_registers>true</micex_registers>" +
		"<utc_time>true</utc_time><push_u_limits>10</push_u_limits><push_pos_equity>10</push_pos_equity>"
	runs := []struct{ run, extra string }{
		{"default", ""},
		{"options", optionsXML},
		{"lang_en", "<language>en</language>"},
	}
	var traces [][]byte
	for _, r := range runs {
		srv := newTestServer(t)
		clientInfo := ci
		clientInfo.Trailer = 0 // the language comes from <connect> below
		c := New(Config{Client: clientInfo, Dial: srv.Dial})
		rec := newRecorder()
		if err := c.Start(rec.handle); err != nil {
			t.Fatal(err)
		}
		connect := strings.Replace(connectCommand(), "</rqdelay>", "</rqdelay>"+r.extra, 1)
		send(t, c, connect)
		rec.next(t, "<server_status")
		// The capture includes the harness get_servtime_difference command: it
		// adds one RQ_TIME (50) after the load.
		send(t, c, "<command id="+quote+"get_servtime_difference"+quote+"/>")
		var types, login, pwd []byte
		for _, f := range srv.Requests() {
			types = append(types, f.Type)
			switch f.Type {
			case session.RqLogin:
				login = f.Payload
			case session.RqPwd:
				pwd = f.Payload
			}
		}
		c.Close()
		if got, want := hex.EncodeToString(login), fx["rq_login"]; got != want {
			t.Fatalf("%s: sent RQ_LOGIN = %s, want %s", r.run, got, want)
		}
		wantTail, err := hex.DecodeString(fx[r.run+".rq_pwd_tail"])
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasSuffix(pwd, wantTail) {
			t.Fatalf("%s: sent RQ_PWD payload does not end with the captured tail", r.run)
		}
		if want := fixtureTrailer(t, fx, r.run); len(pwd) == 0 || pwd[len(pwd)-1] != want {
			t.Fatalf("%s: sent RQ_PWD trailer = %x, want %d", r.run, pwd, want)
		}
		got := normalizeFrameTypes(types)
		want := normalizeFrameTypes(expandRLE(t, fx[r.run+".c2s_types"]))
		if len(got) != len(want) {
			t.Fatalf("%s: sent frame types = %v, capture = %v", r.run, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: sent frame types = %v, capture = %v", r.run, got, want)
			}
		}
		traces = append(traces, got)
	}
	if !bytes.Equal(traces[0], traces[1]) {
		t.Fatalf("default types %v, options types %v: <connect> options changed the wire", traces[0], traces[1])
	}
	if !bytes.Equal(traces[0], traces[2]) {
		t.Fatalf("default types %v, lang_en types %v: language changed the frame set", traces[0], traces[2])
	}
}

// expandRLE expands a fixture "NxM" type list (NxM = type N repeated M times).
func expandRLE(t *testing.T, spec string) []byte {
	t.Helper()
	var out []byte
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		typText, countText := part, "1"
		if i := strings.IndexByte(part, 'x'); i >= 0 {
			typText, countText = part[:i], part[i+1:]
		}
		typ, err := strconv.Atoi(typText)
		if err != nil {
			t.Fatalf("fixture type %q: %v", part, err)
		}
		count, err := strconv.Atoi(countText)
		if err != nil {
			t.Fatalf("fixture count %q: %v", part, err)
		}
		for i := 0; i < count; i++ {
			out = append(out, byte(typ))
		}
	}
	return out
}

// normalizeFrameTypes projects a frame-type trace onto the part both the DLL
// capture and the Go connector must agree on: RQ_MAIN poll counts are timing
// dependent; RQ_CLIENT_HOLDINGS (42) is the DLL end-of-load request of task 7
// (not implemented here yet); RQ_LOGOUT (2) is teardown; repeated RQ_TIME frames
// (the capture has one extra during the load) are collapsed.
func normalizeFrameTypes(types []byte) []byte {
	out := make([]byte, 0, len(types))
	for _, typ := range types {
		switch typ {
		case session.RqMain, session.RqClientHoldings, session.RqLogout:
			continue
		case session.RqTime:
			if len(out) > 0 && out[len(out)-1] == session.RqTime {
				continue
			}
		}
		out = append(out, typ)
	}
	return out
}

// fixtureTrailer returns the RQ_PWD trailer recorded for the run.
func fixtureTrailer(t *testing.T, fx map[string]string, run string) byte {
	t.Helper()
	return byte(fixtureInt(t, fx, run+".rq_pwd_trailer"))
}

// parseHexOrDecimal parses a fixture number with an optional 0x prefix.
func parseHexOrDecimal(t *testing.T, value string) int64 {
	t.Helper()
	value = strings.TrimSpace(value)
	base := 10
	if strings.HasPrefix(value, "0x") {
		base, value = 16, value[2:]
	}
	n, err := strconv.ParseInt(value, base, 64)
	if err != nil {
		t.Fatalf("%q: %v", value, err)
	}
	return n
}

// TestServerTimeDifferenceLocalWall is the P2 regression of the wall-clock
// semantics: a server reporting exactly the local wall clock gives diff≈0 on any
// host time zone, while the old formula (server wall - time.Now().UTC()) added
// the host offset (e.g. -18000 s on UTC+5).
func TestServerTimeDifferenceLocalWall(t *testing.T) {
	srv := newTestServer(t)
	srv.Handle(session.RqTime, func([]byte) (byte, []byte) {
		var w wire.Writer
		w.Int32(int32(fakeUTCWall(time.Now()).Unix()))
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
	diff, err := c.ServerTimeDifference(ctx)
	if err != nil {
		t.Fatalf("ServerTimeDifference: %v", err)
	}
	if diff < -1*time.Second || diff > 1*time.Second {
		t.Fatalf("ServerTimeDifference = %v, want 0 ±1s for a server reporting the local wall clock", diff)
	}
}

// countNonMain counts the deterministic (non-RQ_MAIN) requests; Run polls
// RQ_MAIN in parallel, so its count is timing-dependent.
func countNonMain(requests []wire.Frame) int {
	n := 0
	for _, f := range requests {
		if f.Type != session.RqMain {
			n++
		}
	}
	return n
}

// countType counts the requests of one type.
func countType(requests []wire.Frame, typ byte) int {
	n := 0
	for _, f := range requests {
		if f.Type == typ {
			n++
		}
	}
	return n
}

// TestSessionTimeoutNormalization pins the DLL normalization of
// session_timeout (FUN_1803895c0): 0 means "use the server value"; below 20 s
// (and negative values) become 20 s; 20..299 s are seconds; 300 and above are
// treated as milliseconds.
func TestSessionTimeoutNormalization(t *testing.T) {
	cases := []struct {
		name     string
		override time.Duration
		want     time.Duration
	}{
		{"negative", -5 * time.Second, 20 * time.Second},
		{"below 20s", 5 * time.Second, 20 * time.Second},
		{"seconds", 30 * time.Second, 30 * time.Second},
		{"milliseconds", 300 * time.Second, 300 * time.Millisecond},
		{"more milliseconds", 1000 * time.Second, time.Second},
	}
	for _, c := range cases {
		if got := effectiveSessionTTL(c.override, nil); got != c.want {
			t.Fatalf("%s: effectiveSessionTTL(%v) = %v, want %v", c.name, c.override, got, c.want)
		}
	}
	servers := []struct {
		raw  int32
		want time.Duration
	}{
		{10, 20 * time.Second},      // below 0x14 -> 20 s
		{120, 120 * time.Second},    // seconds
		{120000, 120 * time.Second}, // milliseconds
	}
	for _, c := range servers {
		if got := effectiveSessionTTL(0, &session.Info{SessionTimeout: c.raw}); got != c.want {
			t.Fatalf("server session_timeout %d: effectiveSessionTTL = %v, want %v", c.raw, got, c.want)
		}
	}
}

// TestRequestTimeoutNormalization pins the DLL request_timeout rules: 20 s by
// default, and a value not below session_timeout becomes half the window.
func TestRequestTimeoutNormalization(t *testing.T) {
	cases := []struct {
		request, ttl, want time.Duration
	}{
		{0, 120 * time.Second, session.DefaultRequestTimeout},
		{5 * time.Second, 120 * time.Second, 5 * time.Second},
		{10 * time.Second, 300 * time.Millisecond, 150 * time.Millisecond},
		{120 * time.Second, 120 * time.Second, 60 * time.Second},
	}
	for _, c := range cases {
		if got := normalizeRequestTimeout(c.request, c.ttl); got != c.want {
			t.Fatalf("normalizeRequestTimeout(%v, %v) = %v, want %v", c.request, c.ttl, got, c.want)
		}
	}
	// The Client applies it after RQ_PWD: the 300 ms testserver window turns a
	// 10 s request timeout into 150 ms.
	srv := newTestServer(t)
	srv.SessionTimeout = 300
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Dial(ctx, ClientConfig{
		Address: "testserver:0", Login: testLogin, Password: testPassword, Dial: srv.Dial,
		RequestTimeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	if got := c.cfg.RequestTimeout; got != 150*time.Millisecond {
		t.Fatalf("Client RequestTimeout = %v, want 150ms (session_timeout/2)", got)
	}
}

// TestSessionTimeoutOverride checks that ClientConfig.SessionTimeout drives the
// recovery window with the same normalization.
func TestSessionTimeoutOverride(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	makeClient := func(t *testing.T, sessionTimeout time.Duration) *Client {
		t.Helper()
		srv := newTestServer(t)
		c, err := Dial(ctx, ClientConfig{
			Address: "testserver:0", Login: testLogin, Password: testPassword, Dial: srv.Dial,
			SessionTimeout: sessionTimeout,
		})
		if err != nil {
			t.Fatalf("Dial: %v", err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	if got := makeClient(t, 5*time.Second).sessionTimeout(); got != 20*time.Second {
		t.Fatalf("SessionTimeout 5s: window = %v, want 20s", got)
	}
	if got := makeClient(t, 30*time.Second).sessionTimeout(); got != 30*time.Second {
		t.Fatalf("SessionTimeout 30s: window = %v, want 30s", got)
	}
	if got := makeClient(t, 0).sessionTimeout(); got != 120*time.Second {
		t.Fatalf("server session_timeout: window = %v, want 120s", got)
	}
}

// TestRunRqDelayDefault checks that ClientConfig.RqDelay is the Run default and
// RunOptions overrides it (the DLL's 100 ms default and 10 ms minimum).
func TestRunRqDelayDefault(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	makeClient := func(t *testing.T, delay time.Duration) *Client {
		t.Helper()
		srv := newTestServer(t)
		c, err := Dial(ctx, ClientConfig{
			Address: "testserver:0", Login: testLogin, Password: testPassword, Dial: srv.Dial,
			RqDelay: delay,
		})
		if err != nil {
			t.Fatalf("Dial: %v", err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	if got := makeClient(t, 25*time.Millisecond).runRqDelay(RunOptions{}); got != 25*time.Millisecond {
		t.Fatalf("RqDelay default = %v, want 25ms", got)
	}
	if got := makeClient(t, 25*time.Millisecond).runRqDelay(RunOptions{RqDelay: 40 * time.Millisecond}); got != 40*time.Millisecond {
		t.Fatalf("RunOptions.RqDelay = %v, want 40ms", got)
	}
	if got := makeClient(t, 0).runRqDelay(RunOptions{}); got != 100*time.Millisecond {
		t.Fatalf("default RqDelay = %v, want 100ms", got)
	}
	if got := makeClient(t, 5*time.Millisecond).runRqDelay(RunOptions{}); got != 10*time.Millisecond {
		t.Fatalf("minimum RqDelay = %v, want 10ms", got)
	}
}
