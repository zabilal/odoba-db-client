package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
)

// A query somebody saved in the application, run from a pipeline (FR-16.1).
//
// The requirement says "run a saved query", and this is what makes that more
// than --file: the queries in the application are where the work already is,
// and a pipeline that re-types one has two copies of it to keep together.
//
// Read-only, and never written: a run from here changes nothing about the query
// and records no history. History is the window's account of what somebody did.

// savedQuery is the body of the saved query with that name.
func savedQuery(name string) (string, error) {
	want := strings.TrimSpace(name)
	paths, err := store.Resolve()
	if err != nil {
		return "", err
	}
	db, err := localdb.Open(context.Background(), paths.DatabaseFile())
	if err != nil {
		return "", fmt.Errorf("the saved queries could not be read: %w", err)
	}
	defer db.Close()
	queries, err := db.SavedQueries(context.Background())
	if err != nil {
		return "", err
	}
	for _, q := range queries {
		if strings.EqualFold(strings.TrimSpace(q.Name), want) {
			return q.Body, nil
		}
	}
	return "", fmt.Errorf("no saved query called %q", want)
}
