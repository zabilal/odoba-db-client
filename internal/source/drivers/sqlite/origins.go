package sqlite

import (
	"context"

	msqlite "modernc.org/sqlite"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// columnInfo asks SQLite where each column of a statement is read from,
// preparing it without running it. Nil where it cannot say.
func (ss *session) columnInfo(query string) []msqlite.ColumnInfo {
	var info []msqlite.ColumnInfo
	_ = ss.conn.Raw(func(dc any) error {
		if ci, ok := dc.(interface {
			ColumnInfo(string) ([]msqlite.ColumnInfo, error)
		}); ok {
			info, _ = ci.ColumnInfo(query)
		}
		return nil
	})
	return info
}

// resultOrigins says where a query's columns were read from (FR-4.8,
// ADR-0035): each column's table and its name there, as SQLite reports them.
// When every column comes from one table and the table's key, or its rowid,
// is among them, the result is known by that key, and its rows can be
// edited.
func (s *sqliteSource) resultOrigins(ctx context.Context, r *rowStream, info []msqlite.ColumnInfo) {
	if len(info) == 0 || len(info) != len(r.cols) {
		return
	}
	one := true // every column from the first column's table
	for i, c := range info {
		if c.TableName != info[0].TableName || c.DatabaseName != info[0].DatabaseName {
			one = false
		}
		if c.TableName != "" {
			r.cols[i].Origin = model.NewRef(model.KindTable, c.DatabaseName, c.TableName)
			r.cols[i].OriginColumn = c.OriginName
		}
	}
	if !one {
		return
	}
	id, err := s.identity(ctx, r.cols[0].Origin)
	if err != nil {
		return
	}
	have := map[string]bool{}
	for _, c := range r.cols {
		have[c.OriginColumn] = true
	}
	for _, k := range id.Columns {
		if !have[k] {
			return
		}
	}
	r.id = id
}
