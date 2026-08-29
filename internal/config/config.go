// Package config loads service configuration from a YAML file and/or
// environment variables. Environment variables override file values.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"minimax-music/internal/minimax"
	"minimax-music/internal/proxy"
)

// Config is the top-level service configuration.
type Config struct {
	// Addr is the HTTP listen address. Default ":8080".
	Addr string `json:"addr" yaml:"addr"`

	// Auth holds inbound request authentication. When Auth.APIKey is set, every
	// received request (except /healthz) must carry
	// "Authorization: Bearer <api_key>". Empty = open (no auth).
	Auth Auth `json:"auth" yaml:"auth"`

	// Minimax holds the MiniMaxi client credentials/fingerprint.
	Minimax minimax.Config `json:"minimax" yaml:"minimax"`

	// Proxy holds the outbound proxy configuration. Empty = direct.
	Proxy proxy.Config `json:"proxy" yaml:"proxy"`
}

// Auth holds inbound request authentication.
type Auth struct {
	// APIKey is the shared secret callers must present as
	// "Authorization: Bearer <api_key>". When empty, inbound auth is disabled
	// (the service is open to anyone who can reach it).
	APIKey string `json:"api_key" yaml:"api_key"`
}

// Load reads the YAML file at path (if non-empty and present) and then applies
// environment-variable overrides. Env vars use the prefix MINIMAX_, e.g.:
//
//	MINIMAX_ADDR
//	MINIMAX_AUTH_API_KEY
//	MINIMAX_TOKEN
//	MINIMAX_UUID
//	MINIMAX_DEVICE_ID
//	MINIMAX_PROXY_URL
//	MINIMAX_PROXY_USERNAME
//	MINIMAX_PROXY_PASSWORD
func Load(path string) (*Config, error) {
	cfg := &Config{Addr: ":8080"}
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("config: read %s: %w", path, err)
			}
			// missing file is fine; fall through to env
		} else if len(b) > 0 {
			if err := yaml.Unmarshal(b, cfg); err != nil {
				return nil, fmt.Errorf("config: parse %s: %w", path, err)
			}
		}
	}
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}
	applyEnv(cfg)
	return cfg, nil
}

// applyEnv overlays environment variables on top of the loaded config.
func applyEnv(cfg *Config) {
	if v := os.Getenv("MINIMAX_ADDR"); v != "" {
		cfg.Addr = v
	}
	if v := os.Getenv("MINIMAX_AUTH_API_KEY"); v != "" {
		cfg.Auth.APIKey = v
	}
	if v := os.Getenv("MINIMAX_TOKEN"); v != "" {
		cfg.Minimax.Token = v
	}
	if v := os.Getenv("MINIMAX_OP_TICKET"); v != "" {
		cfg.Minimax.OpTicket = v
	}
	if v := os.Getenv("MINIMAX_UUID"); v != "" {
		cfg.Minimax.UUID = v
	}
	if v := os.Getenv("MINIMAX_DEVICE_ID"); v != "" {
		cfg.Minimax.DeviceID = v
	}
	if v := os.Getenv("MINIMAX_LANG"); v != "" {
		cfg.Minimax.Lang = v
	}
	if v := os.Getenv("MINIMAX_HTTP_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Minimax.HTTPTimeout = d
		}
	}
	if v := os.Getenv("MINIMAX_WS_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Minimax.WSTimeout = d
		}
	}
	if v := os.Getenv("MINIMAX_PROXY_URL"); v != "" {
		cfg.Proxy.ProxyURL = v
	}
	if v := os.Getenv("MINIMAX_PROXY_USERNAME"); v != "" {
		cfg.Proxy.Username = v
	}
	if v := os.Getenv("MINIMAX_PROXY_PASSWORD"); v != "" {
		cfg.Proxy.Password = v
	}
	if v := os.Getenv("MINIMAX_PROXY_INSECURE"); v != "" {
		cfg.Proxy.InsecureSkipVerify, _ = strconv.ParseBool(v)
	}
}

// String renders a redacted summary for logging.
func (c *Config) String() string {
	var b strings.Builder
	b.WriteString("addr=" + c.Addr)
	if c.Auth.APIKey != "" {
		b.WriteString(" auth=<set>")
	} else {
		b.WriteString(" auth=open")
	}
	if c.Minimax.Token != "" {
		b.WriteString(" token=<set>")
	} else {
		b.WriteString(" token=<empty>")
	}
	if c.Minimax.UUID != "" {
		b.WriteString(" uuid=" + c.Minimax.UUID)
	}
	if c.Proxy.ProxyURL != "" {
		b.WriteString(" proxy=" + c.Proxy.ProxyURL)
	} else {
		b.WriteString(" proxy=direct")
	}
	return b.String()
}
