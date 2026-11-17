//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecureStateRejectsSymlinksAndPublicFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := writeSecret(path, []byte(`{"id":"test"}`)); err != nil {
		t.Fatal(err)
	}
	data, err := readSecret(path)
	if err != nil || !strings.Contains(string(data), "test") {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = readSecret(path); err == nil {
		t.Fatal("accepted public state file")
	}
	link := filepath.Join(dir, "link")
	if err = os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err = writeSecret(link, []byte("replaced")); err == nil {
		t.Fatal("wrote through symlink")
	}
}
