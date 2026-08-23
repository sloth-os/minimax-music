package minimax

// MusicModel describes one selectable generation model.
type MusicModel struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description"`
	IsNew       bool   `json:"isNew"`
}

// ModelInfoResp is the response from /v1/music/model_info.
type ModelInfoResp struct {
	Data struct {
		ModelList []struct {
			Model     string `json:"model"`
			TrialInfo struct {
				IsFreeTrial       bool  `json:"is_free_trial"`
				FreeTrialDeadline int64 `json:"free_trial_deadline"`
				TodayUsedCount    int   `json:"today_used_count"`
				TodayTotalCount   int   `json:"today_total_count"`
			} `json:"trial_info"`
		} `json:"model_list"`
	} `json:"data"`
	StatusInfo StatusInfo `json:"statusInfo"`
}

// StatusInfo is the common envelope status block.
type StatusInfo struct {
	Code        int    `json:"code"`
	HTTPCode    int    `json:"httpCode"`
	Message     string `json:"message"`
	RequestID   string `json:"requestID"`
	ServiceTime int64  `json:"serviceTime"`
}

// MusicItem is one music record (from history or generation).
type MusicItem struct {
	MusicID        string `json:"music_id"`
	UserID         string `json:"user_id"`
	Title          string `json:"title"`
	AudioURL       string `json:"audio_url"`
	CoverURL       string `json:"cover_url"`
	Idea           string `json:"idea"`
	Lyrics         string `json:"lyrics"`
	Model          string `json:"model"`
	Status         int    `json:"status"` // 1=generating, 2=success
	Instrumental   bool   `json:"instrumental"`
	GenerationType int    `json:"generation_type"`
	Duration       int64  `json:"duration"` // milliseconds
	HasWav         bool   `json:"hasWav"`
	// Audio holds hex-encoded MP3 frame bytes streamed over the WebSocket
	// during generation. Present only on streaming chunks; absent on the
	// final message and on history items. Use DecodeAudio to get raw MP3.
	Audio   string `json:"audio,omitempty"`
	TagList []Tag  `json:"tag_list"`
}

// DecodeAudio decodes the hex-encoded Audio field to raw MP3 bytes.
func (m MusicItem) DecodeAudio() ([]byte, error) {
	if m.Audio == "" {
		return nil, nil
	}
	return hexDecodeString(m.Audio)
}

// Tag is a style/mood tag attached to a music item.
type Tag struct {
	TagName string `json:"tag_name"`
	TagType int    `json:"tag_type"`
}

// HistoryResp is the response from /v1/api/music/history_list.
type HistoryResp struct {
	Data struct {
		MusicList []MusicItem `json:"music_list"`
		// other pagination fields exist but are not needed for our use.
	} `json:"data"`
	StatusInfo StatusInfo `json:"statusInfo"`
}

// GenerateRequest is the input to music generation. It maps to the
// `music_payLoad` object sent in the MusicGen WebSocket message.
type GenerateRequest struct {
	// Model: "music-3.0" or "music-2.6".
	Model string `json:"model"`
	// GenerationType: 1 = text-to-music (idea+lyrics), 2 = reference-audio.
	// Defaults to 1 when zero.
	GenerationType int `json:"generation_type"`
	// Idea is the style/mood prompt (e.g. "氛围感"). Optional when lyrics given.
	Idea string `json:"idea"`
	// Lyrics is the full lyrics text. May be empty for instrumental.
	Lyrics string `json:"lyrics,omitempty"`
	// Title of the generated track. Optional.
	Title string `json:"title,omitempty"`
	// N is the number of variations to generate (1, 2 or 3).
	N int `json:"n"`
	// RewriteIdeaSwitch mirrors the browser flag (true only for music-2.0).
	RewriteIdeaSwitch bool `json:"rewrite_idea_switch"`
	// Stream: always true for the web client (streams hex MP3 chunks).
	Stream bool `json:"stream"`
}

// GenerateResult is the final outcome of a generation session.
type GenerateResult struct {
	Items []MusicItem `json:"items"`
	// AudioData holds the concatenated streamed MP3 bytes for the first item,
	// collected from the WebSocket. This is the same audio as AudioURL but
	// available immediately without a second download. May be empty if the
	// server did not stream chunks; fall back to AudioURL in that case.
	AudioData []byte `json:"-"`
	// TraceID of the generation session.
	TraceID string `json:"trace_id"`
}

// wsEnvelope is the JSON envelope for WebSocket messages. Incoming messages
// reuse the same shape: method distinguishes MusicGen data/heartbeat from
// errors. Data is raw so we can decode items lazily (streaming chunks carry a
// large hex `audio` field we only decode when needed).
type wsEnvelope struct {
	Method         string      `json:"method"`
	Data           []MusicItem `json:"data"`
	MsgID          string      `json:"msg_id"`
	Timestamp      int64       `json:"timestamp"`
	InputSensitive bool        `json:"input_sensitive"`
	Ended          bool        `json:"ended"`
	TraceID        string      `json:"trace_id"`
	StatusInfo     *StatusInfo `json:"statusInfo"`
}
