package main

import "testing"

func TestLoadConfigFromEnvDefaults(t *testing.T) {
	// No gateway env vars set -> defaults.
	for _, k := range []string{
		"GATEWAY_HTTP_ADDR", "GATEWAY_HTTPS_ADDR", "CERTMAGIC_MODE",
		"CERTMAGIC_DATA_DIR", "GATEWAY_ERROR_PAGE_PATH",
		"GATEWAY_DOMAINS_CONFIG_PATH", "GATEWAY_SERVICES_CONFIG_PATH",
		"GATEWAY_ADMIN_HOSTS",
	} {
		t.Setenv(k, "")
	}
	cfg := LoadConfigFromEnv()
	if cfg.HTTPAddr != ":8085" || cfg.HTTPSAddr != ":8443" {
		t.Errorf("addrs = %q/%q, want :8085/:8443", cfg.HTTPAddr, cfg.HTTPSAddr)
	}
	if cfg.CertMagicMode != "selfsigned" {
		t.Errorf("CertMagicMode = %q, want selfsigned", cfg.CertMagicMode)
	}
	if len(cfg.AdminHosts) != 2 {
		t.Errorf("AdminHosts = %v, want 2 defaults", cfg.AdminHosts)
	}
}

func TestLoadConfigFromEnvOverride(t *testing.T) {
	t.Setenv("GATEWAY_HTTP_ADDR", ":9090")
	t.Setenv("CERTMAGIC_MODE", "acme-staging")
	t.Setenv("GATEWAY_ADMIN_HOSTS", "localhost, admin.local ,")

	cfg := LoadConfigFromEnv()
	if cfg.HTTPAddr != ":9090" {
		t.Errorf("HTTPAddr = %q, want :9090", cfg.HTTPAddr)
	}
	if cfg.CertMagicMode != "acme-staging" {
		t.Errorf("CertMagicMode = %q, want acme-staging", cfg.CertMagicMode)
	}
	// Comma list is trimmed and empty entries dropped.
	if len(cfg.AdminHosts) != 2 || cfg.AdminHosts[0] != "localhost" || cfg.AdminHosts[1] != "admin.local" {
		t.Errorf("AdminHosts = %v, want [localhost admin.local]", cfg.AdminHosts)
	}
}
