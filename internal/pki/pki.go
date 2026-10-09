// Package pki is Forgeyard's private certificate authority. It signs one client certificate per node,
// and the control plane's own certificate for the agent port, so server and agents authenticate each
// other with mutual TLS without any public CA.
package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// ServerName is the name in the control plane's agent-port certificate. Agents verify it whatever
	// address they dial, since the certificate only has to chain to the pinned CA.
	ServerName = "forgeyard-server"

	nodeCNPrefix = "forgeyard-node:"
	caCertFile   = "ca.crt"
	caKeyFile    = "ca.key"
	nodeCertTTL  = 5 * 365 * 24 * time.Hour
)

// CA signs certificates with a key kept on the control plane.
type CA struct {
	Cert    *x509.Certificate
	CertPEM []byte
	key     *ecdsa.PrivateKey
}

// LoadOrCreateCA reads the CA from dir, creating it on first start.
func LoadOrCreateCA(dir string) (*CA, error) {
	certPath, keyPath := filepath.Join(dir, caCertFile), filepath.Join(dir, caKeyFile)
	certPEM, err := os.ReadFile(certPath)
	if errors.Is(err, os.ErrNotExist) {
		return createCA(certPath, keyPath)
	}
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	cert, err := ParseCertPEM(certPEM)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", certPath, err)
	}
	key, err := parseKeyPEM(keyPEM)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", keyPath, err)
	}
	return &CA{Cert: cert, CertPEM: certPEM, key: key}, nil
}

func createCA(certPath, keyPath string) (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          randomSerial(),
		Subject:               pkix.Name{CommonName: "Forgeyard CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(20 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	keyPEM, err := EncodeKeyPEM(key)
	if err != nil {
		return nil, err
	}
	// The key first and exclusively: a CA cert without its key would be unusable.
	if err := writeExclusive(keyPath, keyPEM, 0o600); err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := writeExclusive(certPath, certPEM, 0o644); err != nil {
		return nil, err
	}
	cert, _ := x509.ParseCertificate(der)
	return &CA{Cert: cert, CertPEM: certPEM, key: key}, nil
}

// Fingerprint identifies the CA in join commands, so an agent can check it enrolls with the right server.
func (ca *CA) Fingerprint() string {
	return Fingerprint(ca.Cert)
}

// Fingerprint returns "sha256:<hex>" of a certificate.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// SignNode signs a node's certificate signing request. The certificate names the node by ID.
func (ca *CA) SignNode(csrPEM []byte, nodeID int64) (certPEM []byte, serial string, err error) {
	block, _ := pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, "", errors.New("invalid certificate request")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, "", err
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, "", err
	}
	sn := randomSerial()
	tmpl := &x509.Certificate{
		SerialNumber: sn,
		Subject:      pkix.Name{CommonName: nodeCNPrefix + strconv.FormatInt(nodeID, 10)},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(nodeCertTTL),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, csr.PublicKey, ca.key)
	if err != nil {
		return nil, "", err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), sn.Text(16), nil
}

// NodeIdentity extracts the node ID and serial from a verified client certificate.
func NodeIdentity(cert *x509.Certificate) (nodeID int64, serial string, err error) {
	raw, ok := strings.CutPrefix(cert.Subject.CommonName, nodeCNPrefix)
	if !ok {
		return 0, "", errors.New("not a node certificate")
	}
	nodeID, err = strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, "", errors.New("not a node certificate")
	}
	return nodeID, cert.SerialNumber.Text(16), nil
}

// ServerTLS returns the TLS config of the agent port: a fresh server certificate signed by the CA,
// and mandatory client certificates signed by the CA.
func (ca *CA) ServerTLS() (*tls.Config, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: randomSerial(),
		Subject:      pkix.Name{CommonName: ServerName},
		DNSNames:     []string{ServerName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, &key.PublicKey, ca.key)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca.Cert)
	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS13,
	}, nil
}

// NewNodeKey creates a node's private key and the request to send to the control plane.
func NewNodeKey() (keyPEM, csrPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "forgeyard-node"},
	}, key)
	if err != nil {
		return nil, nil, err
	}
	keyPEM, err = EncodeKeyPEM(key)
	if err != nil {
		return nil, nil, err
	}
	return keyPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr}), nil
}

// ParseCertPEM parses a single PEM certificate.
func ParseCertPEM(data []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("no certificate found")
	}
	return x509.ParseCertificate(block.Bytes)
}

// EncodeKeyPEM encodes an ECDSA private key.
func EncodeKeyPEM(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
}

func parseKeyPEM(data []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("no key found")
	}
	return x509.ParseECPrivateKey(block.Bytes)
}

func randomSerial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		panic(err)
	}
	return n
}

func writeExclusive(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
