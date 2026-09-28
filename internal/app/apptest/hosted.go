// Package apptest holds fakes of what the app talks to, for tests of every
// surface built on it.
package apptest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// FakeHosted is a hosted service that approves on the second poll and then
// serves the account, as the real one does after the browser leg. Shared by
// every surface that signs in, so they all test against one account of the
// service rather than each against a kinder one.
func FakeHosted(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var polls, revoked atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v2/device/code", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"device_code": "dc", "user_code": "ABCD-EFGH", "verification_uri": "http://x/device", "verification_uri_complete": "http://x/device?code=ABCD-EFGH", "expires_in": 900, "interval": 1})
	})
	mux.HandleFunc("POST /v2/device/token", func(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc("GET /v2/account", keyed(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"email":"ann@example.com","plan":{"code":"solo","active":true},"keys":[{"id":7,"this_device":true}]}`))
	}))
	mux.HandleFunc("GET /v2/usage", keyed(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"usage":{"month":{"used":220,"limit":1500000,"resets_at":"2026-10-01T00:00:00Z"},"day":{"used":220,"limit":150000,"resets_at":"2026-09-19T00:00:00Z"},"searches_used":1,"searches_limit":200}}`))
	}))
	mux.HandleFunc("GET /v2/models", keyed(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":"tonelab"}]}`))
	}))
	mux.HandleFunc("DELETE /v2/account/keys/7", keyed(func(w http.ResponseWriter, r *http.Request) {
		revoked.Add(1)
		w.Write([]byte(`{"status":"revoked"}`))
	}))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, &revoked
}
