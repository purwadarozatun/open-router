# poc-proxy-gateway

Proof-of-concept API gateway in Go, evaluating whether [Fiber](https://github.com/gofiber/fiber)
(reverse proxy) + [CertMagic](https://github.com/caddyserver/certmagic) (on-demand,
host-based TLS from SNI) can replace Caddy as the sole API gateway in front of
Alurkerja's backend services.

This is a separate, standalone module — not part of the main Alurkerja project.
The result of this POC feeds into the architecture decision for the "Custom
Domain / API Gateway Alurkerja" PRD.

## Tujuan

Menguji dua kapabilitas inti secara terpisah dari Caddy:

1. **TLS otomatis per-hostname dari SNI** — host policy diambil dari domain
   registry lokal (in-memory), lalu CertMagic menerbitkan/mengelola sertifikat
   on-demand untuk hostname yang terdaftar.
2. **Reverse proxy dinamis berdasarkan Host header** — request diteruskan ke
   backend service sesuai hasil resolusi domain registry.

## Struktur

```
poc-proxy-gateway/
├── main.go        # entrypoint: dynamic reverse proxy + CertMagic on-demand TLS listener
├── registry/      # in-memory domain -> backend target registry
├── tlsissuer/     # self-signed certmagic.Issuer (ACME fallback, see below)
├── dummybackend/  # throwaway net/http server for manual proxy verification
├── go.mod
└── README.md
```

The `registry` package is the in-memory domain → tenant/target lookup that
backs both the CertMagic host policy (`IsAllowed`) and reverse-proxy
routing (`Resolve`, not wired up yet — dynamic proxying is a later POC
step).

Registry API:

```go
type DomainEntry struct {
    Domain   string
    TenantID string
    Target   string // upstream URL, e.g. "http://localhost:9101"
}

func IsAllowed(domain string) bool           // CertMagic HostPolicy check
func Resolve(domain string) (*DomainEntry, error) // Fiber routing lookup
func Domains() []DomainEntry                 // list all registered entries
```

Data is hardcoded for the POC (no DB yet) — three entries seeded in
`registry/registry.go`, using `*.localtest.me` hostnames which resolve to
`127.0.0.1` publicly (no `/etc/hosts` edits needed):

| Domain                 | TenantID   | Target                  |
|-------------------------|-----------|--------------------------|
| client1.localtest.me    | tenant-1  | http://localhost:9101    |
| client2.localtest.me    | tenant-2  | http://localhost:9102    |
| custom.localtest.me     | tenant-1  | http://localhost:9101 *(simulasi BYOC domain)* |

`9101`/`9102` (not `9001`/`9002`) because `:9001` is already bound by an
unrelated MinIO console on the reference dev machine — picked once and
kept consistent everywhere (registry, dummy backends, this README).

## Cara jalankan

```bash
go mod tidy
go run .
```

This starts **two** listeners on the same Fiber app:

- `:8085` — plain HTTP, dynamic reverse proxy + diagnostic routes
- `:8443` — TLS, served via CertMagic's on-demand `tls.Config` (same
  routes/proxy behind it)

Requests are routed by Host header (see "Reverse proxy dinamis" below):
any hostname registered in `registry/registry.go` gets forwarded to its
`Target`; `localhost`/`127.0.0.1` fall through to this gateway's own
diagnostic routes; anything else gets a 404. Diagnostic routes (only
reachable via `localhost`/`127.0.0.1`):

- `GET /` — hello-world sanity check
- `GET /registry` — lists all registered domain entries
- `GET /registry/:domain` — resolves a single domain to its `DomainEntry`
  (404 if not registered)

To manually exercise the proxy, run a dummy backend per registered
target (see `dummybackend/main.go`):

```bash
go run ./dummybackend -port 9101 -tenant tenant-1
go run ./dummybackend -port 9102 -tenant tenant-2
```

Run unit tests:

```bash
go test ./...
```

## TLS otomatis (CertMagic + HostPolicy dari registry)

`buildTLSConfig()` in `main.go` wires `certmagic.OnDemandConfig.DecisionFunc`
straight to `registry.IsAllowed` — this **is** the replacement for Caddy's
`on_demand_tls.ask`: any SNI name that CertMagic doesn't already have a
cached cert for gets checked against the registry before an issuer is ever
invoked. Names not in the registry are rejected before certificate
issuance is attempted, exactly like the `ask` HTTP callback in Caddy.

