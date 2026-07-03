package registry

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDomains(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "domains.json")
	body := `[
	  {"domain":"a.example.com","tenantId":"tenant-a","target":"http://localhost:9101"},
	  {"domain":"b.example.com","tenantId":"tenant-b","target":"http://localhost:9101"}
	]`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	// Snapshot and restore the package-level registry so this test does not
	// leak its loaded entries into the others.
	saved := Domains()
	t.Cleanup(func() {
		mu.Lock()
		domains = saved
		mu.Unlock()
	})

	if err := LoadDomains(path); err != nil {
		t.Fatalf("LoadDomains(%q) unexpected error: %v", path, err)
	}
	entry, err := Resolve("a.example.com")
	if err != nil {
		t.Fatalf("Resolve(a.example.com) after load: %v", err)
	}
	if entry.TenantID != "tenant-a" {
		t.Errorf("Resolve(a.example.com).TenantID = %q, want tenant-a", entry.TenantID)
	}
	// The old seed entries must be gone — LoadDomains replaces, not appends.
	if IsAllowed("client1.localtest.me") {
		t.Error("IsAllowed(client1.localtest.me) = true after replacing load, want false")
	}
}

func TestLoadDomainsErrors(t *testing.T) {
	// Missing file and empty list are both errors; the registry is untouched.
	if err := LoadDomains(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("LoadDomains(missing) error = nil, want non-nil")
	}
	empty := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(empty, []byte("[]"), 0o644); err != nil {
		t.Fatalf("write empty config: %v", err)
	}
	if err := LoadDomains(empty); err == nil {
		t.Error("LoadDomains(empty list) error = nil, want non-nil")
	}
}

func TestIsAllowed(t *testing.T) {
	tests := []struct {
		domain string
		want   bool
	}{
		{"client1.localtest.me", true},
		{"client2.localtest.me", true},
		{"custom.localtest.me", true},
		{"tidak-terdaftar.com", false},
	}

	for _, tt := range tests {
		if got := IsAllowed(tt.domain); got != tt.want {
			t.Errorf("IsAllowed(%q) = %v, want %v", tt.domain, got, tt.want)
		}
	}
}

func TestResolve(t *testing.T) {
	entry, err := Resolve("client1.localtest.me")
	if err != nil {
		t.Fatalf("Resolve(client1.localtest.me) unexpected error: %v", err)
	}
	if entry.TenantID != "tenant-1" || entry.Target != "http://localhost:9101" {
		t.Errorf("Resolve(client1.localtest.me) = %+v, want TenantID=tenant-1 Target=http://localhost:9101", entry)
	}
}

func TestResolveNotFound(t *testing.T) {
	entry, err := Resolve("tidak-terdaftar.com")
	if err == nil {
		t.Fatal("Resolve(tidak-terdaftar.com) expected error, got nil")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Resolve(tidak-terdaftar.com) error = %v, want wrapping ErrNotFound", err)
	}
	if entry != nil {
		t.Errorf("Resolve(tidak-terdaftar.com) entry = %+v, want nil", entry)
	}
}

func TestAdd(t *testing.T) {
	newEntry := DomainEntry{Domain: "new-tenant.localtest.me", TenantID: "tenant-3", Target: "http://localhost:9003"}

	if IsAllowed(newEntry.Domain) {
		t.Fatalf("IsAllowed(%q) = true before Add, want false", newEntry.Domain)
	}

	if err := Add(newEntry); err != nil {
		t.Fatalf("Add(%+v) unexpected error: %v", newEntry, err)
	}

	if !IsAllowed(newEntry.Domain) {
		t.Errorf("IsAllowed(%q) = false after Add, want true", newEntry.Domain)
	}
	entry, err := Resolve(newEntry.Domain)
	if err != nil {
		t.Fatalf("Resolve(%q) unexpected error: %v", newEntry.Domain, err)
	}
	if *entry != newEntry {
		t.Errorf("Resolve(%q) = %+v, want %+v", newEntry.Domain, entry, newEntry)
	}
}

func TestAddDuplicate(t *testing.T) {
	err := Add(DomainEntry{Domain: "client1.localtest.me", TenantID: "tenant-1", Target: "http://localhost:9001"})
	if !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("Add(client1.localtest.me) error = %v, want wrapping ErrAlreadyExists", err)
	}
}
