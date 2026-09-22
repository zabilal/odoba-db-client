package app

import (
	"fmt"
	"slices"

	"github.com/ikigai-db/ikigai-db/internal/importer"
	"github.com/ikigai-db/ikigai-db/internal/store"
)

// ExportSet writes connections out as a file somebody can keep, send on or
// put under version control (FR-1.13).
//
// Secrets are excluded unless they are asked for, which is the requirement
// and also the only sensible default: an exported set is a file that gets
// attached to messages. When they are asked for, the file says so about
// itself, so that whoever opens it later knows what they are holding.
//
// ids selects connections; empty means all of them.
func (c *Connections) ExportSet(ids []string, withSecrets bool) ([]byte, error) {
	names := map[string]store.Folder{}
	for _, f := range c.Folders() {
		names[f.ID] = f
	}

	set := importer.Set{HasSecrets: withSecrets}
	seenFolder := map[string]bool{}

	for _, conn := range c.List() {
		if len(ids) > 0 && !slices.Contains(ids, conn.ID) {
			continue
		}
		out := importer.SetConnection{
			Name: conn.Name, Driver: conn.Driver, Host: conn.Host, Port: conn.Port,
			Database: conn.Database, User: conn.User, Params: conn.Params,
			TLS: conn.TLS, SSH: conn.SSH, Cloud: conn.Cloud,
			Environment: conn.Environment, ReadOnly: conn.ReadOnly, Color: conn.Color,
			Needs: append([]string(nil), conn.Secrets...),
		}
		if f, ok := names[conn.Folder]; ok {
			out.Folder = f.Name
			if !seenFolder[f.ID] {
				seenFolder[f.ID] = true
				set.Folders = append(set.Folders, importer.SetFolder{Name: f.Name, Color: f.Color})
			}
		}

		if withSecrets {
			for _, key := range conn.Secrets {
				v, err := c.vault.Get(conn.ID, key)
				if err != nil {
					// A set that was asked to carry credentials and quietly
					// came back without some of them would be a file that
					// looks complete and is not.
					return nil, fmt.Errorf("app: %s: the %s could not be read to export it: %w",
						conn.Name, key, err)
				}
				if v == "" {
					continue
				}
				if out.Secrets == nil {
					out.Secrets = map[string]string{}
				}
				out.Secrets[key] = v
			}
		}
		set.Connections = append(set.Connections, out)
	}
	return importer.MarshalSet(set)
}
