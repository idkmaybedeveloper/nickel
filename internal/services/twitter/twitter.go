package twitter

import (
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/sonic"
)

const (
	graphqlURL  = "https://api.x.com/graphql/4Siu98E55GquhG52zHdY5w/TweetDetail"
	tokenURL    = "https://api.x.com/1.1/guest/activate.json"
	bearerToken = "Bearer AAAAAAAAAAAAAAAAAAAAANRILgAAAAAAnNwIzUejRCOuH5E6I8xnZz4puTs%3D1Zv7ttfk8LF81IUq16cHjhLTvJu4FA33AGWWjCpTnA" // some kind of wheelchair
)

var tweetFeatures = `{"rweb_video_screen_enabled":false,"payments_enabled":false,"rweb_xchat_enabled":false,"profile_label_improvements_pcf_label_in_post_enabled":true,"rweb_tipjar_consumption_enabled":true,"verified_phone_label_enabled":false,"creator_subscriptions_tweet_preview_api_enabled":true,"responsive_web_graphql_timeline_navigation_enabled":true,"responsive_web_graphql_skip_user_profile_image_extensions_enabled":false,"premium_content_api_read_enabled":false,"communities_web_enable_tweet_community_results_fetch":true,"c9s_tweet_anatomy_moderator_badge_enabled":true,"responsive_web_grok_analyze_button_fetch_trends_enabled":false,"responsive_web_grok_analyze_post_followups_enabled":true,"responsive_web_jetfuel_frame":true,"responsive_web_grok_share_attachment_enabled":true,"articles_preview_enabled":true,"responsive_web_edit_tweet_api_enabled":true,"graphql_is_translatable_rweb_tweet_is_translatable_enabled":true,"view_counts_everywhere_api_enabled":true,"longform_notetweets_consumption_enabled":true,"responsive_web_twitter_article_tweet_consumption_enabled":true,"tweet_awards_web_tipping_enabled":false,"responsive_web_grok_show_grok_translated_post":false,"responsive_web_grok_analysis_button_from_backend":true,"creator_subscriptions_quote_tweet_preview_enabled":false,"freedom_of_speech_not_reach_fetch_enabled":true,"standardized_nudges_misinfo":true,"tweet_with_visibility_results_prefer_gql_limited_actions_policy_enabled":true,"longform_notetweets_rich_text_read_enabled":true,"longform_notetweets_inline_media_enabled":true,"responsive_web_grok_image_annotation_enabled":true,"responsive_web_grok_imagine_annotation_enabled":true,"responsive_web_grok_community_note_auto_translation_is_enabled":false,"responsive_web_enhance_cards_enabled":false}`

var tweetFieldToggles = `{"withArticleRichContentState":true,"withArticlePlainText":false,"withGrokAnalyze":false,"withDisallowedReplyControls":false}`

var tweetIDRegex = regexp.MustCompile(`/status/(\d+)`)

type Service struct {
	client      *http.Client
	userAgent   string
	cachedToken string
	tokenMu     sync.RWMutex
}

type MediaItem struct {
	Type         string
	URL          string
	ThumbnailURL string
	IsGif        bool
}

type Result struct {
	Media    []MediaItem
	Filename string
}

func NewService(userAgent string) *Service {
	return &Service{
		client:    &http.Client{Timeout: 30 * time.Second},
		userAgent: userAgent,
	}
}

func (s *Service) Extract(u *url.URL, index int) (*Result, error) {
	tweetID, err := s.extractTweetID(u)
	if err != nil {
		return nil, err
	}

	slog.Debug("extracted tweet ID", "tweetID", tweetID)

	// get guest token
	guestToken, err := s.getGuestToken(false)
	if err != nil {
		return nil, err
	}

	// try GraphQL API first
	media, err := s.fetchFromGraphQL(tweetID, guestToken)
	if err != nil {
		slog.Debug("graphql failed, trying syndication", "error", err)
		// fallback to syndication API
		media, err = s.fetchFromSyndication(tweetID)
		if err != nil {
			return nil, err
		}
	}

	if len(media) == 0 {
		return nil, fmt.Errorf("error.api.fetch.empty")
	}

	// filter by index if specified
	if index >= 0 && index < len(media) {
		media = []MediaItem{media[index]}
	}

	return &Result{
		Media:    media,
		Filename: fmt.Sprintf("twitter_%s", tweetID),
	}, nil
}

