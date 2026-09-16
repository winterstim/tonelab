// Package search reaches the web on the agent's behalf. A model never has
// network access of its own; anything it knows about the world beyond the
// project arrives through here, as data, bounded and attributed.
package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Hit struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
	// When the page was written, where the provider says. Web advice ages,
	// and a model told the date can say so.
	Age string `json:"age,omitempty"`
}

// clean strips the markup providers leave in snippets: HTML entities and
// highlight tags are for a browser, and a model reading "&quot;" is reading
// noise.
func clean(text string) string {
	return strings.TrimSpace(html.UnescapeString(tags.ReplaceAllString(text, "")))
}

var tags = regexp.MustCompile(`<[^>]*>`)

type Provider interface {
	Search(ctx context.Context, query string, limit int) ([]Hit, error)
}

// Config is what the user sets. A hosted provider needs a key and a local
// one needs an address; neither is compiled in.
type Config struct {
	Provider string
	APIKey   string
	BaseURL  string
}

var (
	// Covers 403 as well as 401: a local instance answers 403 when its json
	// output is switched off, which is a setup problem and not a key one.
	ErrUnauthorized = errors.New("search: the provider refused the request")
	ErrRateLimited  = errors.New("search: the provider is rate limiting")
	ErrUnavailable  = errors.New("search: the provider is unavailable")
)

// RateLimited carries the wait the provider asked for, when it said.
type RateLimited struct {
	RetryAfter time.Duration
}

func (r RateLimited) Error() string        { return ErrRateLimited.Error() }
func (r RateLimited) Is(target error) bool { return target == ErrRateLimited }

const requestTimeout = 15 * time.Second

// New returns nil, nil when nothing is configured: absent is a valid state,
// and the tool is not offered rather than offered and failing.
func New(cfg Config) (Provider, error) {
	if cfg.Provider == "" {
		return nil, nil
	}
	factory, ok := providers[cfg.Provider]
	if !ok {
		return nil, fmt.Errorf("search: provider %q is not one of: %s", cfg.Provider, strings.Join(Providers(), ", "))
	}
	return factory(cfg)
}

// Providers is what a settings screen offers, read from the registry so the
// list cannot drift from what New accepts.
func Providers() []string {
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

var providers = map[string]func(Config) (Provider, error){
	"brave":   newBrave,
	"searxng": newSearXNG,
}

// fetch is the one HTTP path both providers share, so status handling is
// decided once.
func fetch(ctx context.Context, endpoint string, headers map[string]string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		log.Printf("[search] %s: %v", endpoint, err)
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer response.Body.Close()
	// One line per request, so quota use can be read off the log the way
	// OSC traffic can.
	log.Printf("[search] %s -> HTTP %d", endpoint, response.StatusCode)
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	switch {
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden:
		return nil, ErrUnauthorized
	case response.StatusCode == http.StatusTooManyRequests:
		wait := time.Second
		if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds > 0 {
			wait = time.Duration(seconds) * time.Second
		}
		return nil, RateLimited{RetryAfter: wait}
	case response.StatusCode >= 400:
		return nil, fmt.Errorf("%w: HTTP %d", ErrUnavailable, response.StatusCode)
	}
	return body, nil
}

func bounded(hits []Hit, limit int) []Hit {
	if limit > 0 && len(hits) > limit {
		return hits[:limit]
	}
	return hits
}

type brave struct {
	key  string
	base string
}

const braveDefaultBase = "https://api.search.brave.com/res/v1/web"

func newBrave(cfg Config) (Provider, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("search: brave needs an api key")
	}
	base := cfg.BaseURL
	if base == "" {
		base = braveDefaultBase
	}
	return &brave{key: cfg.APIKey, base: strings.TrimRight(base, "/")}, nil
}

func (b *brave) Search(ctx context.Context, query string, limit int) ([]Hit, error) {
	endpoint := fmt.Sprintf("%s/search?q=%s&count=%d", b.base, url.QueryEscape(query), limit)
	body, err := fetch(ctx, endpoint, map[string]string{"X-Subscription-Token": b.key})
	if err != nil {
		return nil, err
	}
	var decoded struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
				Age         string `json:"page_age"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("%w: unreadable answer", ErrUnavailable)
	}
	hits := make([]Hit, 0, len(decoded.Web.Results))
	for _, r := range decoded.Web.Results {
		hits = append(hits, Hit{Title: clean(r.Title), URL: r.URL, Snippet: clean(r.Description), Age: r.Age})
	}
	return bounded(hits, limit), nil
}

type searxng struct {
	base string
}

func newSearXNG(cfg Config) (Provider, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("search: searxng needs the instance url")
	}
	return &searxng{base: strings.TrimRight(cfg.BaseURL, "/")}, nil
}

func (s *searxng) Search(ctx context.Context, query string, limit int) ([]Hit, error) {
	endpoint := fmt.Sprintf("%s/search?q=%s&format=json", s.base, url.QueryEscape(query))
	body, err := fetch(ctx, endpoint, nil)
	if err != nil {
		return nil, err
	}
	var decoded struct {
		Results []struct {
			Title         string `json:"title"`
			URL           string `json:"url"`
			Content       string `json:"content"`
			PublishedDate string `json:"publishedDate"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("%w: unreadable answer", ErrUnavailable)
	}
	hits := make([]Hit, 0, len(decoded.Results))
	for _, r := range decoded.Results {
		hits = append(hits, Hit{Title: clean(r.Title), URL: r.URL, Snippet: clean(r.Content), Age: r.PublishedDate})
	}
	return bounded(hits, limit), nil
}
