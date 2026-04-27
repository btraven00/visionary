package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/b/visionary/internal/player"
)

// ttsConf holds TTS settings populated once in main().
var ttsConf struct {
	proxyURL   string
	proxyToken string
	rate       float64
}

var (
	spaceCollapser = regexp.MustCompile(`\s+`)
	markdownBullet = regexp.MustCompile(`(?m)^\s*[\*\-•]\s*`)
	markdownBold   = regexp.MustCompile(`\*{1,3}([^*]+)\*{1,3}`)
	markdownHeader = regexp.MustCompile(`(?m)^#{1,6}\s*`)
)

// cleanForTTS strips markdown artifacts and normalizes whitespace so the
// TTS engine doesn't read out "asterisk" or other markup literally.
// List items get a trailing period so the TTS engine pauses between them.
// If the text already contains <speak> tags (SSML from Gemini), they are
// preserved. Otherwise the text is wrapped in <speak> tags.
func cleanForTTS(text string) string {
	text = markdownBold.ReplaceAllString(text, "$1")
	text = markdownHeader.ReplaceAllString(text, "")

	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = markdownBullet.ReplaceAllString(line, "")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		last := line[len(line)-1]
		if last != '.' && last != '!' && last != '?' && last != ',' && last != ';' {
			line += "."
		}
		lines = append(lines, line)
	}
	text = strings.Join(lines, " ")
	text = spaceCollapser.ReplaceAllString(text, " ")
	text = strings.TrimSpace(text)

	if !strings.Contains(text, "<speak>") {
		text = "<speak>" + text + "</speak>"
	}
	return text
}

// newSynthesizer returns the appropriate synthesizer based on ttsConf.
func newSynthesizer() synthesizer {
	return newProxyTTSClient(ttsConf.proxyURL, ttsConf.proxyToken)
}

func speakText(text string) error {
	ssml := cleanForTTS(text)

	ctx := context.Background()
	synth := newSynthesizer()
	defer synth.Close()

	mp3Data, err := synth.Synthesize(ctx, ssml, ttsConf.rate)
	if err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp("", "visionary-*.mp3")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(mp3Data); err != nil {
		tmpFile.Close()
		return fmt.Errorf("writing audio: %w", err)
	}
	tmpFile.Close()

	playerPath, playerArgs := player.FindPlayer()
	if playerPath == "" {
		outPath := filepath.Join(".", "description.mp3")
		if err := os.WriteFile(outPath, mp3Data, 0644); err != nil {
			return fmt.Errorf("saving MP3: %w", err)
		}
		fmt.Fprintf(os.Stderr, "No audio player found. Saved MP3 to %s\n", outPath)
		return nil
	}

	args := append(playerArgs, tmpPath)
	cmd := exec.Command(playerPath, args...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("playing audio with %s: %w", filepath.Base(playerPath), err)
	}
	return nil
}
