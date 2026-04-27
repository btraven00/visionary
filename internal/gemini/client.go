package gemini

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/genai"
)

const (
	PromptBase = "You are an assistant to a scientist. " +
		"Your task is to describe plots, with minimal interpretation, unless explicitly asked otherwise. " +
		"The goal is to enable accessibility features in data analysis tools. "

	PromptV1Suffix = "Describe this plot in one clear and concise sentence."
	PromptV2Suffix = "Describe the key characteristics of this plot, focusing on structure, patterns, and notable features. " +
		"Use four or fewer bullet points for your description. " +
		"Enclose answer in <speak> tags, and use basic SSML tags to improve generation, but avoid html tags and <break> in particular."
)

func MIMEFromPath(path string) string {
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".png"):
		return "image/png"
	case strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(lower, ".gif"):
		return "image/gif"
	case strings.HasSuffix(lower, ".webp"):
		return "image/webp"
	case strings.HasSuffix(lower, ".bmp"):
		return "image/bmp"
	default:
		return "application/octet-stream"
	}
}

func NewClient(ctx context.Context, apiKey string) (*genai.Client, error) {
	return genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
}

func Describe(ctx context.Context, client *genai.Client, model, prompt string, imageBytes []byte, imageMIME string) (string, error) {
	parts := []*genai.Part{
		{Text: prompt},
		{InlineData: &genai.Blob{MIMEType: imageMIME, Data: imageBytes}},
	}

	resp, err := client.Models.GenerateContent(ctx, model, []*genai.Content{
		{Parts: parts},
	}, nil)
	if err != nil {
		return "", err
	}

	var text string
	if resp != nil && len(resp.Candidates) > 0 && resp.Candidates[0].Content != nil {
		for _, part := range resp.Candidates[0].Content.Parts {
			if part.Text != "" {
				text += part.Text
			}
		}
	}
	if text == "" {
		return "", fmt.Errorf("no response generated")
	}
	return text, nil
}

// FollowUp builds a follow-up prompt given previous context and a new question.
func FollowUp(ctx context.Context, client *genai.Client, model, promptBase, extraContext, lastResponse, question string, imageBytes []byte, imageMIME string) (string, error) {
	prompt := promptBase + extraContext +
		" Previous description: " + lastResponse +
		" User question: " + question
	return Describe(ctx, client, model, prompt, imageBytes, imageMIME)
}
