package app

import (
	"fmt"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/importer"
	"github.com/ikigai-db/ikigai-db/internal/store"
)

// ImportResult says what an import did, and — as importantly — what it did
// not do. A person who imports forty connections and gets thirty-one needs to
// know which nine and why, rather than counting them.
type ImportResult struct {
	// Saved are the connections now in the settings.
	Saved []store.SavedConnection

	// Already are the ones that were there already, by name.
	Already []string

	// Left are the entries that could not be brought, each with the reason
	// the importer gave.
	Left []string
}

// Import saves connections another tool already had (FR-1.12).
//
// withSecrets decides whether credentials that came with an entry are kept.
// Only a source whose purpose is to hold them ever carries any, and copying
// somebody's credentials into a second store is their decision to make
// rather than a default to discover afterwards.
//
// An entry that matches a connection already saved is not saved twice.
// Importing the same file again is a thing people do, usually because they
// are not sure whether the first one worked.
func (c *Connections) Import(found []importer.Found, withSecrets bool) (ImportResult, error) {
	var result ImportResult
	existing := c.List()

	for _, f := range found {
		if !f.OK() {
			result.Left = append(result.Left, describeLeft(f))
			continue
		}
		if name, dup := sameConnection(existing, f.Connection); dup {
			result.Already = append(result.Already, name)
			continue
		}

		conn := f.Connection
		if f.Folder != "" {
			// The folder came across as a name, because the source had no
			// idea what this application calls its folders. A connection
			// filed under a folder that does not exist here would show as
			// belonging to nothing.
			id, err := c.folderNamed(f.Folder)
			if err != nil {
				result.Left = append(result.Left, fmt.Sprintf("%s could not be filed under %q: %v",
					nameOf(f), f.Folder, err))
				continue
			}
			conn.Folder = id
		}

		var secretValues map[string]string
		if withSecrets && len(f.Secrets) > 0 {
			secretValues = f.Secrets
		}
		saved, err := c.Create(conn, secretValues)
		if err != nil {
			// One connection that will not save must not lose the rest, so
			// this is reported beside them rather than thrown instead of them.
			result.Left = append(result.Left, fmt.Sprintf("%s could not be saved: %v", nameOf(f), err))
			continue
		}
		result.Saved = append(result.Saved, saved)
		existing = append(existing, saved)
	}
	return result, nil
}

func describeLeft(f importer.Found) string {
	if f.Note == "" {
		return f.Where
	}
	return f.Where + ": " + f.Note
}

func nameOf(f importer.Found) string {
	if f.Connection.Name != "" {
		return f.Connection.Name
	}
	return f.Where
}

// sameConnection is what makes importing twice harmless. Two connections are
// the same when they reach the same place as the same account; the name is
// not part of it, because the same database saved under two names is still
// one database.
func sameConnection(existing []store.SavedConnection, c store.SavedConnection) (string, bool) {
	for _, e := range existing {
		if strings.EqualFold(e.Driver, c.Driver) &&
			strings.EqualFold(e.Host, c.Host) &&
			e.Port == c.Port &&
			e.Database == c.Database &&
			e.User == c.User {
			return e.Name, true
		}
	}
	return "", false
}

// folderNamed finds the folder with this name, or makes it. Names are
// compared as people read them, so "Work" and "work" are one folder rather
// than two that look the same in the tree.
func (c *Connections) folderNamed(name string) (string, error) {
	for _, f := range c.Folders() {
		if strings.EqualFold(f.Name, name) {
			return f.ID, nil
		}
	}
	f, err := c.CreateFolder(name, "")
	if err != nil {
		return "", err
	}
	return f.ID, nil
}
