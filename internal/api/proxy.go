package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
)

const (
	streamLifespan = 30 * time.Minute
	secretKey      = "nickel-stream-secret" //FIXME: move to config
)

type streamData struct {
	URL       string
	Filename  string
	Service   string
	Headers   map[string]string
	ExpiresAt time.Time
}

var (
	streamCache = make(map[string]*streamData)
	streamMu    sync.RWMutex
)

// service-specific headers for proxying
var serviceHeaders = map[string]map[string]string{
	"tiktok": {
		"Referer": "https://www.tiktok.com/",
	},
	"twitter": {
		"Referer": "https://twitter.com/",
	},
}

func createStreamURL(baseURL, service, videoURL, filename string, headers map[string]string) string {
	streamID := generateStreamID()
	exp := time.Now().Add(streamLifespan)

	data := &streamData{
		URL:       videoURL,
		Filename:  filename,
		Service:   service,
		Headers:   headers,
		ExpiresAt: exp,
	}

	streamMu.Lock()
	streamCache[streamID] = data
	streamMu.Unlock()

	// create signature
	sig := signStream(streamID, exp.Unix())

	return fmt.Sprintf("%s/tunnel?id=%s&exp=%d&sig=%s", baseURL, streamID, exp.Unix(), sig)
}

func generateStreamID() string {
	b := make([]byte, 16)
	for i := range b {
		b[i] = byte(time.Now().UnixNano() >> (i * 8))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func signStream(id string, exp int64) string {
	h := hmac.New(sha256.New, []byte(secretKey))
	h.Write([]byte(fmt.Sprintf("%s:%d", id, exp)))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func verifySignature(id string, exp int64, sig string) bool {
	expected := signStream(id, exp)
	return hmac.Equal([]byte(expected), []byte(sig))
}

func (h *Handler) HandleTunnel(c *fiber.Ctx) error {
	id := c.Query("id")
	expStr := c.Query("exp")
	sig := c.Query("sig")

	if id == "" || expStr == "" || sig == "" {
		return c.SendStatus(400)
	}

	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return c.SendStatus(400)
	}

	// verify signature
	if !verifySignature(id, exp, sig) {
		slog.Debug("invalid signature", "id", id)
		return c.SendStatus(401)
	}

	// check expiration
	if time.Now().Unix() > exp {
		slog.Debug("stream expired", "id", id)
		return c.SendStatus(410)
	}

	// get stream data
	streamMu.RLock()
	data, exists := streamCache[id]
	streamMu.RUnlock()

	if !exists {
		slog.Debug("stream not found", "id", id)
		return c.SendStatus(404)
	}

	// proxy the request
	return proxyStream(c, data)
}

func proxyStream(c *fiber.Ctx, data *streamData) error {
	client := &http.Client{
		Timeout: 5 * time.Minute,
	}

	req, err := http.NewRequest("GET", data.URL, nil)
	if err != nil {
		slog.Error("failed to create proxy request", "error", err)
		return c.SendStatus(500)
	}

	// set default user agent
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	// set service-specific headers
	if headers, ok := serviceHeaders[data.Service]; ok {
		for k, v := range headers {
			req.Header.Set(k, v)
		}
	}

	// set custom headers from stream data
	for k, v := range data.Headers {
		req.Header.Set(k, v)
	}

	// forward range header if present
	if rangeHeader := c.Get("Range"); rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}

	resp, err := client.Do(req)
	if err != nil {
		slog.Error("proxy request failed", "error", err, "url", data.URL)
		return c.SendStatus(502)
	}
	defer resp.Body.Close()

	// set response headers
	c.Set("Content-Type", resp.Header.Get("Content-Type"))
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		c.Set("Content-Length", cl)
	}
	if ar := resp.Header.Get("Accept-Ranges"); ar != "" {
		c.Set("Accept-Ranges", ar)
	}
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		c.Set("Content-Range", cr)
	}

	// set filename
	if data.Filename != "" {
		// escape filename for Content-Disposition
		escaped := strings.ReplaceAll(data.Filename, `"`, `\"`)
		c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, escaped))
	}

	c.Status(resp.StatusCode)

	// stream the response
	_, err = io.Copy(c.Response().BodyWriter(), resp.Body)
	if err != nil {
		slog.Debug("error streaming response", "error", err)
	}

	return nil
}

// cleanup expired streams periodically
func init() {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		for range ticker.C {
			now := time.Now()
			streamMu.Lock()
			for id, data := range streamCache {
				if now.After(data.ExpiresAt) {
					delete(streamCache, id)
				}
			}
			streamMu.Unlock()
		}
	}()
}
