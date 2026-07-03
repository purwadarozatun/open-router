// Command poc-proxy-gateway is a proof-of-concept API gateway.
//
// It exists to evaluate whether Fiber (reverse proxy) + CertMagic
// (on-demand, host-based TLS from SNI) can replace Caddy as the sole
// API gateway in front of Alurkerja's backend services. On-demand TLS
// wires CertMagic's DecisionFunc to the domain registry as its HostPolicy
// (the replacement for Caddy's `on_demand_tls.ask`), and a TLS listener on
// :8443 serves certificates for registered hostnames only. The dynamic
// reverse proxy middleware (below) reads the Host header on every
// request, looks it up in the registry, and forwards to whatever upstream
// target is registered — see README for the manual verification and the
// header-forwarding findings.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"html"
	"log"
	"os"

	"github.com/caddyserver/certmagic"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/proxy"

	"poc-proxy-gateway/registry"
	"poc-proxy-gateway/tlsissuer"
)

// certmagicDataDir is where CertMagic persists issued certificates/keys
// (FileStorage) and where the self-signed dev CA is written for test
// clients to trust.
const certmagicDataDir = "./certmagic-data"

// adminHosts are hostnames served locally by this gateway process itself
// (the diagnostic routes below), bypassing the reverse proxy. Any other
// hostname is resolved dynamically against the registry.
var adminHosts = map[string]bool{
	"localhost": true,
	"127.0.0.1": true,
}

func main() {
	// renderErrorPage turns any handler/middleware error into a styled HTML
	// page (e.g. the 404 shown when a Host or an API path is not registered),
	// instead of Fiber's default plain-text error body.
	app := fiber.New(fiber.Config{
		ErrorHandler: renderErrorPage,
	})

	// Dynamic reverse proxy: registered first so it sees every request
	// before route matching. It reads the Host header per request (not a
	// startup-time static target like proxy.DomainForward), looks it up in
	// the registry, and forwards accordingly. Admin hosts fall through via
	// c.Next() so the diagnostic routes below stay reachable.
	app.Use(func(c fiber.Ctx) error {
		host := c.Hostname()
		if adminHosts[host] {
			return c.Next()
		}

		entry, err := registry.Resolve(host)
		if err != nil {
			return fiber.NewError(fiber.StatusNotFound, "domain tidak terdaftar")
		}

		req := c.Request()
		req.Header.Set(fiber.HeaderXForwardedHost, host)
		req.Header.Set(fiber.HeaderXForwardedProto, c.Scheme())
		req.Header.Set(fiber.HeaderXForwardedFor, c.IP())

		// Tenant identity is resolved here from the Host (via the registry)
		// and forwarded to the shared upstreams as X-Tenant-ID. This is what
		// makes "many URLs -> one service" work: each backend reads this
		// header to know which tenant a request belongs to, instead of
		// running one instance per tenant.
		req.Header.Set("X-Tenant-ID", entry.TenantID)

		// Path-based routing to microservices: the request path picks the
		// upstream service (/api/v1/tasklist -> tasklist-service, ...). A path
		// that matches no registered service route is rejected with 404 — the
		// gateway only forwards URLs it explicitly knows, it does not fall
		// through to a default backend.
		svc, err := registry.ResolveService(c.Path())
		if err != nil {
			return fiber.NewError(fiber.StatusNotFound, "route tidak terdaftar")
		}
		req.Header.Set("X-Gateway-Service", svc.Name)

		// proxy.Forward(addr) forwards addr verbatim, dropping the
		// incoming path/query — fine for proxy.DomainForward's own use
		// but wrong for a real gateway. Do(addr+OriginalURL) preserves
		// path and query, matching what DomainForward/BalancerForward do
		// internally.
		return proxy.Do(c, svc.Target+c.OriginalURL())
	})

	app.Get("/", func(c fiber.Ctx) error {
		return c.SendString("hello world - poc-proxy-gateway skeleton")
	})

	app.Get("/registry", func(c fiber.Ctx) error {
		return c.JSON(registry.Domains())
	})

	// /services exposes the path-prefix -> microservice route table, the
	// counterpart to /registry (host -> tenant). Diagnostic only; reachable
	// via adminHosts like the other routes below.
	app.Get("/services", func(c fiber.Ctx) error {
		return c.JSON(registry.Services())
	})

	app.Get("/registry/:domain", func(c fiber.Ctx) error {
		entry, err := registry.Resolve(c.Params("domain"))
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(entry)
	})

	// POST /registry demonstrates that the registry is mutable while the
	// gateway is running — a new domain added here becomes routable and
	// TLS-issuable on its very next request, without restarting the
	// process. Stands in for the future DB write/webhook that would call
	// registry.Add for real; only reachable via adminHosts (see app.Use
	// above), same as the other diagnostic routes.
	app.Post("/registry", func(c fiber.Ctx) error {
		var entry registry.DomainEntry
		if err := c.Bind().Body(&entry); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		if err := registry.Add(entry); err != nil {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusCreated).JSON(entry)
	})

	tlsConfig, err := buildTLSConfig()
	if err != nil {
		log.Fatalf("certmagic: %v", err)
	}

	// Full integration: TLS listener and reverse-proxy routing share this
	// one `app` — same middleware/routes as the :8085 listener below, just
	// reached over TLS. fiber.ListenConfig.TLSConfig wraps the listener in
	// tls.NewListener internally, same effect as the integration task's
	// reference pattern (tls.Listen + app.Listener(ln)) which is Fiber v2
	// idiom; v3 exposes it as a config field instead. See README for the
	// end-to-end verification of this listener against the registry.
	go func() {
		log.Fatal(app.Listen(":8443", fiber.ListenConfig{
			TLSConfig:             tlsConfig,
			DisableStartupMessage: true,
		}))
	}()

	// 8085 chosen to avoid clashing with other local dev services (e.g.
	// agent-manager on :8080). No significance beyond that.
	log.Fatal(app.Listen(":8085"))
}

