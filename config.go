package main

import (
	"os"
	"strings"
)

// Config holds the gateway's runtime settings, read from environment
// variables at startup (see LoadConfigFromEnv). Every field has a built-in
// default via defaultConfig, so an env-less run still yields a working
// gateway — only the variables that are set override the defaults.
type Config struct {
	// HTTPAddr is the plain-HTTP listen address (GATEWAY_HTTP_ADDR).
	HTTPAddr string
	// HTTPSAddr is the TLS listen address served by CertMagic
	// (GATEWAY_HTTPS_ADDR).
	HTTPSAddr string
	// CertMagicMode selects the TLS issuance backend: "selfsigned" or
	// "acme-staging" (CERTMAGIC_MODE).
	CertMagicMode string
	// CertMagicDataDir is where CertMagic persists certs and the dev CA
	// (CERTMAGIC_DATA_DIR).
	CertMagicDataDir string
	// ErrorPagePath is the static HTML template rendered for gateway errors
	// (GATEWAY_ERROR_PAGE_PATH).
	ErrorPagePath string
	// DomainsConfigPath is the JSON domain list loaded into the registry
	// (GATEWAY_DOMAINS_CONFIG_PATH).
	DomainsConfigPath string
	// ServicesConfigPath is the JSON path-prefix -> internal service route
	// table loaded into the registry (GATEWAY_SERVICES_CONFIG_PATH).
	ServicesConfigPath string
	// AdminHosts are hostnames served locally by the gateway itself
	// (diagnostic routes), bypassing the reverse proxy. Set via
	// GATEWAY_ADMIN_HOSTS as a comma-separated list.
	AdminHosts []string
}

// defaultConfig returns the built-in gateway settings used when the
// corresponding environment variables are unset. These mirror the values the
// gateway used before config was externalized.
func defaultConfig() Config {
	return Config{
		HTTPAddr:           ":8085",
		HTTPSAddr:          ":8443",
		CertMagicMode:      "selfsigned",
		CertMagicDataDir:   "./certmagic-data",
		ErrorPagePath:      "./static/404.html",
		DomainsConfigPath:  "./config/domains.json",
		ServicesConfigPath: "./config/services.json",
		AdminHosts:         []string{"localhost", "127.0.0.1"},
	}
}

// LoadConfigFromEnv builds the gateway config from environment variables,
// each falling back to its defaultConfig value when unset or empty.
func LoadConfigFromEnv() Config {
	cfg := defaultConfig()
	cfg.HTTPAddr = envOr("GATEWAY_HTTP_ADDR", cfg.HTTPAddr)
	cfg.HTTPSAddr = envOr("GATEWAY_HTTPS_ADDR", cfg.HTTPSAddr)
	cfg.CertMagicMode = envOr("CERTMAGIC_MODE", cfg.CertMagicMode)
	cfg.CertMagicDataDir = envOr("CERTMAGIC_DATA_DIR", cfg.CertMagicDataDir)
	cfg.ErrorPagePath = envOr("GATEWAY_ERROR_PAGE_PATH", cfg.ErrorPagePath)
	cfg.DomainsConfigPath = envOr("GATEWAY_DOMAINS_CONFIG_PATH", cfg.DomainsConfigPath)
	cfg.ServicesConfigPath = envOr("GATEWAY_SERVICES_CONFIG_PATH", cfg.ServicesConfigPath)
	if v := os.Getenv("GATEWAY_ADMIN_HOSTS"); strings.TrimSpace(v) != "" {
		cfg.AdminHosts = splitAndTrim(v)
	}
	return cfg
}

// envOr returns the value of environment variable key, or def if it is unset
// or empty.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// splitAndTrim splits a comma-separated list, dropping empty/whitespace-only
// entries.
func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
