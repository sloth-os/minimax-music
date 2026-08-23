package api

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"minimax-music/internal/minimax"
)

// TestValidateMDReq_Model covers the required+enum rules for the model field.
func TestValidateMDReq_Model(t *testing.T) {
	cases := []struct {
		name    string
		req     GenerateMusicReq
		wantErr string // substring expected in error; "" = expect success... but cover/text need more fields
	}{
		{"missing model", GenerateMusicReq{}, "model is required"},
		{"invalid model", GenerateMusicReq{Model: "music-9.9"}, "invalid model"},
	}
	for _, c := range cases {
		err := validateMDReq(&c.req)
		if err == nil {
			t.Fatalf("%s: expected error containing %q, got nil", c.name, c.wantErr)
		}
		if !strings.Contains(err.Error(), c.wantErr) {
			t.Fatalf("%s: error %q does not contain %q", c.name, err.Error(), c.wantErr)
		}
	}
}

// TestValidateMDReq_TextToMusic covers the text-to-music model rules.
func TestValidateMDReq_TextToMusic(t *testing.T) {
	// Valid: non-instrumental with lyrics.
	if err := validateMDReq(&GenerateMusicReq{Model: "music-3.0", Lyrics: "[verse] hello world"}); err != nil {
		t.Fatalf("valid non-instrumental: unexpected error: %v", err)
	}
	// Valid: instrumental with prompt, no lyrics.
	if err := validateMDReq(&GenerateMusicReq{Model: "music-3.0", IsInstrumental: true, Prompt: "chill lo-fi beat"}); err != nil {
		t.Fatalf("valid instrumental: unexpected error: %v", err)
	}
	// Valid: lyrics_optimizer true, no lyrics.
	if err := validateMDReq(&GenerateMusicReq{Model: "music-2.6", LyricsOptimizer: true, Prompt: "epic orchestral"}); err != nil {
		t.Fatalf("valid lyrics_optimizer: unexpected error: %v", err)
	}
	// Invalid: non-instrumental, no lyrics, no optimizer.
	err := validateMDReq(&GenerateMusicReq{Model: "music-3.0"})
	if err == nil || !strings.Contains(err.Error(), "lyrics is required") {
		t.Fatalf("missing lyrics: expected 'lyrics is required', got %v", err)
	}
	// Invalid: instrumental on cover model.
	err = validateMDReq(&GenerateMusicReq{Model: "music-cover", IsInstrumental: true})
	if err == nil || !strings.Contains(err.Error(), "is_instrumental is not supported") {
		t.Fatalf("instrumental cover: expected error, got %v", err)
	}
	// Invalid: lyrics_optimizer on cover model.
	err = validateMDReq(&GenerateMusicReq{Model: "music-cover", LyricsOptimizer: true})
	if err == nil || !strings.Contains(err.Error(), "lyrics_optimizer is not supported") {
		t.Fatalf("lyrics_optimizer cover: expected error, got %v", err)
	}
	// Invalid: instrumental without prompt (prompt required 1-2000).
	err = validateMDReq(&GenerateMusicReq{Model: "music-3.0", IsInstrumental: true})
	if err == nil || !strings.Contains(err.Error(), "prompt is required") {
		t.Fatalf("instrumental no prompt: expected 'prompt is required', got %v", err)
	}
	// Invalid: prompt too long.
	err = validateMDReq(&GenerateMusicReq{Model: "music-3.0", Lyrics: "ok", Prompt: strings.Repeat("a", 2001)})
	if err == nil || !strings.Contains(err.Error(), "exceeds maximum") {
		t.Fatalf("prompt too long: expected error, got %v", err)
	}
	// Invalid: lyrics too long.
	err = validateMDReq(&GenerateMusicReq{Model: "music-3.0", Lyrics: strings.Repeat("a", 3501)})
	if err == nil || !strings.Contains(err.Error(), "exceeds maximum") {
		t.Fatalf("lyrics too long: expected error, got %v", err)
	}
}

