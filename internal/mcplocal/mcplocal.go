// Package mcplocal lets several Tonelab processes on one machine share the
// DAW. A DAW sends its feedback to one port, so only one process can hold
// the connection: the window, the command line, or an MCP server a host
// started. The holder publishes an MCP endpoint on this machine, and any
// other MCP server reaches the DAW through it instead of failing to bind.
//
// A holder that is only an MCP server lets go when asked, so opening the
// window while Claude or Codex is running works. A person's own session,
// the window or the interactive command line, never lets go.
package mcplocal

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/winterstim/tonelab/internal/config"
)

// fileName is where the holder says where it is, beside the settings: the
// same user, the same private directory, and gone with them.
const fileName = "daw-holder.json"

// dialTimeout bounds finding the holder. It is on this machine, so an answer
// takes milliseconds; one that does not come is a holder that has gone.
const dialTimeout = 2 * time.Second

// Holder is what the file says. The token keeps other local programs and
// web pages out: only a process that can read our settings can read it.
type Holder struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	// Keeps is true for a person's own session, which does not let go.
	Keeps bool `json:"keeps"`
	PID   int  `json:"pid"`
}

var ErrNoHolder = errors.New("mcplocal: nothing holds the DAW")

// Hold publishes server as the DAW's holder until stop is called. release
// is called when another process needs the DAW, and must have let the DAW
// go when it returns; nil means this holder never lets go.
func Hold(server *mcp.Server, dir string, release func()) (stop func(), err error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	holder := Holder{
		URL:   "http://" + listener.Addr().String(),
		Token: newToken(),
		Keeps: release == nil,
		PID:   os.Getpid(),
	}

	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		// A bridge that goes away without saying so leaves its session
		// here; half an hour is past any pause a person takes.
		SessionTimeout: 30 * time.Minute,
	}))
	released := make(chan struct{})
	mux.HandleFunc("POST /release", func(w http.ResponseWriter, r *http.Request) {
		if release == nil {
			http.Error(w, "this session keeps the DAW", http.StatusConflict)
			return
		}
		select {
		case <-released:
		default:
			close(released)
			// The file goes first, so a process asking where the DAW is
			// is not sent here while the port is being given up.
			remove(dir, holder.Token)
			release()
		}
		w.WriteHeader(http.StatusNoContent)
	})
	httpServer := &http.Server{
		Handler:           http.NewCrossOriginProtection().Handler(authorized(holder.Token, mux)),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go httpServer.Serve(listener)

	body, _ := json.Marshal(holder)
	if err := config.WritePrivate(filepath.Join(dir, fileName), body); err != nil {
		httpServer.Close()
		return nil, err
	}
	return func() {
		remove(dir, holder.Token)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		httpServer.Shutdown(ctx)
	}, nil
}

// authorized refuses a request without the holder's token, in constant
// time so the token cannot be guessed a byte at a time.
func authorized(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(bearer), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Find reads who holds the DAW. A file left by a process that died is
// found here too; Dial is what tells.
func Find(dir string) (Holder, error) {
	body, err := os.ReadFile(filepath.Join(dir, fileName))
	if errors.Is(err, os.ErrNotExist) {
		return Holder{}, ErrNoHolder
	}
	if err != nil {
		return Holder{}, err
	}
	var holder Holder
	if err := json.Unmarshal(body, &holder); err != nil || holder.URL == "" || holder.Token == "" {
		return Holder{}, ErrNoHolder
	}
	return holder, nil
}

// Dial opens a session with the holder's server. A holder that does not
// answer is treated as gone, and its file with it, so the caller can take
// the DAW itself.
func Dial(ctx context.Context, dir string, client *mcp.Client) (*mcp.ClientSession, Holder, error) {
	holder, err := Find(dir)
	if err != nil {
		return nil, Holder{}, err
	}
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	session, err := client.Connect(dialCtx, &mcp.StreamableClientTransport{
		Endpoint:             holder.URL + "/mcp",
		HTTPClient:           &http.Client{Transport: bearer{token: holder.Token}},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		remove(dir, holder.Token)
		return nil, Holder{}, fmt.Errorf("%w: the holder did not answer: %v", ErrNoHolder, err)
	}
	return session, holder, nil
}

// Release asks a holder that is only an MCP server to let the DAW go, and
// returns once it has, so the caller can bind the port. A person's own
// session is never asked; a holder that has gone needs no asking.
func Release(ctx context.Context, dir string) error {
	holder, err := Find(dir)
	if errors.Is(err, ErrNoHolder) {
		return nil
	}
	if err != nil {
		return err
	}
	if holder.Keeps {
		return errors.New("mcplocal: another Tonelab window or command line holds the DAW; close it first")
	}
	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, holder.URL+"/release", nil)
	if err != nil {
		return err
	}
	response, err := (&http.Client{Transport: bearer{token: holder.Token}}).Do(request)
	if err != nil {
		remove(dir, holder.Token)
		return nil
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("mcplocal: the holder refused to let go: %s", response.Status)
	}
	return nil
}

// remove deletes the file only if it is still the one with this token, so
// a holder going away cannot delete the file of the one that replaced it.
func remove(dir, token string) {
	path := filepath.Join(dir, fileName)
	if holder, err := Find(dir); err == nil && holder.Token == token {
		os.Remove(path)
	}
}

type bearer struct{ token string }

func (b bearer) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(request)
}

func newToken() string {
	var raw [32]byte
	rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}