func (s *Service) extractTweetID(u *url.URL) (string, error) {
	matches := tweetIDRegex.FindStringSubmatch(u.Path)
	if len(matches) > 1 {
		return matches[1], nil
	}
	return "", fmt.Errorf("error.api.link.unsupported")
}

func (s *Service) getGuestToken(forceRefresh bool) (string, error) {
	s.tokenMu.RLock()
	if s.cachedToken != "" && !forceRefresh {
		token := s.cachedToken
		s.tokenMu.RUnlock()
		return token, nil
	}
	s.tokenMu.RUnlock()

	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()

	// double-check after acquiring write lock
	if s.cachedToken != "" && !forceRefresh {
		return s.cachedToken, nil
	}

	req, err := http.NewRequest("POST", tokenURL, nil)
	if err != nil {
		return "", fmt.Errorf("error.api.fetch.fail")
	}

	req.Header.Set("User-Agent", s.userAgent)
	req.Header.Set("Authorization", bearerToken)
	req.Header.Set("x-twitter-client-language", "en")
	req.Header.Set("x-twitter-active-user", "yes")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error.api.fetch.fail")
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("error.api.fetch.fail")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error.api.fetch.fail")
	}

	var data map[string]any
	if err := sonic.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("error.api.fetch.fail")
	}

	token, ok := data["guest_token"].(string)
	if !ok {
		return "", fmt.Errorf("error.api.fetch.fail")
	}

	s.cachedToken = token
	slog.Debug("got guest token", "token", token)
	return token, nil
}

func (s *Service) fetchFromGraphQL(tweetID, guestToken string) ([]MediaItem, error) {
	graphqlTweetURL, _ := url.Parse(graphqlURL)

	variables := map[string]any{
		"focalTweetId":                           tweetID,
		"with_rux_injections":                    false,
		"rankingMode":                            "Relevance",
		"includePromotedContent":                 true,
		"withCommunity":                          true,
		"withQuickPromoteEligibilityTweetFields": true,
		"withBirdwatchNotes":                     true,
		"withVoice":                              true,
	}

	variablesJSON, _ := sonic.MarshalString(variables)
	q := graphqlTweetURL.Query()
	q.Set("variables", variablesJSON)
	q.Set("features", tweetFeatures)
	q.Set("fieldToggles", tweetFieldToggles)
	graphqlTweetURL.RawQuery = q.Encode()

	req, err := http.NewRequest("GET", graphqlTweetURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("error.api.fetch.fail")
	}

	req.Header.Set("User-Agent", s.userAgent)
	req.Header.Set("Authorization", bearerToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-guest-token", guestToken)
	req.Header.Set("x-twitter-client-language", "en")
	req.Header.Set("x-twitter-active-user", "yes")
	req.Header.Set("Cookie", fmt.Sprintf("guest_id=%s", url.QueryEscape("v1:"+guestToken)))

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error.api.fetch.fail")
	}
	defer resp.Body.Close()

	// retry with new token if needed
	if resp.StatusCode == 403 || resp.StatusCode == 429 {
		newToken, err := s.getGuestToken(true)
		if err != nil {
			return nil, err
		}
		return s.fetchFromGraphQL(tweetID, newToken)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("error.api.fetch.fail: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error.api.fetch.fail")
	}

	return s.parseGraphQLResponse(body, tweetID)
}

