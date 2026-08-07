// Copyright 2024 Woodpecker Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package api

import (
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// knownCDNDomains maps CDN hosts to their forge referrer hosts.
// When proxying an avatar from a known CDN, the handler sets the
// Referer header to the forge host so CDNs with hotlink protection
// (防盗链) serve the image.
var knownCDNDomains = map[string]string{
	"cdn-img.gitcode.com": "https://gitcode.com",
	"cdn-img.atomgit.com": "https://atomgit.com",
}

// AvatarProxy proxies an avatar image through the Woodpecker server
// so CDNs with referrer-based hotlink protection can be bypassed.
//
//	@Summary		Proxy an avatar image
//	@Description	Fetches an avatar image server-side with the correct Referer header for CDNs that have hotlink protection.
//	@Router			/avatar-proxy [get]
//	@Param			url	query	string	true	"the avatar URL to proxy"
//	@Success		200	{file}	binary
//	@Failure		400
//	@Failure		502
//	@Tags			General
func AvatarProxy(c *gin.Context) {
	avatarURL := c.Query("url")
	if avatarURL == "" {
		c.String(http.StatusBadRequest, "missing url query parameter")
		return
	}

	parsedURL, err := url.Parse(avatarURL)
	if err != nil {
		c.String(http.StatusBadRequest, "invalid url: %s", err)
		return
	}

	referer := c.Query("referer")
	if referer == "" {
		// Derive the forge referrer from known CDN domains.
		if f, ok := knownCDNDomains[parsedURL.Host]; ok {
			referer = f
		} else {
			// Fall back to the CDN host itself as referrer.
			referer = parsedURL.Scheme + "://" + parsedURL.Host
		}
	}

	// Build the HTTP request with the correct Referer header.
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, avatarURL, nil)
	if err != nil {
		c.String(http.StatusBadRequest, "invalid request: %s", err)
		return
	}
	req.Header.Set("Referer", referer)
	req.Header.Set("User-Agent", "Woodpecker-AvatarProxy/1.0")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Error().Err(err).Str("avatar_url", avatarURL).Msg("failed to proxy avatar")
		c.String(http.StatusBadGateway, "failed to fetch avatar: %s", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Error().Int("status", resp.StatusCode).Str("avatar_url", avatarURL).Msg("avatar proxy returned non-ok status")
		c.String(http.StatusBadGateway, "avatar fetch returned status %d", resp.StatusCode)
		return
	}

	// Copy the Content-Type from the upstream response.
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	c.Header("Content-Type", contentType)

	// Cache the image for 1 hour.
	c.Header("Cache-Control", "public, max-age=3600")

	// Copy the response body.
	_, err = io.Copy(c.Writer, resp.Body)
	if err != nil {
		log.Error().Err(err).Str("avatar_url", avatarURL).Msg("failed to copy avatar response")
	}
}

// IsCDNAvatarURL returns true if the given URL is hosted on a known
// hotlink-protected CDN domain that should be proxied.
func IsCDNAvatarURL(rawURL string) bool {
	if rawURL == "" {
		return false
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	_, ok := knownCDNDomains[parsed.Host]
	return ok
}

// ProxyAvatarURL converts a direct CDN avatar URL into a relative proxy URL
// that routes through the Woodpecker server's AvatarProxy handler.
// It returns the proxy path relative to the server root (e.g., "/api/avatar-proxy?url=...").
func ProxyAvatarURL(rawURL string) string {
	if !IsCDNAvatarURL(rawURL) {
		return rawURL
	}

	referer := ""
	parsed, _ := url.Parse(rawURL)
	if f, ok := knownCDNDomains[parsed.Host]; ok {
		referer = f
	}

	// Build the proxy URL with query parameters.
	v := url.Values{}
	v.Set("url", rawURL)
	if referer != "" {
		v.Set("referer", referer)
	}

	return "/api/avatar-proxy?" + v.Encode()
}

// EnsureProxyAvatarURLs rewrites the avatar_url field in a slice of users
// so that known CDN avatar URLs are replaced with proxy URLs.
func EnsureProxyAvatarURLs[T interface {
	GetAvatarURL() string
	SetAvatarURL(string)
}](items []T) {
	for i, item := range items {
		url := item.GetAvatarURL()
		if IsCDNAvatarURL(url) {
			items[i].SetAvatarURL(ProxyAvatarURL(url))
		}
	}
}
