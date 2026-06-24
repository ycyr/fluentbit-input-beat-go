//go:build integration

package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestTransport verifies the three transport modes against Filebeat 8.
// No-TLS reuses the existing compose; TLS and mTLS use compose-secured.yml
// with certs generated fresh into a temp dir per subtest.
func TestTransport(t *testing.T) {
	t.Run("NoTLS", func(t *testing.T) {
		t.Parallel()
		runIntegration(t, "example/integration/compose-v78.yml", "FILEBEAT_IMAGE=8.13.4")
	})
	t.Run("TLS", func(t *testing.T) {
		t.Parallel()
		runSecuredIntegration(t, "fluent-bit.tls.conf", "filebeat.tls.yml")
	})
	t.Run("MTLS", func(t *testing.T) {
		t.Parallel()
		runSecuredIntegration(t, "fluent-bit.mtls.conf", "filebeat.mtls.yml")
	})
}

func runSecuredIntegration(t *testing.T, flbConf, filebeatConf string) {
	t.Helper()

	certDir := t.TempDir()
	generateTestCerts(t, certDir)

	// Compose resolves absolute-path env vars as-is; use them so the
	// config files are found regardless of where compose resolves ".".
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	integDir := filepath.Join(wd, "example", "integration")

	runIntegration(t, "example/integration/compose-secured.yml",
		"CERT_DIR="+certDir,
		"FLB_CONF="+filepath.Join(integDir, flbConf),
		"FILEBEAT_CONF="+filepath.Join(integDir, filebeatConf),
	)
}

// generateTestCerts writes a throwaway CA, server cert (SAN: DNS:fluent-bit),
// and client cert into dir. The server SAN must match the compose service name
// that Filebeat dials, otherwise hostname verification fails.
func generateTestCerts(t *testing.T, dir string) {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "beats-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)
	writeCertPEM(t, filepath.Join(dir, "ca.crt"), caDER)
	writeKeyPEM(t, filepath.Join(dir, "ca.key"), caKey)

	serverKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	serverDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "fluent-bit"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     []string{"fluent-bit"},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	writeCertPEM(t, filepath.Join(dir, "server.crt"), serverDER)
	writeKeyPEM(t, filepath.Join(dir, "server.key"), serverKey)

	clientKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	clientDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "beats-client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}, caCert, &clientKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	writeCertPEM(t, filepath.Join(dir, "client.crt"), clientDER)
	writeKeyPEM(t, filepath.Join(dir, "client.key"), clientKey)
}

func writePEM(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	pem.Encode(f, &pem.Block{Type: blockType, Bytes: der}) //nolint:errcheck
}

func writeCertPEM(t *testing.T, path string, der []byte) { writePEM(t, path, "CERTIFICATE", der) }

func writeKeyPEM(t *testing.T, path string, key *ecdsa.PrivateKey) {
	t.Helper()
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	writePEM(t, path, "EC PRIVATE KEY", der)
}
