package api

// Guard tests: the loopback API must reject cross-origin and DNS-rebinding
// requests before a handler runs and require the per-launch token.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// guardTestHost is the loopback Host of a direct API request.
const guardTestHost = "127.0.0.1:8090"

// guardTestToken is a dummy token for the tests: real tokens come from the
// environment and never reach the repository.
const guardTestToken = "guard-test-token"

// serveGuard runs one request through Guard and reports the recorder plus
// whether the wrapped handler was reached.
func serveGuard(t *testing.T, cfg GuardConfig, req *http.Request) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	Guard(cfg, next).ServeHTTP(rec, req)
	return rec, called
}

// newGuardRequest builds a request against the direct local API host.
func newGuardRequest(t *testing.T, method, target, body string) *http.Request {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Host = guardTestHost
	return req
}

// assertGuardStatus checks the guard response: an allowed request reaches the
// next handler, a rejected one answers with the JSON error envelope.
func assertGuardStatus(t *testing.T, rec *httptest.ResponseRecorder, called bool, wantStatus int, wantCode string) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, wantStatus, rec.Body.String())
	}
	if wantStatus == http.StatusOK {
		if !called {
			t.Fatal("guard rejected an allowed request")
		}
		return
	}
	if called {
		t.Fatalf("guard let a rejected request through (status %d)", wantStatus)
	}
	if got := errorCode(t, rec.Body.Bytes()); got != wantCode {
		t.Fatalf("error code = %q, want %q", got, wantCode)
	}
}

// TestGuardRejectsForeignOrigin checks that an unsafe cross-site request is
// rejected with 403 forbidden_origin (CSRF).
func TestGuardRejectsForeignOrigin(t *testing.T) {
	req := newGuardRequest(t, http.MethodPost, "/api/orders", `{"symbol":"TQBR:SBER","side":"buy","qty":1}`)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	rec, called := serveGuard(t, GuardConfig{}, req)
	assertGuardStatus(t, rec, called, http.StatusForbidden, "forbidden_origin")
}

// TestGuardRejectsTextPlainPost checks that a body with a non-JSON content
// type is rejected with 415: text/plain is a CORS "simple request" and must
// not reach handlers (CSRF).
func TestGuardRejectsTextPlainPost(t *testing.T) {
	req := newGuardRequest(t, http.MethodPost, "/api/orders", `{"symbol":"TQBR:SBER","side":"buy","qty":1}`)
	req.Header.Set("Content-Type", "text/plain")
	rec, called := serveGuard(t, GuardConfig{}, req)
	assertGuardStatus(t, rec, called, http.StatusUnsupportedMediaType, "unsupported_media_type")
}

// TestGuardRejectsRebindHost checks that a DNS-rebinding request (a foreign
// Host header pointing at 127.0.0.1) is rejected with 421.
func TestGuardRejectsRebindHost(t *testing.T) {
	req := newGuardRequest(t, http.MethodGet, "/api/health", "")
	req.Host = "attacker.example:8090"
	rec, called := serveGuard(t, GuardConfig{}, req)
	assertGuardStatus(t, rec, called, http.StatusMisdirectedRequest, "forbidden_host")
}

// TestGuardRequiresTokenWhenSet checks the per-launch token: every /api/*
// request except /api/health needs it when a token is configured.
func TestGuardRequiresTokenWhenSet(t *testing.T) {
	cfg := GuardConfig{Token: guardTestToken}

	req := newGuardRequest(t, http.MethodGet, "/api/instruments", "")
	rec, called := serveGuard(t, cfg, req)
	assertGuardStatus(t, rec, called, http.StatusUnauthorized, "unauthorized")

	// The health endpoint stays readable: the plugin probes it before it
	// knows whether the backend is its own.
	req = newGuardRequest(t, http.MethodGet, "/api/health", "")
	rec, called = serveGuard(t, cfg, req)
	assertGuardStatus(t, rec, called, http.StatusOK, "")

	// A wrong token is rejected like a missing one.
	req = newGuardRequest(t, http.MethodGet, "/api/instruments", "")
	req.Header.Set("X-Terminal-Token", "wrong-token")
	rec, called = serveGuard(t, cfg, req)
	assertGuardStatus(t, rec, called, http.StatusUnauthorized, "unauthorized")

	// The header token opens the API.
	req = newGuardRequest(t, http.MethodGet, "/api/instruments", "")
	req.Header.Set("X-Terminal-Token", guardTestToken)
	rec, called = serveGuard(t, cfg, req)
	assertGuardStatus(t, rec, called, http.StatusOK, "")
}

