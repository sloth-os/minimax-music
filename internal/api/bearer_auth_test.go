package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"minimax-music/internal/minimax"
)

// newPingServer returns a Server whose mux answers /api/ping with a 200 and
// /healthz with the real handler, with the given inbound auth key configured.
// It is used to exercise the bearer-auth middleware in isolation.
func newPingServer(t *testing.T, key string) *Server {
	t.Helper()
	s := &Server{mux: http.NewServeMux()}
	s.mux.HandleFunc("/api/ping", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
	})
	s.mux.HandleFunc("/healthz", s.handleHealth)
	s.SetAuthKey(key)
	return s
}

// TestBearerAuth_OpenMode verifies that with no key configured the service is
// open: requests without an Authorization header reach the handler.
func TestBearerAuth_OpenMode(t *testing.T) {
	srv := newPingServer(t, "")
	req := httptest.NewRequest(http.MethodGet, "/api/ping", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("open mode: status = %d, want 200", rec.Code)
	}
}

// TestBearerAuth_ProtectedEndpoint covers the 401 path and a valid token
// reaching the handler, including scheme case-insensitivity and whitespace.
func TestBearerAuth_ProtectedEndpoint(t *testing.T) {
	srv := newPingServer(t, "s3cr3t")

	cases := []struct {
		name  string
		auth  string
		want  int
		reach bool // whether the inner handler should be reached (200 body)
	}{
		{"no header", "", http.StatusUnauthorized, false},
		{"wrong key", "Bearer nope", http.StatusUnauthorized, false},
		{"key prefix longer", "Bearer s3cr3t-extra", http.StatusUnauthorized, false},
		{"key prefix shorter", "Bearer s3cr", http.StatusUnauthorized, false},
		{"bare Bearer no token", "Bearer ", http.StatusUnauthorized, false},
		{"wrong scheme", "Basic s3cr3t", http.StatusUnauthorized, false},
		{"correct key", "Bearer s3cr3t", http.StatusOK, true},
		{"lowercase scheme", "bearer s3cr3t", http.StatusOK, true},
		{"extra whitespace", "Bearer   s3cr3t", http.StatusOK, true},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/ping", nil)
		if c.auth != "" {
			req.Header.Set("Authorization", c.auth)
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)

		if rec.Code != c.want {
			t.Errorf("%s: status = %d, want %d", c.name, rec.Code, c.want)
		}
		if c.want == http.StatusUnauthorized {
			if got := rec.Header().Get("WWW-Authenticate"); got == "" {
				t.Errorf("%s: missing WWW-Authenticate header", c.name)
			}
		}
		if c.reach {
			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("%s: unmarshal: %v; body=%s", c.name, err, rec.Body.String())
			}
			if body["ok"] != "true" {
				t.Errorf("%s: handler not reached; body=%s", c.name, rec.Body.String())
			}
		}
	}
}

// TestBearerAuth_HealthzAlwaysOpen verifies /healthz stays reachable without
// credentials even when a key is configured (liveness probes).
func TestBearerAuth_HealthzAlwaysOpen(t *testing.T) {
	srv := newPingServer(t, "s3cr3t")
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz with key set: status = %d, want 200", rec.Code)
	}
}

// TestBearerAuth_MusicGenerationSpec verifies that /v1/music_generation answers
// auth failures per the official schema (HTTP 200 + base_resp.status_code=1004)
// rather than a transport-level 401, while still enforcing the configured key.
func TestBearerAuth_MusicGenerationSpec(t *testing.T) {
	audio := []byte("ID3\x04audio-bytes")
	gen := &fakeGenerator{result: &minimax.GenerateResult{
		TraceID:   "trace-auth",
		AudioData: audio,
		Items: []minimax.MusicItem{{
			MusicID: "m1", AudioURL: "https://cdn/x.mp3", Duration: 1000, Status: 2,
		}},
	}}
	srv := newTestServer(t, gen)
	srv.SetAuthKey("s3cr3t")
	h := srv.Handler()

	// Missing header => 200 / 1004 (spec contract), not 401.
	req := httptest.NewRequest(http.MethodPost, "/v1/music_generation",
		strings.NewReader(`{"model":"music-3.0","lyrics":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("missing auth: status = %d, want 200", rec.Code)
	}
	if md := decodeBaseResp(t, rec.Body.Bytes()); md.StatusCode != StatusAuthFailed {
		t.Fatalf("missing auth: status_code = %d, want 1004", md.StatusCode)
	}

	// Wrong key => 200 / 1004.
	req = httptest.NewRequest(http.MethodPost, "/v1/music_generation",
		strings.NewReader(`{"model":"music-3.0","lyrics":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer wrong")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("wrong key: status = %d, want 200", rec.Code)
	}
	if md := decodeBaseResp(t, rec.Body.Bytes()); md.StatusCode != StatusAuthFailed {
		t.Fatalf("wrong key: status_code = %d, want 1004", md.StatusCode)
	}

	// Correct key => reaches the generator (status_code 0 success).
	req = httptest.NewRequest(http.MethodPost, "/v1/music_generation",
		strings.NewReader(`{"model":"music-3.0","lyrics":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer s3cr3t")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("correct key: status = %d, want 200", rec.Code)
	}
	if md := decodeBaseResp(t, rec.Body.Bytes()); md.StatusCode != StatusSuccess {
		t.Fatalf("correct key: status_code = %d, want 0; body=%s", md.StatusCode, rec.Body.String())
	}
}

// decodeBaseResp pulls just base_resp out of a GenerateMusicResp body.
func decodeBaseResp(t *testing.T, b []byte) BaseResp {
	t.Helper()
	var resp GenerateMusicResp
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatalf("unmarshal base_resp: %v; body=%s", err, b)
	}
	if resp.BaseResp == nil {
		t.Fatalf("no base_resp in body: %s", b)
	}
	return *resp.BaseResp
}
