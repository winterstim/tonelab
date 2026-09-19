package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"tonelab/backend/search"
)

// maxPage bounds what one page hands the model: enough for the section of a
// manual that matters, well short of a whole page of somebody's prose.
// minPage is where a page stops counting as read: a site that draws itself
// in the browser leaves only its title behind, and handing the model the
// word "Reddit" as page text had it reading the same page twice.
const (
	maxPage     = 4000
	minPage     = 200
	maxPageRead = 400000
)

// BeginTurn forgets the pages the previous turn was offered. A page may be
// read only if a search in the same turn returned it, so the set of readable
// URLs is exactly what the model was shown, and page text cannot steer the
// model to a host of its own choosing later.
func (t *Tools) BeginTurn() {
	t.searchMu.Lock()
	defer t.searchMu.Unlock()
	t.offered = nil
	t.searched = nil
	t.read = nil
}

// offer records a search's results as readable, and the query as spent. A
// model that gets the same five hits back and searches again with the same
// words is looping, not looking; seen live, five identical searches in a
// turn and the page never read. The second identical search is refused
// with what to do instead.
func (t *Tools) offer(query string, hits []search.Hit) {
	t.searchMu.Lock()
	defer t.searchMu.Unlock()
	if t.offered == nil {
		t.offered = map[string]struct{}{}
		t.searched = map[string]struct{}{}
	}
	t.searched[strings.ToLower(query)] = struct{}{}
	for _, hit := range hits {
		t.offered[hit.URL] = struct{}{}
	}
}

func (t *Tools) alreadySearched(query string) bool {
	t.searchMu.Lock()
	defer t.searchMu.Unlock()
	_, ok := t.searched[strings.ToLower(query)]
	return ok
}

func (t *Tools) wasOffered(address string) bool {
	t.searchMu.Lock()
	defer t.searchMu.Unlock()
	_, ok := t.offered[address]
	return ok
}

// markRead reports whether the page was already read this turn. The text
// is in the conversation; fetching it again only spends the step budget.
func (t *Tools) markRead(address string) (again bool) {
	t.searchMu.Lock()
	defer t.searchMu.Unlock()
	if t.read == nil {
		t.read = map[string]struct{}{}
	}
	_, again = t.read[address]
	t.read[address] = struct{}{}
	return again
}

func (t *Tools) fetchDefinition() Tool {
	return Tool{
		Name: "fetch_page",
		Description: "Read one page search returned, by its exact url, when the snippet is not enough. " +
			"Returns a few thousand characters, from the start or around the words in 'about'.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url":   map[string]any{"type": "string"},
				"about": map[string]any{"type": "string", "description": "A few words naming the section wanted, optional"},
			},
			"required": []string{"url"},
		},
	}
}

// Page is what fetch_page returns: the text with its source, so the model
// can say where advice came from the way it does for snippets.
type Page struct {
	URL  string `json:"url"`
	Text string `json:"text"`
}

func (t *Tools) fetchPage(args json.RawMessage) Result {
	if t.searcher() == nil {
		return failure("not_supported", "Web search is not set up.")
	}
	var decoded struct {
		URL   *string `json:"url"`
		About string  `json:"about"`
	}
	if err := json.Unmarshal(args, &decoded); err != nil || decoded.URL == nil || strings.TrimSpace(*decoded.URL) == "" {
		return invalidArguments("url must be one of the urls search returned")
	}
	address := strings.TrimSpace(*decoded.URL)
	if !t.wasOffered(address) {
		return failure("url_not_from_search", "Only a url that search returned in this turn can be read. Search first, then give the url exactly as returned.")
	}
	if t.markRead(address) {
		return failure("page_repeated", "That page was already read this turn and its text is above. Read a different result or answer from what you have.")
	}
	read := t.readPage
	if read == nil {
		read = search.ReadPage
	}
	text, err := read(context.Background(), address, maxPageRead)
	switch {
	case errors.Is(err, search.ErrBadURL):
		return failure("url_not_readable", "That address is not a public web page.")
	case errors.Is(err, search.ErrUnreadablePage):
		return failure("page_not_text", "That page is not readable text, such as a PDF or an image.")
	case err != nil:
		return failure("page_unavailable", "The page could not be fetched.")
	case len([]rune(text)) < minPage:
		return failure("page_empty", "That page has almost no readable text; it probably needs a browser to draw it. Read a different result.")
	}
	return Result{Value: Page{URL: address, Text: excerpt(asLine(text, maxPageRead), decoded.About, maxPage)}}
}

// excerpt cuts the page to limit runes around the first place any of the
// words in about occurs, or from the start when none does or none was given.
// A manual's section on one effect sits far past its introduction, and the
// head of the page alone had the model searching again for what it had.
func excerpt(text, about string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	start := 0
	lower := strings.ToLower(text)
	for _, word := range strings.Fields(strings.ToLower(about)) {
		if at := strings.Index(lower, word); at >= 0 {
			start = len([]rune(lower[:at])) - limit/4
			break
		}
	}
	if start < 0 {
		start = 0
	}
	if start+limit > len(runes) {
		start = len(runes) - limit
	}
	return string(runes[start : start+limit])
}
