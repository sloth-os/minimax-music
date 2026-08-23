package minimax

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// Download fetches the MP3 audio from a CDN audio_url. The CDN requires no
// authentication; only a Referer and Range header (matching the browser). The
// audio is written to w. If the URL is empty, errNoAudioURL is returned.
//
// Download uses the same *http.Client as the rest of the client, so it routes
// through the configured proxy.
func (c *Client) Download(ctx context.Context, audioURL string, w io.Writer) (int64, error) {
	if audioURL == "" {
		return 0, errNoAudioURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, audioURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Range", "bytes=0-")
	req.Header.Set("Referer", BaseURL+"/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36 Edg/151.0.0.0")
	req.Header.Set("Accept", "*/*")

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("minimax: download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return 0, fmt.Errorf("minimax: download: HTTP %d", resp.StatusCode)
	}
	n, err := io.Copy(w, resp.Body)
	if err != nil {
		return n, fmt.Errorf("minimax: download: %w", err)
	}
	return n, nil
}

// DownloadBytes is a convenience wrapper that downloads into a byte slice.
func (c *Client) DownloadBytes(ctx context.Context, audioURL string) ([]byte, error) {
	var buf byteBuf
	if _, err := c.Download(ctx, audioURL, &buf); err != nil {
		return nil, err
	}
	return buf.b, nil
}

type byteBuf struct{ b []byte }

func (b *byteBuf) Write(p []byte) (int, error) {
	b.b = append(b.b, p...)
	return len(p), nil
}

var errNoAudioURL = fmt.Errorf("minimax: audio_url is empty")
