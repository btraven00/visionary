package tts

import (
	"context"
	"fmt"
	"os"

	texttospeech "cloud.google.com/go/texttospeech/apiv1"
	texttospeechpb "cloud.google.com/go/texttospeech/apiv1/texttospeechpb"
	"google.golang.org/api/option"
)

type Client struct {
	inner    *texttospeech.Client
	language string
	voice    string
}

func NewClient(ctx context.Context, language, voice string) (*Client, error) {
	var opts []option.ClientOption
	if project := os.Getenv("GOOGLE_CLOUD_PROJECT"); project != "" {
		opts = append(opts, option.WithQuotaProject(project))
	}
	c, err := texttospeech.NewClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("creating TTS client: %w", err)
	}
	return &Client{inner: c, language: language, voice: voice}, nil
}

func (c *Client) Close() error {
	return c.inner.Close()
}

func (c *Client) Synthesize(ctx context.Context, ssml string, rate float64) ([]byte, error) {
	audioCfg := &texttospeechpb.AudioConfig{
		AudioEncoding: texttospeechpb.AudioEncoding_MP3,
	}
	if rate > 0 {
		audioCfg.SpeakingRate = rate
	}

	resp, err := c.inner.SynthesizeSpeech(ctx, &texttospeechpb.SynthesizeSpeechRequest{
		Input: &texttospeechpb.SynthesisInput{
			InputSource: &texttospeechpb.SynthesisInput_Ssml{Ssml: ssml},
		},
		Voice: &texttospeechpb.VoiceSelectionParams{
			LanguageCode: c.language,
			Name:         c.voice,
		},
		AudioConfig: audioCfg,
	})
	if err != nil {
		return nil, fmt.Errorf("synthesizing speech: %w", err)
	}
	return resp.AudioContent, nil
}
