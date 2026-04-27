package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/b/visionary/internal/config"
	"github.com/b/visionary/internal/gemini"
	"github.com/b/visionary/internal/session"
	"github.com/b/visionary/internal/tts"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"golang.org/x/crypto/acme/autocert"
)

const maxImageSize = 20 << 20 // 20 MB

type sessionMeta struct {
	Verbose  bool   `json:"verbose"`
	Question string `json:"question"`
	Context  string `json:"context"`
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

type synthesizeRequest struct {
	SSML         string  `json:"ssml"`
	SpeakingRate float64 `json:"speaking_rate"`
}

func main() {
	configPath := flag.String("config", "/etc/visionary/config.json", "Path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}
	if len(cfg.Tokens) == 0 {
		if t := os.Getenv("VISIONARY_TOKEN"); t != "" {
			cfg.Tokens = append(cfg.Tokens, config.Token{Token: t, Label: "env"})
			log.Println("no config tokens found, using VISIONARY_TOKEN env var")
		} else {
			log.Fatal("no tokens configured: set tokens in config file or VISIONARY_TOKEN env var")
		}
	}

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		log.Fatal("GEMINI_API_KEY must be set")
	}

	ctx := context.Background()

	geminiClient, err := gemini.NewClient(ctx, apiKey)
	if err != nil {
		log.Fatalf("creating Gemini client: %v", err)
	}

	ttsClient, err := tts.NewClient(ctx, cfg.TTS.Language, cfg.TTS.Voice)
	if err != nil {
		log.Fatalf("creating TTS client: %v", err)
	}
	defer ttsClient.Close()

	store := session.NewStore(cfg.SessionTTL.Duration, cfg.SessionIdle.Duration)

	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	e.GET("/health", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	v1 := e.Group("/v1")
	v1.Use(tokenAuth(cfg.Tokens))

	v1.POST("/sessions", func(c echo.Context) error {
		label := c.Get("token_label").(string)

		if err := c.Request().ParseMultipartForm(maxImageSize); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid multipart form")
		}

		imgFile, imgHeader, err := c.Request().FormFile("image")
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "image file required")
		}
		defer imgFile.Close()

		imgBytes, err := io.ReadAll(io.LimitReader(imgFile, maxImageSize+1))
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "reading image")
		}
		if int64(len(imgBytes)) > maxImageSize {
			return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "image too large (max 20 MB)")
		}

		var meta sessionMeta
		if s := c.FormValue("meta"); s != "" {
			if err := json.Unmarshal([]byte(s), &meta); err != nil {
				return echo.NewHTTPError(http.StatusBadRequest, "invalid meta JSON")
			}
		}

		mime := imgHeader.Header.Get("Content-Type")
		if mime == "" {
			mime = gemini.MIMEFromPath(imgHeader.Filename)
		}

		var promptSuffix string
		if meta.Verbose {
			promptSuffix = gemini.PromptV2Suffix
		} else {
			promptSuffix = gemini.PromptV1Suffix
		}
		promptBase := gemini.PromptBase + promptSuffix
		if meta.Context != "" {
			promptBase += " Additional context: " + meta.Context
		}

		initialPrompt := promptBase
		if meta.Question != "" {
			initialPrompt += " The user has the following question about the plot: " + meta.Question
		}

		description, err := gemini.Describe(c.Request().Context(), geminiClient, cfg.Gemini.Model, initialPrompt, imgBytes, mime)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadGateway, fmt.Sprintf("Gemini error: %v", err))
		}

		sess := store.Create(label, imgBytes, mime, promptBase)
		sess.LastResponse = description

		return c.JSON(http.StatusOK, sessionCreateResponse{
			SessionID:   sess.ID,
			Description: description,
		})
	})

	v1.POST("/sessions/:id/messages", func(c echo.Context) error {
		label := c.Get("token_label").(string)
		sessionID := c.Param("id")

		sess := store.Get(sessionID, label)
		if sess == nil {
			return echo.NewHTTPError(http.StatusNotFound, "session not found")
		}

		var req messageRequest
		if err := c.Bind(&req); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
		}
		if req.Question == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "question is required")
		}

		sess.Mu.Lock()
		lastResponse := sess.LastResponse
		imgBytes := sess.ImageBytes
		imageMIME := sess.ImageMIME
		promptBase := sess.PromptBase
		sess.Mu.Unlock()

		prompt := promptBase +
			" Previous description: " + lastResponse +
			" User question: " + req.Question

		response, err := gemini.Describe(c.Request().Context(), geminiClient, cfg.Gemini.Model, prompt, imgBytes, imageMIME)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadGateway, fmt.Sprintf("Gemini error: %v", err))
		}

		sess.Mu.Lock()
		sess.LastResponse = response
		sess.LastUsedAt = time.Now()
		sess.Mu.Unlock()

		return c.JSON(http.StatusOK, messageResponse{Response: response})
	})

	v1.DELETE("/sessions/:id", func(c echo.Context) error {
		label := c.Get("token_label").(string)
		store.Delete(c.Param("id"), label)
		return c.NoContent(http.StatusNoContent)
	})

	v1.POST("/synthesize", func(c echo.Context) error {
		var req synthesizeRequest
		if err := c.Bind(&req); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
		}
		if req.SSML == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "ssml is required")
		}

		rate := req.SpeakingRate
		if rate == 0 {
			rate = cfg.TTS.SpeakingRate
		}
		mp3, err := ttsClient.Synthesize(c.Request().Context(), req.SSML, rate)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadGateway, fmt.Sprintf("TTS error: %v", err))
		}
		return c.Blob(http.StatusOK, "audio/mpeg", mp3)
	})

	if cfg.Domain != "" {
		e.AutoTLSManager.HostPolicy = autocert.HostWhitelist(cfg.Domain)
		e.AutoTLSManager.Cache = autocert.DirCache(cfg.CertCache)
		log.Printf("Starting with AutoTLS for %s", cfg.Domain)
		e.Logger.Fatal(e.StartAutoTLS(":443"))
	} else {
		addr := os.Getenv("VISIONARY_ADDR")
		if addr == "" {
			addr = ":8080"
		}
		log.Printf("Starting on %s (no TLS)", addr)
		e.Logger.Fatal(e.Start(addr))
	}
}

// tokenAuth checks the bearer token against the configured allow-list.
func tokenAuth(tokens []config.Token) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			auth := c.Request().Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") {
				return echo.NewHTTPError(http.StatusUnauthorized, "missing bearer token")
			}
			got := strings.TrimPrefix(auth, "Bearer ")
			for _, t := range tokens {
				if subtle.ConstantTimeCompare([]byte(got), []byte(t.Token)) == 1 {
					c.Set("token_label", t.Label)
					return next(c)
				}
			}
			return echo.NewHTTPError(http.StatusUnauthorized, "invalid token")
		}
	}
}
