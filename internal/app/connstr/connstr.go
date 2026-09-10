// Package connstr turns connection strings a user pastes — URLs, JDBC URLs,
// libpq key=value strings — and files they import (.pgpass, ~/.my.cnf) into a
// saved connection and its secrets (FR-1.3, FR-1.11).
//
// A password found in the input goes to Result.Secrets, never into the
// connection's Params. Errors never repeat the input, because the input is
// exactly where the password is.
package connstr

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/redact"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/store"
)

// Result is a parsed connection. Conn has no ID yet; Secrets are headed for
// the keychain.
type Result struct {
	Conn     store.SavedConnection
	Secrets  map[string]string
	Warnings []string
}

func newResult(driver string) Result {
	return Result{Conn: store.SavedConnection{Driver: driver}, Secrets: map[string]string{}}
}

func (r *Result) warn(w string) { r.Warnings = append(r.Warnings, w) }

// Parse accepts a URL (postgres://...), a JDBC URL (jdbc:postgresql://...),
// or a libpq key=value string.
func Parse(s string) (Result, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return Result{}, errors.New("connstr: the connection string is empty")
	case len(s) > 5 && strings.EqualFold(s[:5], "jdbc:"):
		return parseURL(s[5:], true)
	case strings.Contains(s, "://"):
		return parseURL(s, false)
	case strings.Contains(s, "="):
		return parseKeywords(s)
	}
	return Result{}, errors.New("connstr: not a URL (scheme://...) or a key=value connection string")
}

func parseURL(s string, jdbc bool) (Result, error) {
	if jdbc && strings.Contains(s, ";") && !strings.Contains(s, "?") {
		return Result{}, errors.New("connstr: semicolon-delimited JDBC URLs (SQL Server style) are not supported yet")
	}
	u, err := url.Parse(s)
	if err != nil {
		// Deliberately not wrapped. url.Parse quotes the entire input in its
		// error, and even its inner error echoes the offending fragment — which
		// is, as often as not, part of the password.
		return Result{}, errors.New("connstr: malformed URL; check for unescaped @ : / % characters in the user name or password")
	}
	desc, ok := source.LookupScheme(u.Scheme)
	if !ok {
		return Result{}, fmt.Errorf("connstr: no installed driver handles %q connection strings", u.Scheme)
	}
	if strings.Contains(u.Host, ",") {
		return Result{}, errors.New("connstr: multiple hosts in one connection string are not supported yet")
	}

	r := newResult(desc.ID)
	if u.User != nil {
		r.Conn.User = u.User.Username()
		if pw, set := u.User.Password(); set && pw != "" {
			r.Secrets["password"] = pw
		}
	}
	r.Conn.Host = u.Hostname()
	if p := u.Port(); p != "" {
		if err := r.setPort(p); err != nil {
			return Result{}, err
		}
	}
	r.Conn.Database = strings.TrimPrefix(u.Path, "/")

	q := u.Query()
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic warnings
	for _, k := range keys {
		if err := r.apply(k, q[k][len(q[k])-1]); err != nil {
			return Result{}, err
		}
	}
	r.Conn.Name = suggestName(r.Conn)
	return r, nil
}

func (r *Result) setPort(p string) error {
	port, err := strconv.Atoi(p)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("connstr: port %q is not a number between 1 and 65535", p)
	}
	r.Conn.Port = port
	return nil
}

// apply interprets one parameter, from a URL query or a key=value string.
func (r *Result) apply(key, val string) error {
	switch k := strings.ToLower(key); k {
	case "host", "hostaddr":
		if val != "" {
			r.Conn.Host = val // a path here is a Unix socket directory
		}
	case "port":
		if val != "" {
			return r.setPort(val)
		}
	case "dbname", "database", "databasename":
		r.Conn.Database = val
	case "user", "username":
		r.Conn.User = val
	case "sslmode":
		r.sslmode(val)
	case "ssl": // JDBC: ssl=false turns TLS off; ssl=true is already the default
		if v := strings.ToLower(val); v == "false" || v == "0" {
			r.Conn.TLS.Mode = "disable"
		}
	case "sslrootcert":
		r.Conn.TLS.CAFile = val
	case "sslcert":
		r.Conn.TLS.CertFile = val
	case "sslkey":
		r.Conn.TLS.KeyFile = val
	case "service", "passfile", "servicefile":
		r.warn(fmt.Sprintf("%q is ignored: settings are never read from other files implicitly", key))
	default:
		if k == "password" || redact.IsSecretKey(k) {
			if val != "" {
				r.Secrets[secretName(k)] = val
			}
			return nil
		}
		if r.Conn.Params == nil {
			r.Conn.Params = map[string]string{}
		}
		r.Conn.Params[key] = val
	}
	return nil
}

// secretName normalises a key into the form the settings file accepts for a
// secret's name: lowercase letters, digits and underscores.
func secretName(k string) string {
	b := []byte(strings.ToLower(k))
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			b[i] = '_'
		}
	}
	return string(b)
}

func (r *Result) sslmode(v string) {
	switch m := strings.ToLower(v); m {
	case "disable", "require", "verify-ca", "verify-full":
		r.Conn.TLS.Mode = m
	case "allow", "prefer":
		// libpq's allow and prefer fall back to plaintext without saying so.
		// Nothing in this application downgrades silently (ADR-0008), so the
		// connection verifies TLS, and the user is told why.
		r.warn("sslmode=" + m + " can silently fall back to an unencrypted connection; " +
			"this connection verifies TLS instead. Choose require or disable explicitly if the server needs it.")
	default:
		r.warn(fmt.Sprintf("unknown sslmode %q ignored; TLS will be verified", v))
	}
}

