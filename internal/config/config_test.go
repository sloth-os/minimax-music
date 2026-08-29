package config

import (
	"strings"
	"testing"

	"minimax-music/internal/minimax"
)

// TestAuth_EnvOverride verifies MINIMAX_AUTH_API_KEY populates Auth.APIKey.
func TestAuth_EnvOverride(t *testing.T) {
	const key = "test-bearer-key-123"
	t.Setenv("MINIMAX_AUTH_API_KEY", key)

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Auth.APIKey != key {
		t.Fatalf("Auth.APIKey = %q, want %q", cfg.Auth.APIKey, key)
	}
}

// TestConfig_String_MasksAuth verifies the redacted summary never echoes the
// API key (or the minimax token), only whether one is set.
func TestConfig_String_MasksAuth(t *testing.T) {
	const secret = "super-secret-value-xyz"
	cfg := &Config{
		Addr:    ":8080",
		Auth:    Auth{APIKey: secret},
		Minimax: minimax.Config{Token: secret},
	}
	s := cfg.String()
	if strings.Contains(s, secret) {
		t.Fatalf("String() leaked the secret: %q", s)
	}
	if !strings.Contains(s, "auth=<set>") {
		t.Fatalf("String() does not show auth=<set>: %q", s)
	}
	if !strings.Contains(s, "token=<set>") {
		t.Fatalf("String() does not show token=<set>: %q", s)
	}

	// Open mode reports auth=open.
	cfg.Auth.APIKey = ""
	if s2 := cfg.String(); !strings.Contains(s2, "auth=open") {
		t.Fatalf("open mode String() = %q, want auth=open", s2)
	}
}