func (s *Service) parseGraphQLResponse(body []byte, tweetID string) ([]MediaItem, error) {
	var data map[string]any
	if err := sonic.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("error.api.fetch.fail")
	}

	// navigate to tweet result
	instructions := getPath(data, "data", "threaded_conversation_with_injections_v2", "instructions")
	if instructions == nil {
		return nil, fmt.Errorf("error.api.fetch.fail")
	}

	instructionsArr, ok := instructions.([]any)
	if !ok {
		return nil, fmt.Errorf("error.api.fetch.fail")
	}

	var tweetResult map[string]any
	for _, insn := range instructionsArr {
		insnMap, ok := insn.(map[string]any)
		if !ok {
			continue
		}
		if insnMap["type"] != "TimelineAddEntries" {
			continue
		}

		entries, ok := insnMap["entries"].([]any)
		if !ok {
			continue
		}

		for _, entry := range entries {
			entryMap, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			if entryMap["entryId"] == fmt.Sprintf("tweet-%s", tweetID) {
				tweetResult, _ = getPath(entryMap, "content", "itemContent", "tweet_results", "result").(map[string]any)
				break
			}
		}
	}

	if tweetResult == nil {
		return nil, fmt.Errorf("error.api.fetch.empty")
	}

	typename, _ := tweetResult["__typename"].(string)

	if typename == "TweetUnavailable" || typename == "TweetTombstone" {
		return nil, fmt.Errorf("error.api.content.post.unavailable")
	}

	if typename != "Tweet" && typename != "TweetWithVisibilityResults" {
		return nil, fmt.Errorf("error.api.content.post.unavailable")
	}

	// get legacy data
	var legacy map[string]any
	if typename == "TweetWithVisibilityResults" {
		legacy, _ = getPath(tweetResult, "tweet", "legacy").(map[string]any)
	} else {
		legacy, _ = tweetResult["legacy"].(map[string]any)
	}

	if legacy == nil {
		return nil, fmt.Errorf("error.api.fetch.empty")
	}

	// check for retweet
	extendedEntities, _ := getPath(legacy, "extended_entities", "media").([]any)
	if extendedEntities == nil {
		// try retweeted status
		extendedEntities, _ = getPath(legacy, "retweeted_status_result", "result", "legacy", "extended_entities", "media").([]any)
	}

	if extendedEntities == nil {
		return nil, fmt.Errorf("error.api.fetch.empty")
	}

	return s.parseMediaEntities(extendedEntities, tweetID)
}

func (s *Service) fetchFromSyndication(tweetID string) ([]MediaItem, error) {
	// generate token like yt-dlp does
	idNum, _ := strconv.ParseFloat(tweetID, 64)
	token := strconv.FormatFloat((idNum/1e15)*math.Pi, 'f', -1, 64)
	token = strings.NewReplacer("0", "", ".", "").Replace(token)

	syndicationURL := fmt.Sprintf("https://cdn.syndication.twimg.com/tweet-result?id=%s&token=%s", tweetID, token)

	req, err := http.NewRequest("GET", syndicationURL, nil)
	if err != nil {
		return nil, fmt.Errorf("error.api.fetch.fail")
	}

	req.Header.Set("User-Agent", s.userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error.api.fetch.fail")
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("error.api.fetch.fail")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error.api.fetch.fail")
	}

	var data map[string]any
	if err := sonic.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("error.api.fetch.fail")
	}

	mediaDetails, ok := data["mediaDetails"].([]any)
	if !ok || len(mediaDetails) == 0 {
		return nil, fmt.Errorf("error.api.fetch.empty")
	}

	return s.parseMediaEntities(mediaDetails, tweetID)
}

func (s *Service) parseMediaEntities(media []any, tweetID string) ([]MediaItem, error) {
	var items []MediaItem

	for i, m := range media {
		mediaMap, ok := m.(map[string]any)
		if !ok {
			continue
		}

		mediaType, _ := mediaMap["type"].(string)

		switch mediaType {
		case "photo":
			mediaURL, _ := mediaMap["media_url_https"].(string)
			if mediaURL == "" {
				continue
			}
			items = append(items, MediaItem{
				Type:         "photo",
				URL:          mediaURL + "?name=4096x4096",
				ThumbnailURL: mediaURL,
			})

		case "video", "animated_gif":
			videoInfo, ok := mediaMap["video_info"].(map[string]any)
			if !ok {
				continue
			}

			variants, ok := videoInfo["variants"].([]any)
			if !ok {
				continue
			}

			// find best quality mp4
			var bestURL string
			var bestBitrate float64

			for _, v := range variants {
				variant, ok := v.(map[string]any)
				if !ok {
					continue
				}

				contentType, _ := variant["content_type"].(string)
				if contentType != "video/mp4" {
					continue
				}

				bitrate, _ := variant["bitrate"].(float64)
				variantURL, _ := variant["url"].(string)

				if variantURL != "" && bitrate >= bestBitrate {
					bestBitrate = bitrate
					bestURL = variantURL
				}
			}

			if bestURL != "" {
				thumbnail, _ := mediaMap["media_url_https"].(string)
				items = append(items, MediaItem{
					Type:         "video",
					URL:          bestURL,
					ThumbnailURL: thumbnail,
					IsGif:        mediaType == "animated_gif",
				})
			}
		}
		_ = i // suppress unused variable warning
	}

	return items, nil
}

// helper to navigate nested maps
func getPath(data any, keys ...string) any {
	current := data
	for _, key := range keys {
		m, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = m[key]
		if current == nil {
			return nil
		}
	}
	return current
}
