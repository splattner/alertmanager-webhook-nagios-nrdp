package tlsutil

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeCertPair generates a self-signed certificate and writes it (plus its
// key) as PEM files, returning their paths.
func writeCertPair(t *testing.T, dir, name string) (certPath, keyPath string) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	certPath = filepath.Join(dir, name+".crt")
	keyPath = filepath.Join(dir, name+".key")
	writeFile(t, certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	writeFile(t, keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	return certPath, keyPath
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestServerConfig(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeCertPair(t, dir, "server")

	cfg, err := ServerConfig(certPath, keyPath, "")
	if err != nil {
		t.Fatalf("ServerConfig: %v", err)
	}
	if len(cfg.Certificates) != 1 {
		t.Errorf("got %d certificates, want 1", len(cfg.Certificates))
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %x, want TLS 1.2", cfg.MinVersion)
	}
	// Without a client CA the server must not demand a client certificate.
	if cfg.ClientAuth != tls.NoClientCert {
		t.Errorf("ClientAuth = %v, want NoClientCert when no client CA is configured", cfg.ClientAuth)
	}
}

func TestServerConfigWithClientCARequiresMTLS(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeCertPair(t, dir, "server")
	caPath, _ := writeCertPair(t, dir, "ca")

	cfg, err := ServerConfig(certPath, keyPath, caPath)
	if err != nil {
		t.Fatalf("ServerConfig: %v", err)
	}
	if cfg.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Errorf("ClientAuth = %v, want RequireAndVerifyClientCert", cfg.ClientAuth)
	}
	if cfg.ClientCAs == nil {
		t.Error("ClientCAs is nil despite a client CA being configured")
	}
}

func TestServerConfigErrors(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeCertPair(t, dir, "server")
	missing := filepath.Join(dir, "nope.pem")
	notPEM := filepath.Join(dir, "garbage.pem")
	writeFile(t, notPEM, []byte("this is not a certificate"))

	tests := []struct {
		name                string
		cert, key, clientCA string
	}{
		{"no cert or key", "", "", ""},
		{"cert without key", certPath, "", ""},
		{"key without cert", "", keyPath, ""},
		{"missing cert file", missing, keyPath, ""},
		{"client CA file missing", certPath, keyPath, missing},
		{"client CA not PEM", certPath, keyPath, notPEM},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ServerConfig(tt.cert, tt.key, tt.clientCA); err == nil {
				t.Error("want error, got nil")
			}
		})
	}
}

func TestClientConfig(t *testing.T) {
	dir := t.TempDir()
	caPath, _ := writeCertPair(t, dir, "ca")
	certPath, keyPath := writeCertPair(t, dir, "client")

	t.Run("defaults verify", func(t *testing.T) {
		cfg, err := ClientConfig("", "", "", false)
		if err != nil {
			t.Fatalf("ClientConfig: %v", err)
		}
		if cfg.InsecureSkipVerify {
			t.Error("InsecureSkipVerify is true by default")
		}
		if cfg.RootCAs != nil {
			t.Error("RootCAs should be nil so the system pool is used")
		}
	})

	t.Run("with CA", func(t *testing.T) {
		cfg, err := ClientConfig(caPath, "", "", false)
		if err != nil {
			t.Fatalf("ClientConfig: %v", err)
		}
		if cfg.RootCAs == nil {
			t.Error("RootCAs is nil despite a CA being configured")
		}
	})

	t.Run("with client certificate", func(t *testing.T) {
		cfg, err := ClientConfig("", certPath, keyPath, false)
		if err != nil {
			t.Fatalf("ClientConfig: %v", err)
		}
		if len(cfg.Certificates) != 1 {
			t.Errorf("got %d certificates, want 1", len(cfg.Certificates))
		}
	})

	t.Run("insecure honored", func(t *testing.T) {
		cfg, err := ClientConfig("", "", "", true)
		if err != nil {
			t.Fatalf("ClientConfig: %v", err)
		}
		if !cfg.InsecureSkipVerify {
			t.Error("InsecureSkipVerify = false despite being requested")
		}
	})
}

func TestClientConfigErrors(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeCertPair(t, dir, "client")
	missing := filepath.Join(dir, "nope.pem")

	tests := []struct {
		name          string
		ca, cert, key string
	}{
		// Half a keypair is a config mistake, not a request for no identity.
		{"cert without key", "", certPath, ""},
		{"key without cert", "", "", keyPath},
		{"missing CA file", missing, "", ""},
		{"missing cert file", "", missing, keyPath},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ClientConfig(tt.ca, tt.cert, tt.key, false); err == nil {
				t.Error("want error, got nil")
			}
		})
	}
}
