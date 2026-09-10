package postgres

import (
	"crypto/tls"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/tlsconf"
)

// tlsConfig builds transport security from the connection's TLS settings
// (FR-1.10); see tlsconf, which every network driver shares.
func tlsConfig(t source.TLSConfig, host string) (*tls.Config, error) { return tlsconf.Config(t, host) }
