package api

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"minimax-music/internal/minimax"
)

// fakeGenerator is a test MusicGenerator. It records the last request and
// returns a canned result, optionally invoking the chunk callback to simulate
// streaming.
type fakeGenerator struct {
	lastReq   *minimax.GenerateRequest
	result    *minimax.GenerateResult
	err       error
	chunks    [][]byte // bytes to feed to onChunk when streaming
	downloads map[string][]byte
}

func (f *fakeGenerator) Generate(ctx context.Context, req *minimax.GenerateRequest, onChunk func(chunk []byte)) (*minimax.GenerateResult, error) {
	f.lastReq = req
	if f.err != nil {
		return nil, f.err
	}
	if onChunk != nil {
		for _, c := range f.chunks {
			onChunk(c)
		}
	}
	return f.result, nil
}

func (f *fakeGenerator) DownloadBytes(ctx context.Context, audioURL string) ([]byte, error) {
	if b, ok := f.downloads[audioURL]; ok {
		return b, nil
	}
	return nil, io.ErrUnexpectedEOF
}

// newTestServer builds a Server with a fake generator installed and the
// fetchAudioBytes fallback wired to the same fake.
func newTestServer(t *testing.T, gen *fakeGenerator) *Server {
	t.Helper()
	// New requires a *minimax.Client; pass nil — the api.md endpoint only uses
	// the generator override, and the other endpoints aren't exercised here.
	s := &Server{client: nil, mux: http.NewServeMux()}
	s.SetGenerator(gen)
	fetchAudioBytes = func(audioURL string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return gen.DownloadBytes(ctx, audioURL)
	}
	s.routes()
	return s
}

// TestMusicGeneration_NonStream_Hex exercises the full POST /v1/music_generation
// path end-to-end with a fake generator: validation, translation, generation,
// and response building for a non-streaming hex request.
func TestMusicGeneration_NonStream_Hex(t *testing.T) {
	audio := []byte("ID3\x04audio-bytes-here")
	gen := &fakeGenerator{
		result: &minimax.GenerateResult{
			TraceID:   "trace-abc",
			AudioData: audio,
			Items: []minimax.MusicItem{{
				MusicID:  "m1",
				AudioURL: "https://cdn/x.mp3",
				Duration: 25364,
				Status:   2,
			}},
		},
	}
	srv := newTestServer(t, gen)

	body := `{"model":"music-3.0","prompt":"moody lo-fi","lyrics":"[verse] hello"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/music_generation", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp GenerateMusicResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rec.Body.String())
	}
	if resp.BaseResp.StatusCode != StatusSuccess {
		t.Fatalf("base_resp.status_code = %d (%s), want 0", resp.BaseResp.StatusCode, resp.BaseResp.StatusMsg)
	}
	if resp.Data.Status != 2 {
		t.Fatalf("data.status = %d, want 2", resp.Data.Status)
	}
	if resp.Data.Audio != hex.EncodeToString(audio) {
		t.Fatalf("data.audio = %q, want %q", resp.Data.Audio, hex.EncodeToString(audio))
	}
	if resp.TraceID != "trace-abc" {
		t.Fatalf("trace_id = %q, want trace-abc", resp.TraceID)
	}
	if resp.ExtraInfo == nil || resp.ExtraInfo.MusicDuration != 25364 {
		t.Fatalf("extra_info wrong: %+v", resp.ExtraInfo)
	}
	// analysis_info must be present and null.
	if _, ok := bytesContains(rec.Body.Bytes(), []byte(`"analysis_info":null`)); !ok {
		t.Fatalf("body missing analysis_info:null: %s", rec.Body.String())
	}
	// Verify translation: prompt -> idea, lyrics preserved, stream forced true.
	if gen.lastReq.Idea != "moody lo-fi" {
		t.Fatalf("translated idea = %q, want moody lo-fi", gen.lastReq.Idea)
	}
	if gen.lastReq.Lyrics != "[verse] hello" {
		t.Fatalf("translated lyrics = %q", gen.lastReq.Lyrics)
	}
	if gen.lastReq.Model != "music-3.0" {
		t.Fatalf("translated model = %q", gen.lastReq.Model)
	}
	if !gen.lastReq.Stream {
		t.Fatalf("translated stream = false, want true")
	}
}

// TestMusicGeneration_NonStream_URL verifies url output_format returns the
// audio_url in data.audio and does not require downloading.
func TestMusicGeneration_NonStream_URL(t *testing.T) {
	gen := &fakeGenerator{
		result: &minimax.GenerateResult{
			TraceID: "t",
			Items: []minimax.MusicItem{{
				MusicID:  "m1",
				AudioURL: "https://cdn/x.mp3",
				Duration: 1000,
				Status:   2,
			}},
		},
	}
	srv := newTestServer(t, gen)

	body := `{"model":"music-2.6-free","lyrics":"some lyrics here","output_format":"url"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/music_generation", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer k")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp GenerateMusicResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.Audio != "https://cdn/x.mp3" {
		t.Fatalf("data.audio = %q, want the url", resp.Data.Audio)
	}
	// free variant maps to base model in translation.
	if gen.lastReq.Model != "music-2.6" {
		t.Fatalf("translated model = %q, want music-2.6", gen.lastReq.Model)
	}
}

