package search

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// A page is read once, whole, and cut down to text. The caps are tight on
// purpose: a manual page is well under a megabyte, and anything larger is
// not something a model needs in one piece.
const (
	pageMaxBytes = 1 << 20
	pageTimeout  = 5 * time.Second
)

var (
	ErrUnreadablePage = errors.New("search: the page is not readable text")
	ErrBadURL         = errors.New("search: only public http and https pages can be read")
)

// ReadPage downloads one page and returns its readable text, bounded to
// limit runes. Only http and https to a public host: the URLs come from
// search hits, but a hit can point anywhere, and a local address from here
// would reach services the user never meant to expose to the web.
func ReadPage(ctx context.Context, address string, limit int) (string, error) {
	if err := publicHTTP(address); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, pageTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return "", ErrBadURL
	}
	request.Header.Set("Accept", "text/html, text/plain")
	request.Header.Set("User-Agent", "Tonelab")
	response, err := pageClient.Do(request)
	if err != nil {
		log.Printf("[search] page %s: %v", address, err)
		return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer response.Body.Close()
	log.Printf("[search] page %s -> HTTP %d", address, response.StatusCode)
	if response.StatusCode >= 400 {
		return "", fmt.Errorf("%w: HTTP %d", ErrUnavailable, response.StatusCode)
	}
	kind := response.Header.Get("Content-Type")
	if !strings.HasPrefix(kind, "text/html") && !strings.HasPrefix(kind, "text/plain") {
		return "", ErrUnreadablePage
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, pageMaxBytes))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	text := string(body)
	if strings.HasPrefix(kind, "text/html") {
		text = readable(text)
	}
	text = strings.TrimSpace(spaces.ReplaceAllString(text, " "))
	if text == "" {
		return "", ErrUnreadablePage
	}
	if runes := []rune(text); len(runes) > limit {
		text = string(runes[:limit])
	}
	return text, nil
}

// Redirects are re-checked: a public page may bounce to a private address,
// and the first check would have passed it.
var pageClient = &http.Client{
	CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return publicHTTP(request.URL.String())
	},
}

// loopbackAllowed is for tests, which can only serve a page from loopback.
var loopbackAllowed = false

func publicHTTP(address string) error {
	parsed, err := url.Parse(address)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return ErrBadURL
	}
	host := parsed.Hostname()
	if host == "localhost" || strings.HasSuffix(host, ".local") {
		return ErrBadURL
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() && loopbackAllowed {
			return nil
		}
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return ErrBadURL
		}
	}
	return nil
}

// readable keeps what a reader would: whole blocks of chrome go first, then
// every remaining tag, then entities. Block boundaries become spaces so words
// from neighbouring paragraphs do not fuse.
func readable(page string) string {
	page = chrome.ReplaceAllString(page, " ")
	page = blocks.ReplaceAllString(page, " ")
	page = tags.ReplaceAllString(page, "")
	return html.UnescapeString(page)
}

var (
	chrome = regexp.MustCompile(`(?is)<(script|style|noscript|nav|header|footer|aside|svg|template|iframe)\b.*?</\s*(script|style|noscript|nav|header|footer|aside|svg|template|iframe)\s*>`)
	blocks = regexp.MustCompile(`(?i)</?(p|div|br|li|ul|ol|h[1-6]|tr|td|th|table|section|article|blockquote|pre|dd|dt)\b[^>]*>`)
	spaces = regexp.MustCompile(`\s+`)
)
