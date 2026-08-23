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

// buildCommonParams returns the common query parameters every request carries,
// matching the browser's mC() builder. The values come from the client config
// (device id, uuid, language) and the current time.
func (c *Client) buildCommonParams(nowMs int64) url.Values {
	v := url.Values{}
	v.Set("device_platform", "web")
	v.Set("app_id", c.cfg.AppID)
	v.Set("version_code", c.cfg.VersionCode)
	v.Set("biz_id", c.cfg.BizID)
	v.Set("uuid", c.cfg.UUID)
	v.Set("lang", c.cfg.Lang)
	if c.cfg.DeviceID != "" {
		v.Set("device_id", c.cfg.DeviceID)
	}
	v.Set("os_name", c.cfg.OSName)
	v.Set("browser_name", c.cfg.BrowserName)
	if c.cfg.DeviceMemory > 0 {
		v.Set("device_memory", strconv.Itoa(c.cfg.DeviceMemory))
	}
	if c.cfg.CPUCoreNum > 0 {
		v.Set("cpu_core_num", strconv.Itoa(c.cfg.CPUCoreNum))
	}
	if c.cfg.BrowserLanguage != "" {
		v.Set("browser_language", c.cfg.BrowserLanguage)
	}
	if c.cfg.BrowserPlatform != "" {
		v.Set("browser_platform", c.cfg.BrowserPlatform)
	}
	if c.cfg.ScreenWidth > 0 {
		v.Set("screen_width", strconv.Itoa(c.cfg.ScreenWidth))
	}
	if c.cfg.ScreenHeight > 0 {
		v.Set("screen_height", strconv.Itoa(c.cfg.ScreenHeight))
	}
	v.Set("unix", strconv.FormatInt(nowMs, 10))
	return v
}

// buildSearchParamsPath builds the "hasSearchParamsPath" value: the request
// path with the merged query string appended. extra params (if any) are set
// before the common params, mirroring the browser interceptor which does
// e.params = {...e.params, ...mC(time)}.
//
// Note: url.Values.Encode() sorts keys alphabetically. The captured browser
// traffic preserves insertion order, but the server validates the signature by
// recomputing it from the *same* encoded query string it received — so as long
// as we sign exactly the query string we send, the order is internally
// consistent. We therefore sign the canonical (sorted) encoding and send that
// same encoding on the wire.
func (c *Client) buildSearchParamsPath(path string, extra url.Values, nowMs int64) string {
	merged := url.Values{}
	for k, vs := range extra {
		for _, v := range vs {
			merged.Add(k, v)
		}
	}
	for k, vs := range c.buildCommonParams(nowMs) {
		for _, v := range vs {
			merged.Add(k, v)
		}
	}
	q := merged.Encode()
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