// TestMusicGeneration_Stream_Hex verifies SSE streaming: intermediate status=1
// hex frames for each chunk, then a final status=2 frame with full audio.
func TestMusicGeneration_Stream_Hex(t *testing.T) {
	chunks := [][]byte{[]byte("chunk1-"), []byte("chunk2")}
	full := append([]byte("chunk1-"), []byte("chunk2")...)
	gen := &fakeGenerator{
		chunks: chunks,
		result: &minimax.GenerateResult{
			TraceID:   "trace-stream",
			AudioData: full,
			Items: []minimax.MusicItem{{
				MusicID:  "m1",
				AudioURL: "https://cdn/x.mp3",
				Duration: 5000,
				Status:   2,
			}},
		},
	}
	srv := newTestServer(t, gen)

	body := `{"model":"music-3.0","lyrics":"lyrics","stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/music_generation", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer k")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	// Parse SSE frames: each "data: <json>" line is a GenerateMusicResp.
	frames := parseSSEFrames(rec.Body.Bytes())
	if len(frames) != 3 { // 2 chunks + 1 final
		t.Fatalf("got %d SSE frames, want 3", len(frames))
	}
	// First two frames: status=1, hex chunk.
	for i, want := range chunks {
		if frames[i].Data == nil || frames[i].Data.Status != 1 {
			t.Fatalf("frame %d: status = %d, want 1", i, frames[i].Data.Status)
		}
		if frames[i].Data.Audio != hex.EncodeToString(want) {
			t.Fatalf("frame %d: audio = %q, want %q", i, frames[i].Data.Audio, hex.EncodeToString(want))
		}
	}
	// Final frame: status=2, full audio, trace_id, extra_info.
	final := frames[2]
	if final.Data.Status != 2 {
		t.Fatalf("final status = %d, want 2", final.Data.Status)
	}
	if final.Data.Audio != hex.EncodeToString(full) {
		t.Fatalf("final audio = %q, want %q", final.Data.Audio, hex.EncodeToString(full))
	}
	if final.TraceID != "trace-stream" {
		t.Fatalf("final trace_id = %q, want trace-stream", final.TraceID)
	}
	if final.ExtraInfo == nil || final.ExtraInfo.MusicSize != int64(len(full)) {
		t.Fatalf("final extra_info wrong: %+v", final.ExtraInfo)
	}
}

// TestMusicGeneration_ValidationErrors verifies that validation failures return
// HTTP 200 with base_resp.status_code=2013 (spec style).
func TestMusicGeneration_ValidationErrors(t *testing.T) {
	gen := &fakeGenerator{}
	srv := newTestServer(t, gen)

	cases := []struct {
		name string
		body string
	}{
		{"missing model", `{}`},
		{"invalid model", `{"model":"music-9.9"}`},
		{"non-instrumental no lyrics", `{"model":"music-3.0"}`},
		{"stream with url", `{"model":"music-3.0","lyrics":"x","stream":true,"output_format":"url"}`},
		{"cover unsupported", `{"model":"music-cover","prompt":"` + strings.Repeat("a", 20) + `","audio_url":"https://x/a.mp3"}`},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodPost, "/v1/music_generation", strings.NewReader(c.body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer k")
		rec := httptest.NewRecorder()
		srv.mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", c.name, rec.Code)
		}
		var resp GenerateMusicResp
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Errorf("%s: unmarshal: %v; body=%s", c.name, err, rec.Body.String())
			continue
		}
		if resp.BaseResp.StatusCode != StatusInvalidParams {
			t.Errorf("%s: status_code = %d, want 2013; msg=%q", c.name, resp.BaseResp.StatusCode, resp.BaseResp.StatusMsg)
		}
		if resp.Data != nil {
			t.Errorf("%s: expected no data on validation error, got %+v", c.name, resp.Data)
		}
	}
}

// TestMusicGeneration_AuthAndContentType verifies that auth/content-type
// failures are conveyed as HTTP 200 with base_resp.status_code (per the spec's
// single-200-response contract), and that a wrong HTTP method is a 405.
func TestMusicGeneration_AuthAndContentType(t *testing.T) {
	gen := &fakeGenerator{}
	srv := newTestServer(t, gen)

	// Missing Authorization => 200 with base_resp.status_code=1004.
	req := httptest.NewRequest(http.MethodPost, "/v1/music_generation", strings.NewReader(`{"model":"music-3.0","lyrics":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("missing auth: status = %d, want 200", rec.Code)
	}
	var resp GenerateMusicResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.BaseResp.StatusCode != StatusAuthFailed {
		t.Fatalf("missing auth: status_code = %d, want 1004", resp.BaseResp.StatusCode)
	}

	// Wrong Content-Type => 200 with 2013.
	req = httptest.NewRequest(http.MethodPost, "/v1/music_generation", strings.NewReader(`{"model":"music-3.0","lyrics":"x"}`))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Authorization", "Bearer k")
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bad content-type: status = %d, want 200", rec.Code)
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.BaseResp.StatusCode != StatusInvalidParams {
		t.Fatalf("bad content-type: status_code = %d, want 2013", resp.BaseResp.StatusCode)
	}

	// Missing Content-Type => 200 with 2013 (spec marks the header required).
	req = httptest.NewRequest(http.MethodPost, "/v1/music_generation", strings.NewReader(`{"model":"music-3.0","lyrics":"x"}`))
	req.Header.Set("Authorization", "Bearer k")
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("missing content-type: status = %d, want 200", rec.Code)
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.BaseResp.StatusCode != StatusInvalidParams {
		t.Fatalf("missing content-type: status_code = %d, want 2013", resp.BaseResp.StatusCode)
	}

	// Wrong method => 405 (plain text, no JSON envelope; outside the POST op).
	req = httptest.NewRequest(http.MethodGet, "/v1/music_generation", nil)
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: status = %d, want 405", rec.Code)
	}
}

