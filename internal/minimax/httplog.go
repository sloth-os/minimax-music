package minimax

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
)

// maxLogBody is the largest number of body bytes captured for logging (request
// bodies and response previews). Full bodies still flow to callers; only the
// logged preview is truncated.
const maxLogBody = 4 * 1024

// sensitiveHeaders are redacted from outbound curl logs (their values are
// long-lived secrets). The per-request `yy` signature is not secret and is
// left in — it is only valid for the single request it was minted for.
var sensitiveHeaders = map[string]bool{
	"token":         true,
	"authorization": true,
	"op_ticket":     true,
}

// loggingTransport wraps an http.RoundTripper and logs each outgoing request to
// the MiniMaxi backend — as an equivalent curl command — plus the backend
// response (status, selected headers, and a truncated body preview). It is
// installed by Client.New over the proxy-routed transport so every REST and
// CDN-download request is logged. Sensitive headers are redacted.
type loggingTransport struct {
	base http.RoundTripper
}

func (t *loggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Capture the request body so the curl preview can include it, then
	// restore it for the actual round trip. GET requests have no body.
	var reqBody []byte
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		reqBody = b
		req.Body = io.NopCloser(bytes.NewReader(b))
	}
	log.Printf("[minimax] --> %s", curlString(req, reqBody))

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		log.Printf("[minimax] <-- error: %v", err)
		return nil, err
	}
	// Wrap the body so we can capture a preview as the caller streams it,
	// without buffering the whole thing (downloaded MP3s can be large). The
	// preview is logged when the body is closed.
	resp.Body = &previewBody{
		rc:     resp.Body,
		status: resp.StatusCode,
		url:    req.URL.String(),
		hdrs:   resp.Header,
		method: req.Method,
	}
	return resp, nil
}

// curlString renders req as an equivalent curl command for logging.
func curlString(req *http.Request, body []byte) string {
	var b strings.Builder
	b.WriteString("curl -X ")
	b.WriteString(req.Method)
	b.WriteString(" '")
	b.WriteString(redactURLQuery(req.URL.String()))
	b.WriteByte('\'')
	for k, vs := range req.Header {
		for _, v := range vs {
			b.WriteString(" -H '")
			b.WriteString(k)
			b.WriteString(": ")
			b.WriteString(redactHeader(k, v))
			b.WriteByte('\'')
		}
	}
	if len(body) > 0 {
		b.WriteString(" --data '")
		b.WriteString(truncate(string(body), maxLogBody))
		b.WriteByte('\'')
	}
	return b.String()
}

// redactHeader returns "<redacted>" for sensitive headers, otherwise the value.
func redactHeader(key, value string) string {
	if sensitiveHeaders[strings.ToLower(key)] {
		return "<redacted>"
	}
	return value
}

// redactURLQuery blanks the value of sensitive query params (token, op_ticket)
// in a URL string. The WebSocket authenticates via a token= query param (not a
// header), so this keeps the JWT out of logs. It steps through the raw query
// pairs so the rest of the query (including the browser's insertion order and
// the non-secret yy signature) is preserved verbatim for debugging.
func redactURLQuery(rawURL string) string {
	q := strings.IndexByte(rawURL, '?')
	if q < 0 {
		return rawURL // no query to redact
	}
	base, query := rawURL[:q], rawURL[q+1:]
	var b strings.Builder
	b.WriteString(base)
	b.WriteByte('?')
	for i, pair := range strings.Split(query, "&") {
		if i > 0 {
			b.WriteByte('&')
		}
		eq := strings.IndexByte(pair, '=')
		if eq < 0 {
			b.WriteString(pair)
			continue
		}
		key := pair[:eq]
		if sensitiveHeaders[strings.ToLower(key)] {
			b.WriteString(key)
			b.WriteString("=<redacted>")
		} else {
			b.WriteString(pair)
		}
	}
	return b.String()
}

// previewBody is an io.ReadCloser that copies into a bounded buffer as the
// underlying body is read, then logs a response summary (status, headers, body
// preview) when closed. The full body is still delivered to the caller.
type previewBody struct {
	rc     io.ReadCloser
	buf    bytes.Buffer
	total  int64
	status int
	url    string
	hdrs   http.Header
	method string

	once sync.Once
}

func (p *previewBody) Read(b []byte) (int, error) {
	n, err := p.rc.Read(b)
	if n > 0 {
		p.total += int64(n)
		// Capture only the first maxLogBody bytes of the preview.
		if p.buf.Len() < maxLogBody {
			room := maxLogBody - p.buf.Len()
			if n < room {
				room = n
			}
			p.buf.Write(b[:room])
		}
	}
	return n, err
}

func (p *previewBody) Close() (err error) {
	// sync.Once makes Close idempotent: both the log summary and the underlying
	// body close run exactly once, even on a double-Close.
	p.once.Do(func() {
		log.Printf("[minimax] <-- HTTP %d %s %s (%d bytes)", p.status, p.method, redactURLQuery(p.url), p.total)
		log.Printf("[minimax]     resp headers: %s", formatRespHeaders(p.hdrs))
		// Only log a body preview for text/JSON responses; binary bodies (the
		// CDN MP3 stream, images, etc.) are useless noise in a log line.
		if isTextContentType(p.hdrs.Get("Content-Type")) {
			log.Printf("[minimax]     resp body: %s", truncate(p.buf.String(), maxLogBody))
		} else {
			log.Printf("[minimax]     resp body: <binary, %d bytes omitted>", p.total)
		}
		err = p.rc.Close()
	})
	return err
}

// isTextContentType reports whether a Content-Type value is human-readable
// (text, JSON, XML, JavaScript). Binary media (audio, image, video, octet
// streams) returns false so their bodies are not dumped into logs.
func isTextContentType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	switch {
	case ct == "",
		ct == "application/json",
		strings.HasSuffix(ct, "+json"),
		ct == "application/xml",
		strings.HasSuffix(ct, "+xml"),
		ct == "application/javascript",
		strings.HasPrefix(ct, "text/"):
		return true
	}
	return false
}

// formatRespHeaders renders the response headers worth logging (Content-Type
// and Content-Length), keeping the log line concise.
func formatRespHeaders(h http.Header) string {
	var b strings.Builder
	first := true
	for _, k := range []string{"Content-Type", "Content-Length"} {
		if vs, ok := h[k]; ok && len(vs) > 0 {
			if !first {
				b.WriteString(", ")
			}
			b.WriteString(k)
			b.WriteString(": ")
			b.WriteString(strings.Join(vs, ", "))
			first = false
		}
	}
	if first {
		return "(none)"
	}
	return b.String()
}
