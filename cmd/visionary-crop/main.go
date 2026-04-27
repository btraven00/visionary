// TODO: add PMC/open-access fetch path — accept -pmc PMC1234567 instead of
// -pdf and pull the figure image + caption/cross-ref metadata directly from
// the PMC OA API (no pdftoppm needed, structured XML metadata available).

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
)

type figureResponse struct {
	Image    []byte          `json:"image"` // base64-decoded by json.Unmarshal
	Metadata json.RawMessage `json:"metadata"`
}

func main() {
	pdfPath := flag.String("pdf", "", "PDF file to extract from (required)")
	figNum := flag.Int("fig", 0, "Figure number to extract (required)")
	outDir := flag.String("o", ".", "Output directory for PNG and JSON sidecar")
	server := flag.String("server", "", "Visionary server URL (env: VISIONARY_SERVER)")
	token := flag.String("token", "", "Bearer token (env: VISIONARY_TOKEN)")

	flag.Parse()

	serverURL := *server
	if serverURL == "" {
		serverURL = os.Getenv("VISIONARY_SERVER")
	}
	if serverURL == "" {
		if rc := loadRC(); rc.Server != "" {
			serverURL = rc.Server
		}
	}
	serverToken := *token
	if serverToken == "" {
		serverToken = os.Getenv("VISIONARY_TOKEN")
	}
	if serverToken == "" {
		if rc := loadRC(); rc.Token != "" {
			serverToken = rc.Token
		}
	}

	if *pdfPath == "" {
		fmt.Fprintln(os.Stderr, "Error: -pdf is required.")
		flag.Usage()
		os.Exit(1)
	}
	if *figNum < 1 {
		fmt.Fprintln(os.Stderr, "Error: -fig must be a positive integer.")
		flag.Usage()
		os.Exit(1)
	}
	if serverURL == "" {
		fmt.Fprintln(os.Stderr, "Error: -server or VISIONARY_SERVER is required.")
		os.Exit(1)
	}

	pdfBytes, err := os.ReadFile(*pdfPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading PDF: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Extracting Figure %d from %s...\n", *figNum, *pdfPath)

	result, err := callServer(serverURL, serverToken, pdfBytes, filepath.Base(*pdfPath), *figNum)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	base := strings.TrimSuffix(filepath.Base(*pdfPath), filepath.Ext(*pdfPath))
	stem := fmt.Sprintf("%s-fig%d", base, *figNum)
	pngPath := filepath.Join(*outDir, stem+".png")
	jsonPath := filepath.Join(*outDir, stem+".json")

	if err := os.MkdirAll(*outDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating output dir: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(pngPath, result.Image, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing PNG: %v\n", err)
		os.Exit(1)
	}

	// Pretty-print JSON sidecar.
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, result.Metadata, "", "  "); err != nil {
		pretty.Write(result.Metadata)
	}
	if err := os.WriteFile(jsonPath, pretty.Bytes(), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing JSON: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Saved: %s\n", pngPath)
	fmt.Fprintf(os.Stderr, "Saved: %s\n", jsonPath)

	// Print caption to stdout.
	var meta struct {
		Caption  string `json:"caption"`
		Page     int    `json:"page"`
		Mentions []struct {
			Page int `json:"page"`
		} `json:"mentions"`
	}
	if err := json.Unmarshal(result.Metadata, &meta); err == nil {
		fmt.Printf("Figure %d (page %d): %s\n", *figNum, meta.Page, meta.Caption)
		fmt.Printf("%d mention(s) found in the text.\n", len(meta.Mentions))
	}
}

func callServer(serverURL, token string, pdfBytes []byte, filename string, figNum int) (*figureResponse, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="pdf"; filename="%s"`, filename))
	h.Set("Content-Type", "application/pdf")
	fw, err := mw.CreatePart(h)
	if err != nil {
		return nil, fmt.Errorf("creating pdf part: %w", err)
	}
	if _, err := fw.Write(pdfBytes); err != nil {
		return nil, fmt.Errorf("writing pdf: %w", err)
	}
	if err := mw.WriteField("figure", fmt.Sprintf("%d", figNum)); err != nil {
		return nil, fmt.Errorf("writing figure field: %w", err)
	}
	mw.Close()

	url := strings.TrimRight(serverURL, "/") + "/v1/figures"
	req, err := http.NewRequest(http.MethodPost, url, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("server request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server %d: %s", resp.StatusCode, bytes.TrimSpace(msg))
	}

	var result figureResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return &result, nil
}

// rcConfig and loadRC mirror the main client — reads ~/.visionaryrc for defaults.
type rcConfig struct {
	Server string
	Token  string
}

func loadRC() rcConfig {
	home, err := os.UserHomeDir()
	if err != nil {
		return rcConfig{}
	}
	data, err := os.ReadFile(home + "/.visionaryrc")
	if err != nil {
		return rcConfig{}
	}
	var rc rcConfig
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "server":
			rc.Server = v
		case "token":
			rc.Token = v
		}
	}
	return rc
}
