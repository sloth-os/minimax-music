// Package api exposes the MiniMaxi music client over a small HTTP API.
//
// Endpoints:
//
//	POST /v1/music_generation   — official MiniMax platform API schema (api.md):
//	                              GenerateMusicReq in, GenerateMusicResp out,
//	                              supports stream (SSE hex frames) and non-stream.
//	POST /api/generate          — generate music (streams audio chunks as SSE)
//	POST /api/generate/wait     — generate and return the final JSON result
//	GET  /api/download?url=...  — download an audio_url as audio/mpeg
//	GET  /api/history           — list generation history
//	GET  /api/models            — list available models
//	GET  /healthz               — liveness
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"minimax-music/internal/minimax"
)

// Server is the HTTP API server. Use New to construct one.
type Server struct {
	client *minimax.Client
	// gen overrides the client for the /v1/music_generation endpoint when set
	// (used by tests). When nil, the real client is used.
	gen MusicGenerator
	mux *http.ServeMux
}

// MusicGenerator is the subset of the minimax client used by the official
// /v1/music_generation endpoint. *minimax.Client satisfies it.
type MusicGenerator interface {
	Generate(ctx context.Context, req *minimax.GenerateRequest, onChunk func(chunk []byte)) (*minimax.GenerateResult, error)
	DownloadBytes(ctx context.Context, audioURL string) ([]byte, error)
}

// New returns a Server backed by the given MiniMaxi client.
func New(client *minimax.Client) *Server {
	s := &Server{client: client, mux: http.NewServeMux()}
	// Wire the audio-fetch fallback (used when a generation stream produced no
	// bytes but an audio_url is available) to the shared client so it honours
	// the configured proxy.
	fetchAudioBytes = func(audioURL string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		return client.DownloadBytes(ctx, audioURL)
	}
	s.routes()
	return s
}

// SetGenerator installs a custom MusicGenerator for the /v1/music_generation
// endpoint (primarily for testing).
func (s *Server) SetGenerator(g MusicGenerator) { s.gen = g }

// generator returns the MusicGenerator to use for /v1/music_generation.
func (s *Server) generator() MusicGenerator {
	if s.gen != nil {
		return s.gen
	}
	return s.client
}

// Handler returns the http.Handler to mount on an http.Server. It wraps the
// mux in the request-log middleware so every received request is logged.
func (s *Server) Handler() http.Handler { return requestLogMiddleware(s.mux) }

func (s *Server) routes() {
	s.mux.HandleFunc("/healthz", s.handleHealth)
	s.mux.HandleFunc("/api/generate", s.handleGenerate)               // POST, SSE stream
	s.mux.HandleFunc("/api/generate/wait", s.handleGenerateWait)      // POST, single JSON
	s.mux.HandleFunc("/api/download", s.handleDownload)               // GET
	s.mux.HandleFunc("/api/history", s.handleHistory)                 // GET
	s.mux.HandleFunc("/api/models", s.handleModels)                   // GET
	s.mux.HandleFunc("/v1/music_generation", s.handleMusicGeneration) // POST, official api.md schema
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "time": time.Now().UTC().Format(time.RFC3339)})
}

// generateBody is the JSON body for /api/generate and /api/generate/wait.
type generateBody struct {
	Model          string `json:"model"`
	Idea           string `json:"idea"`
	Lyrics         string `json:"lyrics"`
	Title          string `json:"title"`
	N              int    `json:"n"`
	Instrumental   bool   `json:"instrumental"`
	RewriteIdea    bool   `json:"rewrite_idea_switch"`
	GenerationType int    `json:"generation_type"`
}

func (g generateBody) toRequest() *minimax.GenerateRequest {
	return &minimax.GenerateRequest{
		Model:             g.Model,
		Idea:              g.Idea,
		Lyrics:            g.Lyrics,
		Title:             g.Title,
		N:                 g.N,
		RewriteIdeaSwitch: g.RewriteIdea,
		GenerationType:    g.GenerationType,
	}
}

// handleGenerate streams the generation as Server-Sent Events. Each audio
// chunk is emitted as an SSE "chunk" event (base64); the final result is an
// SSE "done" event with the music item(s). This lets a client play audio
// incrementally as it is generated.
func (s *Server) handleGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body generateBody
	if err := decodeJSON(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		// Fall back to the wait-style response.
		s.generateWait(w, r, body)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Minute)
	defer cancel()

	result, err := s.client.Generate(ctx, body.toRequest(), func(chunk []byte) {
		ev := map[string]any{"size": len(chunk), "data": base64Std(chunk)}
		_ = writeSSE(w, "chunk", ev)
		flusher.Flush()
	})
	if err != nil {
		_ = writeSSE(w, "error", map[string]string{"error": err.Error()})
		flusher.Flush()
		return
	}
	// Strip the large AudioData from the done payload; clients should use the
	// streamed chunks or the audio_url.
	done := map[string]any{
		"trace_id": result.TraceID,
		"items":    result.Items,
	}
	_ = writeSSE(w, "done", done)
	flusher.Flush()
}

// handleGenerateWait generates and returns a single JSON response with the
// final items. The streamed audio is returned inline as base64 under
// "audio_base64" (for the first item) when include_audio is set.
func (s *Server) handleGenerateWait(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body generateBody
	if err := decodeJSON(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.generateWait(w, r, body)
}

func (s *Server) generateWait(w http.ResponseWriter, r *http.Request, body generateBody) {
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Minute)
	defer cancel()

	result, err := s.client.Generate(ctx, body.toRequest(), nil)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	includeAudio := r.URL.Query().Get("include_audio") == "1"
	resp := map[string]any{
		"trace_id": result.TraceID,
		"items":    result.Items,
	}
	if includeAudio && len(result.AudioData) > 0 {
		resp["audio_base64"] = base64Std(result.AudioData)
		resp["audio_size"] = len(result.AudioData)
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleDownload proxies a CDN audio_url to the client as audio/mpeg. The
// audio_url is taken from the ?url= query parameter (typically obtained from
// /api/generate or /api/history).
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	audioURL := r.URL.Query().Get("url")
	if audioURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing ?url="})
		return
	}
	// Optional filename for Content-Disposition.
	filename := r.URL.Query().Get("filename")
	if filename == "" {
		filename = "music.mp3"
	}
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filename))

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	n, err := s.client.Download(ctx, audioURL, w)
	if err != nil && n == 0 {
		// Headers may already be sent; best-effort error in body.
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
}

// handleHistory lists the user's generated music history.
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	resp, err := s.client.HistoryList(ctx, page, pageSize)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleModels lists available models and trial quota.
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	resp, err := s.client.ModelInfo(ctx)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- helpers ---

func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeSSE(w io.Writer, event string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	return err
}

func base64Std(b []byte) string {
	return stdBase64(b)
}
