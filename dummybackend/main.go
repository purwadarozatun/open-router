// Command dummybackend is a minimal HTTP server used to manually verify the
// dynamic reverse proxy in main.go. It stands in for the SINGLE shared
// backend service that serves every tenant: one instance handles all
// registered domains, and it learns which tenant a request belongs to from
// the X-Tenant-ID header the gateway injects (resolved from the Host against
// the registry). Its root route ("/") responds with
// "Hello from <tenant> (port <port>)" using that per-request tenant, so a
// curl through the gateway for client1 vs client2 hits the same instance yet
// reports the correct tenant. It also echoes back the full request headers
// it received (as JSON on /headers), so a curl test through the gateway can
// confirm which headers (Host, X-Forwarded-*, X-Tenant-ID) actually reach
// the upstream.
//
// The -name flag labels which microservice this instance stands in for
// (e.g. tasklist-service, auth-service) so a curl through the path-based
// router shows which upstream actually handled the request. The -tenant flag
// is only a fallback tenant label for direct curls that bypass the gateway
// (no X-Tenant-ID header present).
//
// Usage:
//
//	go run ./dummybackend -port 9201 -name tasklist-service
//	go run ./dummybackend -port 9202 -name auth-service
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
)

func main() {
	port := flag.Int("port", 9101, "port to listen on")
	tenant := flag.String("tenant", "unknown", "fallback tenant label when no X-Tenant-ID header is present")
	name := flag.String("name", "dummybackend", "service name this instance stands in for")
	flag.Parse()

	// tenantOf returns the tenant this request is for: the gateway-injected
	// X-Tenant-ID header when present, otherwise the -tenant fallback (for
	// direct curls that skip the gateway).
	tenantOf := func(r *http.Request) string {
		if t := r.Header.Get("X-Tenant-ID"); t != "" {
			return t
		}
		return *tenant
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/headers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		headers := make(map[string]string, len(r.Header))
		for k := range r.Header {
			headers[k] = r.Header.Get(k)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"service":    *name,
			"tenant":     tenantOf(r),
			"host":       r.Host,
			"requestURI": r.RequestURI,
			"headers":    headers,
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Hello from %s (service %s, port %d)\nHost header diterima: %s\n", tenantOf(r), *name, *port, r.Host)
	})

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("dummybackend %q (shared, tenant from X-Tenant-ID) listening on %s", *name, addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
