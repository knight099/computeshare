package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

//go:embed dashboard.html
var dashboardHTML string

func startDashboard(ctx context.Context, m *SessionManager, port int) (func(), error) {
	listener, e := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if e != nil {
		return nil, errors.New("local dashboard port unavailable")
	}
	origin := "http://" + listener.Addr().String()
	csrf := randomString()
	server := &http.Server{ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 15 * time.Second}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+csrf+"'; style-src 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'")
		if r.Host != listener.Addr().String() {
			http.Error(w, "Invalid local host", 403)
			return
		}
		if r.Method == "GET" && r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, strings.ReplaceAll(dashboardHTML, "__NONCE__", csrf))
			return
		}
		if r.Method == "GET" && r.URL.Path == "/state" {
			s := m.snapshot()
			m.mu.Lock()
			defer m.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"worker_running": m.execution().running(m.root, m.CurrentAttempt), "csrf": csrf, "session": s, "device_id": m.state.Device, "connected": m.Connected, "execution_available": m.execution().available(m.root), "execution_unavailable_reason": m.execution().reason(), "current_job": m.CurrentJob, "last_job": m.LastJob, "job_status": m.JobStatus, "last_result": m.LastResult, "error": m.LastError})
			return
		}
		if r.Method != "POST" || (r.URL.Path != "/start" && r.URL.Path != "/stop") {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Origin") != origin || r.Header.Get("X-OpenCompute-CSRF") != csrf || r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "Local consent request rejected", 403)
			return
		}
		if r.URL.Path == "/stop" {
			s := m.snapshot()
			m.stopLocal("STOPPED")
			// Network is best-effort only; local cancellation has already happened.
			go func() {
				c, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				if token, e := deviceToken(c, m.state); e == nil && s.ID != "" {
					_ = request(c, m.state.Gateway, "/v1/agent/availability-sessions/"+s.ID+"/stop", "POST", token, map[string]string{}, nil)
				}
			}()
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"stopped":true}`)
			return
		}
		var body StartSharing
		decoder := json.NewDecoder(io.LimitReader(r.Body, 2048))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil {
			http.Error(w, "Invalid sharing policy", 400)
			return
		}
		c, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if e := m.start(c, body); e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"started":true}`)
	})
	go func() { _ = server.Serve(listener) }()
	fmt.Println("Contributor dashboard:", origin)
	return func() { _ = server.Close() }, nil
}
