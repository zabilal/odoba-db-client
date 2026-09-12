// Package redact removes secrets from text destined for logs, error messages
// and diagnostics bundles.
//
// NFR-S2 requires connection strings to be redacted in every log line and
// error message. This package is the single implementation of that rule, so
// there is one place to audit and one place to fix.
//
// The design bias is to over-redact. A log line that loses a hostname is a
// minor inconvenience; one that leaks a password is a security incident.
package redact

import (
	"fmt"
	"regexp"
	"strings"
)

// Mask replaces every redacted value.
const Mask = "[REDACTED]"

// urlCredentials matches the user:password@ portion of a connection URL.
var urlCredentials = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)([^/@\s:]+)(:[^/@\s]*)?@`)

// keyValueSecret matches key=value and key: value pairs whose key names a
// secret, in connection strings, DSNs and structured log output.
var keyValueSecret = regexp.MustCompile(
	`(?i)\b(password|passwd|pwd|secret|token|api[_-]?key|apikey|access[_-]?key|` +
		`secret[_-]?key|private[_-]?key|credential|auth|authorization|sasl\.jaas\.config|` +
		`sslkey|passphrase|session[_-]?key|bearer)\b(\s*[:=]\s*)` +
		// The scheme alternative must precede the bare-token one: without it,
		// "Authorization: Bearer <token>" masks the word "Bearer" and leaves the
		// token in the log. Caught by TestStringRedactsConnectionURLs/bearer.
		`("[^"]*"|'[^']*'|(?:bearer|basic)\s+[^\s,;&)]+|[^\s,;&)]+)`)

// bearerToken matches an Authorization header value.
var bearerToken = regexp.MustCompile(`(?i)\b(bearer|basic)\s+([A-Za-z0-9._~+/=-]{8,})`)

// String returns s with credentials removed.
//
// It is safe to call on text that contains no secrets, and on text that is not
// a connection string at all — it is applied indiscriminately at the logging
// boundary rather than selectively at call sites, because selective redaction
// is redaction that eventually gets forgotten.
func String(s string) string {
	if s == "" {
		return s
	}

	s = urlCredentials.ReplaceAllString(s, "${1}${2}:"+Mask+"@")
	s = keyValueSecret.ReplaceAllString(s, "${1}${2}"+Mask)
	s = bearerToken.ReplaceAllString(s, "${1} "+Mask)

	return s
}

// Error returns err's message with credentials removed.
//
// Driver errors routinely embed the DSN they failed to connect with, which is
// the most common way a password reaches a log file.
func Error(err error) string {
	if err == nil {
		return ""
	}
	return String(err.Error())
}

// Wrap returns an error whose message is redacted, preserving the original for
// errors.Is and errors.As.
//
// The wrapped error is NOT redacted — unwrapping and printing the cause would
// defeat this. Callers that log a cause directly must call Error on it.
func Wrap(err error) error {
	if err == nil {
		return nil
	}
	return redactedError{msg: String(err.Error()), cause: err}
}

type redactedError struct {
	msg   string
	cause error
}

func (e redactedError) Error() string { return e.msg }
func (e redactedError) Unwrap() error { return e.cause }

// Value redacts a single value known to be secret, preserving only whether it
// was empty — which is often the actual bug being diagnosed.
func Value(v string) string {
	if v == "" {
		return "[empty]"
	}
	return Mask
}

// Map returns a copy of m with values redacted for keys that name secrets.
// Used for connection parameter maps in diagnostics bundles.
func Map(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if IsSecretKey(k) {
			out[k] = Value(v)
			continue
		}
		out[k] = String(v)
	}
	return out
}

var secretKeyHints = []string{
	"password", "passwd", "pwd", "secret", "token", "apikey", "api_key", "api-key",
	"accesskey", "access_key", "access-key", "privatekey", "private_key",
	"credential", "auth", "passphrase", "jaas",
}

// notSecretKeys are the names "auth" catches that are not secrets: a
// MongoDB connection's authSource is a database's name and its
// authMechanism is SCRAM-SHA-256. Redacting them would lose a connection's
// settings on the way in and tell a person nothing on the way out.
var notSecretKeys = map[string]bool{
	"authsource": true, "authmechanism": true, "authmechanismproperties": true,
	"authdb": true, "authdatabase": true, "authenticationdatabase": true,
}

// IsSecretKey reports whether a parameter name denotes a secret.
func IsSecretKey(key string) bool {
	k := strings.ToLower(key)
	if notSecretKeys[k] {
		return false
	}
	for _, hint := range secretKeyHints {
		if strings.Contains(k, hint) {
			return true
		}
	}
	return false
}

// Sprintf formats and redacts in one step, for log call sites.
func Sprintf(format string, args ...any) string {
	return String(fmt.Sprintf(format, args...))
}
