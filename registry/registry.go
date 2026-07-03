// Package registry provides an in-memory domain -> tenant/target lookup,
// used by the gateway to resolve TLS policy (CertMagic HostPolicy) and
// reverse-proxy routing (Fiber) based on the incoming Host/SNI.
package registry

import (
	"errors"
	"fmt"
	"sync"
)

// DomainEntry associates a hostname with the tenant and upstream backend it
// should be routed to.
type DomainEntry struct {
	Domain   string
	TenantID string
	Target   string // upstream URL, e.g. "http://localhost:9101"
}

// ErrNotFound is returned when a domain has no registered entry.
var ErrNotFound = errors.New("registry: domain not found")

// ErrAlreadyExists is returned by Add when domain is already registered.
var ErrAlreadyExists = errors.New("registry: domain already registered")

// mu guards domains. The integration POC proves the registry can be
// mutated while the process is running (Add, below) — CertMagic's
// DecisionFunc and Fiber's routing middleware both read the registry on
// every request, so a concurrent writer (a future DB-backed reload, an
// admin endpoint, ...) must be safe without restarting the gateway.
var mu sync.RWMutex

// domains is the POC's hardcoded-at-startup domain registry. A later
// iteration will back this with a database; for now it seeds the gateway
// with enough entries to exercise CertMagic's on-demand TLS host policy and
// Fiber's routing end to end, including a simulated BYOC domain
// (custom.localtest.me) that maps onto an existing tenant. Add lets new
// entries be registered at runtime, simulating rows appearing in that
// future database without a redeploy.
var domains = []DomainEntry{
	{Domain: "client1.localtest.me", TenantID: "tenant-1", Target: "http://localhost:9101"},
	{Domain: "client2.localtest.me", TenantID: "tenant-2", Target: "http://localhost:9102"},
	{Domain: "custom.localtest.me", TenantID: "tenant-1", Target: "http://localhost:9101"}, // simulasi BYOC domain
}

// IsAllowed reports whether domain is registered. Intended for use as a
// CertMagic on-demand TLS host policy check.
func IsAllowed(domain string) bool {
	mu.RLock()
	defer mu.RUnlock()
	for _, d := range domains {
		if d.Domain == domain {
			return true
		}
	}
	return false
}

// Resolve returns the DomainEntry registered for domain, used by Fiber
// routing to determine which tenant/backend a request should be proxied
// to. It returns ErrNotFound (wrapped) if domain is not registered.
func Resolve(domain string) (*DomainEntry, error) {
	mu.RLock()
	defer mu.RUnlock()
	for _, d := range domains {
		if d.Domain == domain {
			entry := d
			return &entry, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrNotFound, domain)
}

// Domains returns a copy of all currently registered domain entries.
func Domains() []DomainEntry {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]DomainEntry, len(domains))
	copy(out, domains)
	return out
}

// Add registers a new domain entry while the process is running — no
// restart needed for it to become reachable through IsAllowed/Resolve on
// the very next request. This is what proves "domain dari DB, bukan
// config statis" for the integration POC: a real backend would call this
// (or an equivalent reload) from a DB write/webhook instead of a hardcoded
// slice literal at startup. Returns ErrAlreadyExists (wrapped) if domain is
// already registered.
func Add(entry DomainEntry) error {
	mu.Lock()
	defer mu.Unlock()
	for _, d := range domains {
		if d.Domain == entry.Domain {
			return fmt.Errorf("%w: %s", ErrAlreadyExists, entry.Domain)
		}
	}
	domains = append(domains, entry)
	return nil
}
