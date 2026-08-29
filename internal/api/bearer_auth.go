package api

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// bearerScheme is the required Authorization scheme prefix (RFC 7235: the
// scheme name is case-insensitive).
const bearerScheme = "Bearer "

// bearerTokenOK reports whether r carries an "Authorization: Bearer <token>"
// header whose token equals expectedKey. The comparison is constant-time.
// An absent or malformed header returns false. expectedKey must be non-empty
// (callers only invoke this when a key is configured).
func bearerTokenOK(r *http.Request, expectedKey string) bool {
	got := r.Header.Get("Authorization")
	if len(got) < len(bearerScheme) || !strings.EqualFold(got[:len(bearerScheme)], bearerScheme) {
		return false
	}
	token := strings.TrimSpace(got[len(bearerScheme):])
	// ConstantTimeCompare returns 0 for unequal lengths, so a shorter or longer
	// token is rejected without branching on its contents.
	return subtle.ConstantTimeCompare([]byte(token), []byte(expectedKey)) == 1
}

// bearerAuthMiddleware enforces that incoming requests carry an
// "Authorization: Bearer <api_key>" header matching the configured key.
//
// When the key is empty the middleware is a no-op: the service is open to any
// caller that can reach it (the historical default).
//
// /healthz is always allowed without auth so liveness probes are not gated on
// credentials. /v1/music_generation is also skipped here because that endpoint
// must answer auth failures with an HTTP 200 + base_resp.status_code=1004 per
// the official schema (see music_generation.go); it performs its own value
// check against the same configured key.
func bearerAuthMiddleware(key string, next http.Handler) http.Handler {
	if key == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz", "/v1/music_generation":
			next.ServeHTTP(w, r)
			return
		}
		if !bearerTokenOK(r, key) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("WWW-Authenticate", `Bearer realm="minimax-music"`)
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized: missing or invalid Authorization header"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}
