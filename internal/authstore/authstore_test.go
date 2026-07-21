package authstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfilesDefaultSelectionAndPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "credentials")
	SetPathOverride(path)
	t.Cleanup(func() { SetPathOverride("") })

	if err := PutProfile("first", Profile{Endpoint: "https://one.example", Token: "one"}, false); err != nil {
		t.Fatalf("put first profile: %v", err)
	}
	if err := PutProfile("second", Profile{Endpoint: "https://two.example", Token: "two"}, false); err != nil {
		t.Fatalf("put second profile: %v", err)
	}
	profile, err := ResolveProfile("")
	if err != nil || profile.Name != "first" {
		t.Fatalf("default profile = %#v, %v", profile, err)
	}
	if err := SetDefault("second"); err != nil {
		t.Fatalf("set default: %v", err)
	}
	profile, err = ResolveProfile("")
	if err != nil || profile.Name != "second" {
		t.Fatalf("changed default profile = %#v, %v", profile, err)
	}
	fileInfo, err := os.Stat(path)
	if err != nil || fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("credentials mode = %v, %v", fileInfo.Mode().Perm(), err)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil || dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("credentials directory mode = %v, %v", dirInfo.Mode().Perm(), err)
	}

	store, err := LoadStore()
	if err != nil {
		t.Fatal(err)
	}
	first := store.Profiles["first"]
	first.Default = true
	store.Profiles["first"] = first
	if err := SaveStore(store); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveProfile(""); err == nil || !strings.Contains(err.Error(), "multiple default profiles") || !strings.Contains(err.Error(), "auth use") {
		t.Fatalf("multiple-default error = %v", err)
	}
}

func TestPermissionWarning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials")
	SetPathOverride(path)
	t.Cleanup(func() { SetPathOverride("") })
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	warning, err := PermissionWarning()
	if err != nil || !strings.Contains(warning, "chmod 600") {
		t.Fatalf("warning = %q, %v", warning, err)
	}
}
