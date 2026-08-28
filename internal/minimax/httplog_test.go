package minimax

import (
	"net/http"
	"strings"
	"testing"
)

// TestRedactURLQuery verifies the token= and op_ticket= query params are
// blanked while the rest of the query (including the non-secret yy signature
// and the browser's insertion order) is preserved verbatim.
func TestRedactURLQuery(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"no query",
			"https://www.minimaxi.com/v1/music/model_info",
			"https://www.minimaxi.com/v1/music/model_info",
		},
		{
			"token redacted, yy preserved",
			"https://www.minimaxi.com/ws?app_id=3001&yy=abc123&token=SECRET&op_ticket=",
			"https://www.minimaxi.com/ws?app_id=3001&yy=abc123&token=<redacted>&op_ticket=<redacted>",
		},
		{
			"token in the middle",
			"https://www.minimaxi.com/ws?a=1&token=SECRET&z=9",
			"https://www.minimaxi.com/ws?a=1&token=<redacted>&z=9",
		},
		{
			"no sensitive params",
			"https://www.minimaxi.com/v1/x?app_id=3001&yy=abc",
			"https://www.minimaxi.com/v1/x?app_id=3001&yy=abc",
		},
		{
			"token only at end",
			"https://www.minimaxi.com/ws?yy=abc&token=SECRET",
			"https://www.minimaxi.com/ws?yy=abc&token=<redacted>",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := redactURLQuery(c.in)
			if got != c.want {
				t.Fatalf("redactURLQuery(%q)\n  got  %q\n  want %q", c.in, got, c.want)
			}
			// The raw secret token must never survive redaction.
			if strings.Contains(c.in, "SECRET") && strings.Contains(got, "SECRET") {
				t.Fatalf("secret leaked into logged URL: %q", got)
			}
		})
	}
}

// TestCurlString_RedactsSensitiveHeaders verifies the curl preview redacts the
// Token header (a JWT) while leaving the per-request yy signature visible.
func TestCurlString_RedactsSensitiveHeaders(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://www.minimaxi.com/v1/x?app_id=3001", nil)
	req.Header.Set("Token", "eyJ-secret-jwt")
	req.Header.Set("Yy", "5e0b4c302c8274f8")
	req.Header.Set("User-Agent", "edge")
	got := curlString(req, nil)
	if strings.Contains(got, "eyJ-secret-jwt") {
		t.Fatalf("Token header leaked into curl preview: %s", got)
	}
	if !strings.Contains(got, "Token: <redacted>") {
		t.Fatalf("Token not redacted in curl preview: %s", got)
	}
	if !strings.Contains(got, "Yy: 5e0b4c302c8274f8") {
		t.Fatalf("yy signature should NOT be redacted (not secret): %s", got)
	}
}

// TestCurlString_IncludesBody verifies the request body is included in the curl
// preview and truncated when it exceeds the cap.
func TestCurlString_IncludesBody(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "https://www.minimaxi.com/v1/x", nil)
	big := []byte(strings.Repeat("a", maxLogBody+100))
	got := curlString(req, big)
	if !strings.Contains(got, "--data '") {
		t.Fatalf("curl preview missing --data: %s", got)
	}
	if strings.Contains(got, strings.Repeat("a", maxLogBody+1)) {
		t.Fatalf("body preview not truncated to %d bytes", maxLogBody)
	}
}
