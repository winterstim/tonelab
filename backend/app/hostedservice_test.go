package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"tonelab/backend/agent"
	"tonelab/backend/config"
	"tonelab/backend/search"
)

// A hosted service that approves on the second poll and then serves the
// account, as the real one does after the browser leg.
func fakeHosted(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var polls, revoked atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /device/code", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"device_code": "dc", "user_code": "ABCD-EFGH", "verification_uri": "http://x/device", "verification_uri_complete": "http://x/device?code=ABCD-EFGH", "expires_in": 900, "interval": 1})
	})
	mux.HandleFunc("POST /device/token", func(w http.ResponseWriter, r *http.Request) {
		if polls.Add(1) < 2 {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"code":"authorization_pending","message":"Waiting."}}`))
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"key": "tl_secret"})
	})
	keyed := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer tl_secret" || r.Header.Get("X-Tonelab-Device") == "" {
				w.WriteHeader(401)
				w.Write([]byte(`{"error":{"code":"key_required","message":"Sign in from the app to get a key."}}`))
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("GET /account", keyed(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"email":"ann@example.com","plan":{"code":"solo","active":true},"keys":[{"id":7,"this_device":true}]}`))
	}))
	mux.HandleFunc("GET /usage", keyed(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"usage":{"month":{"used":220,"limit":1500000,"resets_at":"2026-10-01T00:00:00Z"},"day":{"used":220,"limit":150000,"resets_at":"2026-09-19T00:00:00Z"},"searches_used":1,"searches_limit":200}}`))
	}))
	mux.HandleFunc("GET /v1/models", keyed(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":"tonelab"}]}`))
	}))
	mux.HandleFunc("DELETE /account/keys/7", keyed(func(w http.ResponseWriter, r *http.Request) {
		revoked.Add(1)
		w.Write([]byte(`{"status":"revoked"}`))
	}))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, &revoked
}

func TestSignInWithTonelabPointsEverythingAtTheSubscriptionAndSignOutUndoesIt(t *testing.T) {
	server, revoked := fakeHosted(t)
	path := filepath.Join(t.TempDir(), "config.json")
	own := config.Config{LLM: config.LLM{BaseURL: "https://api.groq.com/openai/v1", APIKey: "gsk_mine", Model: "openai/gpt-oss-20b"}, DAW: config.DAW{Backend: "reaper", Host: "127.0.0.1", Port: 8000, FeedbackPort: 9000}}
	if err := config.Save(path, own); err != nil {
		t.Fatal(err)
	}
	config.Load(path) // names the install

	live := agent.NewOrchestrator(agent.Config{BaseURL: own.LLM.BaseURL}, nil)
	previews := agent.NewOrchestrator(agent.Config{BaseURL: own.LLM.BaseURL}, nil)
	var applied search.Provider
	var opened string
	svc := NewHostedService(path, live, previews, func(p search.Provider) { applied = p }, func(url string) error { opened = url; return nil })

	if status, _ := svc.Status(); status.SignedIn || status.URL != config.DefaultHostedURL {
		t.Fatalf("before: %+v", status)
	}
	state, err := svc.SignIn(server.URL)
	if err != nil || !state.Running || state.UserCode != "ABCD-EFGH" || opened != "http://x/device?code=ABCD-EFGH" {
		t.Fatalf("%+v %v opened %q", state, err, opened)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		state, _ = svc.State()
		if !state.Running || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !state.Done || state.Error != "" {
		t.Fatalf("after approval: %+v", state)
	}

	saved, _ := config.Load(path)
	if !saved.SignedIn() || saved.LLM.BaseURL != server.URL+"/v1" || saved.LLM.APIKey != "tl_secret" || saved.LLM.Model != "tonelab" || saved.Search.Provider != "tonelab" || saved.Hosted.URL != server.URL {
		t.Fatalf("saved: %+v", saved)
	}
	if applied == nil {
		t.Fatal("search switched to the subscription at once")
	}
	status, _ := svc.Status()
	if !status.SignedIn || status.Email != "ann@example.com" || !status.Active || status.Month.Limit != 1500000 || len(status.Models) != 1 {
		t.Fatalf("status: %+v", status)
	}

	if _, err := svc.SignOut(); err != nil {
		t.Fatal(err)
	}
	after, _ := config.Load(path)
	if after.SignedIn() || after.Hosted.APIKey != "" || after.LLM.APIKey != "" || after.LLM.BaseURL == server.URL+"/v1" || after.Search.Provider != "" || after.Hosted.URL != server.URL || revoked.Load() != 1 {
		t.Fatalf("after sign out: %+v revoked %d", after, revoked.Load())
	}
}

func TestSignInAgainstNothingSaysSo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	config.Save(path, config.Config{LLM: config.LocalLLM(), DAW: config.DAW{Backend: "reaper", Host: "127.0.0.1", Port: 8000, FeedbackPort: 9000}})
	svc := NewHostedService(path, agent.NewOrchestrator(agent.Config{}, nil), agent.NewOrchestrator(agent.Config{}, nil), nil, nil)
	state, _ := svc.SignIn("http://127.0.0.1:1")
	if state.Running || state.Error == "" {
		t.Fatalf("%+v", state)
	}
}
