// Command dummybackend is a minimal HTTP server used to manually verify the
// dynamic reverse proxy in main.go. Its root route ("/") responds with
// "Hello from <tenant> (port <port>)" plus the Host header it received, so
// two instances on different ports can be curled directly to confirm each
// serves its own tenant identity before being put behind the proxy. It also
// echoes back the full request headers it received (as JSON on /headers),
// so a curl test through the gateway can confirm which headers (Host,
// X-Forwarded-*) actually reach the upstream.
//
// Usage:
//
//	go run ./dummybackend -port 9001 -tenant tenant-1
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
)

func main() {
	port := flag.Int("port", 9001, "port to listen on")
	tenant := flag.String("tenant", "tenant-1", "tenant label returned in responses")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/headers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		headers := make(map[string]string, len(r.Header))
		for k := range r.Header {
			headers[k] = r.Header.Get(k)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"tenant":     *tenant,
			"host":       r.Host,
			"requestURI": r.RequestURI,
			"headers":    headers,
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Hello from %s (port %d)\nHost header diterima: %s\n", *tenant, *port, r.Host)
	})

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("dummybackend %s listening on %s", *tenant, addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
