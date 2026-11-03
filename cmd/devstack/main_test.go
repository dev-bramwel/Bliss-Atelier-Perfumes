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
	if err = os.Remove(filepath.Join(directory, "runtime_password")); err != nil {
		t.Fatal(err)
	}
	if err = secrets(directory); err == nil {
		t.Fatal("partial credentials were silently replaced")
	}
}

func TestSetupUpgradesLegacyCredentialsPreservingPasswords(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "legacy_secrets")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	legacyPasswords := map[string]string{
		"admin_password":    "1111111111111111111111111111111111111111111111111111111111111111",
		"migrator_password": "2222222222222222222222222222222222222222222222222222222222222222",
		"runtime_password":  "3333333333333333333333333333333333333333333333333333333333333333",
		"migrator_url":      "postgresql://bliss_migrator:2222222222222222222222222222222222222222222222222222222222222222@db:5432/bliss_dev?sslmode=disable",
		"runtime_url":       "postgresql://bliss_runtime:3333333333333333333333333333333333333333333333333333333333333333@db:5432/bliss_dev?sslmode=disable",
	}
	for name, val := range legacyPasswords {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(val), 0444); err != nil {
			t.Fatal(err)
		}
	}

	// secrets() should upgrade the legacy directory by adding catalog_password and catalog_url
	if err := secrets(directory); err != nil {
		t.Fatalf("failed to upgrade legacy credentials: %v", err)
	}

	// Verify legacy passwords and URLs are untouched
	for name, want := range legacyPasswords {
		got, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatalf("could not read %s: %v", name, err)
		}
		if string(got) != want {
			t.Fatalf("legacy credential %s altered: want %s, got %s", name, want, string(got))
		}
	}

	// Verify catalog_password and catalog_url were generated
	catPass, err := os.ReadFile(filepath.Join(directory, "catalog_password"))
	if err != nil || len(catPass) != 64 {
		t.Fatalf("invalid catalog_password after upgrade: %v, len=%d", err, len(catPass))
	}
	catURL, err := os.ReadFile(filepath.Join(directory, "catalog_url"))
	if err != nil || !strings.Contains(string(catURL), "bliss_catalog:"+string(catPass)) {
		t.Fatalf("invalid catalog_url after upgrade: %v, url=%s", err, string(catURL))
	}
}

func TestSetupReplacesDirectoryPlaceholders(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "secrets_with_dir_placeholders")
	if err := os.MkdirAll(filepath.Join(directory, "catalog_password"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(directory, "catalog_url"), 0700); err != nil {
		t.Fatal(err)
	}

	if err := secrets(directory); err != nil {
		t.Fatalf("secrets() failed when directory placeholders existed: %v", err)
	}

	for _, name := range []string{"catalog_password", "catalog_url", "runtime_password", "admin_password"} {
		info, err := os.Stat(filepath.Join(directory, name))
		if err != nil {
			t.Fatalf("missing file %s: %v", name, err)
		}
		if info.IsDir() {
			t.Fatalf("expected regular file for %s, got directory", name)
		}
	}
}
