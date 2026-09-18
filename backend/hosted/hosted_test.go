package hosted_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"tonelab/backend/hosted"
)

// fakeService is the hosted API as the client sees it: the device flow
// with an approval that happens after a couple of polls, then the reads.
func fakeService(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var polls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v2/device/code", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["device_id"] != "dev-1" || body["name"] == "" || r.Header.Get("X-Tonelab-Device") != "dev-1" {
			t.Errorf("start: %v %v", body, r.Header)
		}
		json.NewEncoder(w).Encode(map[string]any{"device_code": "dc", "user_code": "ABCD-EFGH", "verification_uri": "http://x/device", "verification_uri_complete": "http://x/device?code=ABCD-EFGH", "expires_in": 900, "interval": 1})
	})
	mux.HandleFunc("POST /v2/device/token", func(w http.ResponseWriter, r *http.Request) {
		n := polls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case n == 1:
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"code":"authorization_pending","message":"Waiting."}}`))
		case n == 2:
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"code":"slow_down","message":"Slow.","retry_after":5}}`))
		default:
			json.NewEncoder(w).Encode(map[string]string{"key": "tl_secret", "prefix": "tl_secret", "name": "MacBook"})
		}
	})
	keyed := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer tl_secret" || r.Header.Get("X-Tonelab-Device") != "dev-1" {
				w.WriteHeader(401)
				w.Write([]byte(`{"error":{"code":"key_required","message":"Sign in from the app to get a key."}}`))
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("GET /v2/account", keyed(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"account_id":1,"email":"ann@example.com","plan":{"code":"solo","active":true,"status":"active"},"keys":[{"id":7,"this_device":true},{"id":8,"this_device":false}]}`))
	}))
	mux.HandleFunc("GET /v2/usage", keyed(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"plan":"solo","usage":{"month":{"kind":"month","used":220,"limit":1500000,"resets_at":"2026-10-01T00:00:00Z"},"day":{"kind":"day","used":220,"limit":150000,"resets_at":"2026-09-19T00:00:00Z"},"searches_used":1,"searches_limit":200}}`))
	}))
	mux.HandleFunc("GET /v2/models", keyed(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"object":"list","data":[{"id":"tonelab","object":"model"}]}`))
	}))
	mux.HandleFunc("DELETE /v2/account/keys/{id}", keyed(func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "7" {
			t.Errorf("revoked key %s, this device is 7", r.PathValue("id"))
		}
		w.Write([]byte(`{"status":"revoked"}`))
	}))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, &polls
}

func TestSignInWaitsForApprovalThenReadsTheAccount(t *testing.T) {
	server, polls := fakeService(t)
	client := hosted.NewClient(server.URL+"/", "dev-1")
	ctx := context.Background()

	started, err := client.Start(ctx, "MacBook, tonelab")
	if err != nil || started.UserCode != "ABCD-EFGH" || !strings.HasSuffix(started.VerifyURLComplete, "ABCD-EFGH") || started.ExpiresIn != 15*time.Minute {
		t.Fatalf("%+v %v", started, err)
	}
	key, err := client.Wait(ctx, started)
	if err != nil || key != "tl_secret" || polls.Load() != 3 {
		t.Fatalf("%q %v polls %d", key, err, polls.Load())
	}
	account, err := client.Account(ctx, key)
	if err != nil || account.Email != "ann@example.com" || !account.Active || account.Plan != "solo" || account.Month.Used != 220 || account.Month.Limit != 1500000 || account.Day.ResetsAt.IsZero() || account.Searches.Limit != 200 || account.KeyID != 7 || len(account.Models) != 1 {
		t.Fatalf("%+v %v", account, err)
	}
	if err := client.SignOut(ctx, key, account.KeyID); err != nil {
		t.Fatal(err)
	}
	var refused *hosted.Error
	if _, err := client.Account(ctx, "tl_wrong"); !errors.As(err, &refused) || refused.Code != "key_required" || refused.Status != 401 {
		t.Fatalf("the service's own refusal comes through: %v", err)
	}
}

func TestUnreachableAndCancelled(t *testing.T) {
	client := hosted.NewClient("http://127.0.0.1:1", "dev-1")
	if _, err := client.Start(context.Background(), "x"); !errors.Is(err, hosted.ErrUnreachable) {
		t.Fatal(err)
	}
	server, _ := fakeService(t)
	slow := hosted.NewClient(server.URL, "dev-1")
	started := hosted.Started{UserCode: "x", ExpiresIn: time.Hour, Interval: time.Hour}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := slow.Wait(ctx, started); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a cancelled wait stops: %v", err)
	}
}
