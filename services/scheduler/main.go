// The scheduler proposes assignments. The API alone owns durable mutations.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		client := http.Client{Timeout: 2 * time.Second}
		r, e := client.Get("http://127.0.0.1:8082/readyz")
		if e != nil {
			os.Exit(1)
		}
		_ = r.Body.Close()
		if r.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	base, tokenURL := os.Getenv("API_BASE_URL"), os.Getenv("OIDC_TOKEN_URL")
	allowHTTP := os.Getenv("SCHEDULER_ALLOW_HTTP") == "true"
	if validateURL(base, allowHTTP, false) != nil || validateURL(tokenURL, allowHTTP, true) != nil || os.Getenv("OIDC_CLIENT_ID") == "" || os.Getenv("OIDC_CLIENT_SECRET") == "" {
		log.Fatal("Invalid scheduler configuration; explicit service identity and HTTPS URLs required")
	}
	s := newScheduler(base, tokenURL, os.Getenv("OIDC_CLIENT_ID"), os.Getenv("OIDC_CLIENT_SECRET"))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		ready := !s.lastSuccess.IsZero() && time.Since(s.lastSuccess) < 15*time.Second
		s.mu.Unlock()
		if !ready {
			w.WriteHeader(503)
		}
		_, _ = w.Write([]byte("scheduler\n"))
	})
	server := &http.Server{Addr: ":8082", Handler: mux, ReadHeaderTimeout: 2 * time.Second}
	go func() {
		if e := server.ListenAndServe(); e != nil && !errors.Is(e, http.ErrServerClosed) {
			log.Print("Scheduler health listener unavailable")
			cancel()
		}
	}()
	timer := time.NewTicker(100 * time.Millisecond)
	defer timer.Stop()
	var lastError time.Time
	for ctx.Err() == nil {
		if e := s.cycle(ctx); e != nil && time.Since(lastError) > 10*time.Second {
			log.Print("Scheduling unavailable; retrying without changing durable state")
			lastError = time.Now()
		}
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
	}
	shutdown, done := context.WithTimeout(context.Background(), 3*time.Second)
	defer done()
	_ = server.Shutdown(shutdown)
}
