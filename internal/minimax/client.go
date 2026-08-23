package minimax

import (
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

// Config holds the credentials and device fingerprint needed to impersonate
// the web client. Token is the only strictly required field; the device fields
// default to the captured browser values when zero/empty.
type Config struct {
	// Token is the JWT stored by the browser under localStorage "_token".
	// It authenticates every request. Obtain it from a logged-in browser
	// session (cookie/localStorage) — it is long-lived (exp ~ days).
	Token string `json:"token" yaml:"token"`

	// OpTicket is the optional op_ticket value (from localStorage
	// user_detail.op_ticket). Usually empty for the web client; sent as the
	// op_ticket query/header when non-empty.
	OpTicket string `json:"op_ticket" yaml:"op_ticket"`

	// Device fingerprint. These are not secret; they identify the device.
	UUID            string `json:"uuid" yaml:"uuid"`
	DeviceID        string `json:"device_id" yaml:"device_id"`
	Lang            string `json:"lang" yaml:"lang"`
	OSName          string `json:"os_name" yaml:"os_name"`
	BrowserName     string `json:"browser_name" yaml:"browser_name"`
	DeviceMemory    int    `json:"device_memory" yaml:"device_memory"`
	CPUCoreNum      int    `json:"cpu_core_num" yaml:"cpu_core_num"`
	BrowserLanguage string `json:"browser_language" yaml:"browser_language"`
	BrowserPlatform string `json:"browser_platform" yaml:"browser_platform"`
	ScreenWidth     int    `json:"screen_width" yaml:"screen_width"`
	ScreenHeight    int    `json:"screen_height" yaml:"screen_height"`

	// AppID/VersionCode/BizID default to the web constants; overridable for
	// forward-compatibility.
	AppID       string `json:"app_id" yaml:"app_id"`
	VersionCode string `json:"version_code" yaml:"version_code"`
	BizID       string `json:"biz_id" yaml:"biz_id"`

	// HTTPTimeout for REST calls. Defaults to 30s.
	HTTPTimeout time.Duration `json:"http_timeout" yaml:"http_timeout"`
	// WSTimeout is the overall deadline for a generation WebSocket session.
	// Defaults to 5m.
	WSTimeout time.Duration `json:"ws_timeout" yaml:"ws_timeout"`
}

// Client is a MiniMaxi web API client. The zero value is not usable; use New.
type Client struct {
	cfg      Config
	http     *http.Client // transport honouring the configured proxy
	baseURL  *url.URL
	wsDialer WSDialer
}

// New builds a Client. The http.Client is expected to already route through the
// configured proxy (see internal/proxy). If httpClient is nil a default client
// is used. The WebSocket dialer must be set separately via SetWSDialer when
// music generation is needed (so it can route through the same proxy).
func New(cfg Config, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	if cfg.HTTPTimeout > 0 {
		httpClient.Timeout = cfg.HTTPTimeout
	}
	cfg = withDefaults(cfg)
	base, _ := url.Parse(BaseURL)
	c := &Client{cfg: cfg, http: httpClient, baseURL: base}
	// Default WS dialer (no proxy). Override with SetWSDialer for proxy support.
	c.wsDialer = &websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
	}
	return c
}

// SetWSDialer installs a WebSocket dialer (typically one that routes through
// the configured proxy). Must be called before Generate. *websocket.Dialer
// satisfies WSDialer.
func (c *Client) SetWSDialer(d WSDialer) {
	if d != nil {
		c.wsDialer = d
	}
}

// Config returns a copy of the client configuration.
func (c *Client) Config() Config { return c.cfg }

func withDefaults(cfg Config) Config {
	if cfg.AppID == "" {
		cfg.AppID = AppID
	}
	if cfg.VersionCode == "" {
		cfg.VersionCode = VersionCode
	}
	if cfg.BizID == "" {
		cfg.BizID = BizID
	}
	if cfg.OSName == "" {
		cfg.OSName = OSName
	}
	if cfg.BrowserName == "" {
		cfg.BrowserName = BrowserName
	}
	if cfg.Lang == "" {
		cfg.Lang = "zh-Hans"
	}
	if cfg.BrowserLanguage == "" {
		cfg.BrowserLanguage = "en-US"
	}
	if cfg.BrowserPlatform == "" {
		cfg.BrowserPlatform = "Win32"
	}
	if cfg.DeviceMemory == 0 {
		cfg.DeviceMemory = 16
	}
	if cfg.CPUCoreNum == 0 {
		cfg.CPUCoreNum = 16
	}
	if cfg.ScreenWidth == 0 {
		cfg.ScreenWidth = 1707
	}
	if cfg.ScreenHeight == 0 {
		cfg.ScreenHeight = 1067
	}
	if cfg.HTTPTimeout == 0 {
		cfg.HTTPTimeout = 30 * time.Second
	}
	if cfg.WSTimeout == 0 {
		cfg.WSTimeout = 5 * time.Minute
	}
	return cfg
}
