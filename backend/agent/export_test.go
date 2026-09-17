package agent

import "context"

// The bound, so a test can assert on it without restating the number.
const MaxRememberedForTest = maxRemembered

// SetPageReader replaces the page fetch in tests, which cannot serve a
// public page from a test server.
func SetPageReader(t *Tools, read func(ctx context.Context, address string, limit int) (string, error)) {
	t.readPage = read
}

const MaxPage = maxPage
