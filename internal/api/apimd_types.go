package api

// This file defines the request/response types for the official MiniMax
// platform API schema (api.md), exposed at POST /v1/music_generation.
//
// These types intentionally mirror the OpenAPI schema field names exactly
// (snake_case) so that the endpoint is consumable by clients generated from
// the official spec. Internally, the handler translates between these types
// and the reverse-engineered web client (internal/minimax).

// GenerateMusicReq is the request body for POST /v1/music_generation.
//
// Only `model` is required. All other fields are optional or conditionally
// required depending on the model and flags; see field docs and the
// validation in music_generation.go.
type GenerateMusicReq struct {
	// Model is the model name. One of:
	// music-3.0, music-2.6, music-cover, music-3.0-free, music-2.6-free,
	// music-cover-free. Required.
	Model string `json:"model"`

	// Prompt describes style/mood/scenario. Length 0–2000 (cover: 10–300).
	// Required when is_instrumental is true or model is a cover variant.
	Prompt string `json:"prompt,omitempty"`

	// Lyrics are the song lyrics, "\n"-separated. Supports structure tags
	// ([Intro], [Verse], ...). Length 1–3500 (cover: 10–1000). Required for
	// non-instrumental text-to-music unless lyrics_optimizer generates them.
	Lyrics string `json:"lyrics,omitempty"`

	// Stream selects streaming output. When true, output_format must be hex.
	Stream bool `json:"stream,omitempty"`

	// OutputFormat is "url" or "hex" (default "hex"). url links expire 24h.
	// When stream is true, only "hex" is supported.
	OutputFormat string `json:"output_format,omitempty"`

	// AudioSetting configures sample rate / bitrate / format.
	AudioSetting *AudioSetting `json:"audio_setting,omitempty"`

	// LyricsOptimizer: when true and lyrics is empty, auto-generate lyrics
	// from prompt. Only music-3.0 / music-2.6 (+ free variants).
	LyricsOptimizer bool `json:"lyrics_optimizer,omitempty"`

	// IsInstrumental: generate instrumental music (no vocals). When true,
	// lyrics is not required. Only music-3.0 / music-2.6 (+ free variants).
	IsInstrumental bool `json:"is_instrumental,omitempty"`

	// AudioURL: reference audio URL. Only music-cover (+ free). Exactly one of
	// AudioURL / AudioBase64. Mutually exclusive with CoverFeatureID.
	AudioURL string `json:"audio_url,omitempty"`

	// AudioBase64: base64 reference audio. Only music-cover (+ free). Exactly
	// one of AudioURL / AudioBase64.
	AudioBase64 string `json:"audio_base64,omitempty"`

	// CoverFeatureID: feature ID from the Music Cover Preprocess API. Only
	// music-cover (+ free). Mutually exclusive with AudioURL / AudioBase64.
	// When provided, Lyrics is required (10–1000).
	CoverFeatureID string `json:"cover_feature_id,omitempty"`
}

// AudioSetting configures the output audio.
type AudioSetting struct {
	SampleRate int    `json:"sample_rate,omitempty"` // 16000|24000|32000|44100
	Bitrate    int    `json:"bitrate,omitempty"`     // 32000|64000|128000|256000
	Format     string `json:"format,omitempty"`      // mp3|wav|pcm
}

// GenerateMusicResp is the response for POST /v1/music_generation.
//
// Field presence follows the api.md example: base_resp and analysis_info are
// always present (analysis_info is null when there is no analysis); data,
// trace_id and extra_info are present on success.
type GenerateMusicResp struct {
	Data         *MusicData `json:"data,omitempty"`
	BaseResp     *BaseResp  `json:"base_resp"`
	TraceID      string     `json:"trace_id,omitempty"`
	ExtraInfo    *ExtraInfo `json:"extra_info,omitempty"`
	AnalysisInfo any        `json:"analysis_info"`
}

// MusicData carries the generation status and (for hex) the audio payload.
type MusicData struct {
	// Status: 1 = in progress, 2 = completed.
	Status int `json:"status"`
	// Audio is the hex-encoded audio string (when output_format is hex).
	Audio string `json:"audio,omitempty"`
}

// BaseResp is the status envelope. Status_code 0 means success.
type BaseResp struct {
	StatusCode int    `json:"status_code"`
	StatusMsg  string `json:"status_msg"`
}

// ExtraInfo holds audio metadata. Populated on the final (status=2) response.
type ExtraInfo struct {
	MusicDuration   int64 `json:"music_duration,omitempty"` // milliseconds
	MusicSampleRate int   `json:"music_sample_rate,omitempty"`
	MusicChannel    int   `json:"music_channel,omitempty"`
	Bitrate         int   `json:"bitrate,omitempty"`
	MusicSize       int64 `json:"music_size,omitempty"` // bytes
}

// Official MiniMax base_resp status codes (from api.md).
const (
	StatusSuccess          = 0
	StatusRateLimit        = 1002
	StatusAuthFailed       = 1004
	StatusInsufficientBal  = 1008
	StatusSensitiveContent = 1026
	StatusInvalidParams    = 2013
	StatusInvalidAPIKey    = 2049
)

// validModels is the set of model names accepted by the spec.
var validModels = map[string]bool{
	"music-3.0":        true,
	"music-2.6":        true,
	"music-cover":      true,
	"music-3.0-free":   true,
	"music-2.6-free":   true,
	"music-cover-free": true,
}

// isCoverModel reports whether the model is a cover variant.
func isCoverModel(model string) bool {
	return model == "music-cover" || model == "music-cover-free"
}

// isInstrumentalCapable reports whether the model supports is_instrumental /
// lyrics_optimizer (the text-to-music models, not cover).
func isInstrumentalCapable(model string) bool {
	switch model {
	case "music-3.0", "music-3.0-free", "music-2.6", "music-2.6-free":
		return true
	}
	return false
}