// TestValidateMDReq_AudioSetting covers audio_setting enum + web-client
// capability conformance.
func TestValidateMDReq_AudioSetting(t *testing.T) {
	// Valid: unset (omitted) sub-fields.
	if err := validateMDReq(&GenerateMusicReq{Model: "music-3.0", Lyrics: "x", AudioSetting: &AudioSetting{}}); err != nil {
		t.Fatalf("empty audio_setting: unexpected error: %v", err)
	}
	// Valid: web-client defaults (44100 / 256000 / mp3).
	if err := validateMDReq(&GenerateMusicReq{Model: "music-3.0", Lyrics: "x",
		AudioSetting: &AudioSetting{SampleRate: 44100, Bitrate: 256000, Format: "mp3"}}); err != nil {
		t.Fatalf("default audio_setting: unexpected error: %v", err)
	}
	// Invalid: out-of-enum sample_rate.
	err := validateMDReq(&GenerateMusicReq{Model: "music-3.0", Lyrics: "x",
		AudioSetting: &AudioSetting{SampleRate: 9999}})
	if err == nil || !strings.Contains(err.Error(), "invalid audio_setting.sample_rate") {
		t.Fatalf("bad sample_rate: expected error, got %v", err)
	}
	// Invalid: valid enum but unsupported by web client (24000).
	err = validateMDReq(&GenerateMusicReq{Model: "music-3.0", Lyrics: "x",
		AudioSetting: &AudioSetting{SampleRate: 24000}})
	if err == nil || !strings.Contains(err.Error(), "not supported by this service") {
		t.Fatalf("unsupported sample_rate: expected error, got %v", err)
	}
	// Invalid: unsupported bitrate.
	err = validateMDReq(&GenerateMusicReq{Model: "music-3.0", Lyrics: "x",
		AudioSetting: &AudioSetting{Bitrate: 128000}})
	if err == nil || !strings.Contains(err.Error(), "not supported by this service") {
		t.Fatalf("unsupported bitrate: expected error, got %v", err)
	}
	// Invalid: unsupported format.
	err = validateMDReq(&GenerateMusicReq{Model: "music-3.0", Lyrics: "x",
		AudioSetting: &AudioSetting{Format: "wav"}})
	if err == nil || !strings.Contains(err.Error(), "not supported by this service") {
		t.Fatalf("unsupported format: expected error, got %v", err)
	}
}

// TestValidateMDReq_StreamFormat enforces "stream true => hex only".
func TestValidateMDReq_StreamFormat(t *testing.T) {
	err := validateMDReq(&GenerateMusicReq{Model: "music-3.0", Lyrics: "x", Stream: true, OutputFormat: "url"})
	if err == nil || !strings.Contains(err.Error(), "output_format must be hex") {
		t.Fatalf("stream+url: expected hex-only error, got %v", err)
	}
	// stream + hex (default) is valid.
	if err := validateMDReq(&GenerateMusicReq{Model: "music-3.0", Lyrics: "x", Stream: true}); err != nil {
		t.Fatalf("stream+hex: unexpected error: %v", err)
	}
	// invalid output_format
	err = validateMDReq(&GenerateMusicReq{Model: "music-3.0", Lyrics: "x", OutputFormat: "mp3"})
	if err == nil || !strings.Contains(err.Error(), "invalid output_format") {
		t.Fatalf("bad output_format: expected error, got %v", err)
	}
}

// TestValidateMDReq_Cover covers the cover-model mutual-exclusion rules.
func TestValidateMDReq_Cover(t *testing.T) {
	// Cover not supported by web client => always errors with the unsupported
	// message, but only AFTER passing the structural checks. A structurally
	// invalid cover request should surface the structural error first.
	err := validateMDReq(&GenerateMusicReq{Model: "music-cover"})
	if err == nil {
		t.Fatalf("cover no reference: expected error")
	}
	// Both audio_url and audio_base64 => mutual exclusion error.
	err = validateMDReq(&GenerateMusicReq{
		Model: "music-cover", Prompt: strings.Repeat("a", 20),
		AudioURL: "https://x/a.mp3", AudioBase64: "AAAA",
	})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("cover both refs: expected mutual-exclusion error, got %v", err)
	}
	// cover_feature_id + audio_url => mutual exclusion.
	err = validateMDReq(&GenerateMusicReq{
		Model: "music-cover", Prompt: strings.Repeat("a", 20),
		CoverFeatureID: "feat", AudioURL: "https://x/a.mp3",
	})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("cover feat+url: expected mutual-exclusion error, got %v", err)
	}
}

// TestTranslateReq covers the official->web request mapping.
func TestTranslateReq(t *testing.T) {
	t.Run("free variant maps to base model", func(t *testing.T) {
		got := translateReq(&GenerateMusicReq{Model: "music-3.0-free", Prompt: "p", Lyrics: "l"})
		if got.Model != "music-3.0" {
			t.Fatalf("model = %q, want music-3.0", got.Model)
		}
	})
	t.Run("instrumental clears lyrics", func(t *testing.T) {
		got := translateReq(&GenerateMusicReq{Model: "music-3.0", IsInstrumental: true, Prompt: "p", Lyrics: "l"})
		if got.Lyrics != "" {
			t.Fatalf("lyrics = %q, want empty for instrumental", got.Lyrics)
		}
	})
	t.Run("prompt maps to idea", func(t *testing.T) {
		got := translateReq(&GenerateMusicReq{Model: "music-3.0", Prompt: "moody", Lyrics: "l"})
		if got.Idea != "moody" {
			t.Fatalf("idea = %q, want moody", got.Idea)
		}
	})
	t.Run("stream always true for web", func(t *testing.T) {
		got := translateReq(&GenerateMusicReq{Model: "music-3.0", Lyrics: "l"})
		if !got.Stream {
			t.Fatalf("stream = false, want true")
		}
	})
}

