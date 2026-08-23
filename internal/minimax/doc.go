// Package minimax implements a client for the MiniMaxi (Hailuo Audio) web music
// generation API, reverse-engineered from the www.minimaxi.com front-end.
//
// The protocol has two transports:
//
//  1. A REST API (https://www.minimaxi.com/v1/...) used for history, model info,
//     billing, etc. Every request carries a signed query/header `yy`.
//
//  2. A WebSocket (wss://www.minimaxi.com/v1/api/music/ws) used to actually
//     generate music. The server streams hex-encoded MP3 frame chunks back over
//     the socket, ending with a final message that carries the canonical
//     audio_url and ended=true.
//
// Both transports are authenticated with a JWT `token` (the same one the browser
// stores in localStorage under "_token") and signed with an `yy` parameter
// whose algorithm is reproduced in sign.go and verified against the captured
// HAR traffic.
package minimax

// BaseURL is the web front-end / API origin.
const BaseURL = "https://www.minimaxi.com"

// WsPath is the music generation WebSocket endpoint (path only).
const WsPath = "/v1/api/music/ws"

// Default web client constants, taken from the captured request. These are the
// values the browser sends as common query parameters on every call; they are
// not secret and only identify the "web" app variant.
const (
	AppID       = "3001"
	VersionCode = "22201"
	BizID       = "1"
	OSName      = "Windows"
	BrowserName = "edge"
)
