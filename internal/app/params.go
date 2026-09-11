package app

import (
	"context"

	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
)

// ParamStore remembers the values given to named query parameters, by
// connection and name, so the next run asks with them filled in (FR-5.7).
type ParamStore interface {
	Param(ctx context.Context, connID, name string) (localdb.ParamValue, bool, error)
	PutParam(ctx context.Context, connID, name string, v localdb.ParamValue) error
}
