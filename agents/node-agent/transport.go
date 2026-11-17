package main

import (
	"context"
	"errors"
	"github.com/gorilla/websocket"
	"log"
	"net/http"
	"net/url"
	"time"
)

type controlAck struct {
	Version  int      `json:"protocol_version"`
	Type     string   `json:"type"`
	Device   string   `json:"device_id"`
	Sequence int64    `json:"sequence"`
	Trace    string   `json:"trace_id"`
	Offer    *HashJob `json:"offer"`
	Cancel   string   `json:"cancel_attempt_id"`
	JobState string   `json:"job_state"`
}
type completion struct {
	result WorkResult
	err    error
}

func connect(ctx context.Context, s State, m *SessionManager) error {
	token, e := deviceToken(ctx, s)
	if e != nil {
		return e
	}
	u, _ := url.Parse(s.Gateway + "/v1/agent/connect")
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second, Proxy: http.ProxyFromEnvironment}
	ws, response, e := dialer.DialContext(ctx, u.String(), http.Header{"Authorization": {"Bearer " + token}})
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if e != nil {
		return errors.New("gateway connection rejected")
	}
	defer ws.Close()
	ws.SetReadLimit(16384)
	caps, e := capabilities()
	if e != nil {
		return e
	}
	if executionAvailable(m.root) {
		caps.Runtimes = []string{"synthetic-hash-v1"}
	}
	connection, cancelConnection := context.WithCancel(ctx)
	defer cancelConnection()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			ws.Close()
		case <-done:
		}
	}()
	results := make(chan completion, 1)
	var active *HashJob
	defer func() {
		cancelConnection()
		m.disconnect()
		if active != nil {
			select {
			case <-results:
				m.finished(*active, WorkResult{}, "CANCELED")
			case <-time.After(3 * time.Second):
			}
		}
	}()
	var sequence int64
	send := func(kind string, attempt map[string]interface{}) (controlAck, error) {
		trace := randomID()
		message := map[string]interface{}{"protocol_version": 1, "type": kind, "device_id": s.Device, "sequence": sequence, "trace_id": trace}
		if kind == "HELLO" {
			message["capabilities"] = caps
		}
		session := m.snapshot()
		if session.ID != "" && session.Status != "STARTING" {
			message["session"] = map[string]string{"id": session.ID, "status": session.Status}
		}
		if attempt != nil {
			message["attempt"] = attempt
		}
		_ = ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if e := ws.WriteJSON(message); e != nil {
			return controlAck{}, errors.New("gateway send failed")
		}
		_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
		var ack controlAck
		if e := ws.ReadJSON(&ack); e != nil {
			return ack, errors.New("gateway acknowledgement unavailable")
		}
		if ack.Version != 1 || ack.Type != "ACK" || ack.Device != s.Device || ack.Sequence != sequence || ack.Trace != trace {
			return ack, errors.New("invalid acknowledgement")
		}
		sequence++
		if active != nil && ack.Cancel == active.AttemptID {
			m.mu.Lock()
			if m.cancel != nil {
				m.cancel()
			}
			m.mu.Unlock()
		}
		return ack, nil
	}
	if _, e = send("HELLO", nil); e != nil {
		return e
	}
	m.mu.Lock()
	m.Connected = true
	m.mu.Unlock()
	log.Println("Connected to company gateway; explicit local sharing consent required")
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	lastSession := ""
	fields := func(j HashJob) map[string]interface{} {
		return map[string]interface{}{"id": j.AttemptID, "epoch": j.Epoch}
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case outcome := <-results:
			if active == nil {
				continue
			}
			j := *active
			values := fields(j)
			kind := "JOB_COMPLETE"
			if outcome.err != nil {
				kind = "JOB_FAIL"
			} else {
				values["result"] = outcome.result.Result
				values["usage"] = map[string]uint64{"cpu_usage_usec": outcome.result.CPUUsec, "memory_peak_bytes": outcome.result.MemoryBytes}
			}
			ack, e := send(kind, values)
			status := "FAILED"
			if e == nil && ack.JobState == "SUCCEEDED" {
				status = "VERIFIED"
			}
			m.finished(j, outcome.result, status)
			active = nil
			if e != nil {
				return e
			}
		case <-heartbeat.C:
			if _, e := send("HEARTBEAT", nil); e != nil {
				return e
			}
		case <-ticker.C:
			session := m.snapshot()
			identity := session.ID + ":" + session.Status
			if session.ID == "" || session.Status == "STARTING" {
				continue
			}
			if session.Status != "ACTIVE" && identity == lastSession {
				continue
			}
			lastSession = identity
			var values map[string]interface{}
			if active != nil {
				values = fields(*active)
			}
			ack, e := send("WORK_REQUEST", values)
			if e != nil {
				return e
			}
			if ack.Offer == nil || active != nil {
				continue
			}
			j := *ack.Offer
			remaining := time.Until(j.LeaseExpires)
			if limit := time.Duration(j.Timeout) * time.Second; limit < remaining {
				remaining = limit
			}
			if remaining <= 0 {
				continue
			}
			workerCtx, cancel := context.WithTimeout(connection, remaining)
			if !m.begin(j, cancel) {
				cancel()
				_, _ = send("JOB_FAIL", fields(j))
				continue
			}
			active = &j
			accepted, e := send("JOB_ACCEPT", fields(j))
			if e != nil {
				cancel()
				m.finished(j, WorkResult{}, "FAILED")
				active = nil
				return e
			}
			if accepted.Cancel != "" {
				cancel()
				m.finished(j, WorkResult{}, "CANCELED")
				active = nil
				continue
			}
			started, e := send("JOB_START", fields(j))
			if e != nil {
				cancel()
				m.finished(j, WorkResult{}, "FAILED")
				active = nil
				return e
			}
			if started.Cancel != "" {
				cancel()
				m.finished(j, WorkResult{}, "CANCELED")
				active = nil
				continue
			}
			go func(job HashJob, p ResourcePolicy) {
				defer cancel()
				result, e := runLimited(workerCtx, m.root, job, p)
				results <- completion{result, e}
			}(j, session.Policy)
		}
	}
}
