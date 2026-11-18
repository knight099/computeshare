package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestIdleWaitStopsWithoutContactingCompany(t *testing.T) {
	for _, mode := range []string{"local", "cli", "shutdown", "restart"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			idle := uint64(0)
			m := &SessionManager{dir: testStateDir(t), backend: &protocolAcceptanceRuntime{}, safetySource: func(context.Context) hostObservation { return hostObservation{IdleSeconds: &idle} }}
			if err := m.start(ctx, StartSharing{Duration: 3600, Accepted: true, Policy: ResourcePolicy{CPUPercent: 25, MemoryMB: 128}, Safety: SafetyPolicy{IdleSeconds: 60}, WaitForIdle: true}); err != nil {
				t.Fatal(err)
			}
			if s := m.snapshot(); s.Status != "STARTING" || !s.WaitingIdle {
				t.Fatal("idle wait did not reserve local consent")
			}
			switch mode {
			case "local":
				m.stopLocal("STOPPED")
			case "cli":
				if err := stopFromCLI(m.dir); err != nil {
					t.Fatal(err)
				}
			case "shutdown":
				cancel()
			case "restart":
				restored, err := newSessionManager(m.dir, State{})
				if err != nil || restored.snapshot().Status != "STOPPED" {
					t.Fatalf("restart resumed wait: %v", err)
				}
			}
			deadline := time.Now().Add(2 * time.Second)
			for m.snapshot().Status != "STOPPED" {
				if time.Now().After(deadline) {
					t.Fatal("wait survived cancellation")
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

func TestIdleWaitRejectsWithoutIdleGuard(t *testing.T) {
	m := &SessionManager{dir: testStateDir(t), backend: &protocolAcceptanceRuntime{}}
	if m.start(context.Background(), StartSharing{Duration: 3600, Accepted: true, Policy: ResourcePolicy{CPUPercent: 25, MemoryMB: 128}, WaitForIdle: true}) == nil {
		t.Fatal("unbounded wait without idle guard accepted")
	}
}

func TestIdleWaitRejectsUnsafeRequiredSensors(t *testing.T) {
	for _, sample := range []hostObservation{{}, {IdleSeconds: func() *uint64 { v := uint64(0); return &v }(), AC: func() *bool { v := false; return &v }()}} {
		m := &SessionManager{dir: testStateDir(t), backend: &protocolAcceptanceRuntime{}, safetySource: func(context.Context) hostObservation { return sample }}
		if err := m.start(context.Background(), StartSharing{Duration: 3600, Accepted: true, Policy: ResourcePolicy{CPUPercent: 25, MemoryMB: 128}, Safety: SafetyPolicy{ACOnly: true, IdleSeconds: 60}, WaitForIdle: true}); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(2 * time.Second)
		for m.snapshot().Status != "STOPPED" {
			if time.Now().After(deadline) {
				t.Fatal("unsafe wait remained armed")
			}
			time.Sleep(10 * time.Millisecond)
		}
		if m.LastError == "" {
			t.Fatal("unsafe cancellation had no diagnostic")
		}
	}
}

func TestPreparedConsentCannotActivateAfterDeadline(t *testing.T) {
	m := &SessionManager{dir: testStateDir(t)}
	boot, up, err := bootClock()
	if err != nil {
		t.Fatal(err)
	}
	generation, err := m.prepareStart(boot, up, 3600, ResourcePolicy{CPUPercent: 25, MemoryMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	if m.adoptSession(generation, LocalSession{ID: randomID(), Status: "ACTIVE", BootID: boot, ExpiresBoot: up + 3600, ExpiresAt: time.Now().Add(-time.Second)}) == nil {
		t.Fatal("expired consent activated after company exchange")
	}
}

func TestIdleWaitExpiresBeforeEligibility(t *testing.T) {
	m := &SessionManager{dir: testStateDir(t)}
	boot, up, err := bootClock()
	if err != nil {
		t.Fatal(err)
	}
	policy := ResourcePolicy{CPUPercent: 25, MemoryMB: 128}
	generation, err := m.prepareStart(boot, up, 3600, policy)
	if err != nil {
		t.Fatal(err)
	}
	err = m.awaitIdle(context.Background(), StartSharing{Duration: 3600, Policy: policy, Safety: SafetyPolicy{IdleSeconds: 60}}, generation, boot, up, time.Now().Add(-time.Second))
	if err == nil {
		t.Fatal("expired idle wait reached company")
	}
	m.abortStart(generation, err)
	if m.snapshot().Status != "STOPPED" {
		t.Fatal("expired wait stayed armed")
	}
}

func TestIdleArmCannotOverwriteCLIStop(t *testing.T) {
	m := &SessionManager{dir: testStateDir(t)}
	boot, up, err := bootClock()
	if err != nil {
		t.Fatal(err)
	}
	b := StartSharing{Duration: 3600, Policy: ResourcePolicy{CPUPercent: 25, MemoryMB: 128}, Safety: SafetyPolicy{IdleSeconds: 60}}
	g, err := m.prepareStart(boot, up, b.Duration, b.Policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := stopFromCLI(m.dir); err != nil {
		t.Fatal(err)
	}
	if err := m.armIdle(context.Background(), b, g, boot, up, time.Now().Add(time.Hour)); err == nil {
		t.Fatal("arming overwrote CLI Stop")
	}
}

func TestWorkerCleanupCannotLosePendingStartCancellation(t *testing.T) {
	m := &SessionManager{dir: testStateDir(t)}
	canceled := false
	m.startCancel = func() { canceled = true }
	m.finished(HashJob{}, WorkResult{}, "CANCELED")
	m.stopLocal("STOPPED")
	if !canceled {
		t.Fatal("worker cleanup cleared pending start cancellation")
	}
}

func TestDisconnectDoesNotCancelLocalIdleConsent(t *testing.T) {
	m := &SessionManager{dir: testStateDir(t), backend: &protocolAcceptanceRuntime{}, safetySource: func(context.Context) hostObservation { idle := uint64(0); return hostObservation{IdleSeconds: &idle} }}
	if err := m.start(context.Background(), StartSharing{Duration: 3600, Accepted: true, Policy: ResourcePolicy{CPUPercent: 25, MemoryMB: 128}, Safety: SafetyPolicy{IdleSeconds: 60}, WaitForIdle: true}); err != nil {
		t.Fatal(err)
	}
	defer m.stopLocal("STOPPED")
	m.disconnect()
	time.Sleep(50 * time.Millisecond)
	if m.snapshot().Status != "STARTING" {
		t.Fatal("connection loss canceled local idle wait")
	}
}

func TestDashboardStopOfIdleWaitNeverContactsCompany(t *testing.T) {
	contacted := make(chan struct{}, 1)
	company := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case contacted <- struct{}{}:
		default:
		}
		w.WriteHeader(500)
	}))
	defer company.Close()
	idle := uint64(0)
	m := &SessionManager{dir: testStateDir(t), state: State{Gateway: company.URL}, backend: &protocolAcceptanceRuntime{}, safetySource: func(context.Context) hostObservation { return hostObservation{IdleSeconds: &idle} }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := m.start(ctx, StartSharing{Duration: 3600, Accepted: true, Policy: ResourcePolicy{CPUPercent: 25, MemoryMB: 128}, Safety: SafetyPolicy{IdleSeconds: 60}, WaitForIdle: true}); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	origin := "http://" + listener.Addr().String()
	listener.Close()
	closeDashboard, err := startDashboard(ctx, m, port)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDashboard()
	response, err := http.Get(origin + "/state")
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		CSRF string `json:"csrf"`
	}
	json.NewDecoder(response.Body).Decode(&state)
	response.Body.Close()
	request, _ := http.NewRequest("POST", origin+"/stop", bytes.NewBufferString("{}"))
	request.Header.Set("Origin", origin)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-OpenCompute-CSRF", state.CSRF)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.Status)
	}
	select {
	case <-contacted:
		t.Fatal("pending local consent contacted company on Stop")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestIdleDeadlinePassingDuringSensorReadDoesNotContactCompany(t *testing.T) {
	var contacted atomic.Int32
	company := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { contacted.Add(1); w.WriteHeader(500) }))
	defer company.Close()
	m := &SessionManager{dir: testStateDir(t), state: State{Gateway: company.URL}, safetySource: func(context.Context) hostObservation {
		time.Sleep(100 * time.Millisecond)
		idle := uint64(60)
		return hostObservation{IdleSeconds: &idle}
	}}
	boot, up, err := bootClock()
	if err != nil {
		t.Fatal(err)
	}
	b := StartSharing{Duration: 3600, Policy: ResourcePolicy{CPUPercent: 25, MemoryMB: 128}, Safety: SafetyPolicy{IdleSeconds: 60}}
	g, err := m.prepareStart(boot, up, b.Duration, b.Policy)
	if err != nil {
		t.Fatal(err)
	}
	err = m.awaitIdle(context.Background(), b, g, boot, up, time.Now().Add(20*time.Millisecond))
	m.abortStart(g, err)
	if contacted.Load() != 0 {
		t.Fatal("expired idle consent contacted company after sensor collection")
	}
}