// TestGuardRequiresTokenForMCP checks that the MCP endpoint is guarded by the
// same token as /api/*: external MCP clients pass X-Terminal-Token.
func TestGuardRequiresTokenForMCP(t *testing.T) {
	cfg := GuardConfig{Token: guardTestToken}

	req := newGuardRequest(t, http.MethodPost, "/mcp", `{}`)
	req.Header.Set("Content-Type", "application/json")
	rec, called := serveGuard(t, cfg, req)
	assertGuardStatus(t, rec, called, http.StatusUnauthorized, "unauthorized")

	req = newGuardRequest(t, http.MethodPost, "/mcp", `{}`)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Terminal-Token", guardTestToken)
	rec, called = serveGuard(t, cfg, req)
	assertGuardStatus(t, rec, called, http.StatusOK, "")

	// The nested transport path is protected as well.
	req = newGuardRequest(t, http.MethodPost, "/mcp/messages", `{}`)
	req.Header.Set("Content-Type", "application/json")
	rec, called = serveGuard(t, cfg, req)
	assertGuardStatus(t, rec, called, http.StatusUnauthorized, "unauthorized")

	// GET /mcp is not a data endpoint but stays closed without the token too.
	req = newGuardRequest(t, http.MethodGet, "/mcp", "")
	rec, called = serveGuard(t, cfg, req)
	assertGuardStatus(t, rec, called, http.StatusUnauthorized, "unauthorized")
}

// TestGuardAllowsSameOriginGET checks that a direct loopback GET /api/health
// passes without a token.
func TestGuardAllowsSameOriginGET(t *testing.T) {
	req := newGuardRequest(t, http.MethodGet, "/api/health", "")
	rec, called := serveGuard(t, GuardConfig{}, req)
	assertGuardStatus(t, rec, called, http.StatusOK, "")
}

// TestGuardAllowsSameOriginPost checks that an unsafe request from the API's
// own origin (the direct SPA) passes the Origin check.
func TestGuardAllowsSameOriginPost(t *testing.T) {
	req := newGuardRequest(t, http.MethodPost, "/api/orders", `{}`)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://"+guardTestHost)
	rec, called := serveGuard(t, GuardConfig{}, req)
	assertGuardStatus(t, rec, called, http.StatusOK, "")
}

// TestGuardAllowsAllowedOrigin checks the -allowed-origins escape hatch used
// when the API is embedded behind another trusted origin (the DSH plugin).
func TestGuardAllowsAllowedOrigin(t *testing.T) {
	cfg := GuardConfig{AllowedOrigins: []string{"https://harness.example:52729"}}
	req := newGuardRequest(t, http.MethodPost, "/api/orders", `{}`)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://harness.example:52729")
	rec, called := serveGuard(t, cfg, req)
	assertGuardStatus(t, rec, called, http.StatusOK, "")
}

// TestGuardAllowsAllowedHost checks the AllowedHosts escape hatch for a
// reverse-proxy host name.
func TestGuardAllowsAllowedHost(t *testing.T) {
	cfg := GuardConfig{AllowedHosts: []string{"terminal.example:8090"}}
	req := newGuardRequest(t, http.MethodGet, "/api/health", "")
	req.Host = "terminal.example:8090"
	rec, called := serveGuard(t, cfg, req)
	assertGuardStatus(t, rec, called, http.StatusOK, "")
}

// TestGuardAllowsBodylessDelete checks the contract of DELETE /api/orders/{id}:
// without a body it passes without a Content-Type.
func TestGuardAllowsBodylessDelete(t *testing.T) {
	req := newGuardRequest(t, http.MethodDelete, "/api/orders/ord_000001", "")
	rec, called := serveGuard(t, GuardConfig{}, req)
	assertGuardStatus(t, rec, called, http.StatusOK, "")
}

// TestGuardAcceptsJSONWithCharset checks the usual browser content type.
func TestGuardAcceptsJSONWithCharset(t *testing.T) {
	req := newGuardRequest(t, http.MethodPost, "/api/orders", `{}`)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	rec, called := serveGuard(t, GuardConfig{}, req)
	assertGuardStatus(t, rec, called, http.StatusOK, "")
}

