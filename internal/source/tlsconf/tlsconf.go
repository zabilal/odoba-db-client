// Package tlsconf turns a connection's TLS settings into a *tls.Config, the
// same way for every network driver: verification on unless turned down
// explicitly (NFR-S3), and verify-ca really verifying the chain.
package tlsconf

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Config builds transport security from the connection's TLS settings
// (FR-1.10). An empty mode means verify-full: NFR-S3 puts verification on by
// default, so turning it down is always an explicit choice.
func Config(t source.TLSConfig, host string) (*tls.Config, error) {
	mode := t.Mode
	if mode == "" {
		mode = "verify-full"
	}
	switch mode {
	case "disable":
		return nil, nil
	case "require", "verify-ca", "verify-full":
	default:
		return nil, fmt.Errorf("unknown TLS mode %q", mode)
	}

	c := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}
	if t.ServerName != "" {
		c.ServerName = t.ServerName
	}
	if t.CAFile != "" {
		pem, err := os.ReadFile(t.CAFile)
		if err != nil {
			return nil, fmt.Errorf("reading CA file: %w", err)
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("CA file contains no PEM certificates")
		}
		c.RootCAs = roots
	}
	if t.CertFile != "" || t.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(t.CertFile, t.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("loading client certificate: %w", err)
		}
		c.Certificates = []tls.Certificate{cert}
	}

	switch mode {
	case "require":
		// Encrypted but unverified. Only reachable by an explicit choice.
		c.InsecureSkipVerify = true
	case "verify-ca":
		// Verify the chain but not the host name. crypto/tls has no such mode,
		// so the built-in check is skipped and the chain verified by hand —
		// never skipped and left unverified.
		roots := c.RootCAs
		c.InsecureSkipVerify = true
		c.VerifyConnection = func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("server presented no certificate")
			}
			opts := x509.VerifyOptions{Roots: roots, Intermediates: x509.NewCertPool()}
			for _, ic := range cs.PeerCertificates[1:] {
				opts.Intermediates.AddCert(ic)
			}
			_, err := cs.PeerCertificates[0].Verify(opts)
			return err
		}
	}
	return c, nil
}
