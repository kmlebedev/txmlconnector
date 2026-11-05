package api

// The local API is reachable from a browser on the same machine, so a web page
// of any other site could otherwise forge requests to it: CSRF on unsafe
// methods, DNS rebinding on Host, and "simple request" bodies that bypass the
// CORS preflight. Guard is the single choke point that rejects those requests
// before the mux sees them. It also enforces the per-launch token that the DSH
// plugin passes to the backend with TERMINAL_API_TOKEN.

import (
	"crypto/subtle"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// Header and cookie carrying the API token. The plugin injects the header into
// every proxied request; a browser that talks to the backend directly gets the
// cookie from index.html, so the SPA and EventSource work without any JS
// changes (a cross-site request never sends a SameSite=Strict cookie).
const (
	headerTerminalToken = "X-Terminal-Token"
	cookieTerminalToken = "terminal_token"
)

// Error codes of the guard (docs/ai-terminal/ARCHITECTURE.md §10).
const (
	codeForbiddenHost        = "forbidden_host"
	codeForbiddenOrigin      = "forbidden_origin"
	codeUnsupportedMediaType = "unsupported_media_type"
	codeUnauthorized         = "unauthorized"
)

// GuardConfig configures Guard.
type GuardConfig struct {
	// AllowedOrigins are extra origins accepted for unsafe methods
	// (-allowed-origins); the request's own origin is always accepted.
	AllowedOrigins []string
	// AllowedHosts are extra Host values accepted besides loopback hosts
	// (for a reverse proxy that forwards its own host name).
	AllowedHosts []string
	// Token, when non-empty, is required on every /api/* request except
	// /api/health (TERMINAL_API_TOKEN).
	Token string
}

// Guard wraps next with the request checks of the local API. Order of checks:
// Host, Origin (unsafe methods only), Content-Type of a body, API token.
func Guard(cfg GuardConfig, next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	allowedOrigins := normalizeAllowedOrigins(cfg.AllowedOrigins)
	allowedHosts := make(map[string]struct{}, len(cfg.AllowedHosts))
	for _, host := range cfg.AllowedHosts {
		if host = strings.ToLower(strings.TrimSpace(host)); host != "" {
			allowedHosts[host] = struct{}{}
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Host: only loopback names (plus explicitly allowed ones) may
		// reach the API. A DNS-rebinding page resolves its name to 127.0.0.1
		// but keeps sending its own Host, so it is rejected here with 421.
		if !guardHostAllowed(r.Host, allowedHosts) {
			writeError(w, http.StatusMisdirectedRequest, codeForbiddenHost,
				"недопустимый Host: локальный API принимает только loopback-имена")
			return
		}
		// 2. Origin: browsers send it on unsafe methods. Accept the API's own
		// origin and the configured allowlist, reject any other site (CSRF).
		if methodIsUnsafe(r.Method) {
			if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" &&
				!guardOriginAllowed(origin, r, allowedOrigins) {
				writeError(w, http.StatusForbidden, codeForbiddenOrigin,
					"межсайтовый запрос отклонён: недопустимый Origin")
				return
			}
		}
		// 3. Content-Type: a request body must be JSON. text/plain is a CORS
		// "simple request" that browsers send without a preflight; a bodyless
		// DELETE /api/orders/{id} passes without a Content-Type.
		if r.ContentLength != 0 && !isJSONContentType(r) {
			writeError(w, http.StatusUnsupportedMediaType, codeUnsupportedMediaType,
				"тело запроса должно быть application/json")
			return
		}
		// 4. Token: everything under /api/ except the health probe needs it.
		if cfg.Token != "" && guardRequiresToken(r.URL.Path) && !guardTokenValid(r, cfg.Token) {
			writeError(w, http.StatusUnauthorized, codeUnauthorized,
				"не указан или неверен токен API (заголовок X-Terminal-Token или cookie terminal_token)")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// setTerminalTokenCookie hands the directly opened SPA its API token. The
// cookie is HttpOnly and SameSite=Strict, so scripts cannot read it and
// cross-site requests do not carry it.
func setTerminalTokenCookie(w http.ResponseWriter, token string) {
	if token == "" {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieTerminalToken,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

// methodIsUnsafe reports whether the method may mutate state; only GET and
// HEAD are exempt from the Origin check.
func methodIsUnsafe(method string) bool {
	return method != http.MethodGet && method != http.MethodHead
}

// guardRequiresToken reports whether the path is protected by the API token.
// /api/health stays readable: the plugin probes it before it knows whether the
// backend on the port is its own.
func guardRequiresToken(path string) bool {
	if path == "/api/health" {
		return false
	}
	return path == "/api" || strings.HasPrefix(path, "/api/")
}

// guardHostAllowed reports whether the Host header names a loopback host or an
// explicitly allowed one. The entry may be a host:port pair or a bare host.
func guardHostAllowed(host string, allowed map[string]struct{}) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	if _, ok := allowed[host]; ok {
		return true
	}
	name := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		name = h
	}
	if _, ok := allowed[name]; ok {
		return true
	}
	name = strings.Trim(name, "[]")
	if name == "localhost" {
		return true
	}
	if ip := net.ParseIP(name); ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}

// guardOriginAllowed reports whether origin is the request's own origin or an
// explicitly allowed one.
func guardOriginAllowed(origin string, r *http.Request, allowed map[string]struct{}) bool {
	origin = strings.TrimRight(strings.TrimSpace(origin), "/")
	if _, ok := allowed[strings.ToLower(origin)]; ok {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return strings.EqualFold(u.Scheme, scheme) && strings.EqualFold(u.Host, r.Host)
}

// normalizeAllowedOrigins makes the allowlist case-insensitive and ignores
// trailing slashes.
func normalizeAllowedOrigins(origins []string) map[string]struct{} {
	allowed := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		origin = strings.ToLower(strings.TrimRight(strings.TrimSpace(origin), "/"))
		if origin == "" {
			continue
		}
		allowed[origin] = struct{}{}
	}
	return allowed
}

// isJSONContentType reports whether the request body is declared as JSON.
// Parameters such as charset=utf-8 are ignored.
func isJSONContentType(r *http.Request) bool {
	contentType := r.Header.Get("Content-Type")
	if i := strings.IndexByte(contentType, ';'); i >= 0 {
		contentType = contentType[:i]
	}
	return strings.EqualFold(strings.TrimSpace(contentType), "application/json")
}

// guardTokenValid compares the presented token with the configured one in
// constant time. A correct header or a correct cookie is enough.
func guardTokenValid(r *http.Request, token string) bool {
	if header := r.Header.Get(headerTerminalToken); header != "" && tokenEqual(header, token) {
		return true
	}
	if cookie, err := r.Cookie(cookieTerminalToken); err == nil && tokenEqual(cookie.Value, token) {
		return true
	}
	return false
}

// tokenEqual is a constant-time string comparison of two tokens.
func tokenEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
