package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"minimax-music/internal/minimax"
)

// handleMusicGeneration implements POST /v1/music_generation per the official
// MiniMax platform API schema (api.md). It accepts a GenerateMusicReq,
// translates it to the reverse-engineered web client, and returns a
// GenerateMusicResp. Both streaming (stream:true, SSE hex frames) and
// non-streaming (single JSON) modes are supported.
//
// Spec conformance note: the OpenAPI operation declares only an HTTP 200
// response; business errors (auth failure, invalid params, rate limit, etc.)
// are conveyed inside the body via base_resp.status_code. Accordingly this
// handler returns HTTP 200 with the appropriate base_resp for missing/wrong
// Content-Type, missing Authorization, malformed JSON, unknown fields, and
// validation failures. The only non-200 response is 405 for a wrong HTTP
// method, which is outside the declared POST operation.
func (s *Server) handleMusicGeneration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		// Wrong method is outside the declared POST operation; no JSON body to
		// conform to.
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Content-Type is required (enum [application/json]) per the OpenAPI
	// parameter. Reject missing or non-JSON as invalid params (2013) in a 200
	// body, per the spec's single-200-response contract.
	ct := strings.TrimSpace(r.Header.Get("Content-Type"))
	if !isJSONContentType(ct) {
		writeMDResp(w, http.StatusOK, StatusInvalidParams, "Content-Type must be application/json")
		return
	}

	// Authorization: Bearer <key> is required by the spec. This service wraps
	// the web client (which uses a configured JWT, not the caller's key), so we
	// require the header's presence for schema conformance but do not forward
	// the value. Missing/invalid => 1004 auth failed, returned as a 200 body.
	if !strings.HasPrefix(strings.TrimSpace(r.Header.Get("Authorization")), "Bearer ") {
		writeMDResp(w, http.StatusOK, StatusAuthFailed, "missing or invalid Authorization header (expected 'Bearer <api_key>')")
		return
	}

	var reqv GenerateMusicReq
	if err := decodeMDJSON(r, &reqv); err != nil {
		// Malformed JSON / unknown fields => invalid params (2013) in a 200 body.
		writeMDResp(w, http.StatusOK, StatusInvalidParams, "invalid JSON body: "+err.Error())
		return
	}
	if err := validateMDReq(&reqv); err != nil {
		// Validation failure: spec conveys business errors via base_resp inside
		// an HTTP 200 body. We return 200 with status_code=2013.
		writeMDResp(w, http.StatusOK, StatusInvalidParams, err.Error())
		return
	}

	if reqv.Stream {
		s.musicGenStream(w, r, &reqv)
		return
	}
	s.musicGenWait(w, r, &reqv)
}

// isJSONContentType reports whether a Content-Type header value is
// application/json, optionally with a charset parameter.
func isJSONContentType(ct string) bool {
	if ct == "" {
		return false
	}
	mime := ct
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = strings.TrimSpace(mime[:i])
	}
	return mime == "application/json"
}

// musicGenWait performs a non-streaming generation and returns a single JSON
// GenerateMusicResp with status=2 and the full audio (hex or url).
func (s *Server) musicGenWait(w http.ResponseWriter, r *http.Request, req *GenerateMusicReq) {
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Minute)
	defer cancel()

	webReq := translateReq(req)
	result, err := s.generator().Generate(ctx, webReq, nil)
	if err != nil {
		// Generation failure: return HTTP 200 with base_resp error (spec style).
		writeMDResp(w, http.StatusOK, mdStatusCode(err), err.Error())
		return
	}
	resp := buildMDResp(req, result, nil)
	writeJSON(w, http.StatusOK, resp)
}

