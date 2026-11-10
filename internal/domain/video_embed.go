package domain

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
)

const (
	VideoProviderYouTube = "youtube"
	VideoProviderRuTube  = "rutube"
	VideoProviderVimeo   = "vimeo"
	VideoProviderDirect  = "direct"
)

// ParseVideoEmbed inspects a video URL and returns provider, embedURL, videoID, and error.
// Supports YouTube (watch, youtu.be, embed, shorts), RuTube, Vimeo, and direct video links.
func ParseVideoEmbed(rawURL string) (provider string, embedURL string, videoID string, err error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", "", "", fmt.Errorf("video URL cannot be empty")
	}

	u, err := url.Parse(rawURL)
	if err != nil || !ValidPublicationURL(rawURL) {
		return "", "", "", fmt.Errorf("invalid video URL")
	}

	host := strings.ToLower(u.Hostname())
	videoIDPattern := regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

	// YouTube
	if host == "youtube.com" || host == "www.youtube.com" || host == "m.youtube.com" || host == "youtu.be" || host == "www.youtu.be" {
		provider = "youtube"
		if host == "youtu.be" || host == "www.youtu.be" {
			videoID = strings.Trim(u.Path, "/")
		} else if strings.HasPrefix(u.Path, "/embed/") {
			videoID = strings.TrimPrefix(u.Path, "/embed/")
		} else if strings.HasPrefix(u.Path, "/shorts/") {
			videoID = strings.TrimPrefix(u.Path, "/shorts/")
		} else {
			videoID = u.Query().Get("v")
		}
		videoID = strings.Split(videoID, "&")[0]
		videoID = strings.Split(videoID, "?")[0]
		if !videoIDPattern.MatchString(videoID) {
			return "", "", "", fmt.Errorf("could not extract YouTube video ID")
		}
		embedURL = fmt.Sprintf("https://www.youtube.com/embed/%s", videoID)
		return provider, embedURL, videoID, nil
	}

	// RuTube
	if host == "rutube.ru" || host == "www.rutube.ru" {
		provider = "rutube"
		pathParts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(pathParts) >= 2 && (pathParts[0] == "video" || pathParts[0] == "embed" || pathParts[0] == "play") {
			videoID = pathParts[len(pathParts)-1]
		} else if len(pathParts) == 1 {
			videoID = pathParts[0]
		}
		if !videoIDPattern.MatchString(videoID) {
			return "", "", "", fmt.Errorf("could not extract RuTube video ID")
		}
		embedURL = fmt.Sprintf("https://rutube.ru/play/embed/%s", videoID)
		return provider, embedURL, videoID, nil
	}

	// Vimeo
	if host == "vimeo.com" || host == "www.vimeo.com" || host == "player.vimeo.com" {
		provider = "vimeo"
		pathParts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(pathParts) > 0 {
			videoID = pathParts[len(pathParts)-1]
		}
		if !regexp.MustCompile(`^[0-9]{1,20}$`).MatchString(videoID) {
			return "", "", "", fmt.Errorf("could not extract Vimeo video ID")
		}
		embedURL = fmt.Sprintf("https://player.vimeo.com/video/%s", videoID)
		return provider, embedURL, videoID, nil
	}

	// Direct video URL (mp4, webm, ogg, etc.)
	switch strings.ToLower(path.Ext(u.Path)) {
	case ".mp4", ".webm", ".ogg", ".mov":
	default:
		return "", "", "", fmt.Errorf("unsupported direct video URL")
	}
	provider = "direct"
	embedURL = rawURL
	return provider, embedURL, "", nil
}

func ValidPublicationURL(raw string) bool {
	if len(raw) > 8192 || strings.ContainsAny(raw, "\\\r\n\t") {
		return false
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil
}