The issuance backend behind that HostPolicy is selectable via the
`CERTMAGIC_MODE` env var:

```bash
CERTMAGIC_MODE=selfsigned  go run .   # default
CERTMAGIC_MODE=acme-staging go run .  # documented below, does not complete locally
```

### Pendekatan yang dipakai: self-signed local issuer (default)

**`selfsigned` is the default and the one that actually completes
end-to-end on a local dev machine.** `tlsissuer.SelfSigned` implements
`certmagic.Issuer` (`IssuerKey()` + `Issue(ctx, *x509.CertificateRequest)`)
backed by an in-memory root CA generated at startup. It plugs into exactly
the same `OnDemandConfig`/`DecisionFunc`/cache machinery as the ACME
issuer — the only difference is `Issue()` signs the CSR locally instead of
running a domain-control challenge over the network. The root CA is
written to `./certmagic-data/dev-ca.pem` on startup so test clients can
trust it explicitly (`curl --cacert`) instead of disabling verification.

**Verified manually** (server on `:8443`, `client1.localtest.me` and
`client2.localtest.me`/`custom.localtest.me` registered,
`tidak-terdaftar.localtest.me` not registered — `*.localtest.me` resolves
publicly to `127.0.0.1`, no `/etc/hosts` edits needed):

```bash
# registered domain: handshake succeeds, cert chains to the dev CA
$ curl --cacert ./certmagic-data/dev-ca.pem \
    https://client1.localtest.me:8443/registry/client1.localtest.me
{"Domain":"client1.localtest.me","TenantID":"tenant-1","Target":"http://localhost:9101"}
# openssl s_client confirms: issuer = poc-proxy-gateway local dev CA, Verify return code: 0 (ok)

# unregistered domain: DecisionFunc rejects it, handshake fails
$ curl --cacert ./certmagic-data/dev-ca.pem https://tidak-terdaftar.localtest.me:8443/
curl: (35) error:0A000438:SSL routines::tlsv1 alert internal error
```

Both Definition of Done handshake cases are satisfied by this path.

### Pendekatan yang dicoba dan tidak feasible: Let's Encrypt staging CA

The ACME path is fully wired (`CERTMAGIC_MODE=acme-staging`, `DefaultACME.CA
= LetsEncryptStagingCA` — production CA is never used) and **was actually
run** against this dev machine. It gets further than a simple "no
internet" failure — it successfully reaches Let's Encrypt's staging API and
registers an ACME account — but then fails, confirmed from the live log:

```
info  creating new account ... ca: https://acme-staging-v02.api.letsencrypt.org/directory
info  new ACME account registered  {"status": "valid"}
info  .acme_client  trying to solve challenge  {"challenge_type": "tls-alpn-01"}
error obtain  could not get certificate from issuer
  error: "...presenting with embedded solver: could not start listener
  for challenge server at :443: listen tcp :443: bind: permission denied"
error obtain  will retry  {"attempt": 1, "retrying_in": 60, ...}
```

Two independent, stacked reasons this can never complete from here, not
just a config tweak:

1. **Immediate cause**: the TLS-ALPN-01 challenge solver needs to bind
   port `:443`, which requires root/`CAP_NET_BIND_SERVICE` on Linux — the
   POC runs unprivileged.
2. **Fundamental cause, would still block even as root**: `client1.localtest.me`
   resolves publicly to `127.0.0.1` (that's the whole point of
   `*.localtest.me` — no `/etc/hosts` edits needed for local dev), which is
   the loopback address and is by definition not publicly routable. Let's
   Encrypt's servers connect to the *public* IP of the SNI name to run the
   challenge; they cannot reach into this machine's loopback interface
   over the internet no matter what's listening on port 443 locally. This
   dev machine also isn't port-forwarded/publicly reachable at all.

So the staging-CA code path is real, tested, and left in the codebase for
when this proxy runs on a machine with genuine public inbound reachability
(the actual Alurkerja gateway target) — but for this local POC, the
self-signed issuer is the only one that can prove the DecisionFunc/registry
wiring end-to-end, which is what this task set out to prove.