// TestBuildMDResp_Hex verifies hex output_format produces hex-encoded audio.
func TestBuildMDResp_Hex(t *testing.T) {
	audio := []byte("ID3\x04fake-audio-bytes")
	result := &minimax.GenerateResult{
		TraceID:   "trace123",
		AudioData: audio,
		Items:     []minimax.MusicItem{{MusicID: "m1", AudioURL: "https://cdn/x.mp3", Duration: 25000, Status: 2}},
	}
	resp := buildMDResp(&GenerateMusicReq{Model: "music-3.0", Lyrics: "x", OutputFormat: "hex"}, result, nil)
	if resp.BaseResp.StatusCode != StatusSuccess {
		t.Fatalf("status_code = %d, want 0", resp.BaseResp.StatusCode)
	}
	if resp.Data.Status != 2 {
		t.Fatalf("data.status = %d, want 2", resp.Data.Status)
	}
	if resp.Data.Audio != hex.EncodeToString(audio) {
		t.Fatalf("data.audio = %q, want %q", resp.Data.Audio, hex.EncodeToString(audio))
	}
	if resp.TraceID != "trace123" {
		t.Fatalf("trace_id = %q, want trace123", resp.TraceID)
	}
	if resp.ExtraInfo == nil || resp.ExtraInfo.MusicDuration != 25000 {
		t.Fatalf("extra_info.music_duration wrong: %+v", resp.ExtraInfo)
	}
	if resp.ExtraInfo.MusicSize != int64(len(audio)) {
		t.Fatalf("extra_info.music_size = %d, want %d", resp.ExtraInfo.MusicSize, len(audio))
	}
}

// TestBuildMDResp_URL verifies url output_format places audio_url in data.audio.
func TestBuildMDResp_URL(t *testing.T) {
	result := &minimax.GenerateResult{
		TraceID: "trace123",
		Items:   []minimax.MusicItem{{MusicID: "m1", AudioURL: "https://cdn/x.mp3", Duration: 25000, Status: 2}},
	}
	resp := buildMDResp(&GenerateMusicReq{Model: "music-3.0", Lyrics: "x", OutputFormat: "url"}, result, nil)
	if resp.Data.Audio != "https://cdn/x.mp3" {
		t.Fatalf("data.audio = %q, want the audio_url", resp.Data.Audio)
	}
}

// TestBuildMDResp_JSONShape verifies the response JSON field names match the
// api.md schema exactly (snake_case).
func TestBuildMDResp_JSONShape(t *testing.T) {
	resp := buildMDResp(&GenerateMusicReq{Model: "music-3.0", Lyrics: "x"}, &minimax.GenerateResult{
		TraceID: "t", AudioData: []byte("ab"),
		Items: []minimax.MusicItem{{MusicID: "m", Duration: 1}},
	}, nil)
	b, _ := json.Marshal(resp)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"data", "base_resp", "trace_id", "extra_info", "analysis_info"} {
		if _, ok := m[key]; !ok {
			t.Errorf("response missing top-level key %q; got %v", key, m)
		}
	}
	// analysis_info must serialize as JSON null (per the api.md example).
	if m["analysis_info"] != nil {
		t.Errorf("analysis_info = %v, want null", m["analysis_info"])
	}
	d := m["data"].(map[string]any)
	if _, ok := d["status"]; !ok {
		t.Errorf("data missing status; got %v", d)
	}
	if _, ok := d["audio"]; !ok {
		t.Errorf("data missing audio; got %v", d)
	}
	br := m["base_resp"].(map[string]any)
	if _, ok := br["status_code"]; !ok {
		t.Errorf("base_resp missing status_code; got %v", br)
	}
	if _, ok := br["status_msg"]; !ok {
		t.Errorf("base_resp missing status_msg; got %v", br)
	}
}

// TestBuildMDResp_AnalysisInfoNull verifies a pure error response still
// includes analysis_info: null and base_resp.
func TestBuildMDResp_AnalysisInfoNull(t *testing.T) {
	resp := &GenerateMusicResp{
		BaseResp: &BaseResp{StatusCode: StatusInvalidParams, StatusMsg: "bad"},
	}
	b, _ := json.Marshal(resp)
	if !strings.Contains(string(b), `"analysis_info":null`) {
		t.Fatalf("error response must contain analysis_info:null; got %s", b)
	}
	if !strings.Contains(string(b), `"base_resp"`) {
		t.Fatalf("error response must contain base_resp; got %s", b)
	}
}

// TestMdStatusCode covers error->status_code mapping.
func TestMdStatusCode(t *testing.T) {
	cases := []struct {
		err  string
		want int
	}{
		{"token is required", StatusAuthFailed},
		{"auth failed", StatusAuthFailed},
		{"rate limit exceeded", StatusRateLimit},
		{"insufficient balance", StatusInsufficientBal},
		{"quota exceeded", StatusInsufficientBal},
		{"sensitive content", StatusSensitiveContent},
		{"some other error", StatusInvalidParams},
	}
	for _, c := range cases {
		got := mdStatusCode(strErr(c.err))
		if got != c.want {
			t.Errorf("mdStatusCode(%q) = %d, want %d", c.err, got, c.want)
		}
	}
}

type strErr string

func (e strErr) Error() string { return string(e) }
