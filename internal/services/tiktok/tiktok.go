package tiktok

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/bytedance/sonic"
)

const shortDomain = "https://vt.tiktok.com/"

var (
	postIDRegex   = regexp.MustCompile(`/(?:video|photo)/(\d+)`)
	shortURLRegex = regexp.MustCompile(`https://[^"]+`)
)

type Service struct {
	client    *http.Client
	userAgent string
}

type Result struct {
	VideoURL      string
	VideoFilename string
	AudioURL      string
	AudioFilename string
	Images        []string
	Cookies       string // cookies to pass when downloading
}

func NewService(userAgent string) *Service {
	return &Service{
		client:    &http.Client{},
		userAgent: userAgent,
	}
}

func getKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func (s *Service) Extract(u *url.URL, audioOnly, allowH265, fullAudio bool) (*Result, error) {
	postID, err := s.resolvePostID(u)
	if err != nil {
		return nil, err
	}

	slog.Debug("resolved post ID", "postID", postID)

	data, cookies, err := s.fetchVideoData(postID)
	if err != nil {
		return nil, err
	}

	return s.parseResult(data, postID, cookies, audioOnly, allowH265, fullAudio)
}

func (s *Service) resolvePostID(u *url.URL) (string, error) {
	// direct link: tiktok.com/@user/video/1234567890
	if matches := postIDRegex.FindStringSubmatch(u.Path); len(matches) > 1 {
		return matches[1], nil
	}

	// short link: vt.tiktok.com/XXXXX or vm.tiktok.com/XXXXX
	if strings.Contains(u.Host, "vt.tiktok") || strings.Contains(u.Host, "vm.tiktok") {
		return s.resolveShortLink(u)
	}

	// try treating the last path segment as short code
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) > 0 {
		shortCode := parts[len(parts)-1]
		if shortCode != "" {
			return s.resolveShortLink(&url.URL{
				Scheme: "https",
				Host:   "vt.tiktok.com",
				Path:   "/" + shortCode,
			})
		}
	}

	return "", fmt.Errorf("error.api.link.unsupported")
}

func (s *Service) resolveShortLink(u *url.URL) (string, error) {
	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("error.api.fetch.fail")
	}

	// use a trimmed user agent to avoid tiktok redirecting to app store
	ua := strings.Split(s.userAgent, " Chrome/1")[0]
	req.Header.Set("User-Agent", ua)

	// dont follow redirects, we just want the Location header or HTML
	s.client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	defer func() { s.client.CheckRedirect = nil }()

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error.api.fetch.fail")
	}
	defer resp.Body.Close()

	// check Location header first
	if loc := resp.Header.Get("Location"); loc != "" {
		if matches := postIDRegex.FindStringSubmatch(loc); len(matches) > 1 {
			return matches[1], nil
		}
	}

	// parse HTML for redirect URL
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error.api.fetch.fail")
	}

	html := string(body)
	if strings.HasPrefix(html, `<a href="https://`) {
		// extract URL from <a href="...">
		start := strings.Index(html, `href="`) + 6
		end := strings.Index(html[start:], `"`)
		if end > 0 {
			extractedURL := html[start : start+end]
			// remove query params
			if idx := strings.Index(extractedURL, "?"); idx > 0 {
				extractedURL = extractedURL[:idx]
			}
			if matches := postIDRegex.FindStringSubmatch(extractedURL); len(matches) > 1 {
				return matches[1], nil
			}
		}
	}

	return "", fmt.Errorf("error.api.fetch.short_link")
}

func (s *Service) fetchVideoData(postID string) (map[string]any, string, error) {
	videoURL := fmt.Sprintf("https://www.tiktok.com/@i/video/%s", postID)

	req, err := http.NewRequest("GET", videoURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("error.api.fetch.fail")
	}

	req.Header.Set("User-Agent", s.userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("error.api.fetch.fail")
	}
	defer resp.Body.Close()

	// collect cookies from response
	var cookies []string
	for _, cookie := range resp.Cookies() {
		cookies = append(cookies, cookie.Name+"="+cookie.Value)
	}
	cookieStr := strings.Join(cookies, "; ")
	slog.Debug("collected cookies", "cookies", cookieStr)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("error.api.fetch.fail")
	}

	html := string(body)

	// extract JSON from __UNIVERSAL_DATA_FOR_REHYDRATION__
	marker := `<script id="__UNIVERSAL_DATA_FOR_REHYDRATION__" type="application/json">`
	start := strings.Index(html, marker)
	if start == -1 {
		return nil, "", fmt.Errorf("error.api.fetch.fail")
	}
	start += len(marker)

	end := strings.Index(html[start:], `</script>`)
	if end == -1 {
		return nil, "", fmt.Errorf("error.api.fetch.fail")
	}

	jsonStr := html[start : start+end]

	var data map[string]any
	if err := sonic.UnmarshalString(jsonStr, &data); err != nil {
		slog.Error("failed to unmarshal tiktok data", "error", err)
		return nil, "", fmt.Errorf("error.api.fetch.fail")
	}

	return data, cookieStr, nil
}