func suggestName(c store.SavedConnection) string {
	switch {
	case c.Database != "" && c.Host != "":
		return c.Database + " @ " + c.Host
	case c.Host != "":
		return c.Host
	case c.Database != "":
		return c.Database
	}
	return "New connection"
}

// --- libpq key=value ---------------------------------------------------------

func parseKeywords(s string) (Result, error) {
	pairs, err := splitKeywords(s)
	if err != nil {
		return Result{}, err
	}
	desc, ok := source.LookupScheme("postgres") // key=value is libpq's format
	if !ok {
		return Result{}, errors.New("connstr: key=value strings are PostgreSQL's format, and no PostgreSQL driver is installed")
	}
	r := newResult(desc.ID)
	for _, kv := range pairs {
		if err := r.apply(kv[0], kv[1]); err != nil {
			return Result{}, err
		}
	}
	r.Conn.Name = suggestName(r.Conn)
	return r, nil
}

// splitKeywords tokenises libpq's "key = value" syntax. A value may be
// single-quoted; \' and \\ are the escapes. Errors name the key, never the
// value.
func splitKeywords(s string) ([][2]string, error) {
	var out [][2]string
	i, n := 0, len(s)
	space := func() {
		for i < n && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n') {
			i++
		}
	}
	for {
		space()
		if i >= n {
			return out, nil
		}
		start := i
		for i < n && s[i] != '=' && s[i] != ' ' && s[i] != '\t' {
			i++
		}
		key := s[start:i]
		space()
		if key == "" || i >= n || s[i] != '=' {
			return nil, fmt.Errorf("connstr: expected key=value near %q", key)
		}
		i++
		space()

		var val strings.Builder
		if i < n && s[i] == '\'' {
			i++
			closed := false
			for i < n {
				c := s[i]
				if c == '\\' && i+1 < n {
					val.WriteByte(s[i+1])
					i += 2
					continue
				}
				if c == '\'' {
					i++
					closed = true
					break
				}
				val.WriteByte(c)
				i++
			}
			if !closed {
				return nil, fmt.Errorf("connstr: unterminated quoted value for %q", key)
			}
		} else {
			for i < n && s[i] != ' ' && s[i] != '\t' && s[i] != '\n' {
				if s[i] == '\\' && i+1 < n {
					i++
				}
				val.WriteByte(s[i])
				i++
			}
		}
		out = append(out, [2]string{key, val.String()})
	}
}

// --- .pgpass -------------------------------------------------------------------

// ParsePgpass imports a PostgreSQL password file: hostname:port:database:
// username:password per line, with \: and \\ as escapes. A line whose host is
// the wildcard * cannot become a concrete connection, so it is skipped with a
// warning rather than guessed at.
func ParsePgpass(rd io.Reader) ([]Result, []string, error) {
	desc, ok := source.LookupScheme("postgres")
	if !ok {
		return nil, nil, errors.New("connstr: no PostgreSQL driver is installed")
	}
	var out []Result
	var warns []string
	sc := bufio.NewScanner(rd)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		f := splitPgpass(text)
		if len(f) != 5 {
			warns = append(warns, fmt.Sprintf(".pgpass line %d is not host:port:database:user:password; skipped", line))
			continue
		}
		if f[0] == "*" {
			warns = append(warns, fmt.Sprintf(".pgpass line %d matches any host; skipped", line))
			continue
		}
		r := newResult(desc.ID)
		r.Conn.Host = f[0]
		if f[1] != "*" {
			if err := r.setPort(f[1]); err != nil {
				warns = append(warns, fmt.Sprintf(".pgpass line %d has an invalid port; skipped", line))
				continue
			}
		}
		if f[2] != "*" {
			r.Conn.Database = f[2]
		}
		if f[3] != "*" {
			r.Conn.User = f[3]
		}
		if f[4] != "" {
			r.Secrets["password"] = f[4]
		}
		r.Conn.Name = suggestName(r.Conn)
		out = append(out, r)
	}
	return out, warns, sc.Err()
}

func splitPgpass(line string) []string {
	var fields []string
	var cur strings.Builder
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c == '\\' && i+1 < len(line):
			cur.WriteByte(line[i+1])
			i++
		case c == ':':
			fields = append(fields, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	return append(fields, cur.String())
}

// --- ~/.my.cnf ----------------------------------------------------------------

// ParseMyCnf imports the [client] and [mysql] sections of a MySQL option
// file, [mysql] taking precedence as it does for the mysql client itself.
func ParseMyCnf(rd io.Reader) (Result, error) {
	desc, ok := source.LookupScheme("mysql")
	if !ok {
		return Result{}, errors.New("connstr: no MySQL driver is installed yet")
	}
	return parseMyCnf(rd, desc.ID)
}

func parseMyCnf(rd io.Reader, driver string) (Result, error) {
	values := map[string]map[string]string{}
	section := ""
	sc := bufio.NewScanner(rd)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			continue
		}
		k, v, _ := strings.Cut(line, "=")
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		if values[section] == nil {
			values[section] = map[string]string{}
		}
		values[section][k] = v
	}
	if err := sc.Err(); err != nil {
		return Result{}, err
	}

	r := newResult(driver)
	for _, sec := range []string{"client", "mysql"} {
		for k, v := range values[sec] {
			switch k {
			case "host":
				r.Conn.Host = v
			case "port":
				if err := r.setPort(v); err != nil {
					return Result{}, err
				}
			case "user":
				r.Conn.User = v
			case "password":
				if v != "" {
					r.Secrets["password"] = v
				}
			case "database":
				r.Conn.Database = v
			case "socket":
				r.Conn.Host = v
			}
		}
	}
	r.Conn.Name = suggestName(r.Conn)
	return r, nil
}
