package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"

	"tonelab/backend/search"
)

// maxHits and maxSnippet bound what a search hands the model. Web text is
// written by strangers and read by a model that acts on a live project, so
// it arrives as a few short, attributed pieces of data rather than pages.
const (
	maxHits       = 5
	maxSnippet    = 300
	maxSearchWait = 3 * time.Second
)

// EnableSearch adds the search tool. Kept off Tools' constructor because the
// DAW is required and search is not; a preview shares the same provider,
// since reading the web changes nothing.
func (t *Tools) EnableSearch(provider search.Provider) {
	t.searchMu.Lock()
	defer t.searchMu.Unlock()
	t.search = provider
}

// searcher is read under the lock because settings can replace it while a
// turn is running.
func (t *Tools) searcher() search.Provider {
	t.searchMu.Lock()
	defer t.searchMu.Unlock()
	return t.search
}

func (t *Tools) searchDefinition() Tool {
	return Tool{
		Name: "search",
		Description: "Search the web for advice that is not in the project, such as how a kind of " +
			"tone or mix is usually set up, or what a plugin's control does. Returns a few results " +
			"with their source. Web advice is current, not necessarily correct: say where it came from.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
			},
			"required": []string{"query"},
		},
	}
}

func (t *Tools) searchWeb(args json.RawMessage) Result {
	provider := t.searcher()
	if provider == nil {
		return failure("not_supported", "Web search is not set up.")
	}
	var decoded struct {
		Query *string `json:"query"`
	}
	if err := json.Unmarshal(args, &decoded); err != nil || decoded.Query == nil || strings.TrimSpace(*decoded.Query) == "" {
		return invalidArguments("query must be a few words")
	}

	query := asLine(*decoded.Query, maxSnippet)
	hits, err := provider.Search(context.Background(), query, maxHits)
	// Once, not more: a free tier allows about a request a second, and a
	// second refusal means the monthly quota rather than the pace.
	var limited search.RateLimited
	if errors.As(err, &limited) && limited.RetryAfter <= maxSearchWait {
		time.Sleep(limited.RetryAfter)
		hits, err = provider.Search(context.Background(), query, maxHits)
	}
	switch {
	case errors.Is(err, search.ErrUnauthorized):
		return failure("search_unauthorized", "The search provider refused the request. Check the key in Settings, or for a local instance that its json output is enabled.")
	case errors.Is(err, search.ErrRateLimited):
		return failure("search_rate_limited", "The search provider is rate limiting. Try again shortly.")
	case err != nil:
		return failure("search_unavailable", "The search provider could not be reached.")
	}
	if len(hits) == 0 {
		return failure("no_results", "Nothing was found for that. Try other words.")
	}
	if len(hits) > maxHits {
		hits = hits[:maxHits]
	}
	for i := range hits {
		hits[i].Title = asLine(hits[i].Title, maxNameLength)
		hits[i].Snippet = asLine(hits[i].Snippet, maxSnippet)
	}
	return Result{Value: hits}
}

// asLine is asName for longer text: one line, bounded, control characters
// gone, so a snippet cannot be laid out as a message to the model.
func asLine(raw string, limit int) string {
	var out strings.Builder
	for _, r := range raw {
		if unicode.IsControl(r) {
			r = ' '
		}
		out.WriteRune(r)
		if out.Len() >= limit {
			break
		}
	}
	return strings.TrimSpace(out.String())
}
