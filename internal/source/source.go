// Package source defines the contract every data source implements.
//
// A new source is added by implementing the interfaces here and registering a
// Driver. REQ-DB-1 requires that this take zero edits to UI code; anything the
// UI needs to know is expressed through capability.Capabilities.
//
// This package must not import any UI package (ARCH-1), and every operation
// takes a context and honours cancellation (ARCH-4, NFR-P12).
package source

import (
	"context"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
)

// Driver is the static half of a source: what it is and how to open it.
// Drivers register themselves at init time (see registry.go).
type Driver interface {
	// Describe returns metadata used to build the connection form and the
	// new-connection picker, before any connection exists.
	Describe() Descriptor

	// Open dials, authenticates and returns a live Source. It must not perform
	// introspection: FR-1.4 requires "test connection" to be fast and to fail
	// with a precise reason, so Open does the minimum to prove reachability.
	Open(ctx context.Context, cfg ConnectionConfig) (Source, error)
}

// Descriptor is a driver's static metadata.
type Descriptor struct {
	// ID is the stable identifier persisted in connection settings.
	ID string

	// Name is the display name, e.g. "PostgreSQL".
	Name string

	Paradigm model.Paradigm

	// DefaultPort is 0 for sources that are not host/port based.
	DefaultPort int

	// Fields describes the connection form (FR-1.2). Only fields relevant to
	// this source appear, which is why the form is data-driven rather than a
	// union of every possible field.
	Fields []Field

	// URLSchemes lists the schemes this driver claims when the user pastes a
	// connection string (FR-1.3), e.g. "postgres", "postgresql".
	URLSchemes []string
}

// FieldKind determines the widget used for a connection-form field.
type FieldKind uint8

const (
	FieldText FieldKind = iota
	FieldPassword
	FieldNumber
	FieldBool
	FieldSelect
	FieldFile
	FieldTextArea
)

// Field is one connection-form input.
type Field struct {
	Key      string
	Label    string
	Kind     FieldKind
	Required bool
	Default  string
	Options  []string // for FieldSelect
	Help     string

	// Secret marks a value that must be stored in the OS keychain rather than
	// the settings file, and redacted from all logs (NFR-S1, NFR-S2).
	Secret bool
}

// ConnectionConfig is a resolved connection's settings.
//
// Secrets are supplied through Secret rather than Params so that the settings
// file and the keychain stay cleanly separated (FR-1.5, NFR-S1).
type ConnectionConfig struct {
	DriverID string
	Name     string

	Host string
	Port int

	// Database is the initial database, keyspace or namespace.
	Database string

	User string

	// Secret resolves a named secret from the OS keychain at connect time. It
	// is a function rather than a value so that plaintext credentials are
	// never held in a struct that might be logged or serialised.
	Secret func(key string) (string, error)

	// Params carries driver-specific non-secret settings, keyed by Field.Key.
	Params map[string]string

	TLS    TLSConfig
	SSH    *SSHConfig
	Guard  Guard
	Labels map[string]string
}

// TLSConfig describes transport security (FR-1.10, NFR-S3).
type TLSConfig struct {
	// Mode is one of "disable", "require", "verify-ca", "verify-full".
	// Verification is on by default; "disable" and "require" must be an
	// explicit, acknowledged user choice.
	Mode string

	CAFile   string
	CertFile string
	KeyFile  string

	// ServerName overrides the name checked against the certificate.
	ServerName string
}

// Verifies reports whether the mode actually validates the server certificate.
func (t TLSConfig) Verifies() bool {
	return t.Mode == "verify-ca" || t.Mode == "verify-full"
}

// SSHConfig describes a tunnel (FR-1.9).
type SSHConfig struct {
	Host string
	Port int
	User string

	// Method is one of "password", "key", "agent".
	Method string

	KeyFile string

	// JumpHosts are traversed in order before reaching Host.
	JumpHosts []string
}

// Source is a live connection.
//
// Only Capabilities, Introspector, Browser and the lifecycle methods are
// required. Everything else is an optional interface discovered by type
// assertion, which is what lets a log source like Kafka be a first-class
// source without implementing a query language (REQ-DRV-2).
type Source interface {
	Introspector
	Browser

	// Capabilities describes what this source supports. It may vary by server
	// version, so it is a method on the live connection rather than static
	// driver metadata.
	Capabilities() capability.Capabilities

	// Info returns server identity for the connection health display.
	Info(ctx context.Context) (ServerInfo, error)

	// Ping verifies the connection is still usable.
	Ping(ctx context.Context) error

	// Close releases the connection and every stream derived from it.
	Close() error
}

// ServerInfo identifies the connected server (FR-1.16).
type ServerInfo struct {
	// Product is the engine's self-reported name, which can differ from the
	// driver's — MariaDB answering a MySQL driver, for instance.
	Product string
	Version string

	// Latency is the round-trip time measured while gathering this info.
	Latency time.Duration

	Attrs map[string]string
}
