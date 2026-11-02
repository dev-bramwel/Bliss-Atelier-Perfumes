package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetupPreservesExistingCredentials(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "secrets")
	if err := secrets(directory); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(directory, "runtime_password"))
	if err != nil || len(before) != 64 {
		t.Fatal("invalid generated password")
	}
	if err = secrets(directory); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(directory, "runtime_password"))
	if err != nil || string(before) != string(after) {
		t.Fatal("setup rotated existing database password")
	}
	if err = os.Remove(filepath.Join(directory, "runtime_url")); err != nil {
		t.Fatal(err)
	}
	if err = secrets(directory); err == nil {
		t.Fatal("partial credentials were silently replaced")
	}
}