## Reverse proxy dinamis berdasarkan Host header

`main.go` registers a single `app.Use` middleware **before** any route,
so it sees every request first:

```go
app.Use(func(c fiber.Ctx) error {
    host := c.Hostname()
    if adminHosts[host] {
        return c.Next() // let the diagnostic routes below handle it
    }
    entry, err := registry.Resolve(host)
    if err != nil {
        return fiber.NewError(fiber.StatusNotFound, "domain tidak terdaftar")
    }
    req := c.Request()
    req.Header.Set(fiber.HeaderXForwardedHost, host)
    req.Header.Set(fiber.HeaderXForwardedProto, c.Scheme())
    req.Header.Set(fiber.HeaderXForwardedFor, c.IP())
    return proxy.Do(c, entry.Target+c.OriginalURL())
})
```

This is the pattern the task set out to prove: the upstream is resolved
**per-request** from `c.Hostname()` against the registry, not fixed at
startup like `proxy.DomainForward(hostname, addr)`. Adding a fourth domain
to `registry.domains` and restarting is all it takes to route a new
tenant — no new route/handler needed.

Two deliberate deviations from the task's example snippet:

- **`proxy.Do(c, entry.Target+c.OriginalURL())` instead of
  `proxy.Forward(entry.Target)`.** `proxy.Forward` calls `Do(c, addr)`
  with `addr` used verbatim as the full request URI — it drops the
  incoming path and query string entirely, so every request would land
  on the upstream's `/` regardless of what path the client requested.
  Fiber's own `DomainForward`/`BalancerForward` helpers avoid this by
  appending `c.OriginalURL()`, so the middleware does the same — required
  for this to behave like a real gateway rather than a root-only demo.
- **`adminHosts` fall-through.** Registering the resolver as `app.Use`
  (no path prefix) means it runs for every request, including ones aimed
  at this gateway's own `/` and `/registry*` diagnostic routes. Requests
  to `localhost`/`127.0.0.1` skip the registry lookup and fall through via
  `c.Next()` so those routes stay reachable for local ops/debugging;
  every other hostname is resolved against the registry or rejected.

### Verifikasi manual

Verified against two dummy backends (`dummybackend/main.go`, a throwaway
`net/http` server added for this task — `go run ./dummybackend -port
<port> -tenant <label>`; its `/` route responds `Hello from <tenant> (port
<port>)` plus the `Host` header it received, and `/headers` echoes the
full request headers as JSON, used below to check header propagation).

**Port note**: `registry/registry.go` targets `client1`/`custom` at
`localhost:9101` (not `9001`) because `:9001` is already bound by an
unrelated MinIO console on the reference dev machine — `9101`/`9102` is
the permanent, committed value, matched by the dummy backends and unit
tests below.

**Dummy backends run standalone too** (before even being put behind the
proxy), confirming each instance serves its own tenant identity and
correctly reports whatever `Host` header it receives — the property the
later proxy-forwarding tests above rely on:

```bash
$ go run ./dummybackend -port 9101 -tenant tenant-1 &
$ go run ./dummybackend -port 9102 -tenant tenant-2 &
$ curl http://localhost:9101/
Hello from tenant-1 (port 9101)
Host header diterima: localhost:9101
$ curl http://localhost:9102/
Hello from tenant-2 (port 9102)
Host header diterima: localhost:9102
```

```bash
$ curl http://client1.localtest.me:8085/
Hello from tenant-1
$ curl http://client2.localtest.me:8085/
Hello from tenant-2
$ curl http://custom.localtest.me:8085/       # simulasi BYOC -> tenant-1's target
Hello from tenant-1
$ curl -o /dev/null -w '%{http_code}\n' http://domain-tidak-terdaftar.localtest.me:8085/
404
$ curl http://localhost:8085/registry/client2.localtest.me   # admin host, bypasses proxy
{"Domain":"client2.localtest.me","TenantID":"tenant-2","Target":"http://localhost:9102"}
```

All five Definition of Done checks pass: correct tenant routing for both
registered domains, the BYOC domain resolving to tenant-1's target, an
unregistered domain returning a clean 404 (not a crash/hang), and the
gateway's own diagnostic routes still reachable via `localhost`.

