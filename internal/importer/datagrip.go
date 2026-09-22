package importer

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/store"
)

// DataGrip writes its data sources as XML and keeps its passwords in the
// operating system's password store or in KeePass. Neither is opened here.

// dataGripDrivers maps a driver-ref to a driver of this project's. Where
// there is no match the JDBC URL still answers, which is why this map can be
// short without losing connections.
var dataGripDrivers = map[string]string{
	"postgresql": "postgres",
	"mysql":      "mysql",
	"mariadb":    "mysql",
	"sqlite":     "sqlite",
	"mongodb":    "mongodb",
	"redis":      "redis",
	"cassandra":  "cassandra",
}

type dataGripSource struct {
	Name      string `xml:"name,attr"`
	UUID      string `xml:"uuid,attr"`
	DriverRef string `xml:"driver-ref"`
	URL       string `xml:"jdbc-url"`
	User      string `xml:"user-name"`
	ReadOnly  bool   `xml:"read-only"`
}

// ReadDataGrip reads a DataGrip dataSources.xml (FR-1.12).
func ReadDataGrip(path string) ([]Found, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// The data sources are taken wherever they appear rather than through a
	// fixed nesting. An IDE-level file and a project's own put them at
	// different depths, and a project file can hold several components.
	sources, err := dataGripSources(raw)
	if err != nil {
		return nil, fmt.Errorf("%s is not a DataGrip dataSources.xml: %w", path, err)
	}
	if len(sources) == 0 {
		return []Found{{Where: path, Note: "this file has no data sources in it"}}, nil
	}

	out := make([]Found, 0, len(sources))
	for _, s := range sources {
		where := path
		if s.Name != "" {
			where += " (" + s.Name + ")"
		}
		u, ok := readJDBC(s.URL)
		driver := dataGripDrivers[strings.ToLower(s.DriverRef)]
		if driver == "" {
			driver = u.Driver
		}
		if !ok || driver == "" {
			what := s.DriverRef
			if what == "" {
				what = u.Scheme
			}
			if what == "" {
				what = "an unnamed kind of"
			}
			out = append(out, Found{Where: where, Note: named(what, s.Name) +
				", which there is no driver for here"})
			continue
		}

		conn := store.SavedConnection{
			Driver: driver, Host: u.Host, Port: u.Port, Database: u.Database,
			Name: s.Name, User: s.User, ReadOnly: s.ReadOnly,
		}
		if conn.User == "" {
			conn.User = u.User
		}
		if conn.Name == "" {
			conn.Name = pgpassName(conn)
		}
		note := "its password is in the password store DataGrip was told to use and was not read"
		if u.HadPassword {
			note = "its URL carried a password, which was not brought: a password belongs in the keychain " +
				"and never in the settings file"
		}
		out = append(out, Found{Connection: conn, Where: where, Note: note})
	}
	return out, nil
}

// dataGripSources walks the document and decodes every data source in it,
// whatever it is nested under.
func dataGripSources(raw []byte) ([]dataGripSource, error) {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	var out []dataGripSource
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Local != "data-source" {
			continue
		}
		var s dataGripSource
		if err := dec.DecodeElement(&s, &start); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
}

// dataGripPaths are the IDE-level settings directories. Project-level data
// sources live in a project's own .idea directory, which is somewhere only
// the person knows, so those are imported by being pointed at.
func dataGripPaths() []string {
	var roots []string
	switch runtime.GOOS {
	case "darwin":
		roots = inHome("Library", "Application Support", "JetBrains")
	case "windows":
		if dir := os.Getenv("APPDATA"); dir != "" {
			roots = []string{filepath.Join(dir, "JetBrains")}
		}
	default:
		roots = inHome(".config", "JetBrains")
	}

	var out []string
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), "DataGrip") {
				out = append(out, filepath.Join(root, e.Name(), "options", "dataSources.xml"))
			}
		}
	}
	return out
}
