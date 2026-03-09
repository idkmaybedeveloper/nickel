package api

import (
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/idkmaybedeveloper/nickel/internal/services/tiktok"
)

type Request struct {
	URL             string `json:"url"`
	DownloadMode    string `json:"downloadMode,omitempty"`
	AudioFormat     string `json:"audioFormat,omitempty"`
	FilenameStyle   string `json:"filenameStyle,omitempty"`
	VideoQuality    string `json:"videoQuality,omitempty"`
	AllowH265       bool   `json:"allowH265,omitempty"`
	TiktokFullAudio bool   `json:"tiktokFullAudio,omitempty"`
}

type Handler struct {
	tiktok  *tiktok.Service
	baseURL string
}

func NewHandler(userAgent, baseURL string) *Handler {
	return &Handler{
		tiktok:  tiktok.NewService(userAgent),
		baseURL: baseURL,
	}
}

func (h *Handler) HandlePost(c *fiber.Ctx) error {
	var req Request
	if err := c.BodyParser(&req); err != nil {
		slog.Error("failed to parse request body", "error", err)
		return c.Status(400).JSON(NewError("error.api.invalid_body", nil))
	}

	if req.URL == "" {
		return c.Status(400).JSON(NewError("error.api.link.missing", nil))
	}

	parsedURL, err := url.Parse(req.URL)
	if err != nil {
		slog.Error("failed to parse URL", "error", err)
		return c.Status(400).JSON(NewError("error.api.link.invalid", nil))
	}

	host := strings.ToLower(parsedURL.Host)

	switch {
	case strings.Contains(host, "tiktok.com") || strings.Contains(host, "tiktok"):
		return h.handleTikTok(c, parsedURL, &req)
	default:
		return c.Status(400).JSON(NewError("error.api.service.unsupported", nil))
	}
}

func (h *Handler) handleTikTok(c *fiber.Ctx, u *url.URL, req *Request) error {
	result, err := h.tiktok.Extract(u, req.DownloadMode == "audio", req.AllowH265, req.TiktokFullAudio)
	if err != nil {
		slog.Error("tiktok extraction failed", "error", err, "url", u.String())
		return c.Status(400).JSON(NewError(err.Error(), map[string]any{"service": "tiktok"}))
	}

	// headers with cookies for proxying
	headers := map[string]string{}
	if result.Cookies != "" {
		headers["Cookie"] = result.Cookies
	}

	// picker response (slideshow)
	if len(result.Images) > 0 {
		items := make([]PickerItem, 0, len(result.Images))
		for i, img := range result.Images {
			// proxy images through tunnel
			filename := fmt.Sprintf("%s_%d.jpg", result.VideoFilename, i+1)
			tunnelURL := createStreamURL(h.baseURL, "tiktok", img, filename, headers)
			items = append(items, PickerItem{
				Type: "photo",
				URL:  tunnelURL,
			})
		}

		var audioTunnelURL string
		if result.AudioURL != "" {
			audioTunnelURL = createStreamURL(h.baseURL, "tiktok", result.AudioURL, result.AudioFilename+".mp3", headers)
		}

		return c.JSON(NewPicker(items, audioTunnelURL, result.AudioFilename))
	}

	// video - proxy through tunnel
	if result.VideoURL != "" {
		tunnelURL := createStreamURL(h.baseURL, "tiktok", result.VideoURL, result.VideoFilename, headers)
		return c.JSON(NewTunnel(tunnelURL, result.VideoFilename))
	}

	// audio - proxy through tunnel
	if result.AudioURL != "" {
		tunnelURL := createStreamURL(h.baseURL, "tiktok", result.AudioURL, result.AudioFilename+".mp3", headers)
		return c.JSON(NewTunnel(tunnelURL, result.AudioFilename))
	}

	return c.Status(400).JSON(NewError("error.api.fetch.empty", nil))
}

func (h *Handler) HandleGet(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"cobalt": fiber.Map{
			"version":  "nickel-0.1.0",
			"services": []string{"tiktok"},
		},
	})
}
