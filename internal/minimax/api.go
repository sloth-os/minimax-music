package minimax

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// doRequest builds and executes a signed REST request.
//
// path is the API path beginning with "/v1/...". extra are request-specific
// query parameters (e.g. page/page_size); they are merged with the common
// params. body is the JSON payload for POST requests (nil for GET).
//
// The signature contract (verified against the HAR):
//   - yy = md5(encodeURIComponent(path+"?"+query) + "_" + bodyJSON + md5(unix) + "ooui")
//   - bodyJSON is "{}" for GET, the raw POST body bytes for POST.
//   - The query string we sign is EXACTLY the query string we send, in the
//     browser's parameter insertion order (not alphabetically sorted) — see
//     buildCommonParams.
//
// `token` and `yy` are sent as request headers (matching the browser). The
// `unix` query param carries the same millisecond timestamp used in the
// signature.
func (c *Client) doRequest(ctx context.Context, method, path string, extra []queryParam, body []byte) (*http.Response, error) {
	if c.cfg.Token == "" {
		return nil, fmt.Errorf("minimax: token is required")
	}

	now := nowMs()
	hasPath := c.buildSearchParamsPath(path, extra, now)
	// buildSearchParamsPath returns path+"?"+query; that exact string is what we
	// sign and what we send (the query portion is re-used verbatim below).
	yy := sign(hasPath, method, body, now)

	// Reconstruct the full URL from the signed hasPath so the wire request
	// matches the signed value byte-for-byte.
	fullURL := c.baseURL.Scheme + "://" + c.baseURL.Host + hasPath

	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Token", c.cfg.Token)
	req.Header.Set("yy", yy)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Referer", BaseURL+"/audio/music")
	req.Header.Set("Origin", BaseURL)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36 Edg/151.0.0.0")
	if strings.EqualFold(method, "post") {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cfg.OpTicket != "" {
		req.Header.Set("op_ticket", c.cfg.OpTicket)
	}

	return c.http.Do(req)
}

// doJSON executes a signed request and decodes the JSON response into out.
func (c *Client) doJSON(ctx context.Context, method, path string, extra []queryParam, body any, out any) error {
	var bodyBytes []byte
	if body != nil {
		b, err := compactJSON(body)
		if err != nil {
			return fmt.Errorf("minimax: marshal body: %w", err)
		}
		bodyBytes = b
	}
	resp, err := c.doRequest(ctx, method, path, extra, bodyBytes)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("minimax: read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("minimax: HTTP %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("minimax: decode response: %w (body: %s)", err, truncate(string(raw), 300))
	}
	return nil
}

// ModelInfo fetches the available music models and trial quota.
func (c *Client) ModelInfo(ctx context.Context) (*ModelInfoResp, error) {
	var resp ModelInfoResp
	if err := c.doJSON(ctx, http.MethodGet, "/v1/music/model_info", nil, nil, &resp); err != nil {
		return nil, err
	}
	if resp.StatusInfo.Code != 0 {
		return &resp, fmt.Errorf("minimax: model_info failed: code=%d msg=%s", resp.StatusInfo.Code, resp.StatusInfo.Message)
	}
	return &resp, nil
}

// HistoryList fetches the user's generated music history.
func (c *Client) HistoryList(ctx context.Context, page, pageSize int) (*HistoryResp, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	// Order matches the captured browser request: is_favorite, page, page_size
	// before the common params.
	q := []queryParam{
		{"is_favorite", "false"},
		{"page", fmt.Sprintf("%d", page)},
		{"page_size", fmt.Sprintf("%d", pageSize)},
	}
	var resp HistoryResp
	if err := c.doJSON(ctx, http.MethodGet, "/v1/api/music/history_list", q, nil, &resp); err != nil {
		return nil, err
	}
	if resp.StatusInfo.Code != 0 {
		return &resp, fmt.Errorf("minimax: history_list failed: code=%d msg=%s", resp.StatusInfo.Code, resp.StatusInfo.Message)
	}
	return &resp, nil
}

// CommonConfig fetches the web common config blob (base64-encoded JSON in the
// raw response; decoded here into raw map). Kept for completeness/debugging.
func (c *Client) CommonConfig(ctx context.Context) (map[string]any, error) {
	q := []queryParam{
		{"filter", "t2a_input_config,voice_tag_language,voice_tag_gender,voice_tag_age,voice_tag_accent,default_selected_voice"},
	}
	var resp struct {
		Data struct {
			Config string `json:"config"`
		} `json:"data"`
		StatusInfo StatusInfo `json:"statusInfo"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/v1/api/config/web/common_config", q, nil, &resp); err != nil {
		return nil, err
	}
	// The config field is base64-encoded JSON.
	dec, err := base64Decode(resp.Data.Config)
	if err != nil {
		return nil, fmt.Errorf("minimax: decode common_config: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(dec, &m); err != nil {
		return nil, fmt.Errorf("minimax: parse common_config: %w", err)
	}
	return m, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
