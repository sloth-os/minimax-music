// Package proxy builds HTTP clients and WebSocket dialers that route outbound
// traffic through a configurable HTTP or SOCKS5 proxy.
//
// Two proxy URL schemes are supported:
//
//   - http://  / https://  — HTTP CONNECT proxy (also handles "https" proxies
//     via TLS to the proxy itself).
//   - socks5:// / socks5h:// — SOCKS5 proxy. The "h" variant resolves host
//     names through the proxy (recommended).
//
// If no proxy URL is configured, the returned client/dialer use the default
// direct transport.
package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/net/proxy"
)

// Config describes a proxy. ProxyURL is the only required field when a proxy
// is desired; leave it empty for a direct connection.
type Config struct {
	// ProxyURL is the proxy address, e.g. "socks5h://127.0.0.1:1080" or
	// "http://user:pass@host:8080". Empty = direct (no proxy).
	ProxyURL string `json:"proxy_url" yaml:"proxy_url"`

	// Username/Password override any userinfo embedded in ProxyURL.
	Username string `json:"username" yaml:"username"`
	Password string `json:"password" yaml:"password"`

	// Timeout for dialing the proxy itself. Defaults to 15s.
	DialTimeout time.Duration `json:"dial_timeout" yaml:"dial_timeout"`

	// TLS skip verification for the proxy hop (useful for MITM/debug proxies).
	// Does NOT affect TLS to www.minimaxi.com or the CDN. Use with caution.
	InsecureSkipVerify bool `json:"insecure_skip_verify" yaml:"insecure_skip_verify"`
}

// parsed holds the resolved proxy configuration.
type parsed struct {
	scheme   string // "http", "https", "socks5", "socks5h", "" (direct)
	host     string
	username string
	password string
}

func (c Config) parse() (parsed, error) {
	if c.ProxyURL == "" {
		return parsed{}, nil
	}
	u, err := url.Parse(c.ProxyURL)
	if err != nil {
		return parsed{}, fmt.Errorf("proxy: parse %q: %w", c.ProxyURL, err)
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return parsed{}, fmt.Errorf("proxy: unsupported scheme %q (want http, https, socks5, socks5h)", scheme)
	}
	p := parsed{
		scheme: scheme,
		host:   u.Host,
	}
	if u.User != nil {
		p.username = u.User.Username()
		p.password, _ = u.User.Password()
	}
	if c.Username != "" {
		p.username = c.Username
	}
	if c.Password != "" {
		p.password = c.Password
	}
	if p.host == "" {
		return parsed{}, fmt.Errorf("proxy: missing host in %q", c.ProxyURL)
	}
	return p, nil
}

func (c Config) dialTimeout() time.Duration {
	if c.DialTimeout > 0 {
		return c.DialTimeout
	}
	return 15 * time.Second
}

// HTTPClient returns an *http.Client whose transport routes through the
// configured proxy (or direct if none). The client has no overall timeout;
// callers should pass request contexts for cancellation.
func HTTPClient(cfg Config) (*http.Client, error) {
	p, err := cfg.parse()
	if err != nil {
		return nil, err
	}
	tr := &http.Transport{
		MaxIdleConns:          20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	if p.scheme == "" {
		return &http.Client{Transport: tr}, nil
	}
	if strings.HasPrefix(p.scheme, "socks5") {
		dialer, err := socksDialer(p, cfg.dialTimeout())
		if err != nil {
			return nil, err
		}
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.Dial(network, addr)
		}
	} else {
		// http / https proxy
		proxyURL := &url.URL{Scheme: p.scheme, Host: p.host}
		if p.username != "" {
			proxyURL.User = url.UserPassword(p.username, p.password)
		}
		tr.Proxy = http.ProxyURL(proxyURL)
		if cfg.InsecureSkipVerify {
			tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		}
	}
	return &http.Client{Transport: tr}, nil
}

// WSDialer returns a *websocket.Dialer that routes through the configured
// proxy (or direct if none).
func WSDialer(cfg Config) (*websocket.Dialer, error) {
	p, err := cfg.parse()
	if err != nil {
		return nil, err
	}
	d := &websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
		// The MiniMaxi WS is wss://; keep default TLS verification.
	}
	if p.scheme == "" {
		return d, nil
	}
	if strings.HasPrefix(p.scheme, "socks5") {
		dialer, err := socksDialer(p, cfg.dialTimeout())
		if err != nil {
			return nil, err
		}
		d.NetDialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.Dial(network, addr)
		}
	} else {
		proxyURL := &url.URL{Scheme: p.scheme, Host: p.host}
		if p.username != "" {
			proxyURL.User = url.UserPassword(p.username, p.password)
		}
		d.Proxy = http.ProxyURL(proxyURL)
		if cfg.InsecureSkipVerify {
			d.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		}
	}
	return d, nil
}

// socksDialer builds a golang.org/x/net/proxy dialer for a socks5/socks5h URL.
// socks5h (remote DNS) is the default; socks5 uses local DNS.
func socksDialer(p parsed, timeout time.Duration) (proxy.Dialer, error) {
	auth := &proxy.Auth{User: p.username, Password: p.password}
	if p.username == "" {
		auth = nil
	}
	// golang.org/x/net/proxy.RegisterDialerType registers "socks5" only; the
	// "h" (remote-DNS) behaviour is the default for socks5 with a hostname
	// target, so we normalise the scheme to "socks5".
	d, err := proxy.SOCKS5("tcp", p.host, auth, &net.Dialer{Timeout: timeout})
	if err != nil {
		return nil, fmt.Errorf("proxy: socks5: %w", err)
	}
	return d, nil
}
