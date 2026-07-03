// Package tlsissuer provides a local, non-ACME certmagic.Issuer.
//
// It exists as this POC's TLS fallback for environments — like a dev
// machine behind NAT with no public inbound access — where ACME's
// HTTP-01/TLS-ALPN-01 challenges can never complete because the CA has no
// way to connect back to this host. Instead of validating domain control
// over the network, SelfSigned signs the incoming CSR directly against an
// in-memory root CA generated at startup. It still plugs into CertMagic's
// on-demand issuance machinery (OnDemandConfig.DecisionFunc, storage,
// caching) exactly like the ACME issuer would.
package tlsissuer

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"

	"github.com/caddyserver/certmagic"
)

// SelfSigned is a certmagic.Issuer backed by an in-memory root CA. Each
// call to Issue signs the requested CSR with that CA instead of contacting
// an external ACME server.
type SelfSigned struct {
	caCert *x509.Certificate
	caKey  *ecdsa.PrivateKey
}

// NewSelfSigned generates a fresh root CA (P-256, five-year validity) and
// returns an Issuer backed by it. The CA is not persisted across restarts;
// this is a POC convenience, not a production certificate authority.
func NewSelfSigned() (*SelfSigned, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("tlsissuer: generate CA key: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return nil, fmt.Errorf("tlsissuer: generate CA serial: %w", err)
	}

	caTemplate := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "poc-proxy-gateway local dev CA",
			Organization: []string{"poc-proxy-gateway"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(5, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("tlsissuer: create CA certificate: %w", err)
	}

	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, fmt.Errorf("tlsissuer: parse CA certificate: %w", err)
	}

	return &SelfSigned{caCert: caCert, caKey: caKey}, nil
}

// IssuerKey uniquely identifies this issuer's configuration to CertMagic,
// so certificates it issues aren't conflated with certificates from a
// different Issuer (e.g. the ACME one) for the same domain.
func (s *SelfSigned) IssuerKey() string {
	return "selfsigned-local-dev"
}

// Issue signs the CSR's public key and SANs with the local root CA. There
// is no domain-control challenge — DecisionFunc (the registry HostPolicy)
// is what gates which names ever reach this method.
func (s *SelfSigned) Issue(_ context.Context, csr *x509.CertificateRequest) (*certmagic.IssuedCertificate, error) {
	serial, err := randomSerial()
	if err != nil {
		return nil, fmt.Errorf("tlsissuer: generate leaf serial: %w", err)
	}

	leafTemplate := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: csr.Subject.CommonName},
		DNSNames:     csr.DNSNames,
		IPAddresses:  csr.IPAddresses,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(0, 3, 0), // 90 days, mirrors Let's Encrypt's lifetime
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, s.caCert, csr.PublicKey, s.caKey)
	if err != nil {
		return nil, fmt.Errorf("tlsissuer: sign leaf certificate: %w", err)
	}

	chain := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	chain = append(chain, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.caCert.Raw})...)

	return &certmagic.IssuedCertificate{Certificate: chain}, nil
}

// CAPEM returns the PEM encoding of the root CA certificate, so operators
// or test clients (curl --cacert, browsers) can trust it explicitly
// instead of disabling TLS verification altogether.
func (s *SelfSigned) CAPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.caCert.Raw})
}

func randomSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
}
