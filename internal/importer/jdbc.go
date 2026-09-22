package importer

import (
	"net/url"
	"strconv"
	"strings"
)

// Both DBeaver and DataGrip describe a connection with a JDBC URL, and for
// some connections it is the only description there is. Reading one is
// therefore shared between them rather than written twice.

// jdbcDrivers maps the scheme in a JDBC URL to a driver of this project's.
// What is absent from here is as meaningful as what is present: a connection
// to something there is no driver for is reported as one that could not be
// brought, rather than imported as something it is not.
var jdbcDrivers = map[string]string{
	"postgresql": "postgres",
	"postgres":   "postgres",
	"mysql":      "mysql",
	"mariadb":    "mysql",
	"sqlite":     "sqlite",
	"mongodb":    "mongodb",
	"redis":      "redis",
	"cassandra":  "cassandra",
	"kafka":      "kafka",
}

// jdbcConn is what a JDBC URL says about a connection.
type jdbcConn struct {
	Driver   string
	Host     string
	Port     int
	Database string
	User     string

	// HadPassword records that the URL carried one. The password itself is
	// deliberately not kept: a connection's credentials belong in the
	// keychain and never in the settings file (FR-1.5), and a URL copied
	// verbatim into settings would put one there in plain text.
	HadPassword bool

	// Scheme is kept for the connections that cannot be brought, so that the
	// note can say what they were.
	Scheme string
}

// readJDBC reads what it can out of a JDBC URL. A URL for something with no
// driver here still answers its scheme, so that the import can say what it
// was rather than dropping it silently.
func readJDBC(raw string) (jdbcConn, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(raw), "jdbc:")
	if !ok {
		return jdbcConn{}, false
	}
	scheme, rest, ok := strings.Cut(rest, ":")
	if !ok {
		return jdbcConn{}, false
	}
	scheme = strings.ToLower(scheme)
	out := jdbcConn{Scheme: scheme, Driver: jdbcDrivers[scheme]}

	if !strings.HasPrefix(rest, "//") {
		// jdbc:sqlite:/path/to/file.db — everything after the scheme is a
		// path, and there is no host in it at all.
		out.Database = strings.TrimPrefix(rest, "//")
		if i := strings.IndexAny(out.Database, "?;"); i >= 0 {
			out.Database = out.Database[:i]
		}
		return out, out.Database != ""
	}

	// A JDBC URL is close enough to a URL to parse as one once the jdbc:
	// prefix is off, and net/url handles the awkward parts — userinfo,
	// bracketed IPv6, percent-encoding — that hand-splitting gets wrong.
	u, err := url.Parse(scheme + ":" + rest)
	if err != nil {
		return out, false
	}
	out.Host = u.Hostname()
	if p := u.Port(); p != "" {
		out.Port, _ = strconv.Atoi(p)
	}
	out.Database = strings.TrimPrefix(u.Path, "/")
	if u.User != nil {
		out.User = u.User.Username()
		_, out.HadPassword = u.User.Password()
	}

	// Some drivers carry the account in the query instead, and SQL Server
	// uses semicolons where everything else uses an ampersand.
	q := u.Query()
	if out.User == "" {
		for _, key := range []string{"user", "username", "UserName"} {
			if v := q.Get(key); v != "" {
				out.User = v
				break
			}
		}
	}
	for _, key := range []string{"password", "Password"} {
		if q.Get(key) != "" {
			out.HadPassword = true
		}
	}
	if out.Database == "" {
		for _, key := range []string{"database", "databaseName", "DatabaseName"} {
			if v := q.Get(key); v != "" {
				out.Database = v
				break
			}
		}
	}
	return out, out.Host != "" || out.Database != ""
}
