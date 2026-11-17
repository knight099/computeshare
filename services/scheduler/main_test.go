package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSelectionUsesFIFOAndRejectsInsufficientCapacityOrTime(t *testing.T) {
	jobs := []Job{{ID: "new", CreatedAt: "2026-11-17T02:00:00Z", TimeoutSeconds: 10, CPUCores: 1, MemoryMB: 64}, {ID: "old", CreatedAt: "2026-11-17T01:00:00Z", TimeoutSeconds: 10, CPUCores: 1, MemoryMB: 64}}
	devices := []Device{{DeviceID: "short", RemainingSeconds: 15, CPUCores: 1, MemoryMB: 64}, {DeviceID: "small", RemainingSeconds: 100, CPUCores: 1, MemoryMB: 32}, {DeviceID: "good", SessionID: "session", Revision: 3, RemainingSeconds: 16, CPUCores: 1, MemoryMB: 64}}
	claims := selectClaims(Snapshot{Jobs: jobs, Devices: devices})
	if len(claims) != 1 || claims[0].JobID != "old" || claims[0].DeviceID != "good" || claims[0].RequestRevision != 3 {
		t.Fatalf("unsafe or unordered selection: %+v", claims)
	}
}
func TestSchedulerObtainsServiceTokenAndClaimsWithoutForwardingRedirects(t *testing.T) {
	claims := 0
	tokens := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			tokens++
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("client_secret") != "secret" {
				t.Error("invalid service authentication")
			}
			_, _ = w.Write([]byte(`{"access_token":"token","expires_in":300}`))
		case "/internal/scheduler/candidates":
			if r.Header.Get("Authorization") != "Bearer token" {
				t.Error("missing bearer")
			}
			_ = json.NewEncoder(w).Encode(Snapshot{Jobs: []Job{{ID: "job", CreatedAt: "2026-11-17T01:00:00Z", TimeoutSeconds: 10, CPUCores: 1, MemoryMB: 64}}, Devices: []Device{{DeviceID: "device", SessionID: "session", Revision: 1, RemainingSeconds: 60, CPUCores: 1, MemoryMB: 64}}})
		case "/internal/scheduler/claims":
			claims++
			w.WriteHeader(409) // stale snapshots are normal
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	s := newScheduler(server.URL, server.URL+"/token", "client", "secret")
	if err := s.cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if claims != 2 || tokens != 1 {
		t.Fatalf("claims=%d tokens=%d", claims, tokens)
	}
	if s.lastSuccess.IsZero() {
		t.Fatal("successful polling did not set readiness")
	}
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("redirect followed") }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer redirect.Close()
	s = newScheduler(redirect.URL, server.URL+"/token", "client", "secret")
	if err := s.cycle(context.Background()); err == nil {
		t.Fatal("redirect accepted")
	}
	if !s.lastSuccess.IsZero() {
		t.Fatal("failed polling marked ready")
	}
}
func TestProductionConfigRejectsInsecureOrAmbiguousURLs(t *testing.T) {
	for _, u := range []string{"http://company.example", "https://user:secret@company.example", "https://company.example/?token=secret"} {
		if err := validateURL(u, false, false); err == nil {
			t.Fatalf("accepted %s", strings.Split(u, "?")[0])
		}
	}
	if err := validateURL("https://company.example/token", false, true); err != nil {
		t.Fatal(err)
	}
}
