package minimax

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// sign computes the `yy` request signature used by the MiniMaxi web API.
//
// The browser algorithm (module 51063, function r0) is:
//
//	u = {}                                  // body is only included for POST
//	if method == "post": u = body || {}
//	u = JSON.stringify(u)
//	if bodyToYY != "": u = bodyToYY         // override (unused for our calls)
//	l = encodeURIComponent(hasSearchParamsPath) + "_" + u + md5(time) + "ooui"
//	yy = md5(l)
//
// Two non-obvious details verified against the captured HAR:
//
//   - The timestamp is itself MD5-hashed (hex) before being concatenated; the
//     raw millisecond timestamp is NOT used directly.
//   - For the WebSocket and for GET requests the body is the literal "{}"
//     (method is not "post"), so the payload never participates in the
//     signature. Only POST requests include their JSON body.
//
// hasSearchParamsPath is the request path plus the full query string produced
// by url.Values.Encode() (which sorts keys alphabetically, matching the JS
// URLSearchParams.toString() behaviour for the common case). The original
// browser order is preserved by buildCommonParams for the common params, but
// because the signature is taken over the encoded string the exact key order
// of the *common* params does not affect the result as long as it matches what
// the server expects — the server re-derives it from the same algorithm.
func sign(hasSearchParamsPath string, method string, body []byte, nowMs int64) string {
	var u string
	if strings.EqualFold(method, "post") {
		if len(body) > 0 {
			u = string(body)
		} else {
			u = "{}"
		}
	} else {
		u = "{}"
	}
	timeHash := md5Hex(strconv.FormatInt(nowMs, 10))
	enc := encodeURIComponent(hasSearchParamsPath)
	l := enc + "_" + u + timeHash + "ooui"
	return md5Hex(l)
}

// md5Hex returns the lowercase hex MD5 of s (UTF-8 bytes).
func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// encodeURIComponent mirrors JavaScript's encodeURIComponent: it percent-encodes
// everything except A-Za-z0-9 and -_.!~*'(). Unlike url.QueryEscape it encodes
// space as %20 (not +) and does encode the reserved characters ?=&/# etc.
func encodeURIComponent(s string) string {
	var b strings.Builder
	b.Grow(len(s) * 3)
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-' || r == '_' || r == '.' || r == '!' || r == '~' || r == '*' || r == '\'' || r == '(' || r == ')':
			b.WriteRune(r)
		default:
			// Encode as UTF-8 bytes, each byte percent-encoded.
			for _, c := range []byte(string(r)) {
				b.WriteByte('%')
				const hexd = "0123456789ABCDEF"
				b.WriteByte(hexd[c>>4])
				b.WriteByte(hexd[c&0xF])
			}
		}
	}
	return b.String()
}

// queryParam is an ordered query parameter. Unlike url.Values (a map that
// re-sorts keys alphabetically on Encode), a slice preserves insertion order —
// which the MiniMaxi signature requires (see buildCommonParams).
type queryParam struct {
	Key   string
	Value string
}

// buildCommonParams returns the common query parameters every request carries,
// matching the browser's mC() builder. The values come from the client config
// (device id, uuid, language) and the current time.
//
// ORDER MATTERS: the captured browser traffic sends these parameters in a
// fixed insertion order, and the `yy` signature is computed over the raw query
// string. Verified against www.minimaxi.com2.har — 0/16 captured signatures
// match when the common params are sorted alphabetically (as url.Values.Encode
// would produce); 16/16 match in this insertion order. We therefore preserve
// the browser's order exactly rather than relying on a self-consistent sorted
// encoding.
func (c *Client) buildCommonParams(nowMs int64) []queryParam {
	p := []queryParam{
		{"device_platform", "web"},
		{"app_id", c.cfg.AppID},
		{"version_code", c.cfg.VersionCode},
		{"biz_id", c.cfg.BizID},
		{"uuid", c.cfg.UUID},
		{"lang", c.cfg.Lang},
	}
	// device_id sits between lang and os_name in the browser query, and is
	// omitted entirely (not sent empty) when the device has not registered yet.
	if c.cfg.DeviceID != "" {
		p = append(p, queryParam{"device_id", c.cfg.DeviceID})
	}
	p = append(p,
		queryParam{"os_name", c.cfg.OSName},
		queryParam{"browser_name", c.cfg.BrowserName},
	)
	if c.cfg.DeviceMemory > 0 {
		p = append(p, queryParam{"device_memory", strconv.Itoa(c.cfg.DeviceMemory)})
	}
	if c.cfg.CPUCoreNum > 0 {
		p = append(p, queryParam{"cpu_core_num", strconv.Itoa(c.cfg.CPUCoreNum)})
	}
	if c.cfg.BrowserLanguage != "" {
		p = append(p, queryParam{"browser_language", c.cfg.BrowserLanguage})
	}
	if c.cfg.BrowserPlatform != "" {
		p = append(p, queryParam{"browser_platform", c.cfg.BrowserPlatform})
	}
	if c.cfg.ScreenWidth > 0 {
		p = append(p, queryParam{"screen_width", strconv.Itoa(c.cfg.ScreenWidth)})
	}
	if c.cfg.ScreenHeight > 0 {
		p = append(p, queryParam{"screen_height", strconv.Itoa(c.cfg.ScreenHeight)})
	}
	p = append(p, queryParam{"unix", strconv.FormatInt(nowMs, 10)})
	return p
}

// encodeQuery serializes ordered params as key=value pairs joined by '&',
// matching the browser's URLSearchParams.toString() output (and Go's
// url.QueryEscape). The common/extra param values in this client are simple
// ASCII (alphanumerics, '-', '.', ':', '/'), so QueryEscape produces output
// identical to the browser for every value actually sent.
func encodeQuery(params []queryParam) string {
	var b strings.Builder
	for i, p := range params {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(url.QueryEscape(p.Key))
		b.WriteByte('=')
		b.WriteString(url.QueryEscape(p.Value))
	}
	return b.String()
}

// buildSearchParamsPath builds the "hasSearchParamsPath" value: the request
// path with the merged query string appended. extra params (if any) come before
// the common params, mirroring the browser interceptor which does
// `e.params = {...e.params, ...mC(time)}` (so the endpoint's own params keep
// their position and the common params are appended after, in browser order).
//
// The returned string is both what we sign AND what we send on the wire for
// REST (REST carries yy/token as headers, not query). The WS dialer appends
// yy/token/op_ticket to this base afterwards (see dialWS).
func (c *Client) buildSearchParamsPath(path string, extra []queryParam, nowMs int64) string {
	params := append(extra[:len(extra):len(extra)], c.buildCommonParams(nowMs)...)
	q := encodeQuery(params)
	if strings.Contains(path, "?") {
		return path + "&" + q
	}
	return path + "?" + q
}

// nowMs returns the current time in milliseconds.
func nowMs() int64 { return time.Now().UnixMilli() }

// compactJSON re-serialises v with no extra whitespace, matching JSON.stringify
// in JavaScript (which produces no spaces by default).
func compactJSON(v any) ([]byte, error) {
	return json.Marshal(v)
}
