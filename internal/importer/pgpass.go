package importer

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/store"
)

// A .pgpass line is hostname:port:database:username:password, with a
// backslash escaping a colon or a backslash inside a field, and any of the
// first four able to be * meaning anything. PostgreSQL documents all of it.
const pgpassFields = 5

// ReadPgpass reads PostgreSQL's password file (FR-1.12).
//
// This is one of the two files whose purpose is a password, so unlike the
// others it brings one. ADR-0009 drew that line: the driver refuses to pick
// this file up by itself, and the import reads it only when somebody points
// at it.
func ReadPgpass(path string) ([]Found, error) {
	if err := refuseIfLoose(path); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var out []Found
	for n, line := range strings.Split(string(raw), "\n") {
		where := fmt.Sprintf("%s:%d", path, n+1)
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		fields := splitEscaped(line)
		if len(fields) != pgpassFields {
			out = append(out, Found{Where: where,
				Note: fmt.Sprintf("this line has %d fields and a .pgpass line has %d", len(fields), pgpassFields)})
			continue
		}
		host, port, database, user, password := fields[0], fields[1], fields[2], fields[3], fields[4]

		if host == "*" {
			// A line that matches any host says which password to use once a
			// server is known. It does not say which server.
			out = append(out, Found{Where: where,
				Note: "this line matches any host, so it names a password but no server to connect to"})
			continue
		}
		conn := store.SavedConnection{
			Driver: "postgres",
			Host:   host,
			Port:   5432,
			User:   user,
		}
		if port != "*" {
			n, err := strconv.Atoi(port)
			if err != nil {
				out = append(out, Found{Where: where, Note: fmt.Sprintf("%q is not a port", port)})
				continue
			}
			conn.Port = n
		}
		if database != "*" {
			conn.Database = database
		}
		if user == "*" {
			conn.User = ""
		}
		conn.Name = pgpassName(conn)

		found := Found{Connection: conn, Where: where}
		if password != "" {
			found.Secrets = map[string]string{"password": password}
		} else {
			found.Note = "this line has no password in it"
		}
		out = append(out, found)
	}
	return out, nil
}

func pgpassName(c store.SavedConnection) string {
	name := c.Host
	if c.User != "" {
		name = c.User + "@" + name
	}
	if c.Database != "" {
		name += "/" + c.Database
	}
	return name
}

// refuseIfLoose mirrors libpq, which ignores a password file that group or
// world can read. Importing from a file the real client refuses would be
// taking a password out of somewhere PostgreSQL has already judged unsafe,
// and doing it quietly.
func refuseIfLoose(path string) error {
	if runtime.GOOS == "windows" {
		return nil // libpq makes no permission check here either
	}
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if mode := st.Mode().Perm(); mode&0o077 != 0 {
		return fmt.Errorf("%s is readable by others (mode %04o), so PostgreSQL itself ignores it; "+
			"run chmod 0600 on it before importing from it", path, mode)
	}
	return nil
}

// splitEscaped splits a .pgpass line on its colons, honouring the backslash
// that PostgreSQL documents for a colon or a backslash inside a field.
func splitEscaped(line string) []string {
	var fields []string
	var b strings.Builder
	for i := 0; i < len(line); i++ {
		switch c := line[i]; c {
		case '\\':
			if i+1 < len(line) {
				i++
				b.WriteByte(line[i])
				continue
			}
			b.WriteByte(c)
		case ':':
			fields = append(fields, b.String())
			b.Reset()
		default:
			b.WriteByte(c)
		}
	}
	return append(fields, b.String())
}