Path and query string are preserved end-to-end (confirms the
`proxy.Forward` deviation above was necessary):

```bash
$ curl 'http://client1.localtest.me:8085/headers?foo=bar' | jq .requestURI
"/headers?foo=bar"
```

**Header forwarding — what actually reaches the backend:**

```bash
$ curl http://client1.localtest.me:8085/headers | jq .
{
  "host": "localhost:9101",
  "headers": {
    "X-Forwarded-Host": "client1.localtest.me",
    "X-Forwarded-Proto": "http",
    "X-Forwarded-For": "127.0.0.1"
  },
  ...
}
```

- **Original `Host` header is NOT preserved.** The dummy backend sees
  `Host: localhost:9101` (the upstream's own address), not
  `client1.localtest.me`. This is `fasthttp`'s behavior, not a bug in our
  middleware: `req.SetRequestURI()` (used internally by `proxy.Do`)
  rewrites the request's URI, and fasthttp derives the wire `Host` header
  from that URI at write time rather than keeping whatever the client
  originally sent. Caddy's reverse proxy preserves the original Host by
  default; Fiber's `proxy` middleware does not — this is a real gap
  between the two if backend apps depend on the original `Host` (e.g. for
  building absolute URLs or multi-tenant routing of their own).
- **`X-Forwarded-For`/`-Host`/`-Proto` are NOT set automatically either.**
  `proxy.Do`/`proxy.Forward` only ever set `X-Real-IP`. The middleware
  above sets all three explicitly before calling `proxy.Do`, and the
  verification confirms they arrive correctly once set that way.
- **Bottom line for the Caddy-replacement decision**: Fiber's proxy
  middleware forwards the original `Host` in neither the `Host` header
  nor any `X-Forwarded-*` header unless the gateway code does it
  explicitly. That's a viable pattern (shown above, ~3 lines), but it's
  manual — any future route added to this gateway needs to remember to
  set these, whereas Caddy does it by default. Worth carrying into the
  PRD comparison.

## Integrasi penuh: TLS listener + Fiber routing dalam satu proses

The TLS listener and the reverse-proxy routing above already run inside
**one Fiber app instance** — `main()` builds a single `app := fiber.New()`,
registers the dynamic-proxy middleware and diagnostic routes on it once,
then starts it on two listeners from two goroutines:

```go
go func() {
    log.Fatal(app.Listen(":8443", fiber.ListenConfig{TLSConfig: tlsConfig}))
}()
log.Fatal(app.Listen(":8085"))
```

**Deviation from the task's reference snippet.** The task description
suggests wiring TLS manually — `tls.Listen("tcp", ":8443", tlsConfig)` to
get a `net.Listener`, then `app.Listener(ln)` to hand it to Fiber. That
pattern is for Fiber v2 (or any case where you need to wrap/inspect the
listener yourself, e.g. adding `proxyproto`). Fiber v3 added
`fiber.ListenConfig.TLSConfig`, which does exactly the same
`tls.NewListener`-wrapping internally, just exposed as a config field
instead of a manual net.Listener you build and pass in. Same integration,
one line instead of four — no functional difference, just v3's idiom for
it. `app.Listener(ln)` is still there in v3 for cases that need a custom
listener Fiber doesn't build for you (e.g. `proxyproto.Listener`,
`autocert`), which isn't needed here since `ListenConfig.TLSConfig`
already covers CertMagic's `*tls.Config`.

The only genuinely new code for *this* task is making the registry
mutable while the process is running: `registry.Add` (mutex-guarded) and
`POST /registry`, which is what scenario 3 below exercises — everything
else (the TLS listener, the proxy middleware) was already wired by the
two dependency tasks and needed no changes to run side by side, because
they were built against the same `app` and the same `registry` package
from the start.

### Verifikasi end-to-end (tiga skenario dari task ini)

Single process, `go run .`, both `:8085` and `:8443` up together (same
PID for the whole run):

```
$ ss -tlnp | grep -E ':(8085|8443)'
LISTEN 0 4096 0.0.0.0:8085 users:(("poc-gw-run",pid=4184762,fd=6))
LISTEN 0 4096 0.0.0.0:8443 users:(("poc-gw-run",pid=4184762,fd=7))
```

