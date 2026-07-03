package registry

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadServices(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "services.json")
	body := `[
	  {"prefix":"/api/v1/orders","name":"orders-service","target":"http://localhost:9301"}
	]`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	// Snapshot and restore so this test does not leak into the others.
	saved := Services()
	t.Cleanup(func() {
		mu.Lock()
		services = saved
		mu.Unlock()
	})

	if err := LoadServices(path); err != nil {
		t.Fatalf("LoadServices(%q) unexpected error: %v", path, err)
	}
	svc, err := ResolveService("/api/v1/orders/42")
	if err != nil {
		t.Fatalf("ResolveService(/api/v1/orders/42) after load: %v", err)
	}
	if svc.Name != "orders-service" {
		t.Errorf("ResolveService(...).Name = %q, want orders-service", svc.Name)
	}
	// The old seed routes must be gone — LoadServices replaces, not appends.
	if _, err := ResolveService("/api/v1/tasklist"); !errors.Is(err, ErrNoRoute) {
		t.Errorf("ResolveService(/api/v1/tasklist) after replacing load = %v, want ErrNoRoute", err)
	}
}

func TestLoadServicesErrors(t *testing.T) {
	if err := LoadServices(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("LoadServices(missing) error = nil, want non-nil")
	}
	empty := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(empty, []byte("[]"), 0o644); err != nil {
		t.Fatalf("write empty config: %v", err)
	}
	if err := LoadServices(empty); err == nil {
		t.Error("LoadServices(empty list) error = nil, want non-nil")
	}
}

func TestResolveService(t *testing.T) {
	tests := []struct {
		path     string
		wantName string
	}{
		{"/api/v1/tasklist", "tasklist-service"},
		{"/api/v1/tasklist/123", "tasklist-service"},
		{"/api/v1/authentication", "auth-service"},
		{"/api/v1/authentication/login", "auth-service"},
	}
	for _, tt := range tests {
		svc, err := ResolveService(tt.path)
		if err != nil {
			t.Fatalf("ResolveService(%q) unexpected error: %v", tt.path, err)
		}
		if svc.Name != tt.wantName {
			t.Errorf("ResolveService(%q) = %q, want %q", tt.path, svc.Name, tt.wantName)
		}
	}
}

func TestResolveServiceNoRoute(t *testing.T) {
	// Boundary check: must not match "/api/v1/tasklistx", and unknown paths
	// return ErrNoRoute.
	for _, path := range []string{"/", "/api/v1/tasklistx", "/api/v1/unknown"} {
		svc, err := ResolveService(path)
		if !errors.Is(err, ErrNoRoute) {
			t.Errorf("ResolveService(%q) error = %v, want wrapping ErrNoRoute", path, err)
		}
		if svc != nil {
			t.Errorf("ResolveService(%q) = %+v, want nil", path, svc)
		}
	}
}
