package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

func validateURL(raw string, allowHTTP, allowPath bool) error {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (!allowPath && u.Path != "" && u.Path != "/") || !(u.Scheme == "https" || (allowHTTP && u.Scheme == "http")) {
		return errors.New("invalid service URL; HTTPS required outside explicit development mode")
	}
	return nil
}

type scheduler struct {
	base, tokenURL, clientID, secret string
	client                           *http.Client
	token                            string
	tokenUntil                       time.Time
	mu                               sync.Mutex
	lastSuccess                      time.Time
}

func newScheduler(base, tokenURL, clientID, secret string) *scheduler {
	return &scheduler{base: strings.TrimRight(base, "/"), tokenURL: tokenURL, clientID: clientID, secret: secret, client: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (s *scheduler) serviceToken(ctx context.Context) (string, error) {
	if s.token != "" && time.Now().Before(s.tokenUntil) {
		return s.token, nil
	}
	body := url.Values{"grant_type": {"client_credentials"}, "client_id": {s.clientID}, "client_secret": {s.secret}}
	req, e := http.NewRequestWithContext(ctx, "POST", s.tokenURL, strings.NewReader(body.Encode()))
	if e != nil {
		return "", errors.New("token request invalid")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, e := s.client.Do(req)
	if e != nil {
		return "", errors.New("identity unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", errors.New("service authentication rejected")
	}
	var result struct {
		Token   string `json:"access_token"`
		Expires int    `json:"expires_in"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&result) != nil || result.Token == "" || result.Expires <= 0 {
		return "", errors.New("invalid service token response")
	}
	s.token = result.Token
	s.tokenUntil = time.Now().Add(time.Duration(max(0, result.Expires-30)) * time.Second)
	return s.token, nil
}
func (s *scheduler) request(ctx context.Context, path string, body any, result any) (int, error) {
	token, e := s.serviceToken(ctx)
	if e != nil {
		return 0, e
	}
	method := "GET"
	var reader io.Reader
	if body != nil {
		data, e := json.Marshal(body)
		if e != nil {
			return 0, e
		}
		reader = bytes.NewReader(data)
		method = "POST"
	}
	req, e := http.NewRequestWithContext(ctx, method, s.base+path, reader)
	if e != nil {
		return 0, errors.New("invalid API request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, e := s.client.Do(req)
	if e != nil {
		return 0, errors.New("company API unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode == 401 {
		s.token = ""
		s.tokenUntil = time.Time{}
	}
	if res.StatusCode == 409 {
		return 409, nil
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return res.StatusCode, errors.New("company API rejected request")
	}
	if result != nil && json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(result) != nil {
		return res.StatusCode, errors.New("invalid company API response")
	}
	return res.StatusCode, nil
}
func (s *scheduler) cycle(ctx context.Context) error {
	var snapshot Snapshot
	if _, e := s.request(ctx, "/internal/scheduler/candidates", nil, &snapshot); e != nil {
		return e
	}
	for _, claim := range selectClaims(snapshot) {
		if _, e := s.request(ctx, "/internal/scheduler/claims", claim, nil); e != nil {
			return e
		}
	}
	s.mu.Lock()
	s.lastSuccess = time.Now()
	s.mu.Unlock()
	return nil
}
