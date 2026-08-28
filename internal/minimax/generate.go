package minimax

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WSDialer is the interface a WebSocket dialer must satisfy. The real
// *websocket.Dialer satisfies it; proxy.WSDialer returns one that routes
// through the configured proxy.
type WSDialer interface {
	DialContext(ctx context.Context, urlStr string, requestHeader http.Header) (*websocket.Conn, *http.Response, error)
}

// Generate connects to the music WebSocket, sends a MusicGen request, and
// streams the result until the server signals ended=true. It returns the
// final music item(s) plus any hex-decoded MP3 audio collected from the
// stream for the first item.
//
// If onChunk is non-nil it is invoked for each streamed audio chunk (raw MP3
// bytes), enabling incremental download/preview. The returned AudioData is the
// concatenation of all chunks for the first item.
func (c *Client) Generate(ctx context.Context, req *GenerateRequest, onChunk func(chunk []byte)) (*GenerateResult, error) {
	if c.cfg.Token == "" {
		return nil, fmt.Errorf("minimax: token is required")
	}
	if req == nil {
		return nil, fmt.Errorf("minimax: request is required")
	}
	if c.wsDialer == nil {
		return nil, fmt.Errorf("minimax: websocket dialer not configured (use minimax.New with a WS dialer)")
	}
	r := normalizeRequest(req)

	conn, err := c.dialWS(ctx)
	if err != nil {
		return nil, fmt.Errorf("minimax: ws dial: %w", err)
	}
	defer conn.Close()

	// Overall deadline for the session.
	deadline := time.Now().Add(c.cfg.WSTimeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}

	// Send the MusicGen message.
	msgID := newMsgID()
	sendMsg := struct {
		Method       string           `json:"method"`
		MusicPayLoad *GenerateRequest `json:"music_payLoad"`
		MsgID        string           `json:"msg_id"`
	}{Method: "MusicGen", MusicPayLoad: r, MsgID: msgID}
	payload, err := compactJSON(sendMsg)
	if err != nil {
		return nil, err
	}
	if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		return nil, fmt.Errorf("minimax: ws send MusicGen: %w", err)
	}

	// Writes are guarded by writeMu because the read loop's heartbeat echoes
	// share the conn with the initial MusicGen send.
	var writeMu sync.Mutex
	writeMsg := func(b []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteMessage(websocket.TextMessage, b)
	}
	hb := struct {
		Method    string `json:"method"`
		MsgID     string `json:"msg_id"`
		Timestamp int64  `json:"timestamp"`
	}{Method: "Heartbeat", MsgID: "", Timestamp: 0}
	heartbeatResp := func(msgID string) {
		hb.MsgID = msgID
		hb.Timestamp = nowMs()
		if b, err := compactJSON(hb); err == nil {
			_ = writeMsg(b)
		}
	}

	// Heartbeat handling. Verified against www.minimaxi.com2.har: the heartbeat
	// is SERVER-INITIATED. The server sends
	//   {"method":"Heartbeat","msg_id":"<id>","timestamp":T1}
	// roughly every 15s, and the client echoes it back with the SAME msg_id and
	// a fresh timestamp:
	//   {"method":"Heartbeat","msg_id":"<SAME>","timestamp":T2}
	// So we do NOT proactively send heartbeats; we reply to each one we receive.

	result := &GenerateResult{}
	var audioBuf []byte
	for {
		_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		_, data, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("minimax: ws read: %w", err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("minimax: ws session timed out after %s", c.cfg.WSTimeout)
		}

		var env wsEnvelope
		if err := json.Unmarshal(data, &env); err != nil {
			// Not JSON (binary frame?) — ignore.
			continue
		}

		if env.Method == "Heartbeat" {
			// Server-initiated keepalive: echo it back with the same msg_id and
			// a fresh timestamp (matches the browser).
			if env.MsgID != "" {
				heartbeatResp(env.MsgID)
			}
			continue
		}

		// MusicGen data message.
		if env.StatusInfo != nil && env.StatusInfo.Code != 0 {
			return nil, fmt.Errorf("minimax: server error: code=%d msg=%s", env.StatusInfo.Code, env.StatusInfo.Message)
		}
		if env.TraceID != "" {
			result.TraceID = env.TraceID
		}
		for i := range env.Data {
			it := &env.Data[i]
			// Collect streamed audio for the first item.
			if it.Audio != "" {
				chunk, err := it.DecodeAudio()
				if err != nil {
					return nil, fmt.Errorf("minimax: decode audio chunk: %w", err)
				}
				audioBuf = append(audioBuf, chunk...)
				if onChunk != nil {
					onChunk(chunk)
				}
			}
			// Keep the latest item state (audio_url is populated on the
			// final messages; status 2 = success).
			if it.MusicID != "" {
				result.Items = upsertItem(result.Items, *it)
			}
		}
		// Completion: the server signals done with ended=true. Some captures
		// never reach ended=true, so also accept an item reaching status==2
		// (server-side success) as a terminal state once audio_url is present.
		if env.Ended || finalItemReady(&env) {
			result.AudioData = audioBuf
			return result, nil
		}
	}
}

