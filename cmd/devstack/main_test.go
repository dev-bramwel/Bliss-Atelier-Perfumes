package main

import (
	"os"
	"path/filepath"
	"strings"
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
	catalogPass, err := os.ReadFile(filepath.Join(directory, "catalog_password"))
	if err != nil || len(catalogPass) != 64 {
		t.Fatal("invalid generated catalog password")
	}
	catalogURL, err := os.ReadFile(filepath.Join(directory, "catalog_url"))
	if err != nil || !strings.Contains(string(catalogURL), "bliss_catalog:") {
		t.Fatal("invalid generated catalog url")
	}
	if err = secrets(directory); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(directory, "runtime_password"))
	if err != nil || string(before) != string(after) {
		t.Fatal("setup rotated existing database password")
	}
	afterCatalogPass, err := os.ReadFile(filepath.Join(directory, "catalog_password"))
	if err != nil || string(catalogPass) != string(afterCatalogPass) {
		t.Fatal("setup rotated existing catalog password")
	}
	if err = os.Remove(filepath.Join(directory, "catalog_url")); err != nil {
		t.Fatal(err)
	}
	if err = secrets(directory); err == nil {
		t.Fatal("partial credentials were silently replaced")
	}
}
