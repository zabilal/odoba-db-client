package importer

import (
	"os"
	"strconv"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/store"
)

// ReadMyCnf reads MySQL's option file (FR-1.12).
//
// The file describes one client rather than a list, so at most one
// connection comes out of it. [client] applies to every MySQL program and
// [mysql] to the command-line client, so the second is read over the first,
// which is the order the MySQL tools themselves read them in.
func ReadMyCnf(path string) ([]Found, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	opts := map[string]string{}
	for _, section := range []string{"client", "mysql"} {
		for k, v := range myCnfSection(string(raw), section) {
			opts[k] = v
		}
	}
	if len(opts) == 0 {
		return []Found{{Where: path, Note: "this file has no [client] or [mysql] section in it"}}, nil
	}

	conn := store.SavedConnection{
		Driver:   "mysql",
		Host:     opts["host"],
		Port:     3306,
		User:     opts["user"],
		Database: opts["database"],
	}
	var note string
	if conn.Host == "" {
		// A local MySQL client with only a socket named reaches the server
		// over that socket. This connects over TCP, so it is said rather
		// than quietly turned into something else.
		conn.Host = "localhost"
		if s := opts["socket"]; s != "" {
			note = "this file connects over the socket " + s +
				", and the connection was brought as TCP to localhost instead"
		}
	}
	if p := opts["port"]; p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return []Found{{Where: path, Note: strconv.Quote(p) + " is not a port"}}, nil
		}
		conn.Port = n
	}
	conn.Name = pgpassName(conn)

	found := Found{Connection: conn, Where: path, Note: note}
	if pw := opts["password"]; pw != "" {
		found.Secrets = map[string]string{"password": pw}
	} else if note == "" {
		found.Note = "this file has no password in it"
	}
	return []Found{found}, nil
}

// myCnfSection reads one section of a MySQL option file. Values may be
// quoted, a bare option has no value at all, and both # and ; start a
// comment.
func myCnfSection(text, want string) map[string]string {
	out := map[string]string{}
	in := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") ||
			strings.HasPrefix(line, "!") { // !include and !includedir are not followed
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			in = strings.EqualFold(strings.TrimSpace(line[1:len(line)-1]), want)
			continue
		}
		if !in {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue // a bare option, such as no-auto-rehash
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if len(value) > 1 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		if key == "database" || key == "host" || key == "port" || key == "user" ||
			key == "password" || key == "socket" {
			out[key] = value
		}
	}
	return out
}