// musicGenStream performs a streaming generation, emitting SSE frames. Each
// streamed audio chunk is emitted as a GenerateMusicResp frame with
// data.status=1 and a hex-encoded audio fragment; the final frame has
// data.status=2 with the complete audio (hex) or audio_url (url).
//
// The SSE wire format is:
//
//	event: message
//	data: <GenerateMusicResp JSON>
//
// (the official API uses unnamed SSE data frames; we emit "event: message"
// for client convenience and also accept clients that read raw data: lines).
func (s *Server) musicGenStream(w http.ResponseWriter, r *http.Request, req *GenerateMusicReq) {
	// Spec: when stream is true, only hex is supported.
	if req.OutputFormat == "" {
		req.OutputFormat = "hex"
	}
	if req.OutputFormat != "hex" {
		writeMDResp(w, http.StatusOK, StatusInvalidParams, "when stream is true, output_format must be hex")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeMDResp(w, http.StatusInternalServerError, StatusInvalidParams, "streaming not supported")
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

	webReq := translateReq(req)
	traceID := ""

	result, err := s.generator().Generate(ctx, webReq, func(chunk []byte) {
		frame := &GenerateMusicResp{
			Data:     &MusicData{Status: 1, Audio: hex.EncodeToString(chunk)},
			BaseResp: &BaseResp{StatusCode: StatusSuccess, StatusMsg: "streaming"},
		}
		if traceID != "" {
			frame.TraceID = traceID
		}
		_ = writeMDSSE(w, frame)
		flusher.Flush()
	})
	if err != nil {
		frame := &GenerateMusicResp{
			BaseResp: &BaseResp{StatusCode: mdStatusCode(err), StatusMsg: err.Error()},
		}
		_ = writeMDSSE(w, frame)
		flusher.Flush()
		return
	}
	traceID = result.TraceID
	final := buildMDResp(req, result, &traceID)
	_ = writeMDSSE(w, final)
	flusher.Flush()
}

// translateReq converts a GenerateMusicReq (official schema) into a web client
// GenerateRequest. The web client is text-to-music only, so cover inputs are
// rejected upstream by validateMDReq.
func translateReq(req *GenerateMusicReq) *minimax.GenerateRequest {
	// The web client only knows music-3.0 / music-2.6. Map free variants to
	// their base model; the web token's plan determines actual quota.
	model := req.Model
	switch model {
	case "music-3.0-free":
		model = "music-3.0"
	case "music-2.6-free":
		model = "music-2.6"
	}

	lyrics := req.Lyrics
	if req.IsInstrumental {
		// Instrumental => no lyrics/vocals on the web client.
		lyrics = ""
	}

	webReq := &minimax.GenerateRequest{
		Model:          model,
		GenerationType: 1, // text-to-music
		Idea:           req.Prompt,
		Lyrics:         lyrics,
		N:              1,
		Stream:         true,
	}
	// lyrics_optimizer: the web client has no direct equivalent. When set and
	// lyrics is empty, we leave Lyrics empty and rely on the web server's
	// idea/lyrics handling (the web client will still produce a track from the
	// prompt). This is best-effort.
	return webReq
}

// buildMDResp builds the final GenerateMusicResp (status=2) from a web
// GenerateResult. For output_format=hex it hex-encodes the audio bytes
// (downloading from audio_url first if the stream produced none). For url it
// places the audio_url in Data.Audio.
func buildMDResp(req *GenerateMusicReq, result *minimax.GenerateResult, traceIDOverride *string) *GenerateMusicResp {
	out := &GenerateMusicResp{
		BaseResp: &BaseResp{StatusCode: StatusSuccess, StatusMsg: "success"},
	}
	if result == nil {
		out.Data = &MusicData{Status: 2}
		return out
	}
	if traceIDOverride != nil {
		out.TraceID = *traceIDOverride
	} else if result.TraceID != "" {
		out.TraceID = result.TraceID
	}

	var item minimax.MusicItem
	if len(result.Items) > 0 {
		item = result.Items[0]
	}

	// ExtraInfo from the item metadata.
	if item.MusicID != "" {
		out.ExtraInfo = &ExtraInfo{
			MusicDuration:   item.Duration,
			MusicSampleRate: 44100, // web client default; not reported per-item
			MusicChannel:    2,
			Bitrate:         256000,
		}
	}

	outfmt := req.OutputFormat
	if outfmt == "" {
		outfmt = "hex"
	}

	switch outfmt {
	case "url":
		// Return the CDN audio_url. Spec notes url links expire 24h.
		out.Data = &MusicData{Status: 2, Audio: item.AudioURL}
	case "hex":
		fallthrough
	default:
		audio := result.AudioData
		if len(audio) == 0 && item.AudioURL != "" {
			// Stream produced no bytes; fetch from the CDN url synchronously.
			// Use a fresh context via the client's downloader.
			fetched, derr := fetchAudioBytes(item.AudioURL)
			if derr == nil {
				audio = fetched
			}
		}
		out.Data = &MusicData{Status: 2, Audio: hex.EncodeToString(audio)}
		if out.ExtraInfo != nil {
			out.ExtraInfo.MusicSize = int64(len(audio))
		}
	}
	return out
}

// fetchAudioBytes downloads audio from a CDN url using the server's client.
// It is set by the Server at construction (see fetchAudio field) so the
// handler can fall back to downloading when the stream produced no bytes.
var fetchAudioBytes = func(audioURL string) ([]byte, error) {
	return nil, errors.New("audio fetch not configured")
}

// validateMDReq enforces the api.md request constraints.
func validateMDReq(req *GenerateMusicReq) error {
	if req.Model == "" {
		return errors.New("model is required")
	}
	if !validModels[req.Model] {
		return fmt.Errorf("invalid model %q (must be one of music-3.0, music-2.6, music-cover, music-3.0-free, music-2.6-free, music-cover-free)", req.Model)
	}

	// output_format
	switch req.OutputFormat {
	case "", "url", "hex":
	default:
		return fmt.Errorf("invalid output_format %q (must be url or hex)", req.OutputFormat)
	}
	// stream true => only hex
	if req.Stream && req.OutputFormat == "url" {
		return errors.New("when stream is true, output_format must be hex (url not supported with streaming)")
	}

	// audio_setting validation
	if req.AudioSetting != nil {
		if err := validateAudioSetting(req.AudioSetting); err != nil {
			return err
		}
	}

	// is_instrumental / lyrics_optimizer are only for text-to-music models.
	// Check this before the cover branch so the spec-specific error wins.
	if req.IsInstrumental && !isInstrumentalCapable(req.Model) {
		return fmt.Errorf("is_instrumental is not supported by model %q", req.Model)
	}
	if req.LyricsOptimizer && !isInstrumentalCapable(req.Model) {
		return fmt.Errorf("lyrics_optimizer is not supported by model %q", req.Model)
	}

	cover := isCoverModel(req.Model)

	if cover {
		// Exactly one of audio_url / audio_base64, OR cover_feature_id.
		refInputs := 0
		if req.AudioURL != "" {
			refInputs++
		}
		if req.AudioBase64 != "" {
			refInputs++
		}
		if refInputs > 1 {
			return errors.New("audio_url and audio_base64 are mutually exclusive; provide exactly one")
		}
		if req.CoverFeatureID != "" && refInputs > 0 {
			return errors.New("cover_feature_id is mutually exclusive with audio_url/audio_base64")
		}
		if req.CoverFeatureID == "" && refInputs == 0 {
			return errors.New("music-cover requires one of audio_url, audio_base64, or cover_feature_id")
		}
		if req.CoverFeatureID != "" {
			if err := checkLen("lyrics", req.Lyrics, 10, 1000, true); err != nil {
				return err
			}
		}
		// cover prompt: required, 10–300
		if err := checkLen("prompt", req.Prompt, 10, 300, true); err != nil {
			return err
		}
		// cover lyrics (when provided via audio_url/base64): 10–1000
		if req.CoverFeatureID == "" && req.Lyrics != "" {
			if err := checkLen("lyrics", req.Lyrics, 10, 1000, false); err != nil {
				return err
			}
		}
		// NOTE: the web client does not support cover generation. We accept
		// the request as valid but translation will surface an unsupported
		// error at generation time. This keeps the schema contract honest.
		return errors.New("music-cover models are not supported by this service (web client is text-to-music only); use music-3.0 or music-2.6")
	}

	// Text-to-music models.
	// prompt length 0–2000
	if err := checkLen("prompt", req.Prompt, 0, 2000, false); err != nil {
		return err
	}
	// lyrics length 1–3500 when provided
	if req.Lyrics != "" {
		if err := checkLen("lyrics", req.Lyrics, 1, 3500, false); err != nil {
			return err
		}
	}

	if req.IsInstrumental {
		// prompt required, 1–2000
		if err := checkLen("prompt", req.Prompt, 1, 2000, true); err != nil {
			return err
		}
	} else {
		// non-instrumental: lyrics required unless lyrics_optimizer will generate
		if req.Lyrics == "" && !req.LyricsOptimizer {
			return errors.New("lyrics is required for non-instrumental generation (or set lyrics_optimizer=true to auto-generate from prompt)")
		}
	}
	return nil
}

// validateAudioSetting validates audio_setting against the api.md enum AND the
// web client's actual capabilities. The reverse-engineered web client always
// produces 44100 Hz, 256 kbps, MP3 output, so any request for a different
// sample rate / bitrate / format cannot be honored and is rejected with a
// clear error rather than silently ignored.
func validateAudioSetting(a *AudioSetting) error {
	// Enum conformance (api.md).
	switch a.SampleRate {
	case 0, 16000, 24000, 32000, 44100:
	default:
		return fmt.Errorf("invalid audio_setting.sample_rate %d (must be one of 16000, 24000, 32000, 44100)", a.SampleRate)
	}
	switch a.Bitrate {
	case 0, 32000, 64000, 128000, 256000:
	default:
		return fmt.Errorf("invalid audio_setting.bitrate %d (must be one of 32000, 64000, 128000, 256000)", a.Bitrate)
	}
	switch a.Format {
	case "", "mp3", "wav", "pcm":
	default:
		return fmt.Errorf("invalid audio_setting.format %q (must be one of mp3, wav, pcm)", a.Format)
	}
	// Web-client capability conformance: only 44100 / 256000 / mp3 (or unset)
	// can actually be produced. Reject anything else honestly.
	if a.SampleRate != 0 && a.SampleRate != 44100 {
		return fmt.Errorf("audio_setting.sample_rate %d is not supported by this service (web client outputs 44100 Hz only)", a.SampleRate)
	}
	if a.Bitrate != 0 && a.Bitrate != 256000 {
		return fmt.Errorf("audio_setting.bitrate %d is not supported by this service (web client outputs 256000 bps only)", a.Bitrate)
	}
	if a.Format != "" && a.Format != "mp3" {
		return fmt.Errorf("audio_setting.format %q is not supported by this service (web client outputs mp3 only)", a.Format)
	}
	return nil
}

// checkLen validates the length of a string field. If required is true and the
// value is empty, an error is returned. min/max are inclusive bounds applied
// to the rune length when the value is non-empty.
func checkLen(name, v string, min, max int, required bool) error {
	n := len([]rune(v))
	if required && n == 0 {
		return fmt.Errorf("%s is required", name)
	}
	if n != 0 {
		if n < min {
			return fmt.Errorf("%s length %d is below minimum %d", name, n, min)
		}
		if n > max {
			return fmt.Errorf("%s length %d exceeds maximum %d", name, n, max)
		}
	}
	return nil
}

// mdStatusCode maps a web client error to an official base_resp status_code.
func mdStatusCode(err error) int {
	if err == nil {
		return StatusSuccess
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "token is required"), strings.Contains(msg, "auth"):
		return StatusAuthFailed
	case strings.Contains(msg, "rate limit"):
		return StatusRateLimit
	case strings.Contains(msg, "balance"), strings.Contains(msg, "quota"):
		return StatusInsufficientBal
	case strings.Contains(msg, "sensitive"):
		return StatusSensitiveContent
	}
	// Default to a generic invalid-params/processing error.
	return StatusInvalidParams
}

// --- helpers ---

func decodeMDJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	// Strict conformance: reject unknown fields (the official schema defines a
	// closed property set).
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// writeMDResp writes a GenerateMusicResp envelope carrying only a base_resp
// error/status. Used for validation, auth, and generation failures. Per the
// spec, business errors are conveyed via base_resp.status_code inside the body
// (HTTP 200); transport-level errors (malformed JSON, wrong method) use the
// appropriate HTTP status.
func writeMDResp(w http.ResponseWriter, httpStatus, code int, msg string) {
	writeJSON(w, httpStatus, &GenerateMusicResp{
		BaseResp: &BaseResp{StatusCode: code, StatusMsg: msg},
	})
}

// writeMDSSE writes one GenerateMusicResp as an SSE frame.
func writeMDSSE(w interface {
	Write(p []byte) (int, error)
}, resp *GenerateMusicResp) error {
	b, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: message\ndata: %s\n\n", b)
	return err
}
