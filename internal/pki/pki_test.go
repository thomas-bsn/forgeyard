package pki

import (
	"crypto/tls"
	"crypto/x509"
	"testing"
)

func TestNodeCertificateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	ca, err := LoadOrCreateCA(dir)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadOrCreateCA(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Fingerprint() != ca.Fingerprint() {
		t.Fatal("CA changed after reload")
	}

	keyPEM, csrPEM, err := NewNodeKey()
	if err != nil {
		t.Fatal(err)
	}
	certPEM, serial, err := ca.SignNode(csrPEM, 42)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal("certificate does not match the node key:", err)
	}
	cert, _ := x509.ParseCertificate(pair.Certificate[0])

	pool := x509.NewCertPool()
	pool.AddCert(ca.Cert)
	if _, err := cert.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatal("node certificate not signed by the CA:", err)
	}
	id, gotSerial, err := NodeIdentity(cert)
	if err != nil || id != 42 || gotSerial != serial {
		t.Fatalf("identity: %d %s %v (want 42 %s)", id, gotSerial, err, serial)
	}

	other, _ := LoadOrCreateCA(t.TempDir())
	otherPool := x509.NewCertPool()
	otherPool.AddCert(other.Cert)
	if _, err := cert.Verify(x509.VerifyOptions{Roots: otherPool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err == nil {
		t.Fatal("another CA accepted the node certificate")
	}
}
