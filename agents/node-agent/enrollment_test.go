package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativeEnrollmentAndDeviceProofReuseProtectedState(t *testing.T) {
	operator, device, nonce := randomID(), randomID(), randomString()
	var public atomic.Value
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/config":
			_ = json.NewEncoder(w).Encode(Config{Operator: operator, Consent: "test-consent"})
		case "/v1/consent":
			if r.Header.Get("Authorization") != "Bearer login-token" {
				t.Error("missing enrollment bearer")
			}
			w.WriteHeader(204)
		case "/v1/devices":
			var input struct {
				Public    string       `json:"public_key"`
				Signature string       `json:"signature"`
				Caps      Capabilities `json:"capabilities"`
			}
			if e := json.NewDecoder(r.Body).Decode(&input); e != nil {
				t.Error(e)
				w.WriteHeader(400)
				return
			}
			key, _ := base64.StdEncoding.DecodeString(input.Public)
			sig, _ := base64.StdEncoding.DecodeString(input.Signature)
			if len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, []byte("opencompute-enroll-v1:"+operator+":"+input.Public), sig) {
				t.Error("invalid enrollment proof")
				w.WriteHeader(400)
				return
			}
			if input.Caps.OS != runtime.GOOS || len(input.Caps.Runtimes) != 0 {
				t.Error("invalid platform inventory or enabled unsupported runtime")
			}
			public.Store(ed25519.PublicKey(key))
			_ = json.NewEncoder(w).Encode(map[string]string{"id": device, "operator_id": operator})
		case "/v1/device-auth/challenge":
			_ = json.NewEncoder(w).Encode(map[string]string{"nonce": nonce, "operator_id": operator})
		case "/v1/device-auth/token":
			var input map[string]string
			_ = json.NewDecoder(r.Body).Decode(&input)
			sig, _ := base64.StdEncoding.DecodeString(input["signature"])
			if input["device_id"] != device || input["nonce"] != nonce || !ed25519.Verify(public.Load().(ed25519.PublicKey), []byte("opencompute-auth-v1:"+operator+":"+device+":"+nonce), sig) {
				t.Error("invalid device proof")
				w.WriteHeader(401)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "device-token"})
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	dir := testStateDir(t)
	if e := save(filepath.Join(dir, "login.json"), Login{Gateway: server.URL, Operator: operator, Token: "login-token", Expires: time.Now().Add(time.Hour).Unix()}); e != nil {
		t.Fatal(e)
	}
	if e := enroll(context.Background(), server.URL, dir, "native-device", false); e == nil || requests.Load() != 0 {
		t.Fatal("enrollment bypassed explicit consent")
	}
	if e := enroll(context.Background(), server.URL, dir, "native-device", true); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(dir, "login.json")); !os.IsNotExist(e) {
		t.Fatal("login credential retained after enrollment")
	}
	data, e := readSecret(filepath.Join(dir, "device.json"))
	if e != nil {
		t.Fatal(e)
	}
	var state State
	if e = json.Unmarshal(data, &state); e != nil {
		t.Fatal(e)
	}
	if state.Device != device || state.Operator != operator {
		t.Fatal("operator binding was lost")
	}
	token, e := deviceToken(context.Background(), state)
	if e != nil || token != "device-token" {
		t.Fatalf("device authentication failed: %v", e)
	}
	if e = enroll(context.Background(), server.URL, dir, "replacement", true); e == nil {
		t.Fatal("silently replaced enrolled identity")
	}
}
