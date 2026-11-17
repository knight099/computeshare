// OpenCompute agent: local consent and constrained built-in synthetic work only.
package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

type Capabilities struct {
	OS       string   `json:"os"`
	Arch     string   `json:"arch"`
	CPU      int      `json:"cpu_cores"`
	Memory   int64    `json:"memory_mb"`
	Version  string   `json:"agent_version"`
	Runtimes []string `json:"runtimes"`
}
type State struct {
	Gateway  string `json:"gateway"`
	Operator string `json:"operator_id"`
	Device   string `json:"device_id"`
	Private  string `json:"private_key"`
}
type Login struct {
	Gateway  string `json:"gateway"`
	Operator string `json:"operator_id"`
	Token    string `json:"access_token"`
	Expires  int64  `json:"expires_at"`
}
type Config struct {
	Operator string `json:"operator_id"`
	Issuer   string `json:"oidc_issuer"`
	Consent  string `json:"consent_version"`
}

var httpClient = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}

func validateGateway(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("gateway must be an origin without credentials, query or path")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")) {
		return "", errors.New("HTTPS required except on loopback for local development")
	}
	return strings.TrimSuffix(u.String(), "/"), nil
}
func save(path string, value interface{}) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writeSecret(path, b)
}
func request(ctx context.Context, gateway, path, method, token string, body, result interface{}) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, gateway+path, reader)
	if err != nil {
		return errors.New("invalid request")
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := httpClient.Do(req)
	if err != nil {
		return errors.New("company endpoint unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("company endpoint returned HTTP %d", response.StatusCode)
	}
	if result == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(response.Body, 65536)).Decode(result)
}
func randomID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	h := hex.EncodeToString(b)
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
func randomString() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func login(ctx context.Context, gateway, dir string) error {
	var config Config
	if err := request(ctx, gateway, "/v1/config", "GET", "", nil, &config); err != nil {
		return err
	}
	issuer, err := url.Parse(config.Issuer)
	if err != nil || issuer.User != nil || issuer.RawQuery != "" {
		return errors.New("invalid identity issuer")
	}
	if _, err = validateGateway(issuer.Scheme + "://" + issuer.Host); err != nil {
		return errors.New("unsafe identity issuer")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:8787")
	if err != nil {
		return errors.New("loopback login port 8787 unavailable")
	}
	state, verifier := randomString(), randomString()
	hash := sha256.Sum256([]byte(verifier))
	redirect := "http://127.0.0.1:8787/callback"
	authURL := config.Issuer + "/protocol/openid-connect/auth?" + url.Values{"client_id": {"opencompute-agent"}, "redirect_uri": {redirect}, "response_type": {"code"}, "scope": {"openid"}, "state": {state}, "code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}, "code_challenge_method": {"S256"}}.Encode()
	codes := make(chan string, 1)
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
		if r.Method != "GET" || r.URL.Path != "/callback" || r.URL.Query().Get("state") != state || r.URL.Query().Get("code") == "" {
			http.Error(w, "Invalid sign-in response", 400)
			return
		}
		select {
		case codes <- r.URL.Query().Get("code"):
			fmt.Fprint(w, "Sign-in received. You can close this window.")
		default:
			http.Error(w, "Already received", 409)
		}
	})
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	fmt.Println("Open this company sign-in URL in your system browser:", authURL)
	if path, err := exec.LookPath("xdg-open"); err == nil {
		cmd := exec.CommandContext(ctx, path, authURL)
		_ = cmd.Start()
		go func() { _ = cmd.Wait() }()
	}
	var code string
	select {
	case code = <-codes:
	case <-ctx.Done():
		return ctx.Err()
	}
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {"opencompute-agent"}, "redirect_uri": {redirect}, "code": {code}, "code_verifier": {verifier}}
	req, err := http.NewRequestWithContext(ctx, "POST", config.Issuer+"/protocol/openid-connect/token", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := httpClient.Do(req)
	if err != nil {
		return errors.New("identity provider unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return errors.New("identity provider rejected sign-in")
	}
	var tokens struct {
		Token   string `json:"access_token"`
		Expires int64  `json:"expires_in"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 65536)).Decode(&tokens); err != nil {
		return err
	}
	if tokens.Token == "" || tokens.Expires <= 0 {
		return errors.New("invalid identity response")
	}
	var me map[string]interface{}
	if err = request(ctx, gateway, "/v1/me", "GET", tokens.Token, nil, &me); err != nil {
		return err
	}
	if err = save(filepath.Join(dir, "login.json"), Login{gateway, config.Operator, tokens.Token, time.Now().Unix() + tokens.Expires}); err != nil {
		return err
	}
	fmt.Println("Signed in. Enrollment requires explicit --accept-consent.")
	return nil
}
func enroll(ctx context.Context, gateway, dir, name string, accept bool) error {
	if !accept {
		return errors.New("review the enrollment disclosure in the operator guide, then explicitly pass --accept-consent; registration shares OS, CPU, RAM and connectivity, and does not execute jobs")
	}
	path := filepath.Join(dir, "device.json")
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return errors.New("device state already exists; refusing to replace its operator binding")
	}
	b, err := readSecret(filepath.Join(dir, "login.json"))
	if err != nil {
		return errors.New("run login first")
	}
	var credentials Login
	if err = json.Unmarshal(b, &credentials); err != nil {
		return err
	}
	if credentials.Gateway != gateway || credentials.Expires <= time.Now().Unix() {
		return errors.New("login expired or belongs to another gateway")
	}
	var config Config
	if err = request(ctx, gateway, "/v1/config", "GET", "", nil, &config); err != nil {
		return err
	}
	if config.Operator != credentials.Operator {
		return errors.New("operator identity changed")
	}
	caps, err := capabilities()
	if err != nil {
		return err
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	key := base64.StdEncoding.EncodeToString(pub)
	if err = request(ctx, gateway, "/v1/consent", "POST", credentials.Token, map[string]interface{}{"version": config.Consent, "accepted": true}, nil); err != nil {
		return err
	}
	proof := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte("opencompute-enroll-v1:"+config.Operator+":"+key)))
	var result struct {
		ID       string `json:"id"`
		Operator string `json:"operator_id"`
	}
	if err = request(ctx, gateway, "/v1/devices", "POST", credentials.Token, map[string]interface{}{"name": name, "public_key": key, "signature": proof, "capabilities": caps}, &result); err != nil {
		return err
	}
	if result.ID == "" || result.Operator != config.Operator {
		return errors.New("invalid enrollment response")
	}
	if err = save(path, State{gateway, config.Operator, result.ID, base64.StdEncoding.EncodeToString(priv)}); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(dir, "login.json"))
	fmt.Println("Device enrolled:", result.ID, "— no compute execution enabled")
	return nil
}
func deviceToken(ctx context.Context, s State) (string, error) {
	var config Config
	if err := request(ctx, s.Gateway, "/v1/config", "GET", "", nil, &config); err != nil {
		return "", err
	}
	if config.Operator != s.Operator {
		return "", errors.New("operator identity mismatch")
	}
	var challenge struct {
		Nonce    string `json:"nonce"`
		Operator string `json:"operator_id"`
	}
	if err := request(ctx, s.Gateway, "/v1/device-auth/challenge", "POST", "", map[string]string{"device_id": s.Device}, &challenge); err != nil {
		return "", err
	}
	if challenge.Operator != s.Operator {
		return "", errors.New("operator identity mismatch")
	}
	priv, err := base64.StdEncoding.DecodeString(s.Private)
	if err != nil || len(priv) != ed25519.PrivateKeySize {
		return "", errors.New("invalid local device key")
	}
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte("opencompute-auth-v1:"+s.Operator+":"+s.Device+":"+challenge.Nonce)))
	var token struct {
		Access string `json:"access_token"`
	}
	if err = request(ctx, s.Gateway, "/v1/device-auth/token", "POST", "", map[string]string{"device_id": s.Device, "nonce": challenge.Nonce, "signature": signature}, &token); err != nil {
		return "", err
	}
	return token.Access, nil
}
func wait(ctx context.Context, duration time.Duration) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
func run(ctx context.Context, dir string, port int) error {
	b, err := readSecret(filepath.Join(dir, "device.json"))
	if err != nil {
		return errors.New("device state unavailable; enroll first")
	}
	var state State
	if err = json.Unmarshal(b, &state); err != nil {
		return err
	}
	if _, err = validateGateway(state.Gateway); err != nil {
		return err
	}
	manager, err := newSessionManager(dir, state)
	if err != nil {
		return err
	}
	defer manager.stopLocal("STOPPED")
	if _, e := cleanupStaleWorkloads(manager.root); e != nil {
		log.Println("Stale workload cleanup incomplete; active groups preserved")
	}
	go manager.watch(ctx)
	closeDashboard, err := startDashboard(ctx, manager, port)
	if err != nil {
		return err
	}
	defer closeDashboard()
	backoff := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		err = connect(ctx, state, manager)
		if ctx.Err() != nil {
			break
		}
		if err != nil {
			log.Println(err)
		}
		if time.Since(started) > time.Minute {
			backoff = time.Second
		}
		jitter, _ := rand.Int(rand.Reader, big.NewInt(int64(backoff/2)+1))
		wait(ctx, backoff+time.Duration(jitter.Int64()))
		if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
	log.Println("Agent stopped locally")
	return nil
}
func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: opencompute-agent login|enroll|run|stop [--state-dir PATH] [--gateway URL] [--name NAME] [--accept-consent]")
		os.Exit(2)
	}
	if os.Args[1] == "internal-hash-worker" {
		if err := internalHashWorker(); err != nil {
			os.Exit(1)
		}
		return
	}
	flags := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	port := flags.Int("dashboard-port", 8788, "loopback contributor dashboard port (0 for a random free port)")
	gateway := flags.String("gateway", "http://localhost:8080", "company gateway origin")
	name := flags.String("name", runtime.GOOS+"-device", "device display name")
	accepted := flags.Bool("accept-consent", false, "explicitly accept disclosed enrollment telemetry; no compute runs")
	home, err := os.UserConfigDir()
	if err != nil {
		log.Fatal("Cannot locate user configuration directory")
	}
	dir := flags.String("state-dir", filepath.Join(home, "opencompute"), "private local state directory")
	_ = flags.Parse(os.Args[2:])
	if err = prepareStateDirectory(*dir); err != nil {
		log.Fatal("State directory must be private and owned by the current user")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	normalized, err := validateGateway(*gateway)
	if err != nil {
		log.Fatal(err)
	}
	switch os.Args[1] {
	case "login":
		var timeout context.CancelFunc
		ctx, timeout = context.WithTimeout(ctx, 3*time.Minute)
		defer timeout()
		err = login(ctx, normalized, *dir)
	case "enroll":
		err = enroll(ctx, normalized, *dir, *name, *accepted)
	case "stop":
		err = stopFromCLI(*dir)
	case "run":
		err = run(ctx, *dir, *port)
	default:
		err = errors.New("unknown command")
	}
	if err != nil {
		log.Fatal(err)
	}
}