func (s *Service) parseResult(data map[string]any, postID, cookies string, audioOnly, allowH265, fullAudio bool) (*Result, error) {
	defaultScope, ok := data["__DEFAULT_SCOPE__"].(map[string]any)
	if !ok {
		slog.Debug("no __DEFAULT_SCOPE__ found")
		return nil, fmt.Errorf("error.api.fetch.fail")
	}

	videoDetail, ok := defaultScope["webapp.video-detail"].(map[string]any)
	if !ok {
		slog.Debug("no webapp.video-detail found", "keys", getKeys(defaultScope))
		return nil, fmt.Errorf("error.api.fetch.fail")
	}

	// check for error status - only fail if statusMsg is a non-empty string
	if statusMsg, ok := videoDetail["statusMsg"].(string); ok && statusMsg != "" {
		slog.Debug("video has statusMsg", "statusMsg", statusMsg)
		return nil, fmt.Errorf("error.api.content.post.unavailable")
	}

	itemInfo, ok := videoDetail["itemInfo"].(map[string]any)
	if !ok {
		slog.Debug("no itemInfo found", "videoDetail keys", getKeys(videoDetail))
		return nil, fmt.Errorf("error.api.fetch.fail")
	}

	itemStruct, ok := itemInfo["itemStruct"].(map[string]any)
	if !ok {
		slog.Debug("no itemStruct found", "itemInfo keys", getKeys(itemInfo))
		return nil, fmt.Errorf("error.api.fetch.fail")
	}

	// check if content is age-restricted
	if classified, _ := itemStruct["isContentClassified"].(bool); classified {
		return nil, fmt.Errorf("error.api.content.post.age")
	}

	author, _ := itemStruct["author"].(map[string]any)
	if author == nil {
		return nil, fmt.Errorf("error.api.fetch.empty")
	}

	uniqueID, _ := author["uniqueId"].(string)
	filenameBase := fmt.Sprintf("tiktok_%s_%s", uniqueID, postID)

	result := &Result{
		Cookies: cookies,
	}

	// check for image slideshow
	if imagePost, ok := itemStruct["imagePost"].(map[string]any); ok {
		if images, ok := imagePost["images"].([]any); ok && len(images) > 0 {
			for _, img := range images {
				imgMap, ok := img.(map[string]any)
				if !ok {
					continue
				}
				imageURL, ok := imgMap["imageURL"].(map[string]any)
				if !ok {
					continue
				}
				urlList, ok := imageURL["urlList"].([]any)
				if !ok {
					continue
				}
				for _, u := range urlList {
					urlStr, ok := u.(string)
					if ok && strings.Contains(urlStr, ".jpeg?") {
						result.Images = append(result.Images, urlStr)
						break
					}
				}
			}

			// get audio for slideshow
			if music, ok := itemStruct["music"].(map[string]any); ok {
				if playURL, ok := music["playUrl"].(string); ok {
					result.AudioURL = playURL
					result.AudioFilename = filenameBase + "_audio"
				}
			}

			return result, nil
		}
	}

	// get video
	video, ok := itemStruct["video"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("error.api.fetch.empty")
	}

	playAddr, _ := video["playAddr"].(string)

	// try to get H265 if allowed
	if allowH265 {
		if bitrateInfo, ok := video["bitrateInfo"].([]any); ok {
			for _, b := range bitrateInfo {
				bMap, ok := b.(map[string]any)
				if !ok {
					continue
				}
				codecType, _ := bMap["CodecType"].(string)
				if strings.Contains(codecType, "h265") {
					if playAddrObj, ok := bMap["PlayAddr"].(map[string]any); ok {
						if urlList, ok := playAddrObj["UrlList"].([]any); ok && len(urlList) > 0 {
							if h265URL, ok := urlList[0].(string); ok {
								playAddr = h265URL
								break
							}
						}
					}
				}
			}
		}
	}

	if !audioOnly {
		result.VideoURL = playAddr
		result.VideoFilename = filenameBase + ".mp4"
	} else {
		// audio only mode
		audioURL := playAddr
		audioFilename := filenameBase + "_audio"

		if fullAudio {
			// get full original audio from music
			if music, ok := itemStruct["music"].(map[string]any); ok {
				if musicPlayURL, ok := music["playUrl"].(string); ok && musicPlayURL != "" {
					audioURL = musicPlayURL
					audioFilename += "_original"
				}
			}
		}

		result.AudioURL = audioURL
		result.AudioFilename = audioFilename
	}

	return result, nil
}
