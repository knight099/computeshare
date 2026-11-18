package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// This is a control-protocol fixture, not a substitute for real company OIDC
// or client-OS release acceptance. Windows runs the actual protected adapter;
// Linux uses a cancellable executor (real Linux cgroups have Compose coverage).
func TestNativeControlProtocolConsentCompletionAndOfflineCancellation(t *testing.T) {
	for _, mode := range []string{"idle-wait", "complete", "late-completion", "late-expiry", "offline-stop", "safety-stop", "expired-session", "boot-expiry", "disconnect", "remote-cancel", "reconnect", "revocation", "batch", "invalid-trace", "invalid-sequence"} {
		t.Run(mode, func(t *testing.T) {
			public, private, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			backend := &protocolAcceptanceRuntime{}
			var connections, consents atomic.Int32
			var revoked, unsafePower atomic.Bool
			var idleSeconds atomic.Uint64
			var batchCPU uint64
			var batchElapsed time.Duration
			operator, device, session, nonce := randomID(), randomID(), randomID(), randomString()
			work := HashJob{Workload: "synthetic-hash-v1", Seed: "public", Iterations: 1000000, AttemptID: randomID(), JobID: randomID(), SessionID: session, Epoch: 1, Timeout: 10, LeaseExpires: time.Now().Add(30 * time.Second)}
			if mode == "idle-wait" || mode == "complete" || mode == "batch" || (mode == "late-completion" || mode == "late-expiry") {
				work.Iterations = 2
			}
			blocked, release, observed := make(chan struct{}), make(chan struct{}), make(chan string, 1)
			computed := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/config":
					json.NewEncoder(w).Encode(Config{Operator: operator})
				case "/v1/device-auth/challenge":
					json.NewEncoder(w).Encode(map[string]string{"nonce": nonce, "operator_id": operator})
				case "/v1/device-auth/token":
					if revoked.Load() {
						w.WriteHeader(401)
						return
					}
					var input map[string]string
					json.NewDecoder(r.Body).Decode(&input)
					sig, _ := base64.StdEncoding.DecodeString(input["signature"])
					if input["device_id"] != device || input["nonce"] != nonce || !ed25519.Verify(public, []byte("opencompute-auth-v1:"+operator+":"+device+":"+nonce), sig) {
						t.Error("invalid device proof")
						w.WriteHeader(401)
						return
					}
					json.NewEncoder(w).Encode(map[string]string{"access_token": "fixture-device-token"})
				case "/v1/agent/availability-sessions":
					consents.Add(1)
					var input struct {
						StartSharing
						Device   string    `json:"device_id"`
						Consent  string    `json:"consent_version"`
						Deadline time.Time `json:"consent_expires_at"`
					}
					json.NewDecoder(r.Body).Decode(&input)
					if r.Header.Get("Authorization") != "Bearer fixture-device-token" || !input.Accepted || input.Duration != 3600 || input.Device != device || input.Consent != "sharing-v1" || input.Policy != (ResourcePolicy{CPUPercent: 10, MemoryMB: 128}) {
						t.Error("sharing bypassed explicit consent")
					}
					expiry := time.Now().Add(time.Hour)
					if mode == "idle-wait" {
						if input.Deadline.IsZero() {
							t.Error("idle activation omitted original company consent deadline")
						} else {
							expiry = input.Deadline
						}
					}
					json.NewEncoder(w).Encode(map[string]interface{}{"id": session, "expires_at": expiry})
				case "/v1/agent/connect":
					if r.Header.Get("Authorization") != "Bearer fixture-device-token" {
						w.WriteHeader(401)
						return
					}
					ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
					if err != nil {
						return
					}
					defer ws.Close()
					connection := connections.Add(1)
					work := work
					successful := mode == "idle-wait" || mode == "complete" || mode == "batch" || mode == "reconnect" && connection == 2
					if mode == "reconnect" && connection == 2 {
						work.AttemptID = randomID()
						work.Epoch = 2
						work.Iterations = 2
					}
					count := 0
					seen := map[string]bool{}
					var batchStarted time.Time
					offered, started := false, false
					phase := 0
					var nextSequence int64
					for {
						var message struct {
							Version  int                    `json:"protocol_version"`
							Type     string                 `json:"type"`
							Device   string                 `json:"device_id"`
							Sequence int64                  `json:"sequence"`
							Trace    string                 `json:"trace_id"`
							Attempt  map[string]interface{} `json:"attempt"`
							Caps     Capabilities           `json:"capabilities"`
						}
						if ws.ReadJSON(&message) != nil {
							return
						}
						ack := controlAck{Version: 1, Type: "ACK", Device: device, Sequence: message.Sequence, Trace: message.Trace}
						if message.Sequence != nextSequence || message.Trace == "" {
							t.Error("invalid control sequence/trace")
							return
						}
						nextSequence++
						if message.Type == "JOB_ACCEPT" || message.Type == "JOB_START" || message.Type == "JOB_COMPLETE" || message.Type == "JOB_FAIL" {
							if message.Attempt["id"] != work.AttemptID || message.Attempt["epoch"] != float64(work.Epoch) {
								t.Error("attempt identity/epoch mismatch")
								return
							}
						}
						if message.Version != 1 || message.Device != device {
							t.Error("invalid protocol identity")
							return
						}
						switch message.Type {
						case "HELLO":
							if mode == "invalid-trace" {
								ack.Trace = randomID()
							}
							if mode == "invalid-sequence" {
								ack.Sequence++
							}
							if len(message.Caps.Runtimes) != 1 {
								t.Error("selected backend not advertised")
							}
						case "WORK_REQUEST":
							if !offered {
								work.LeaseExpires = time.Now().Add(30 * time.Second)
								ack.Offer = &work
								offered = true
							} else if started && !successful {
								if mode == "disconnect" || mode == "reconnect" || mode == "revocation" {
									if mode == "revocation" {
										revoked.Store(true)
									}
									if !backend.running("", work.AttemptID) {
										t.Error("disconnect preceded worker startup")
									}
									close(blocked)
									return
								}
								if mode == "remote-cancel" {
									ack.Cancel = work.AttemptID
								} else {
									close(blocked)
									<-release
								}
								started = false
							}
						case "JOB_ACCEPT":
							if phase != 0 {
								t.Error("out-of-order JOB_ACCEPT")
							}
							phase = 1
						case "JOB_START":
							if batchStarted.IsZero() {
								batchStarted = time.Now()
							}
							if phase != 1 {
								t.Error("JOB_START without acceptance")
							}
							phase = 2
							started = true
						case "JOB_COMPLETE":
							if phase != 2 {
								t.Error("completion without accepted/started attempt")
							}
							if !successful {
								t.Error("canceled worker reported successful completion")
							}
							if message.Attempt["result"] != "315fc33dbdb63592565e8ed77eff5d3cf512638ea9b26631b7277cc73a3fc395" {
								t.Error("wrong protocol hash result")
							}
							ack.JobState = "SUCCEEDED"
							if seen[work.AttemptID] {
								t.Error("duplicate completed attempt")
							}
							seen[work.AttemptID] = true
							count++
							if mode == "batch" {
								usage, ok := message.Attempt["usage"].(map[string]interface{})
								if !ok {
									t.Error("missing technical usage")
								} else {
									cpu, ok := usage["cpu_usage_usec"].(float64)
									if !ok {
										t.Error("missing CPU measurement")
									}
									batchCPU += uint64(cpu)
									ram, _ := usage["memory_peak_bytes"].(float64)
									if ram > 128*1024*1024 {
										t.Error("RAM selection exceeded")
									}
								}
							}
							if mode != "batch" || count == 100 {
								batchElapsed = time.Since(batchStarted)
								observed <- message.Type
							} else {
								offered = false
								started = false
								phase = 0
								work.AttemptID = randomID()
								work.JobID = randomID()
							}
						case "JOB_FAIL":
							if _, present := message.Attempt["result"]; present {
								t.Error("failed attempt leaked a result")
							}
							if _, present := message.Attempt["usage"]; present {
								t.Error("failed attempt leaked successful usage")
							}
							if phase != 2 {
								t.Error("failure without accepted/started attempt")
							}
							observed <- message.Type
						}
						if writeProtocolFixtureACK(ws, ack) != nil {
							return
						}
					}
				default:
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			state := State{Gateway: server.URL, Operator: operator, Device: device, Private: base64.StdEncoding.EncodeToString(private)}
			manager, err := newSessionManager(testStateDir(t), state)
			if err != nil {
				t.Fatal(err)
			}
			manager.safetySource = func(context.Context) hostObservation {
				ac := !unsafePower.Load()
				idle := idleSeconds.Load()
				return hostObservation{AC: &ac, IdleSeconds: &idle}
			}
			manager.backend = backend
			if mode == "late-completion" || mode == "late-expiry" {
				manager.backend = delayedProtocolCompletion{executionBackend: manager.backend, release: release, computed: computed}
			}
			if err := manager.start(context.Background(), StartSharing{Duration: 3600, Policy: ResourcePolicy{CPUPercent: 10, MemoryMB: 128}}); err == nil || consents.Load() != 0 {
				t.Fatal("runtime availability bypassed explicit sharing consent")
			}
			guard := SafetyPolicy{ACOnly: mode == "safety-stop"}
			if mode == "idle-wait" {
				guard.IdleSeconds = 60
			}
			if err := manager.start(context.Background(), StartSharing{Duration: 3600, Policy: ResourcePolicy{CPUPercent: 10, MemoryMB: 128}, Accepted: true, Safety: guard, WaitForIdle: mode == "idle-wait"}); err != nil {
				t.Fatal(err)
			}
			limit := 12 * time.Second
			if mode == "idle-wait" {
				pending := manager.snapshot()
				if pending.Status != "STARTING" || !pending.WaitingIdle || consents.Load() != 0 {
					t.Fatal("idle wait contacted company before eligibility")
				}
				idleSeconds.Store(60)
				deadline := time.Now().Add(3 * time.Second)
				for manager.snapshot().Status != "ACTIVE" {
					if time.Now().After(deadline) {
						t.Fatal("eligible idle wait did not activate")
					}
					time.Sleep(10 * time.Millisecond)
				}
				active := manager.snapshot()
				if active.ExpiresAt.After(pending.ExpiresAt) || active.ExpiresBoot != pending.ExpiresBoot {
					t.Fatal("idle waiting renewed original consent deadline")
				}
			}
			if mode == "batch" {
				limit = 120 * time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), limit)
			defer cancel()
			if mode == "safety-stop" || mode == "expired-session" || mode == "boot-expiry" {
				watched := make(chan struct{})
				go func() { manager.watch(ctx); close(watched) }()
				defer func() { cancel(); <-watched }()
			}
			done := make(chan error, 1)
			go func() { done <- connect(ctx, state, manager) }()
			defer func() {
				unblock()
				cancel()
				select {
				case <-done:
				case <-time.After(4 * time.Second):
					t.Error("control connection did not close")
				}
			}()
			if mode == "invalid-trace" || mode == "invalid-sequence" {
				select {
				case err := <-done:
					done <- err
					if err == nil || backend.running("", work.AttemptID) {
						t.Error("invalid acknowledgement accepted")
					}
				case <-ctx.Done():
					t.Fatal("invalid acknowledgement was not rejected")
				}
				return
			}

			if mode == "offline-stop" || mode == "safety-stop" || mode == "expired-session" || mode == "boot-expiry" || (mode == "late-completion" || mode == "late-expiry") {
				select {
				case <-blocked:
				case <-ctx.Done():
					t.Fatal("worker never reached blocked company ACK")
				}
				if mode == "late-completion" || mode == "late-expiry" {
					select {
					case <-computed:
					case <-ctx.Done():
						t.Fatal("late-result fixture did not finish computation before cancellation")
					}
				}
				if mode != "late-completion" && mode != "late-expiry" && !manager.execution().running(manager.root, work.AttemptID) {
					t.Fatal("fixture never ran an active worker")
				}
				if mode == "safety-stop" {
					unsafePower.Store(true)
				} else if mode == "offline-stop" || mode == "late-completion" {
					manager.stopLocal("STOPPED")
				} else {
					manager.mu.Lock()
					if mode == "boot-expiry" {
						_, up, err := bootClock()
						if err != nil {
							manager.mu.Unlock()
							t.Fatal(err)
						}
						manager.session.ExpiresBoot = up - 1
					} else {
						manager.session.ExpiresAt = time.Now().Add(-time.Second)
					}
					manager.mu.Unlock()
					// The real watch loop discovers wall/boot expiry while the ACK stays blocked.
				}
				deadline := time.Now().Add(2 * time.Second)
				for manager.execution().running(manager.root, work.AttemptID) {
					if time.Now().After(deadline) {
						t.Fatal("local cancellation waited for company ACK")
					}
					time.Sleep(5 * time.Millisecond)
				}
				if mode == "safety-stop" {
					manager.mu.Lock()
					stopped := manager.session.Status == "STOPPED"
					manager.mu.Unlock()
					if !stopped {
						t.Fatal("watch did not stop unsafe sharing before company ACK")
					}
				}
				unblock()
			}
			if mode == "disconnect" || mode == "reconnect" || mode == "revocation" {
				select {
				case <-blocked:
				case <-ctx.Done():
					t.Fatal("disconnect fixture did not reach active work")
				}
				select {
				case err := <-done:
					if err == nil {
						t.Error("disconnect returned no transport error")
					}
					if mode != "reconnect" {
						done <- err
					}
					manager.mu.Lock()
					pending := manager.CurrentAttempt
					connected := manager.Connected
					manager.mu.Unlock()
					if pending != "" || connected || backend.running("", work.AttemptID) {
						t.Error("disconnect left active worker/session connection")
					}
				case <-ctx.Done():
					t.Fatal("disconnect did not cancel worker")
				}
				if mode == "revocation" {
					if err := connect(ctx, state, manager); err == nil {
						t.Error("revoked device reauthenticated")
					}
					if connections.Load() != 1 {
						t.Error("revoked device opened another control channel")
					}
					return
				}
				if mode == "disconnect" {
					return
				}
				go func() { done <- connect(ctx, state, manager) }()
			}
			select {
			case kind := <-observed:
				if (mode == "idle-wait" || mode == "complete" || mode == "reconnect" || mode == "batch") && kind != "JOB_COMPLETE" || (mode != "idle-wait" && mode != "complete" && mode != "reconnect" && mode != "batch") && kind != "JOB_FAIL" {
					t.Fatalf("unexpected terminal message: %s", kind)
				}
			case <-ctx.Done():
				t.Fatal("control flow never finished")
			}
			deadline := time.Now().Add(2 * time.Second)
			for {
				manager.mu.Lock()
				status, result, pending := manager.JobStatus, manager.LastResult, manager.CurrentAttempt
				manager.mu.Unlock()
				if pending == "" {
					success := mode == "idle-wait" || mode == "complete" || mode == "reconnect" || mode == "batch"
					if success && (status != "VERIFIED" || result.Result == "") {
						t.Error("successful result not recorded")
					}
					if !success && (status == "VERIFIED" || result != (WorkResult{})) {
						t.Error("canceled result retained as success")
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("terminal result was not recorded")
				}
				time.Sleep(5 * time.Millisecond)
			}

			if consents.Load() != 1 || manager.snapshot().ID != session {
				t.Error("protocol flow silently renewed consent")
			}
			if mode == "reconnect" && connections.Load() != 2 {
				t.Error("reconnect did not establish a new channel")
			}
			if mode == "batch" {
				allowed := uint64(batchElapsed.Seconds()*0.1*1000000) + 150000
				t.Logf("100-job session: CPU=%d allowed=%d elapsed=%v", batchCPU, allowed, batchElapsed)
				if runtime.GOOS == "windows" && (batchCPU == 0 || batchCPU > allowed) {
					t.Error("aggregate session CPU budget failed")
				}
			}
		})
	}
}

type delayedProtocolCompletion struct {
	executionBackend
	release  <-chan struct{}
	computed chan struct{}
}

func (r delayedProtocolCompletion) execute(ctx context.Context, root string, j HashJob, p ResourcePolicy) (WorkResult, error) {
	result, err := r.executionBackend.execute(ctx, root, j, p)
	if err != nil {
		return result, err
	}
	close(r.computed)
	<-r.release
	return result, nil
}

func writeProtocolFixtureACK(ws *websocket.Conn, ack controlAck) error {
	frame := map[string]interface{}{"protocol_version": ack.Version, "type": ack.Type, "device_id": ack.Device, "sequence": ack.Sequence, "trace_id": ack.Trace}
	if ack.Offer != nil {
		frame["offer"] = ack.Offer
	}
	if ack.Cancel != "" {
		frame["cancel_attempt_id"] = ack.Cancel
	}
	if ack.JobState != "" {
		frame["job_state"] = ack.JobState
	}
	return ws.WriteJSON(frame)
}
