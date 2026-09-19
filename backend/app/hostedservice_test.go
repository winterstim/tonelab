package app

import (
	"path/filepath"
	"testing"
	"time"

	"tonelab/backend/agent"
	"tonelab/backend/app/apptest"
	"tonelab/backend/config"
	"tonelab/backend/search"
)

func TestSignInWithTonelabPointsEverythingAtTheSubscriptionAndSignOutUndoesIt(t *testing.T) {
	server, revoked := apptest.FakeHosted(t)
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
	if !saved.SignedIn() || saved.LLM.BaseURL != server.URL+"/v2" || saved.LLM.APIKey != "tl_secret" || saved.LLM.Model != "tonelab" || saved.Search.Provider != "tonelab" || saved.Hosted.URL != server.URL {
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
	if after.SignedIn() || after.Hosted.APIKey != "" || after.LLM.APIKey != "" || after.LLM.BaseURL == server.URL+"/v2" || after.Search.Provider != "" || after.Hosted.URL != server.URL || revoked.Load() != 1 {
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

// A release is newer only when its numbers say so; a suffix from a dirty
// or untagged build never makes one.
func TestNewerRelease(t *testing.T) {
	cases := []struct {
		tag, current string
		want         bool
	}{
		{"v0.2.0", "v0.1.0", true},
		{"v0.1.0", "v0.1.0", false},
		{"v0.1.0", "v0.2.0", false},
		{"v1.0.0", "v0.9.9", true},
		{"v0.1.1", "v0.1.0-3-gabc123", true},
		{"v0.1.0", "v0.1.0-3-gabc123", false},
		{"garbage", "v0.1.0", false},
		{"v0.2.0", "dev", false},
	}
	for _, c := range cases {
		if got := newer(c.tag, c.current); got != c.want {
			t.Errorf("newer(%q, %q) = %v, want %v", c.tag, c.current, got, c.want)
		}
	}
}
