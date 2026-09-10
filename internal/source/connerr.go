package source

import "github.com/ikigai-db/ikigai-db/internal/redact"

// ConnectKind classifies why a connection attempt failed, so "test connection"
// can say what to fix rather than print a driver's error text (FR-1.4).
type ConnectKind uint8

const (
	ConnectUnknown ConnectKind = iota
	// ConnectUnreachable: DNS failure, or TCP refused or timed out. The fix is
	// host, port, network or tunnel.
	ConnectUnreachable
	// ConnectTLS: the handshake or certificate verification failed.
	ConnectTLS
	// ConnectAuth: the server rejected the credentials.
	ConnectAuth
	// ConnectNoDatabase: reachable and authenticated, but no such database.
	ConnectNoDatabase
	// ConnectRefused: the server is up but not accepting connections — at its
	// connection limit, starting up, or shutting down.
	ConnectRefused
	// ConnectConfig: the settings could not become a connection attempt.
	ConnectConfig
)

// ConnectError is what Driver.Open returns on failure.
//
// Error() is redacted, because driver errors routinely embed the DSN they
// failed with (NFR-S2). Unwrap returns the original for errors.Is and
// errors.As; a caller that logs the unwrapped error must redact it itself.
type ConnectError struct {
	Kind ConnectKind
	// Hint is a short, user-facing statement of what went wrong.
	Hint string
	Err  error
}

func (e *ConnectError) Error() string {
	if e.Err == nil {
		return e.Hint
	}
	return e.Hint + ": " + redact.Error(e.Err)
}

func (e *ConnectError) Unwrap() error { return e.Err }
