package transcribe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

// Client talks to a whisper-server (whisper.cpp examples/server).
type Client struct {
	URL  string // http://127.0.0.1:port
	HTTP *http.Client
}

// Transcribe POSTs a 16 kHz mono WAV to /inference and returns the text.
func (c *Client) Transcribe(ctx context.Context, wav []byte, prompt string) (string, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "clip.wav")
	if err != nil {
		return "", err
	}
	fw.Write(wav)
	for k, v := range map[string]string{"response_format": "json", "temperature": "0", "prompt": prompt} {
		if v != "" {
			mw.WriteField(k, v)
		}
	}
	if err := mw.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(c.URL, "/")+"/inference", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	h := c.HTTP
	if h == nil {
		h = &http.Client{Timeout: 120 * time.Second}
	}
	resp, err := h.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	var out struct {
		Text  string `json:"text"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return "", fmt.Errorf("whisper-server: HTTP %d: %.200s", resp.StatusCode, b)
	}
	if out.Error != "" {
		return "", fmt.Errorf("whisper-server: %s", out.Error)
	}
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("whisper-server: HTTP %d", resp.StatusCode)
	}
	return strings.TrimSpace(strings.Join(strings.Fields(out.Text), " ")), nil
}
