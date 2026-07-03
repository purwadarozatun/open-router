package registry

import (
	"errors"
	"fmt"
	"strings"
)

// ServiceRoute maps an API path prefix to the upstream microservice that
// serves it. Path-based routing sits in front of the (shared, multi-tenant)
// microservices: the gateway picks the service by path prefix, while tenant
// identity still comes from the Host header (resolved via Resolve and
// forwarded upstream as X-Tenant-ID). The same tasklist-service instance
// therefore serves every tenant, distinguishing them by that header rather
// than by running one copy per tenant.
type ServiceRoute struct {
	Prefix string // path prefix, e.g. "/api/v1/tasklist"
	Name   string // service name, surfaced for logging/debugging
	Target string // upstream base URL, e.g. "http://localhost:9201"
}

// ErrNoRoute is returned by ResolveService when no service prefix matches a
// request path.
var ErrNoRoute = errors.New("registry: no service route for path")

// services is the POC's path-prefix -> microservice table. Like domains it is
// hardcoded for now and would later be DB/config backed. Each entry points at
// a distinct backend microservice; the gateway forwards the original path
// unchanged, so the service sees the full "/api/v1/tasklist/..." URL.
var services = []ServiceRoute{
	{Prefix: "/api/v1/tasklist", Name: "tasklist-service", Target: "http://localhost:9201"},
	{Prefix: "/api/v1/authentication", Name: "auth-service", Target: "http://localhost:9202"},
}

// ResolveService returns the ServiceRoute whose Prefix matches path, with the
// longest matching prefix winning so more specific routes are never shadowed
// by shorter ones. A prefix only matches at a path-segment boundary: prefix
// "/api/v1/tasklist" matches "/api/v1/tasklist" and "/api/v1/tasklist/123"
// but not "/api/v1/tasklistx". Returns ErrNoRoute (wrapped) if nothing
// matches.
func ResolveService(path string) (*ServiceRoute, error) {
	mu.RLock()
	defer mu.RUnlock()
	var best *ServiceRoute
	for i := range services {
		s := &services[i]
		if !matchesPrefix(path, s.Prefix) {
			continue
		}
		if best == nil || len(s.Prefix) > len(best.Prefix) {
			best = s
		}
	}
	if best == nil {
		return nil, fmt.Errorf("%w: %s", ErrNoRoute, path)
	}
	route := *best
	return &route, nil
}

// Services returns a copy of all registered service routes.
func Services() []ServiceRoute {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]ServiceRoute, len(services))
	copy(out, services)
	return out
}

// matchesPrefix reports whether path is covered by prefix, requiring the match
// to fall on a path-segment boundary (exact match, or the char after prefix is
// '/') so "/api/v1/tasklistx" does not match "/api/v1/tasklist".
func matchesPrefix(path, prefix string) bool {
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	return len(path) == len(prefix) || path[len(prefix)] == '/'
}
