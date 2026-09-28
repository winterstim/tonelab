package search

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func servePage(t *testing.T, kind, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", kind)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

// The test server listens on loopback, which ReadPage refuses, so the
// loopback rule alone is lifted for the call; private and link-local
// addresses stay refused, which the redirect test relies on.
func readLocal(t *testing.T, address string, limit int) (string, error) {
	t.Helper()
	loopbackAllowed = true
	t.Cleanup(func() { loopbackAllowed = false })
	return ReadPage(context.Background(), address, limit)
}

func TestReadPageKeepsTheArticleAndDropsTheChrome(t *testing.T) {
	page := `<html><head><title>Amp</title><style>p{}</style><script>alert(1)</script></head>
	<body><nav>Home Products</nav><header>Site</header>
	<article><h1>Reverb</h1><p>Turn the <b>mix</b> to 30&percnt; for a room.</p><p>Decay under 2s.</p></article>
	<footer>Copyright</footer></body></html>`
	server := servePage(t, "text/html; charset=utf-8", page)
	text, err := readLocal(t, server.URL, 4000)
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"alert", "p{}", "Home Products", "Site", "Copyright", "<b>"} {
		if strings.Contains(text, gone) {
			t.Errorf("%q should have been stripped from %q", gone, text)
		}
	}
	if !strings.Contains(text, "Reverb Turn the mix to 30% for a room. Decay under 2s.") {
		t.Errorf("article text lost: %q", text)
	}
}

func TestReadPageBoundsTheText(t *testing.T) {
	server := servePage(t, "text/plain", strings.Repeat("word ", 5000))
	text, err := readLocal(t, server.URL, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(text)) != 100 {
		t.Fatalf("got %d runes, want 100", len([]rune(text)))
	}
}

func TestReadPageRefusesWhatIsNotText(t *testing.T) {
	server := servePage(t, "application/pdf", "%PDF-1.4")
	if _, err := readLocal(t, server.URL, 100); !errors.Is(err, ErrUnreadablePage) {
		t.Fatalf("got %v, want ErrUnreadablePage", err)
	}
}

func TestReadPageRefusesPrivateAddresses(t *testing.T) {
	for _, address := range []string{
		"http://localhost/x", "http://127.0.0.1/x", "http://10.0.0.5/x", "http://192.168.1.1/x",
		"http://[::1]/x", "http://printer.local/x", "file:///etc/hosts", "ftp://example.com/x", "not a url",
	} {
		if _, err := ReadPage(context.Background(), address, 100); !errors.Is(err, ErrBadURL) {
			t.Errorf("%s: got %v, want ErrBadURL", address, err)
		}
	}
}

func TestReadPageRefusesRedirectToPrivateAddress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data", http.StatusFound)
	}))
	defer server.Close()
	if _, err := readLocal(t, server.URL, 100); err == nil {
		t.Fatal("a redirect to a link-local address should be refused")
	}
}

func TestReadPageReportsHTTPFailure(t *testing.T) {
	server := servePage(t, "text/html", "")
	server.Config.Handler = http.NotFoundHandler()
	if _, err := readLocal(t, server.URL, 100); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("got %v, want ErrUnavailable", err)
	}
}