**1. Registered domain over HTTPS → TLS handshake succeeds → proxied to
its backend:**

```
$ curl -sS --cacert ./certmagic-data/dev-ca.pem https://client1.localtest.me:8443/ -w '\nHTTP %{http_code}\n'
Hello from tenant-1
HTTP 200

$ openssl s_client -connect client1.localtest.me:8443 -servername client1.localtest.me \
    -CAfile ./certmagic-data/dev-ca.pem </dev/null 2>/dev/null | grep 'Verify return code'
Verify return code: 0 (ok)
```

Gateway log for that request — CertMagic obtains the cert (DecisionFunc
allowed it), then the same process serves the proxied response:

```
info  on_demand  obtaining new certificate  {"server_name": "client1.localtest.me"}
info  obtain     obtaining certificate      {"identifier": "client1.localtest.me"}
info  obtain     certificate obtained successfully  {"identifier": "client1.localtest.me", "issuer": "selfsigned-local-dev"}
```

**2. Unregistered domain over HTTPS → rejected at the TLS layer, before
Fiber routing ever runs — proving `DecisionFunc` is genuinely the first
gate, not just a filter inside the app:**

```
$ curl --cacert ./certmagic-data/dev-ca.pem https://tidak-terdaftar.localtest.me:8443/
curl: (35) error:0A000438:SSL routines::tlsv1 alert internal error
```