// TestMusicGeneration_GenerationError verifies a generator error maps to a
// base_resp status_code via mdStatusCode, HTTP 200.
func TestMusicGeneration_GenerationError(t *testing.T) {
	gen := &fakeGenerator{err: strErr("rate limit exceeded, retry later")}
	srv := newTestServer(t, gen)

	body := `{"model":"music-3.0","lyrics":"x"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/music_generation", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer k")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp GenerateMusicResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.BaseResp.StatusCode != StatusRateLimit {
		t.Fatalf("status_code = %d, want 1002", resp.BaseResp.StatusCode)
	}
}

// TestMusicGeneration_UnknownField verifies DisallowUnknownFields yields a 200
// with base_resp.status_code=2013 (per the spec's single-200-response contract).
func TestMusicGeneration_UnknownField(t *testing.T) {
	gen := &fakeGenerator{}
	srv := newTestServer(t, gen)

	body := `{"model":"music-3.0","lyrics":"x","bogus_field":1}`
	req := httptest.NewRequest(http.MethodPost, "/v1/music_generation", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer k")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp GenerateMusicResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.BaseResp.StatusCode != StatusInvalidParams {
		t.Fatalf("status_code = %d, want 2013", resp.BaseResp.StatusCode)
	}
}

// TestMusicGeneration_HexFallbackDownload verifies that when the stream
// produced no AudioData but an audio_url is present, the handler downloads the
// bytes for hex output.
func TestMusicGeneration_HexFallbackDownload(t *testing.T) {
	audio := []byte("downloaded-audio")
	gen := &fakeGenerator{
		result: &minimax.GenerateResult{
			TraceID: "t",
			Items: []minimax.MusicItem{{
				MusicID:  "m1",
				AudioURL: "https://cdn/x.mp3",
				Duration: 2000,
				Status:   2,
			}},
			// AudioData intentionally empty.
		},
		downloads: map[string][]byte{"https://cdn/x.mp3": audio},
	}
	srv := newTestServer(t, gen)

	body := `{"model":"music-3.0","lyrics":"x","output_format":"hex"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/music_generation", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer k")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp GenerateMusicResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.Audio != hex.EncodeToString(audio) {
		t.Fatalf("data.audio = %q, want downloaded hex", resp.Data.Audio)
	}
	if resp.ExtraInfo == nil || resp.ExtraInfo.MusicSize != int64(len(audio)) {
		t.Fatalf("extra_info.music_size wrong: %+v", resp.ExtraInfo)
	}
}

// --- helpers ---

func parseSSEFrames(b []byte) []GenerateMusicResp {
	var frames []GenerateMusicResp
	for line := range bytes.SplitSeq(b, []byte("\n")) {
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}
		payload := bytes.TrimPrefix(line, []byte("data: "))
		var resp GenerateMusicResp
		if json.Unmarshal(payload, &resp) == nil {
			frames = append(frames, resp)
		}
	}
	return frames
}

func bytesContains(haystack, needle []byte) (int, bool) {
	idx := bytes.Index(haystack, needle)
	return idx, idx >= 0
}
