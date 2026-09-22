package importer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/store"
)

// DBeaver writes its connections as JSON and its credentials somewhere else,
// encrypted. Only the first is read here: an import brings connections, not
// secrets, and a connection arrives saying where its password was left.

// dbeaverProviders maps DBeaver's provider names to drivers of this
// project's. A provider absent from here is reported by name rather than
// guessed at.
var dbeaverProviders = map[string]string{
	"postgresql": "postgres",
	"postgres":   "postgres",
	"mysql":      "mysql",
	"mariadb":    "mysql",
	"sqlite":     "sqlite",
	"mongodb":    "mongodb",
	"mongo":      "mongodb",
	"redis":      "redis",
	"cassandra":  "cassandra",
	"kafka":      "kafka",
}

// dbeaverEnvironments carry DBeaver's connection type across, because the
// difference between a development database and a production one is worth
// more than the label: it is what decides whether this asks before it writes
// (NFR-S4, FR-13.x guardrails).
var dbeaverEnvironments = map[string]string{
	"dev":         "dev",
	"development": "dev",
	"test":        "staging",
	"stage":       "staging",
	"staging":     "staging",
	"prod":        "production",
	"production":  "production",
}

type dbeaverFile struct {
	Connections map[string]struct {
		Provider      string `json:"provider"`
		Driver        string `json:"driver"`
		Name          string `json:"name"`
		Folder        string `json:"folder"`
		ReadOnly      bool   `json:"read-only"`
		Configuration struct {
			Host     string `json:"host"`
			Port     any    `json:"port"`
			Database string `json:"database"`
			URL      string `json:"url"`
			User     string `json:"user"`
			Type     string `json:"type"`
		} `json:"configuration"`
	} `json:"connections"`
}

// ReadDBeaver reads a DBeaver data-sources.json (FR-1.12).
func ReadDBeaver(path string) ([]Found, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file dbeaverFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("%s is not a DBeaver data-sources.json: %w", path, err)
	}
	if len(file.Connections) == 0 {
		return []Found{{Where: path, Note: "this file has no connections in it"}}, nil
	}

	out := make([]Found, 0, len(file.Connections))
	for id, c := range file.Connections {
		where := path + " (" + id + ")"
		cfg := c.Configuration

		driver := dbeaverProviders[strings.ToLower(c.Provider)]
		// The discrete fields are preferred, and the URL is what answers for
		// the connections that have only one.
		conn := store.SavedConnection{
			Driver: driver, Host: cfg.Host, Database: cfg.Database, User: cfg.User,
			Port: atoiAny(cfg.Port), Name: c.Name, Folder: c.Folder, ReadOnly: c.ReadOnly,
		}
		note := "its password is in DBeaver's own encrypted store and was not read"
		if u, ok := readJDBC(cfg.URL); ok {
			if driver == "" {
				driver, conn.Driver = u.Driver, u.Driver
			}
			if conn.Host == "" {
				conn.Host = u.Host
			}
			if conn.Port == 0 {
				conn.Port = u.Port
			}
			if conn.Database == "" {
				conn.Database = u.Database
			}
			if conn.User == "" {
				conn.User = u.User
			}
			if u.HadPassword {
				note = "its URL carried a password, which was not brought: a password belongs in the keychain " +
					"and never in the settings file"
			}
		}
		if driver == "" {
			out = append(out, Found{Where: where, Note: named(c.Provider, c.Name) +
				", which there is no driver for here"})
			continue
		}
		conn.Driver = driver
		if conn.Name == "" {
			conn.Name = pgpassName(conn)
		}
		conn.Environment = dbeaverEnvironments[strings.ToLower(cfg.Type)]
		out = append(out, Found{Connection: conn, Where: where, Note: note})
	}
	return out, nil
}

func named(provider, name string) string {
	if name == "" {
		return "this is a " + provider + " connection"
	}
	return strconv.Quote(name) + " is a " + provider + " connection"
}

// atoiAny reads a port that may have been written as a number or as a
// string, because DBeaver has done both.
func atoiAny(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(n)
		return i
	}
	return 0
}

// dbeaverPaths are the documented workspace locations, with the connection
// file under the General project inside them.
func dbeaverPaths() []string {
	var roots []string
	switch runtime.GOOS {
	case "darwin":
		roots = inHome("Library", "DBeaverData")
	case "windows":
		if dir := os.Getenv("APPDATA"); dir != "" {
			roots = []string{filepath.Join(dir, "DBeaverData")}
		}
	default:
		if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
			roots = []string{filepath.Join(dir, "DBeaverData")}
		} else {
			roots = inHome(".local", "share", "DBeaverData")
		}
	}

	var out []string
	for _, root := range roots {
		// The workspace is numbered by major version, so the ones in use are
		// looked for rather than one being assumed.
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), "workspace") {
				out = append(out, filepath.Join(root, e.Name(), "General", ".dbeaver", "data-sources.json"))
			}
		}
	}
	return out
}
