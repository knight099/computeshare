//go:build windows

package main

import (
	"bytes"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestWindowsStateIsEncryptedAndRejectsPlaintextOrTampering(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := prepareStateDirectory(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "secret.json")
	secret := []byte(`{"private_key":"sensitive-test-key"}`)
	if err := writeSecret(path, secret); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, secret) || bytes.Contains(raw, []byte("sensitive-test-key")) {
		t.Fatal("secret stored in plaintext")
	}
	decoded, err := readSecret(path)
	if err != nil || !bytes.Equal(decoded, secret) {
		t.Fatal("protected state did not round trip")
	}
	raw[len(raw)/2] ^= 1
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = readSecret(path); err == nil {
		t.Fatal("accepted tampered encrypted state")
	}
	if err = os.WriteFile(path, secret, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = readSecret(path); err == nil {
		t.Fatal("accepted legacy plaintext state")
	}
	if _, err = readSecret(filepath.Join(dir, "missing")); !os.IsNotExist(err) {
		t.Fatalf("missing-file semantics lost: %v", err)
	}
}
func TestWindowsSessionLockSerializesWriters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := prepareStateDirectory(dir); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	first := make(chan error, 1)
	second := make(chan error, 1)
	go func() { first <- withSessionLock(dir, func() error { close(entered); <-release; return nil }) }()
	select {
	case <-entered:
	case err := <-first:
		t.Fatalf("first lock failed: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("first lock timed out")
	}
	go func() { second <- withSessionLock(dir, func() error { return nil }) }()
	select {
	case err := <-second:
		close(release)
		t.Fatalf("writer bypassed lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-second:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("lock was not released")
	}
}
func TestWindowsDoesNotAdvertiseUnenforcedCompute(t *testing.T) {
	if executionAvailable("") || executionAvailable(t.TempDir()) {
		t.Fatal("Windows compute enabled before resource enforcement acceptance")
	}
	caps, err := capabilities()
	if err != nil || caps.OS != "windows" || len(caps.Runtimes) != 0 {
		t.Fatal("unsupported runtime advertised")
	}
}

func testWindowsDACL(t *testing.T, path string, broad bool) string {
	t.Helper()
	sid, err := userSID()
	if err != nil {
		t.Fatal(err)
	}
	descriptor := "D:P(A;OICI;FA;;;" + sid.String() + ")(A;OICI;FA;;;SY)"
	if broad {
		descriptor += "(A;OICI;GRGW;;;WD)"
	}
	sd, err := windows.SecurityDescriptorFromString(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	current, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	return current.String()
}
func TestWindowsExistingDirectoryPermissionsAreNeverRewritten(t *testing.T) {
	dir := testStateDir(t)
	before := testWindowsDACL(t, dir, true)
	if err := prepareStateDirectory(dir); err == nil {
		t.Fatal("accepted shared state directory")
	}
	after, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if after.String() != before {
		t.Fatal("changed existing directory permissions")
	}
}
func TestWindowsRejectsBroadFileAndLockACLs(t *testing.T) {
	dir := testStateDir(t)
	path := filepath.Join(dir, "secret.json")
	if err := writeSecret(path, []byte("secret")); err != nil {
		t.Fatal(err)
	}
	testWindowsDACL(t, path, true)
	if _, err := readSecret(path); err == nil {
		t.Fatal("accepted broad state file ACL")
	}
	lock := filepath.Join(dir, "session.lock")
	if err := withSessionLock(dir, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	testWindowsDACL(t, lock, true)
	executed := false
	if err := withSessionLock(dir, func() error { executed = true; return nil }); err == nil || executed {
		t.Fatal("accepted broad lock ACL")
	}
}
func TestWindowsAtomicReplacementWorksWhileReaderIsOpen(t *testing.T) {
	dir := testStateDir(t)
	path := filepath.Join(dir, "secret.json")
	if err := writeSecret(path, []byte("before")); err != nil {
		t.Fatal(err)
	}
	reader, err := openWindowsState(path, windows.GENERIC_READ|windows.READ_CONTROL, windows.OPEN_EXISTING, false)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if err = writeSecret(path, []byte("after")); err != nil {
		t.Fatal(err)
	}
	data, err := readSecret(path)
	if err != nil || string(data) != "after" {
		t.Fatal("replacement not visible to subsequent reads")
	}
}
func TestWindowsRejectsIntermediateJunctions(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	if err := prepareStateDirectory(target); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "junction")
	if output, err := exec.Command("cmd.exe", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("junction fixture unavailable: %v %s", err, output)
	}
	if err := prepareStateDirectory(filepath.Join(link, "child")); err == nil {
		t.Fatal("accepted intermediate junction")
	}
}
func TestWindowsWorkerEntrypointHelper(t *testing.T) {
	if os.Getenv("OPENCOMPUTE_WINDOWS_WORKER_TEST") != "1" {
		t.Skip("subprocess helper")
	}
	os.Args = []string{os.Args[0], "internal-hash-worker"}
	main()
}
func TestWindowsRejectsDirectWorkerEntrypoint(t *testing.T) {
	child := exec.Command(os.Args[0], "-test.run=^TestWindowsWorkerEntrypointHelper$")
	child.Env = append(os.Environ(), "OPENCOMPUTE_WINDOWS_WORKER_TEST=1")
	child.Stdin = bytes.NewBufferString(`{"workload_type":"synthetic-hash-v1","seed":"public","iterations":2}`)
	output, err := child.CombinedOutput()
	if err == nil || bytes.Contains(output, []byte("315fc33d")) {
		t.Fatalf("Windows worker bypassed disabled runtime: %v", err)
	}
}