The handshake fails before a Server Hello/Certificate message is even
sent (confirmed with `curl -v`: no `TLS handshake, Certificate` line for
this request, unlike scenario 1's trace). The gateway log has **zero**
lines mentioning `tidak-terdaftar` — no `on_demand`/`obtain` entries (TLS
never got that far) and no `middleware traversal failed` entries either
(Fiber's `app.Use` proxy middleware never ran, because there was no HTTP
request to run it on — the connection died at the TLS layer).

**3. Add a new domain to the registry while the process is running (no
restart) → immediately routable and TLS-issuable:**

```
$ curl -s --cacert ./certmagic-data/dev-ca.pem https://live-added.localtest.me:8443/ -w '\nHTTP %{http_code}\n'
                                                    # not registered yet
HTTP 000

$ curl -s -X POST http://localhost:8085/registry -H 'Content-Type: application/json' \
    -d '{"Domain":"live-added.localtest.me","TenantID":"tenant-live","Target":"http://localhost:9104"}' \
    -w '\nHTTP %{http_code}\n'
{"Domain":"live-added.localtest.me","TenantID":"tenant-live","Target":"http://localhost:9104"}
HTTP 201

$ curl -sS --cacert ./certmagic-data/dev-ca.pem https://live-added.localtest.me:8443/ -w '\nHTTP %{http_code}\n'
Hello from tenant-live-added
HTTP 200

$ ps -p 4184762 -o pid,etimes,cmd   # same PID throughout, no restart
    PID ELAPSED CMD
4184762      49 /tmp/poc-gw-run
```

Same gateway process (PID unchanged, 49s uptime), same registry
in-memory slice — the `POST /registry` call went through `registry.Add`,
and the very next HTTPS request to that hostname both passed
`DecisionFunc`/got a cert issued *and* resolved/proxied correctly. This
is the "domain dari DB, bukan config statis" property: a real backend
would call `registry.Add` (or reload from a DB row) from a webhook/DB
write instead of a POST route, but the mechanism proven here — registry
mutation visible to both the TLS and routing layers on the very next
request, no reload signal needed — is identical.

All three Definition of Done scenarios pass.

### Gotcha ditemukan saat verifikasi: root CA tidak persisten lintas restart

`tlsissuer.NewSelfSigned()` generates a fresh in-memory root CA on every
process start (documented as a deliberate POC tradeoff in its doc
comment) — but CertMagic's on-disk certificate cache
(`certmagic-data/certificates/`, `FileStorage`) *is* persisted across
restarts by design, so the on-demand TLS cache doesn't re-issue on every
request. Combined, restarting the gateway leaves cached leaf certs signed
by a CA that no longer exists in memory: they fail chain validation
against the newly-written `dev-ca.pem` (`authority and subject key
identifier mismatch`) even though the leaf itself hasn't expired. Looks
like a routing bug when you hit it (empty response, no HTTP status) but
it's a trust-chain issue from the CA/cert-cache lifetime mismatch. Local
dev workaround: `rm -rf certmagic-data/certificates` (or the whole
`certmagic-data/` dir) before a fresh `go run .` when re-verifying TLS.
Not a code fix in scope of this task — worth carrying into the PRD
comparison below, since Caddy's internal CA (`tls internal`) is
persisted by default, so this class of restart gotcha doesn't apply to
it.

## Perbandingan vs Caddy (integrasi penuh)

**Lebih mudah dari perkiraan:**

- Menyatukan TLS + routing dalam satu proses Go ternyata murni soal
  konfigurasi, bukan arsitektur baru — satu `app` instance melayani kedua
  listener (`:8085` plain, `:8443` TLS); middleware/routing-nya sama
  persis untuk HTTP maupun HTTPS, tidak ada konfigurasi terpisah seperti
  Caddyfile vs reverse-proxy config.
- Fiber v3's `ListenConfig.TLSConfig` sudah membungkus listener-wrapping
  otomatis — pola manual di task description (`tls.Listen` +
  `app.Listener(ln)`, gaya Fiber v2) ternyata tidak perlu ditulis; cukup
  satu field config.
- `DecisionFunc` benar-benar jadi gerbang pertama seperti Caddy's
  `on_demand_tls.ask` — terbukti langsung dari log request: domain tak
  terdaftar gagal di level TLS handshake, tidak ada satu baris log pun
  dari layer Fiber untuk request itu.
- Registry mutation saat proses berjalan tidak butuh reload signal apa
  pun — baik TLS issuance maupun HTTP routing membaca struct in-memory
  yang sama, jadi begitu `registry.Add` dipanggil, request berikutnya
  langsung match. Caddy versi terbaru juga bisa reload config tanpa
  downtime, tapi itu tetap lewat menulis ulang Caddyfile/JSON dan
  memanggil reload API; di sini cukup satu function call Go biasa.

**Lebih ribet dari Caddy:**

- Root CA self-signed di POC ini tidak persisten antar restart (lihat
  gotcha di atas) — Caddy's internal CA persisten by default di
  storage-nya. Ini keterbatasan implementasi POC, bukan CertMagic itu
  sendiri, tapi harus diingat kalau pola self-signed ini dipakai lagi.
- Header forwarding (`Host`, `X-Forwarded-*`) tidak otomatis di Fiber's
  proxy middleware (lihat "Reverse proxy dinamis" di atas) — berlaku
  sama persis di jalur HTTPS: developer harus ingat set eksplisit di
  setiap gateway baru, sementara Caddy sudah benar secara default.
- Tidak ada pemisahan proses TLS-terminator vs app-router seperti
  Caddy+upstream — enak untuk POC (satu binary, satu deploy unit), tapi
  berarti crash/panic di satu bagian (misalnya bug di proxy middleware)
  langsung menjatuhkan TLS listener juga. Caddy yang independen dari app
  tetap bisa terminate TLS & balas 502 walau upstream down. Perlu
  supervisor/health-check tambahan kalau proxy-service ini menggantikan
  Caddy sepenuhnya di production.

## Status

Domain registry done (in-memory, hardcoded, `IsAllowed`/`Resolve`, unit
tests). On-demand TLS done: CertMagic listener on `:8443`, HostPolicy
(`DecisionFunc`) backed by the registry, self-signed issuer as the working
local path, ACME staging issuer wired and tested (fails as documented
above, expected on a non-public dev machine). Dynamic reverse proxy done:
Host header resolved against the registry per-request and forwarded via
`proxy.Do`, verified against both registered tenants, the simulated BYOC
domain, and the 404 path for unregistered domains; header-forwarding
behavior documented above. Full integration done: TLS listener and proxy
routing run in the same Fiber app/process, registry is mutable at runtime
(`registry.Add`/`POST /registry`), and all three integration-task
scenarios (registered-domain HTTPS proxy, TLS-layer rejection before
Fiber routing, live registry addition without restart) verified above.
