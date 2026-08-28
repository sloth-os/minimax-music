package api

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"time"
)

// maxReqLogBody is the largest number of request body bytes captured for the
// received-request log line. Request bodies on these endpoints are small JSON.
const maxReqLogBody = 4 * 1024

// requestLogMiddleware logs each HTTP request the server receives — method,
// path, remote address, and a truncated body preview — together with the
// resulting response status and total duration. It wraps the inner handler.
func requestLogMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Capture the request body so the log preview can include it, then
		// restore it for the handler. Bodies are small JSON here.
		var bodyPreview []byte
		if r.Body != nil {
			b, err := io.ReadAll(r.Body)
			if err == nil {
				bodyPreview = b
				r.Body = io.NopCloser(bytes.NewReader(b))
			} else {
				r.Body = io.NopCloser(bytes.NewReader(nil))
			}
		}

		log.Printf("[api] --> %s %s from %s", r.Method, r.URL.RequestURI(), r.RemoteAddr)
		if len(bodyPreview) > 0 {
			log.Printf("[api]     req body: %s", truncateBody(bodyPreview))
		}

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		h.ServeHTTP(rec, r)

		log.Printf("[api] <-- %d %s %s (%s)",
			rec.status, r.Method, r.URL.RequestURI(), time.Since(start).Round(time.Millisecond))
	})
}

// statusRecorder wraps an http.ResponseWriter to capture the status code while
// preserving the Flusher interface (SSE handlers rely on flushing).
type statusRecorder struct {
	http.ResponseWriter
	status     int
	headerSent bool
}

func (s *statusRecorder) WriteHeader(code int) {
	// Relay exactly one WriteHeader. The stdlib ResponseWriter would log a
	// "superfluous response.WriteHeader call" warning on a second call; dropping
	// it here keeps the first status (the one actually sent) as the one we
	// capture and forward, without the warning noise.
	if s.headerSent {
		return
	}
	s.status = code
	s.headerSent = true
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// truncateBody returns at most n bytes of b as a string, with a trailing
// marker when truncated.
func truncateBody(b []byte) string {
	if len(b) <= maxReqLogBody {
		return string(b)
	}
	return string(b[:maxReqLogBody]) + "..."
}