// renderErrorPage is Fiber's ErrorHandler: it renders errors returned by the
// routing middleware/handlers as an HTML page. The two registry-miss cases —
// unregistered Host and unregistered API path — both reach here as
// *fiber.Error with StatusNotFound, so the visitor gets a proper 404 page
// rather than a bare text body. Upstream responses proxied by proxy.Do do not
// pass through here; only errors this gateway itself raises do.
func renderErrorPage(c fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	message := "Internal Server Error"
	var fe *fiber.Error
	if errors.As(err, &fe) {
		code = fe.Code
		message = fe.Message
	}

	c.Status(code).Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
	return c.SendString(fmt.Sprintf(`<!doctype html>
<html lang="id">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%d — %s</title>
<style>
  :root { color-scheme: light dark; }
  body { margin: 0; min-height: 100vh; display: grid; place-items: center;
         font-family: system-ui, -apple-system, Segoe UI, Roboto, sans-serif;
         background: #0b0f1a; color: #e5e9f0; }
  .card { text-align: center; padding: 3rem 2.5rem; max-width: 30rem; }
  .code { font-size: 5rem; font-weight: 800; line-height: 1; letter-spacing: -.03em;
          background: linear-gradient(135deg,#6ea8fe,#a78bfa); -webkit-background-clip: text;
          background-clip: text; color: transparent; }
  .msg { margin-top: .75rem; font-size: 1.15rem; color: #c7cede; }
  .hint { margin-top: 1.5rem; font-size: .85rem; color: #7d879c; }
  code { background: rgba(255,255,255,.08); padding: .1em .4em; border-radius: .3em; }
</style>
</head>
<body>
  <div class="card">
    <div class="code">%d</div>
    <div class="msg">%s</div>
    <div class="hint">API Gateway &mdash; host &amp; path harus terdaftar di registry.</div>
  </div>
</body>
</html>`, code, html.EscapeString(message), code, html.EscapeString(message)))
}

// buildTLSConfig wires CertMagic's on-demand TLS to the domain registry:
// registry.IsAllowed acts as the HostPolicy (DecisionFunc) gating which SNI
// names may ever receive a certificate — this replaces Caddy's
// `on_demand_tls.ask`.
//
// The issuance backend is selected via the CERTMAGIC_MODE env var:
//
//   - "acme-staging" — Let's Encrypt staging CA (never production — see
//     README for why this cannot complete from this dev machine: no public
//     inbound access for the HTTP-01/TLS-ALPN-01 challenge).
//   - "selfsigned" (default) — an in-memory root CA that signs on-demand
//     certs directly, no ACME challenge involved. This is the path that
//     actually completes end-to-end locally.
func buildTLSConfig() (*tls.Config, error) {
	if err := os.MkdirAll(certmagicDataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create certmagic data dir: %w", err)
	}

	magic := certmagic.NewDefault()
	magic.Storage = &certmagic.FileStorage{Path: certmagicDataDir}

	// HostPolicy — pengganti langsung Caddy `ask`.
	magic.OnDemand = &certmagic.OnDemandConfig{
		DecisionFunc: func(_ context.Context, name string) error {
			if !registry.IsAllowed(name) {
				return fmt.Errorf("domain %s tidak terdaftar di registry", name)
			}
			return nil
		},
	}

	mode := os.Getenv("CERTMAGIC_MODE")
	if mode == "" {
		mode = "selfsigned"
	}

	switch mode {
	case "acme-staging":
		certmagic.DefaultACME.CA = certmagic.LetsEncryptStagingCA // WAJIB staging untuk POC, jangan production CA
		myACME := certmagic.NewACMEIssuer(magic, certmagic.DefaultACME)
		magic.Issuers = []certmagic.Issuer{myACME}
	case "selfsigned":
		ss, err := tlsissuer.NewSelfSigned()
		if err != nil {
			return nil, fmt.Errorf("init self-signed issuer: %w", err)
		}
		caPath := certmagicDataDir + "/dev-ca.pem"
		if err := os.WriteFile(caPath, ss.CAPEM(), 0o644); err != nil {
			log.Printf("warning: could not persist dev CA to %s: %v", caPath, err)
		} else {
			log.Printf("self-signed dev CA written to %s (trust it with curl --cacert, or use -k)", caPath)
		}
		magic.Issuers = []certmagic.Issuer{ss}
	default:
		return nil, fmt.Errorf("unknown CERTMAGIC_MODE %q (want acme-staging or selfsigned)", mode)
	}

	tlsConfig := magic.TLSConfig()
	// magic.TLSConfig() only advertises the ACME TLS-ALPN protocol; add
	// http/1.1 so ordinary HTTPS clients (curl, browsers) can still
	// negotiate ALPN successfully against this listener.
	tlsConfig.NextProtos = append(tlsConfig.NextProtos, "http/1.1")
	return tlsConfig, nil
}
