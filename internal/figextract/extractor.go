package figextract

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/b/visionary/internal/pngmeta"
	"google.golang.org/genai"
)

// Mention is one place in the text where the figure is referenced.
type Mention struct {
	Page    int    `json:"page"`
	Context string `json:"context"`
}

// Metadata holds all context extracted for the figure.
type Metadata struct {
	Figure   int       `json:"figure"`
	Page     int       `json:"page"`
	Caption  string    `json:"caption"`
	Mentions []Mention `json:"mentions"`
}

// Result holds the cropped PNG bytes (with embedded iTXt) and structured metadata.
type Result struct {
	PNG      []byte
	Metadata Metadata
}

type boundingBox struct {
	Ymin int `json:"ymin"`
	Xmin int `json:"xmin"`
	Ymax int `json:"ymax"`
	Xmax int `json:"xmax"`
}

// Extract locates figNum in pdfBytes, crops it, embeds metadata in the PNG,
// and returns the result. Requires pdftoppm (poppler-utils) on PATH.
func Extract(ctx context.Context, client *genai.Client, model string, pdfBytes []byte, figNum int) (*Result, error) {
	meta, err := extractMetadata(ctx, client, model, pdfBytes, figNum)
	if err != nil {
		return nil, fmt.Errorf("extracting metadata: %w", err)
	}
	meta.Figure = figNum

	pageImgBytes, err := renderPage(pdfBytes, meta.Page)
	if err != nil {
		return nil, fmt.Errorf("rendering page %d: %w", meta.Page, err)
	}

	bbox, err := extractBBox(ctx, client, model, pageImgBytes, figNum)
	if err != nil {
		return nil, fmt.Errorf("locating figure in page image: %w", err)
	}

	cropped, err := cropImage(pageImgBytes, bbox)
	if err != nil {
		return nil, fmt.Errorf("cropping: %w", err)
	}

	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, cropped); err != nil {
		return nil, fmt.Errorf("encoding PNG: %w", err)
	}

	metaJSON, _ := json.Marshal(meta)

	pngBytes, err := pngmeta.EmbedITXt(pngBuf.Bytes(), "Description", meta.Caption)
	if err == nil {
		pngBytes, err = pngmeta.EmbedITXt(pngBytes, "visionary:metadata", string(metaJSON))
	}
	if err != nil {
		pngBytes = pngBuf.Bytes() // fall back to unembellished PNG
	}

	return &Result{PNG: pngBytes, Metadata: *meta}, nil
}

// extractMetadata asks Gemini to locate the figure in the PDF and return
// page number, caption, and cross-references as JSON.
func extractMetadata(ctx context.Context, client *genai.Client, model string, pdfBytes []byte, figNum int) (*Metadata, error) {
	prompt := fmt.Sprintf(`You are analyzing a scientific paper PDF. Find Figure %d.
Return ONLY a JSON object with this exact structure, no other text:
{
  "page": <1-indexed page number where the figure appears>,
  "caption": "<full caption text of the figure>",
  "mentions": [
    {
      "page": <page number of this mention>,
      "context": "<approximately 100 words of surrounding text from the body of the paper where Figure %d is mentioned>"
    }
  ]
}`, figNum, figNum)

	parts := []*genai.Part{
		{Text: prompt},
		{InlineData: &genai.Blob{MIMEType: "application/pdf", Data: pdfBytes}},
	}

	resp, err := client.Models.GenerateContent(ctx, model, []*genai.Content{{Parts: parts}}, nil)
	if err != nil {
		return nil, err
	}

	raw := responseText(resp)
	raw = strings.TrimSpace(raw)
	// Strip markdown code fences if Gemini wraps in ```json ... ```
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var meta Metadata
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		return nil, fmt.Errorf("parsing Gemini JSON response: %w\nraw: %s", err, raw)
	}
	if meta.Page < 1 {
		return nil, fmt.Errorf("Gemini returned invalid page number %d", meta.Page)
	}
	return &meta, nil
}

