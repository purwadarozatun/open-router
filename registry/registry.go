// Package registry provides an in-memory domain -> tenant/target lookup,
// used by the gateway to resolve TLS policy (CertMagic HostPolicy) and
// reverse-proxy routing (Fiber) based on the incoming Host/SNI.
package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
)

// DomainEntry associates a hostname with the tenant and upstream backend it
// should be routed to. The json tags define the on-disk shape loaded by
// LoadDomains (see config/domains.json) and the JSON returned by the
// gateway's /registry route.
type DomainEntry struct {
	Domain   string `json:"domain"`
	TenantID string `json:"tenantId"`
	Target   string `json:"target"` // upstream URL, e.g. "http://localhost:9101"
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
//
// All domains resolve to a SINGLE upstream service (:9101). This is the
// realistic multi-tenant SaaS shape: one application serves every tenant,
// and tenant identity is carried per request rather than per backend. The
// gateway resolves TenantID from the Host here and forwards it upstream as
// the X-Tenant-ID header (see main.go), so the one backend knows which
// tenant a request belongs to without keeping its own copy of this mapping.
//
// This slice is only the built-in fallback: at startup the gateway calls
// LoadDomains to replace it with config/domains.json, so the domain list is
// editable config rather than a recompile. The seed keeps tests and a
// config-less run working.
var domains = []DomainEntry{
	{Domain: "client1.localtest.me", TenantID: "tenant-1", Target: "http://localhost:9101"},
	{Domain: "client2.localtest.me", TenantID: "tenant-2", Target: "http://localhost:9101"},
	{Domain: "custom.localtest.me", TenantID: "tenant-1", Target: "http://localhost:9101"}, // simulasi BYOC domain
}

// LoadDomains replaces the in-memory domain registry with the entries in the
// JSON file at path (a JSON array of DomainEntry objects — see
// config/domains.json). Intended to be called once at startup so the domain
// list is loaded config rather than the hardcoded seed. On any read/parse
// error the existing domains are left untouched and the error is returned, so
// a missing or malformed file degrades to the built-in fallback rather than
// wiping the registry.
func LoadDomains(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read domains config: %w", err)
	}
	var loaded []DomainEntry
	if err := json.Unmarshal(data, &loaded); err != nil {
		return fmt.Errorf("parse domains config %s: %w", path, err)
	}
	if len(loaded) == 0 {
		return fmt.Errorf("domains config %s has no entries", path)
	}
	mu.Lock()
	defer mu.Unlock()
	domains = loaded
	return nil
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
