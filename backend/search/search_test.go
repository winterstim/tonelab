package search_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"tonelab/backend/search"
)

// Each provider is held to the same shape of answer, so the tool above
// cannot tell them apart. The bodies are the providers' documented ones.
func TestProvidersAnswerAlike(t *testing.T) {
	cases := []struct {
		provider string
		body     string
		wantKey  string
	}{
		{"brave", `{"web":{"results":[{"title":"Amp settings","url":"https://a.example","description":"Turn the <strong>gain</strong> down.","page_age":"2012-09-11T18:58:15"}]}}`, "X-Subscription-Token"},
		{"searxng", `{"results":[{"title":"Amp settings","url":"https://a.example","content":"Turn the gain down.","publishedDate":"2012-09-11T18:58:15"}]}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			var got *http.Request
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(tc.body))
			}))
			defer server.Close()

			provider, err := search.New(search.Config{Provider: tc.provider, APIKey: "k", BaseURL: server.URL})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			hits, err := provider.Search(context.Background(), "amp gain", 5)
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if len(hits) != 1 || hits[0].Title != "Amp settings" || hits[0].URL != "https://a.example" || hits[0].Snippet != "Turn the gain down." || hits[0].Age != "2012-09-11T18:58:15" {
				t.Fatalf("unexpected hits: %+v", hits)
			}
			if got.URL.Query().Get("q") != "amp gain" {
				t.Errorf("query not sent: %s", got.URL.RawQuery)
			}
			if tc.wantKey != "" && got.Header.Get(tc.wantKey) != "k" {
				t.Errorf("key not sent in %s", tc.wantKey)
			}
			if tc.wantKey == "" && got.Header.Get("Authorization") != "" {
				t.Errorf("a local instance must not be sent a key")
			}
		})
	}
}

// HTTP failures become the same three sentinels the agent already knows how
// to explain, so a user sees "your key" or "wait", not a status code.
func TestFailuresAreNamed(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   error
	}{
		{401, search.ErrUnauthorized},
		{429, search.ErrRateLimited},
		{503, search.ErrUnavailable},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
		}))
		provider, _ := search.New(search.Config{Provider: "brave", APIKey: "k", BaseURL: server.URL})
		_, err := provider.Search(context.Background(), "x", 3)
		if !errors.Is(err, tc.want) {
			t.Errorf("status %d: expected %v, got %v", tc.status, tc.want, err)
		}
		server.Close()
	}
}

// Not configured is a state, not an error: the tool is simply absent.
func TestNoProviderMeansNoSearch(t *testing.T) {
	provider, err := search.New(search.Config{})
	if err != nil || provider != nil {
		t.Fatalf("expected nil provider and nil error, got %v, %v", provider, err)
	}
	if _, err := search.New(search.Config{Provider: "bing"}); err == nil {
		t.Fatal("an unknown provider must be refused, naming the known ones")
	}
	if _, err := search.New(search.Config{Provider: "brave"}); err == nil {
		t.Fatal("a hosted provider without a key must be refused")
	}
	if _, err := search.New(search.Config{Provider: "searxng"}); err == nil {
		t.Fatal("a local provider without a URL must be refused")
	}
}

func TestLimitIsHonoured(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[{"title":"a","url":"u"},{"title":"b","url":"u"},{"title":"c","url":"u"}]}`))
	}))
	defer server.Close()
	provider, _ := search.New(search.Config{Provider: "searxng", BaseURL: server.URL})
	hits, err := provider.Search(context.Background(), "x", 2)
	if err != nil || len(hits) != 2 {
		t.Fatalf("expected two hits, got %v, %v", hits, err)
	}
}
