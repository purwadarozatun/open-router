package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config holds the gateway's runtime settings, loaded from a JSON file at
// startup (see config/gateway.json). Every field has a built-in default via
// defaultConfig, so a missing file or a partial JSON still yields a working
// gateway — only the keys present in the file override the defaults.
type Config struct {
	// HTTPAddr is the plain-HTTP listen address (e.g. ":8085").
	HTTPAddr string `json:"httpAddr"`
	// HTTPSAddr is the TLS listen address served by CertMagic (e.g. ":8443").
	HTTPSAddr string `json:"httpsAddr"`
	// CertMagicMode selects the TLS issuance backend: "selfsigned" or
	// "acme-staging". The CERTMAGIC_MODE env var, if set, overrides this.
	CertMagicMode string `json:"certmagicMode"`
	// CertMagicDataDir is where CertMagic persists certs and the dev CA.
	CertMagicDataDir string `json:"certmagicDataDir"`
	// ErrorPagePath is the static HTML template rendered for gateway errors.
	ErrorPagePath string `json:"errorPagePath"`
	// DomainsConfigPath is the JSON domain list loaded into the registry.
	DomainsConfigPath string `json:"domainsConfigPath"`
	// AdminHosts are hostnames served locally by the gateway itself
	// (diagnostic routes), bypassing the reverse proxy.
	AdminHosts []string `json:"adminHosts"`
}

// defaultConfig returns the built-in gateway settings used when no config file
// is present or when a field is omitted from it. These mirror the values the
// gateway used before config was externalized.
func defaultConfig() Config {
	return Config{
		HTTPAddr:          ":8085",
		HTTPSAddr:         ":8443",
		CertMagicMode:     "selfsigned",
		CertMagicDataDir:  "./certmagic-data",
		ErrorPagePath:     "./static/404.html",
		DomainsConfigPath: "./config/domains.json",
		AdminHosts:        []string{"localhost", "127.0.0.1"},
	}
}

// LoadConfig reads gateway settings from the JSON file at path, layered over
// defaultConfig so absent keys keep their defaults. A read error is returned
// with the defaults (caller may warn and continue); a parse error returns the
// pristine defaults so a malformed file cannot half-apply.
func LoadConfig(path string) (Config, error) {
	cfg := defaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read gateway config: %w", err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return defaultConfig(), fmt.Errorf("parse gateway config %s: %w", path, err)
	}
	return cfg, nil
}