// finalItemReady reports whether a MusicGen message indicates the first item is
// complete (status 2 with a populated audio_url), which the server treats as a
// terminal state equivalent to ended=true.
func finalItemReady(env *wsEnvelope) bool {
	if len(env.Data) == 0 {
		return false
	}
	it := &env.Data[0]
	return it.Status == 2 && it.AudioURL != ""
}

// dialWS opens the WebSocket connection with the signed query string.
//
// The browser builds the WS URL as: path?<common params in order>&yy&token&op_ticket
// — i.e. yy/token/op_ticket are appended AFTER the common params (which are
// what get signed), and op_ticket is always present (empty when unset).
func (c *Client) dialWS(ctx context.Context) (*websocket.Conn, error) {
	now := nowMs()
	// hasSearchParamsPath = path + "?" + common-params (NO yy/token/op_ticket).
	common := c.buildCommonParams(now)
	hasPath := WsPath + "?" + encodeQuery(common)
	yy := sign(hasPath, http.MethodGet, nil, now)

	// Append yy, token, op_ticket to the wire query (matching the browser).
	// op_ticket is always sent, even when empty.
	wireParams := append(common[:len(common):len(common)],
		queryParam{"yy", yy},
		queryParam{"token", c.cfg.Token},
		queryParam{"op_ticket", c.cfg.OpTicket},
	)
	wsURL := "wss://" + c.baseURL.Host + WsPath + "?" + encodeQuery(wireParams)

	hdr := http.Header{}
	hdr.Set("Origin", BaseURL)
	hdr.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36 Edg/151.0.0.0")

	log.Printf("[minimax] --> curl -X GET '%s' -H 'Origin: %s' -H 'User-Agent: %s'", redactURLQuery(wsURL), BaseURL, hdr.Get("User-Agent"))
	conn, resp, err := c.wsDialer.DialContext(ctx, wsURL, hdr)
	if err != nil {
		log.Printf("[minimax] <-- ws dial error: %v", err)
		return conn, err
	}
	// resp is the HTTP/101 Switching Protocols handshake response.
	if resp != nil {
		log.Printf("[minimax] <-- WS %d %s (upgrade=%s)", resp.StatusCode, redactURLQuery(wsURL), resp.Header.Get("Upgrade"))
		resp.Body.Close()
	}
	return conn, nil
}

func normalizeRequest(r *GenerateRequest) *GenerateRequest {
	out := *r
	if out.Model == "" {
		out.Model = "music-3.0"
	}
	if out.GenerationType == 0 {
		out.GenerationType = 1
	}
	if out.N <= 0 {
		out.N = 1
	}
	out.Stream = true
	return &out
}

// upsertItem replaces an existing item with the same music_id, or appends.
func upsertItem(items []MusicItem, it MusicItem) []MusicItem {
	for i := range items {
		if items[i].MusicID == it.MusicID {
			// Preserve audio_url if the new one is empty.
			if it.AudioURL == "" {
				it.AudioURL = items[i].AudioURL
			}
			items[i] = it
			return items
		}
	}
	return append(items, it)
}

// newMsgID returns a random RFC 4122 v4 UUID, matching the format the browser
// sends for MusicGen msg_id (e.g. "cea36948-33db-460c-9f0a-b684a0550d58"). The
// server echoes it back in Heartbeat messages; a real v4 avoids any chance of
// collision with the server's own heartbeat ids.
func newMsgID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Fallback: time-based id. Uniqueness within a session is enough.
		t := nowMs()
		return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
			uint32(t), uint16(t>>16), uint16(t>>32), uint16(t>>48), uint64(t))
	}
	// RFC 4122 v4: set version (4) and variant (10xx).
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%02x%02x%02x%02x-%02x%02x-%02x%02x-%02x%02x-%02x%02x%02x%02x%02x%02x",
		b[0], b[1], b[2], b[3], b[4], b[5], b[6], b[7],
		b[8], b[9], b[10], b[11], b[12], b[13], b[14], b[15])
}
