package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

type sessionCreateRequest struct {
	Verbose  bool   `json:"verbose"`
	Question string `json:"question,omitempty"`
	Context  string `json:"context,omitempty"`
}

type sessionCreateResponse struct {
	SessionID   string `json:"session_id"`
	Description string `json:"description"`
}

type messageRequest struct {
	Question string `json:"question"`
}

type messageResponse struct {
	Response string `json:"response"`
}

type serverClient struct {
	baseURL string
	token   string
	http    *http.Client
}

func newServerClient(baseURL, token string) *serverClient {
	return &serverClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    http.DefaultClient,
	}
}

func (c *serverClient) auth(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

func (c *serverClient) createSession(ctx context.Context, imgPath string, imgData []byte, mime string, meta sessionCreateRequest) (*sessionCreateResponse, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="image"; filename="%s"`, imgPath))
	h.Set("Content-Type", mime)
	fw, err := mw.CreatePart(h)
	if err != nil {
		return nil, fmt.Errorf("creating image part: %w", err)
	}
	if _, err := fw.Write(imgData); err != nil {
		return nil, fmt.Errorf("writing image: %w", err)
	}

	metaJSON, _ := json.Marshal(meta)
	if err := mw.WriteField("meta", string(metaJSON)); err != nil {
		return nil, fmt.Errorf("writing meta: %w", err)
	}
	mw.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/sessions", &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	c.auth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("creating session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server %d: %s", resp.StatusCode, bytes.TrimSpace(msg))
	}

	var out sessionCreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return &out, nil
}

func (c *serverClient) sendMessage(ctx context.Context, sessionID, question string) (string, error) {
	body, _ := json.Marshal(messageRequest{Question: question})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/v1/sessions/%s/messages", c.baseURL, sessionID),
		bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	c.auth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("sending message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("server %d: %s", resp.StatusCode, bytes.TrimSpace(msg))
	}

	var out messageResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}
	return out.Response, nil
}

func (c *serverClient) deleteSession(sessionID string) {
	req, err := http.NewRequest(http.MethodDelete,
		fmt.Sprintf("%s/v1/sessions/%s", c.baseURL, sessionID), nil)
	if err != nil {
		return
	}
	c.auth(req)
	resp, err := c.http.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

// rcConfig holds optional client defaults from ~/.visionaryrc.
type rcConfig struct {
	Server  string
	Token   string
	TTSRate float64
}

// loadRC parses ~/.visionaryrc as simple key = value pairs.
// Blank lines and lines starting with # are ignored.
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
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		switch k {
		case "server":
			rc.Server = v
		case "token":
			rc.Token = v
		case "tts_rate":
			fmt.Sscanf(v, "%f", &rc.TTSRate)
		}
	}
	return rc
}

func mimeFromPath(path string) string {
	switch {
	case strings.HasSuffix(strings.ToLower(path), ".png"):
		return "image/png"
	case strings.HasSuffix(strings.ToLower(path), ".jpg"),
		strings.HasSuffix(strings.ToLower(path), ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(strings.ToLower(path), ".gif"):
		return "image/gif"
	case strings.HasSuffix(strings.ToLower(path), ".webp"):
		return "image/webp"
	case strings.HasSuffix(strings.ToLower(path), ".bmp"):
		return "image/bmp"
	default:
		return "application/octet-stream"
	}
}

func main() {
	imagePath := flag.String("f", "", "Image file to describe (required)")
	verbose := flag.Bool("v", false, "Detailed bullet-point description")
	question := flag.String("q", "", "Initial question about the plot")
	contextFile := flag.String("context", "", "Path to a context file with additional plot information")
	outputFile := flag.String("o", "description.txt", "Output file for the description")
	tts := flag.Bool("tts", false, "Speak the description aloud")
	ttsRate := flag.Float64("tts-rate", 0, "TTS speaking rate (0.25–2.0, 0 = default)")
	server := flag.String("server", "", "Visionary server URL (env: VISIONARY_SERVER)")
	token := flag.String("token", "", "Bearer token (env: VISIONARY_TOKEN)")
	interactive := flag.Bool("i", false, "Enter interactive session for follow-up questions")

	flag.Parse()

	rc := loadRC()

	serverURL := *server
	if serverURL == "" {
		serverURL = os.Getenv("VISIONARY_SERVER")
	}
	if serverURL == "" {
		serverURL = rc.Server
	}

	serverToken := *token
	if serverToken == "" {
		serverToken = os.Getenv("VISIONARY_TOKEN")
	}
	if serverToken == "" {
		serverToken = rc.Token
	}

	rate := *ttsRate
	if rate == 0 {
		rate = rc.TTSRate
	}

	ttsConf.rate = rate
	ttsConf.proxyURL = serverURL
	ttsConf.proxyToken = serverToken

	if ttsConf.rate != 0 && (ttsConf.rate < 0.25 || ttsConf.rate > 2.0) {
		fmt.Fprintln(os.Stderr, "Error: -tts-rate must be between 0.25 and 2.0 (or 0 for default).")
		os.Exit(1)
	}
	if *imagePath == "" {
		fmt.Fprintln(os.Stderr, "Error: -f (image file) is required.")
		flag.Usage()
		os.Exit(1)
	}
	if serverURL == "" {
		fmt.Fprintln(os.Stderr, "Error: -server or VISIONARY_SERVER is required.")
		os.Exit(1)
	}

	imgData, err := os.ReadFile(*imagePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading image: %v\n", err)
		os.Exit(1)
	}

	var extraContext string
	if *contextFile != "" {
		ctxData, err := os.ReadFile(*contextFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading context file: %v\n", err)
			os.Exit(1)
		}
		extraContext = strings.TrimSpace(string(ctxData))
	}

	sc := newServerClient(serverURL, serverToken)
	ctx := context.Background()

	sess, err := sc.createSession(ctx, *imagePath, imgData, mimeFromPath(*imagePath), sessionCreateRequest{
		Verbose:  *verbose,
		Question: *question,
		Context:  extraContext,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Best-effort cleanup on exit or signal.
	cleanup := func() { sc.deleteSession(sess.SessionID) }
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cleanup()
		os.Exit(0)
	}()
	defer cleanup()

	description := sess.Description

	if *tts {
		if err := speakText(description); err != nil {
			fmt.Fprintf(os.Stderr, "TTS error: %v\n", err)
		}
	} else {
		fmt.Println(description)
	}

	if *outputFile != "" {
		if err := os.WriteFile(*outputFile, []byte(description), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing output file: %v\n", err)
		}
	}

	if !*interactive {
		return
	}

	lastResponse := description
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Fprintln(os.Stderr, "\nInteractive mode. Commands: /tts, /save [file], /quit")
	fmt.Fprint(os.Stderr, "> ")

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			fmt.Fprint(os.Stderr, "> ")
			continue
		}

		switch {
		case line == "/quit" || line == "/q" || line == "/exit":
			return

		case line == "/tts":
			if err := speakText(lastResponse); err != nil {
				fmt.Fprintf(os.Stderr, "TTS error: %v\n", err)
			}

		case strings.HasPrefix(line, "/save"):
			fname := strings.TrimSpace(strings.TrimPrefix(line, "/save"))
			if fname == "" {
				fname = *outputFile
			}
			if err := os.WriteFile(fname, []byte(lastResponse), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "Error saving: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "Saved to %s\n", fname)
			}

		case line == "/help" || line == "/?":
			fmt.Fprintln(os.Stderr, "Commands:")
			fmt.Fprintln(os.Stderr, "  /tts              Speak the last response")
			fmt.Fprintln(os.Stderr, "  <question> /tts   Ask a question and speak the answer")
			fmt.Fprintln(os.Stderr, "  /save [file]      Save last response to file")
			fmt.Fprintln(os.Stderr, "  /quit             Exit interactive mode")
			fmt.Fprintln(os.Stderr, "  /help             Show this help")

		default:
			speakAnswer := strings.HasSuffix(line, "/tts")
			if speakAnswer {
				line = strings.TrimSpace(strings.TrimSuffix(line, "/tts"))
			}

			resp, err := sc.sendMessage(ctx, sess.SessionID, line)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			} else {
				lastResponse = resp
				if speakAnswer {
					if err := speakText(resp); err != nil {
						fmt.Fprintf(os.Stderr, "TTS error: %v\n", err)
					}
				} else {
					fmt.Println(resp)
				}
			}
		}

		fmt.Fprint(os.Stderr, "> ")
	}
}