// TestGuardIgnoresOriginOnSafeMethods checks that GET requests are not subject
// to the Origin check: browsers do not send Origin for safe methods, and the
// token protects the read path.
func TestGuardIgnoresOriginOnSafeMethods(t *testing.T) {
	req := newGuardRequest(t, http.MethodGet, "/api/health", "")
	req.Header.Set("Origin", "https://evil.example")
	rec, called := serveGuard(t, GuardConfig{}, req)
	assertGuardStatus(t, rec, called, http.StatusOK, "")
}

// TestGuardAcceptsStrictCookieFromIndex checks the cookie flow: serving
// index.html sets an HttpOnly, SameSite=Strict terminal_token cookie, and a
// later API request with that cookie is authorized (the SPA and EventSource
// then work without JS changes).
func TestGuardAcceptsStrictCookieFromIndex(t *testing.T) {
	web := fstest.MapFS{
		"index.html": {Data: []byte("<html><body>AI Terminal</body></html>")},
	}
	deps, _, _, _ := defaultDeps()
	_, ts := newTestServer(t, deps, Config{Web: web, Guard: GuardConfig{Token: guardTestToken}})

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d", resp.StatusCode)
	}
	raw := resp.Header.Get("Set-Cookie")
	cookie, err := http.ParseSetCookie(raw)
	if err != nil {
		t.Fatalf("ParseSetCookie(%q): %v", raw, err)
	}
	if cookie.Name != "terminal_token" || cookie.Value != guardTestToken {
		t.Fatalf("cookie = %s=%q, want terminal_token=%q", cookie.Name, cookie.Value, guardTestToken)
	}
	if !cookie.HttpOnly {
		t.Fatal("terminal_token cookie must be HttpOnly")
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("terminal_token SameSite = %v, want Strict", cookie.SameSite)
	}
	if cookie.Path != "/" {
		t.Fatalf("terminal_token Path = %q, want /", cookie.Path)
	}

	// Authorized by the cookie alone.
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/orders", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Cookie", cookie.String())
	apiResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/orders with cookie: %v", err)
	}
	defer apiResp.Body.Close()
	if apiResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/orders with cookie = %d", apiResp.StatusCode)
	}

	// Without the cookie the API is closed (and no SSE stream starts).
	status, data := do(t, ts, http.MethodGet, "/api/orders", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("GET /api/orders without token = %d %s", status, data)
	}
	status, data = do(t, ts, http.MethodGet, "/api/events", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("GET /api/events without token = %d %s", status, data)
	}
}

// TestHandlerWrapsGuard checks that Server.Handler installs the guard: the
// token is required even though the mux itself knows nothing about it.
func TestHandlerWrapsGuard(t *testing.T) {
	deps, _, _, _ := defaultDeps()
	_, ts := newTestServer(t, deps, Config{Guard: GuardConfig{Token: guardTestToken}})

	status, data := do(t, ts, http.MethodGet, "/api/instruments", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("GET /api/instruments without token = %d %s", status, data)
	}

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/instruments", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("X-Terminal-Token", guardTestToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/instruments with token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/instruments with token = %d", resp.StatusCode)
	}
}

// TestHealthReportsInstanceID checks the instance nonce in /api/health: the
// plugin accepts a backend on the port only when this value matches the nonce
// it generated for the launch (R10: do not trust whatever listens on the port).
func TestHealthReportsInstanceID(t *testing.T) {
	deps, _, _, _ := defaultDeps()
	_, ts := newTestServer(t, deps, Config{InstanceID: "nonce-test-1"})
	status, data := do(t, ts, http.MethodGet, "/api/health", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/health = %d %s", status, data)
	}
	var health struct {
		OK         bool   "json:\"ok\""
		InstanceID string "json:\"instanceId\""
	}
	if err := json.Unmarshal(data, &health); err != nil {
		t.Fatalf("decode health: %v (%s)", err, data)
	}
	if !health.OK {
		t.Fatalf("health ok = false: %s", data)
	}
	if health.InstanceID != "nonce-test-1" {
		t.Fatalf("instanceId = %q, want nonce-test-1", health.InstanceID)
	}
}
