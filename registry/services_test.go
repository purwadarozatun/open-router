package registry

import (
	"errors"
	"testing"
)

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
