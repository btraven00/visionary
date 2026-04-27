package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// synthesizer abstracts TTS backends.
type synthesizer interface {
	Synthesize(ctx context.Context, ssml string, rate float64) ([]byte, error)
	Close() error
}

// proxyTTSClient implements synthesizer by calling the visionary server over HTTP.
type proxyTTSClient struct {
	baseURL string
	token   string
	client  *http.Client
}

func newProxyTTSClient(baseURL, token string) *proxyTTSClient {
	return &proxyTTSClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		client:  http.DefaultClient,
	}
}

func (p *proxyTTSClient) Close() error { return nil }

type synthesizeRequest struct {
	SSML         string  `json:"ssml"`
	SpeakingRate float64 `json:"speaking_rate,omitempty"`
}

func (p *proxyTTSClient) Synthesize(ctx context.Context, ssml string, rate float64) ([]byte, error) {
	body, err := json.Marshal(synthesizeRequest{SSML: ssml, SpeakingRate: rate})
	if err != nil {
		return nil, fmt.Errorf("encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/synthesize", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("server request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, bytes.TrimSpace(msg))
	}

	return io.ReadAll(resp.Body)
}
