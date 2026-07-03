package tlsissuer

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"testing"
)

func TestSelfSignedIssueSignsCSRAgainstCA(t *testing.T) {
	ss, err := NewSelfSigned()
	if err != nil {
		t.Fatalf("NewSelfSigned() error = %v", err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}

	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		DNSNames: []string{"client1.localtest.me"},
	}, leafKey)
	if err != nil {
		t.Fatalf("create CSR: %v", err)
	}
	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		t.Fatalf("parse CSR: %v", err)
	}

	issued, err := ss.Issue(context.Background(), csr)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ss.CAPEM()) {
		t.Fatal("failed to load CA PEM into pool")
	}

	block, _ := pem.Decode(issued.Certificate)
	if block == nil {
		t.Fatal("Issue() returned no PEM block")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse issued leaf cert: %v", err)
	}

	if _, err := leaf.Verify(x509.VerifyOptions{
		DNSName: "client1.localtest.me",
		Roots:   pool,
	}); err != nil {
		t.Errorf("leaf.Verify() error = %v, want cert to chain to the CA returned by CAPEM()", err)
	}
}

func TestSelfSignedIssuerKeyIsStable(t *testing.T) {
	ss, err := NewSelfSigned()
	if err != nil {
		t.Fatalf("NewSelfSigned() error = %v", err)
	}
	if ss.IssuerKey() != ss.IssuerKey() {
		t.Error("IssuerKey() should be stable across calls")
	}
}
