// Package importer reads connections other tools already have (FR-1.12).
//
// Two rules run through all of it.
//
// An import brings connections, not secrets. DBeaver keeps its passwords in
// an encrypted store of its own and DataGrip keeps them in the operating
// system's or in KeePass; neither is opened here, and a connection arrives
// with a note saying its password was left where it was. The exceptions are
// .pgpass and ~/.my.cnf, whose whole purpose is to hold a password and which
// ADR-0009 already said would be read explicitly when somebody asks.
//
// Nothing is searched without being asked for. Scan looks in the documented
// places and reports what exists; reading a file is a separate step, because
// these files belong to other programs and some of them hold credentials.
package importer

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/ikigai-db/ikigai-db/internal/store"
)

// Found is one entry an import read.
//
// An entry that could not be brought is still a Found: it carries no
// connection and a Note saying why. Dropping it silently would leave
// somebody counting their connections and wondering which one went missing.
type Found struct {
	Connection store.SavedConnection

	// Secrets are the credentials that came with this entry, by the name
	// the keychain keeps them under. Only a source whose purpose is to hold
	// credentials ever fills this. They are handed to the keychain and never
	// written to the settings file (FR-1.5).
	Secrets map[string]string

	// Folder is the name of the folder this connection was in, where its
	// source had folders. It is a name and not an ID because the folder it
	// names may not exist here yet; whatever saves the connection resolves
	// it, and makes it if it has to.
	Folder string

	// Where is the file and line this came from, for a person deciding
	// whether to keep it.
	Where string

	// Note says what could not be brought: a password left in another
	// program's store, a database this has no driver for.
	Note string
}

// OK says whether this entry can be saved as a connection.
func (f Found) OK() bool { return f.Connection.Driver != "" }

// Tool is one place connections can be imported from.
type Tool struct {
	// Name is the program, as somebody would call it.
	Name string

	// Paths are where its file usually is, in the order to look.
	Paths []string

	// Read reads one such file.
	Read func(path string) ([]Found, error)

	// HoldsPasswords says this file's purpose is credentials, so that
	// whatever asks about it can say so before opening it.
	HoldsPasswords bool
}

// Candidate is a file that exists and could be read.
type Candidate struct {
	Tool string
	Path string
}

// Tools are the sources this can read.
//
// DBGate and TablePlus are not among them, and TASKS.md records why: one
// does not document where it puts its connections and its source may not be
// read here, and the other keeps them in a binary property list that would
// need either a new dependency or a reader of this project's own.
func Tools() []Tool {
	return []Tool{
		{
			Name:           "PostgreSQL password file",
			Paths:          pgpassPaths(),
			Read:           ReadPgpass,
			HoldsPasswords: true,
		},
		{
			Name:           "MySQL option file",
			Paths:          inHome(".my.cnf"),
			Read:           ReadMyCnf,
			HoldsPasswords: true,
		},
		{
			Name:  "DBeaver",
			Paths: dbeaverPaths(),
			Read:  ReadDBeaver,
		},
		{
			Name:  "DataGrip",
			Paths: dataGripPaths(),
			Read:  ReadDataGrip,
		},
	}
}

// Scan reports which of the known files are actually there. It reads none of
// them: what it answers is a list to be offered, not an import.
func Scan() []Candidate {
	var out []Candidate
	for _, t := range Tools() {
		for _, p := range t.Paths {
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				out = append(out, Candidate{Tool: t.Name, Path: p})
			}
		}
	}
	return out
}

// inHome names a file in the user's home directory, or nothing where there
// is no home directory to name it in.
func inHome(parts ...string) []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{filepath.Join(append([]string{home}, parts...)...)}
}

// pgpassPaths follows libpq: the environment first, then the documented
// place, which is not the same place on Windows.
func pgpassPaths() []string {
	if p := os.Getenv("PGPASSFILE"); p != "" {
		return []string{p}
	}
	if runtime.GOOS == "windows" {
		if dir := os.Getenv("APPDATA"); dir != "" {
			return []string{filepath.Join(dir, "postgresql", "pgpass.conf")}
		}
		return nil
	}
	return inHome(".pgpass")
}
