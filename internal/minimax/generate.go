package minimax

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
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

	// Heartbeat loop: the browser sends a Heartbeat every ~12s with the
	// server's last msg_id and echoes it back. We send a heartbeat every 12s
	// using the most recent server msg_id we've seen.
	hbStop := make(chan struct{})
	var lastServerMsgID string
	var mu sync.Mutex
	go func() {
		ticker := time.NewTicker(12 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-hbStop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				mu.Lock()
				id := lastServerMsgID
				mu.Unlock()
				if id == "" {
					continue
				}
				hb := struct {
					Method    string `json:"method"`
					MsgID     string `json:"msg_id"`
					Timestamp int64  `json:"timestamp"`
				}{Method: "Heartbeat", MsgID: id, Timestamp: nowMs()}
				if b, err := compactJSON(hb); err == nil {
					_ = conn.WriteMessage(websocket.TextMessage, b)
				}
			}
		}
	}()
	defer close(hbStop)

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
			mu.Lock()
			lastServerMsgID = env.MsgID
			mu.Unlock()
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
		if env.Ended {
			result.AudioData = audioBuf
			return result, nil
		}
	}
}

// dialWS opens the WebSocket connection with the signed query string.
func (c *Client) dialWS(ctx context.Context) (*websocket.Conn, error) {
	now := nowMs()
	// hasSearchParamsPath = path + "?" + common-params (NO yy/token/op_ticket).
	hasPath := c.buildSearchParamsPath(WsPath, nil, now)
	yy := sign(hasPath, http.MethodGet, nil, now)

	// Append yy, token, op_ticket to the query (matching the browser).
	q, err := url.ParseQuery(strings.SplitN(hasPath, "?", 2)[1])
	if err != nil {
		return nil, err
	}
	q.Set("yy", yy)
	q.Set("token", c.cfg.Token)
	if c.cfg.OpTicket != "" {
		q.Set("op_ticket", c.cfg.OpTicket)
	}
	wsURL := "wss://" + c.baseURL.Host + WsPath + "?" + q.Encode()

	hdr := http.Header{}
	hdr.Set("Origin", BaseURL)
	hdr.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36 Edg/151.0.0.0")

	conn, _, err := c.wsDialer.DialContext(ctx, wsURL, hdr)
	return conn, err
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

// newMsgID returns a random UUIDv4-style message id. We avoid crypto/rand to
// keep the dependency surface small; a time-based id is sufficient since the
// server only echoes it back for heartbeats.
func newMsgID() string {
	// Use a fixed-format pseudo-UUID. Uniqueness within a session is enough.
	t := nowMs()
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uint32(t), uint16(t>>16), uint16(t>>32), uint16(t>>48), uint64(t))
}
