package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGatewayRejectsRemoteHTTPAndEmbeddedCredentials(t *testing.T) {
	for _, u := range []string{"http://example.com", "https://user:secret@example.com", "file:///tmp/a", "https://example.com/evil", "https://example.com?token=x"} {
		if _, err := validateGateway(u); err == nil {
			t.Fatalf("accepted unsafe gateway %s", u)
		}
	}
	for _, u := range []string{"https://compute.example.org", "http://localhost:8080", "http://127.0.0.1:8080"} {
		if _, err := validateGateway(u); err != nil {
			t.Fatal(err)
		}
	}
}
func TestHTTPDoesNotFollowRedirectsOrLeakBearer(t *testing.T) {
	received := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received = true; w.WriteHeader(200) }))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 307) }))
	defer source.Close()
	err := request(context.Background(), source.URL, "/token", http.MethodPost, "secret", map[string]string{}, &map[string]string{})
	if err == nil || received {
		t.Fatal("followed a redirect with credentials")
	}
}
func TestReconnectWaitHonorsStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	wait(ctx, 30*time.Second)
	if time.Since(start) > time.Second {
		t.Fatal("stop blocked by backoff")
	}
}