// extractBBox asks Gemini for the bounding box of figNum in a rendered page image.
// Returns coordinates normalized to [0, 1000].
func extractBBox(ctx context.Context, client *genai.Client, model string, pageImgBytes []byte, figNum int) (*boundingBox, error) {
	prompt := fmt.Sprintf(`This is a rendered page from a scientific paper. Locate Figure %d.
Return ONLY a JSON object with normalized bounding box coordinates (0 = top/left edge, 1000 = bottom/right edge):
{
  "ymin": <int>,
  "xmin": <int>,
  "ymax": <int>,
  "xmax": <int>
}
Include some padding around the figure and its caption. Return only the JSON, no other text.`, figNum)

	parts := []*genai.Part{
		{Text: prompt},
		{InlineData: &genai.Blob{MIMEType: "image/png", Data: pageImgBytes}},
	}

	resp, err := client.Models.GenerateContent(ctx, model, []*genai.Content{{Parts: parts}}, nil)
	if err != nil {
		return nil, err
	}

	raw := strings.TrimSpace(responseText(resp))
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var bbox boundingBox
	if err := json.Unmarshal([]byte(raw), &bbox); err != nil {
		return nil, fmt.Errorf("parsing bounding box: %w\nraw: %s", err, raw)
	}

	// Expand by 2% on each side to avoid clipping captions and labels.
	const pad = 20 // out of 1000
	bbox.Ymin = clamp(bbox.Ymin-pad, 0, 1000)
	bbox.Xmin = clamp(bbox.Xmin-pad, 0, 1000)
	bbox.Ymax = clamp(bbox.Ymax+pad, 0, 1000)
	bbox.Xmax = clamp(bbox.Xmax+pad, 0, 1000)

	return &bbox, nil
}

// renderPage shells out to pdftoppm to render a single page at 200 DPI.
func renderPage(pdfBytes []byte, page int) ([]byte, error) {
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		return nil, fmt.Errorf("pdftoppm not found — install poppler-utils (e.g. apt install poppler-utils)")
	}

	tmpDir, err := os.MkdirTemp("", "visionary-pdf-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	pdfPath := filepath.Join(tmpDir, "input.pdf")
	if err := os.WriteFile(pdfPath, pdfBytes, 0600); err != nil {
		return nil, err
	}

	outPrefix := filepath.Join(tmpDir, "page")
	pageStr := strconv.Itoa(page)
	out, err := exec.Command("pdftoppm",
		"-r", "200",
		"-f", pageStr, "-l", pageStr,
		"-png",
		pdfPath, outPrefix,
	).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("pdftoppm failed: %s: %w", bytes.TrimSpace(out), err)
	}

	matches, err := filepath.Glob(outPrefix + "-*.png")
	if err != nil || len(matches) == 0 {
		return nil, fmt.Errorf("pdftoppm produced no output for page %d", page)
	}
	return os.ReadFile(matches[0])
}

func cropImage(imgBytes []byte, b *boundingBox) (image.Image, error) {
	src, err := png.Decode(bytes.NewReader(imgBytes))
	if err != nil {
		return nil, err
	}
	bounds := src.Bounds()
	w := bounds.Max.X - bounds.Min.X
	h := bounds.Max.Y - bounds.Min.Y

	x0 := bounds.Min.X + clamp(int(float64(w)*float64(b.Xmin)/1000), 0, w)
	y0 := bounds.Min.Y + clamp(int(float64(h)*float64(b.Ymin)/1000), 0, h)
	x1 := bounds.Min.X + clamp(int(float64(w)*float64(b.Xmax)/1000), 0, w)
	y1 := bounds.Min.Y + clamp(int(float64(h)*float64(b.Ymax)/1000), 0, h)

	if x1 <= x0 || y1 <= y0 {
		return nil, fmt.Errorf("invalid bounding box: (%d,%d)-(%d,%d)", x0, y0, x1, y1)
	}

	rect := image.Rect(0, 0, x1-x0, y1-y0)
	out := image.NewNRGBA(rect)
	draw.Draw(out, rect, src, image.Point{x0, y0}, draw.Src)
	return out, nil
}

func responseText(resp *genai.GenerateContentResponse) string {
	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		return ""
	}
	var sb strings.Builder
	for _, part := range resp.Candidates[0].Content.Parts {
		sb.WriteString(part.Text)
	}
	return sb.String()
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
