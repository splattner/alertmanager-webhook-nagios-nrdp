// Package tlsutil builds tls.Config values for the webhook's inbound
// listener and its outbound connection to the NRDP endpoint.
package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// ServerConfig builds a server-side tls.Config from a certificate/key pair
// and, if clientCAFile is set, requires and verifies a client certificate
// signed by it (mutual TLS).
func ServerConfig(certFile, keyFile, clientCAFile string) (*tls.Config, error) {
	if certFile == "" || keyFile == "" {
		return nil, fmt.Errorf("certFile and keyFile are required")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load server certificate: %w", err)
	}

	cfg := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	}

	if clientCAFile != "" {
		pool, err := loadCertPool(clientCAFile)
		if err != nil {
			return nil, fmt.Errorf("load client CA: %w", err)
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}

	return cfg, nil
}

// ClientConfig builds a client-side tls.Config for connecting to the NRDP
// endpoint: caFile (if set) trusts an additional CA alongside the system
// pool, certFile/keyFile (if both set) present a client certificate, and
// insecureSkipVerify disables verification entirely.
func ClientConfig(caFile, certFile, keyFile string, insecureSkipVerify bool) (*tls.Config, error) {
	cfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: insecureSkipVerify, //nolint:gosec // explicit, documented opt-in
	}

	if caFile != "" {
		pool, err := loadCertPool(caFile)
		if err != nil {
			return nil, fmt.Errorf("load CA: %w", err)
		}
		cfg.RootCAs = pool
	}

	if certFile != "" || keyFile != "" {
		if certFile == "" || keyFile == "" {
			return nil, fmt.Errorf("certFile and keyFile must both be set for a client certificate")
		}
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("load client certificate: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}

	return cfg, nil
}

func loadCertPool(caFile string) (*x509.CertPool, error) {
	raw, err := os.ReadFile(caFile)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(raw) {
		return nil, fmt.Errorf("%s contains no valid PEM certificates", caFile)
	}
	return pool, nil
}
