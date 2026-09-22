package importer

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/store"
)

// A connection set is this application's own export format (FR-1.13): the
// connections somebody has, in a file they can keep, send to a colleague or
// put under version control.
//
// It is written out field by field rather than by serialising the settings
// struct. A file other people hold is a contract, and it should not change
// because something internal was renamed.
const (
	// SetKind marks the file as this and not some other JSON.
	SetKind = "ikigai-db.connections"

	// SetVersion is the format's version. A file written by something newer
	// is refused rather than half-read.
	SetVersion = 1
)

// Set is an exported collection of connections.
type Set struct {
	Kind    string `json:"kind"`
	Version int    `json:"version"`

	// Exported is when, in RFC 3339. It is for the person reading the file,
	// and nothing depends on it.
	Exported string `json:"exported,omitempty"`

	// HasSecrets says the file contains credentials. It is written so that
	// whoever opens the file can be told what they are holding before they
	// send it anywhere.
	HasSecrets bool `json:"includes_secrets"`

	Folders     []SetFolder     `json:"folders,omitempty"`
	Connections []SetConnection `json:"connections"`
}

// SetFolder is a folder by name. No ID travels: an ID means nothing in
// somebody else's settings file.
type SetFolder struct {
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

// SetConnection is one connection in an exported set.
type SetConnection struct {
	Name     string            `json:"name"`
	Driver   string            `json:"driver"`
	Host     string            `json:"host,omitempty"`
	Port     int               `json:"port,omitempty"`
	Database string            `json:"database,omitempty"`
	User     string            `json:"user,omitempty"`
	Params   map[string]string `json:"params,omitempty"`

	TLS   store.TLS    `json:"tls,omitzero"`
	SSH   *store.SSH   `json:"ssh,omitempty"`
	Cloud *store.Cloud `json:"cloud,omitempty"`

	Environment string `json:"environment,omitempty"`
	ReadOnly    bool   `json:"read_only,omitempty"`
	Folder      string `json:"folder,omitempty"`
	Color       string `json:"color,omitempty"`

	// Needs are the names of the secrets this connection uses. They travel
	// even when the secrets themselves do not, so that an import can say
	// what is still to be supplied rather than leaving somebody to find out
	// at connect time.
	Needs []string `json:"needs_secrets,omitempty"`

	// Secrets are present only in a set exported with them.
	Secrets map[string]string `json:"secrets,omitempty"`
}

// MarshalSet writes a set as the JSON that goes in the file.
func MarshalSet(s Set) ([]byte, error) {
	s.Kind, s.Version = SetKind, SetVersion
	if s.Exported == "" {
		s.Exported = time.Now().UTC().Format(time.RFC3339)
	}
	if s.Connections == nil {
		s.Connections = []SetConnection{}
	}
	// Indented, because a file people keep and read is worth the bytes.
	return json.MarshalIndent(s, "", "  ")
}

// ReadSet reads a connection set back (FR-1.13).
//
// It answers the same Found that every other source answers, so that saving
// one is the same operation as saving an import from another tool, with the
// same rule about what is already there (ADR-0111).
func ReadSet(path string) ([]Found, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return readSetBytes(raw, path)
}

func readSetBytes(raw []byte, where string) ([]Found, error) {
	var set Set
	if err := json.Unmarshal(raw, &set); err != nil {
		return nil, fmt.Errorf("%s is not a connection set: %w", where, err)
	}
	if set.Kind != SetKind {
		return nil, fmt.Errorf("%s is not a connection set: it says it is %q", where, set.Kind)
	}
	if set.Version > SetVersion {
		return nil, fmt.Errorf("%s was written by a newer version of this program (format %d, this reads %d)",
			where, set.Version, SetVersion)
	}
	if len(set.Connections) == 0 {
		return []Found{{Where: where, Note: "this set has no connections in it"}}, nil
	}

	colours := map[string]string{}
	for _, f := range set.Folders {
		colours[f.Name] = f.Color
	}

	out := make([]Found, 0, len(set.Connections))
	for i, c := range set.Connections {
		at := fmt.Sprintf("%s (%d)", where, i+1)
		if c.Driver == "" {
			out = append(out, Found{Where: at, Note: quoted(c.Name) + " says which driver it needs nowhere"})
			continue
		}
		conn := store.SavedConnection{
			Name: c.Name, Driver: c.Driver, Host: c.Host, Port: c.Port,
			Database: c.Database, User: c.User, Params: c.Params,
			TLS: c.TLS, SSH: c.SSH, Cloud: c.Cloud,
			Environment: c.Environment, ReadOnly: c.ReadOnly, Color: c.Color,
		}
		found := Found{Connection: conn, Folder: c.Folder, Secrets: c.Secrets, Where: at}
		if len(c.Secrets) == 0 && len(c.Needs) > 0 {
			found.Note = "this set carries no credentials, so this connection still needs " + list(c.Needs)
		}
		out = append(out, found)
	}
	return out, nil
}

func quoted(name string) string {
	if name == "" {
		return "a connection with no name"
	}
	return `"` + name + `"`
}

// list writes names the way a sentence does.
func list(names []string) string {
	switch len(names) {
	case 0:
		return "nothing"
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	}
	out := ""
	for _, n := range names[:len(names)-1] {
		out += n + ", "
	}
	return out + "and " + names[len(names)-1]
}
