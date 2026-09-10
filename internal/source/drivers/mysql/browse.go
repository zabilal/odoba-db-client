package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

var browsableKinds = map[model.ObjectKind]bool{model.KindTable: true, model.KindView: true}

// identity is how a table's rows are ordered for paging and addressed for
// editing.
type identity struct {
	row  model.RowIdentity
	sort []string // the unique key paging ends its ORDER BY with
}

// Browse opens an object's rows, ordered last by a unique key so paging is
// deterministic. MySQL has no row address to fall back on, so a table with
// neither primary key nor NOT NULL unique index is ordered by every column:
// deterministic for distinct rows, and the best there is.
func (s *mysqlSource) Browse(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (model.RowStream, error) {
	if !browsableKinds[ref.Kind] || len(ref.Path) < 2 {
		return nil, fmt.Errorf("mysql: %s is not browsable", ref)
	}
	id := identity{row: model.RowIdentity{Kind: model.IdentityNone}}
	if ref.Kind == model.KindTable {
		var err error
		if id, err = s.identity(ctx, ref); err != nil {
			return nil, statementError(err, ctx)
		}
		opt.Sorts = tiebreak(opt.Sorts, id.sort)
	}
	stmt, err := s.BuildBrowse(ref, opt)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, stmt.SQL, stmt.Args...)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	st, err := newRowStream(rows, ref, id.row)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	st.ctx = ctx
	return st, nil
}

func (s *mysqlSource) identity(ctx context.Context, ref model.ObjectRef) (identity, error) {
	key := ref.Path[0] + "\x00" + ref.Path[1]
	s.mu.Lock()
	id, ok := s.identities[key]
	s.mu.Unlock()
	if ok {
		return id, nil
	}
	pk, err := s.keyColumns(ctx, ref.Path[0], ref.Path[1])
	if err != nil {
		return id, err
	}
	switch {
	case len(pk.primary) > 0:
		id = identity{row: model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: pk.primary, Target: ref}, sort: pk.primary}
	case len(pk.unique) > 0:
		id = identity{row: model.RowIdentity{Kind: model.IdentityUniqueIndex, Columns: pk.unique, Target: ref}, sort: pk.unique}
	default:
		cols, err := s.columns(ctx, ref.Path[0], ref.Path[1])
		if err != nil {
			return id, err
		}
		all := make([]string, len(cols))
		for i, c := range cols {
			all[i] = c.Name
		}
		id = identity{row: model.RowIdentity{Kind: model.IdentityNone}, sort: all}
	}
	s.mu.Lock()
	s.identities[key] = id
	s.mu.Unlock()
	return id, nil
}

type keys struct{ primary, unique []string }

// keyColumns finds the primary key, and failing that the first unique index
// whose columns are all NOT NULL: a unique index over nullable columns does
// not tell NULL rows apart.
func (s *mysqlSource) keyColumns(ctx context.Context, db, table string) (keys, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT st.INDEX_NAME, st.COLUMN_NAME, c.IS_NULLABLE
		FROM information_schema.STATISTICS st
		JOIN information_schema.COLUMNS c ON c.TABLE_SCHEMA = st.TABLE_SCHEMA
		  AND c.TABLE_NAME = st.TABLE_NAME AND c.COLUMN_NAME = st.COLUMN_NAME
		WHERE st.TABLE_SCHEMA = ? AND st.TABLE_NAME = ? AND st.NON_UNIQUE = 0
		ORDER BY st.INDEX_NAME = 'PRIMARY' DESC, st.INDEX_NAME, st.SEQ_IN_INDEX`, db, table)
	if err != nil {
		return keys{}, err
	}
	defer rows.Close()
	var k keys
	cols := map[string][]string{}
	nullable := map[string]bool{}
	var order []string
	for rows.Next() {
		var index, col, isNull string
		if err := rows.Scan(&index, &col, &isNull); err != nil {
			return keys{}, err
		}
		if _, seen := cols[index]; !seen {
			order = append(order, index)
		}
		cols[index] = append(cols[index], col)
		nullable[index] = nullable[index] || isNull == "YES"
	}
	for _, ix := range order {
		if ix == "PRIMARY" {
			k.primary = cols[ix]
		} else if k.unique == nil && !nullable[ix] {
			k.unique = cols[ix]
		}
	}
	return k, rows.Err()
}

func tiebreak(sorts []source.Sort, key []string) []source.Sort {
	have := map[string]bool{}
	for _, s := range sorts {
		have[strings.ToLower(s.Column)] = true
	}
	out := append([]source.Sort(nil), sorts...)
	for _, k := range key {
		if !have[strings.ToLower(k)] {
			out = append(out, source.Sort{Column: k})
		}
	}
	return out
}

// rowStream adapts database/sql rows to the row contract.
type rowStream struct {
	rows    *sql.Rows
	cols    []model.ColumnDef
	vals    []any
	ptrs    []any
	id      model.RowIdentity
	ctx     context.Context // the statement's, to tell a cancel from a failure
	onClose func()
	abandon func() // stops the server sending the rest, for an early Close
	done    bool   // read to the end
	closed  bool
}

var (
	_ model.RowStream  = (*rowStream)(nil)
	_ model.Identified = (*rowStream)(nil)
)

func newRowStream(rows *sql.Rows, origin model.ObjectRef, id model.RowIdentity) (*rowStream, error) {
	cts, err := rows.ColumnTypes()
	if err != nil {
		rows.Close()
		return nil, err
	}
	r := &rowStream{rows: rows, id: id, cols: make([]model.ColumnDef, len(cts)),
		vals: make([]any, len(cts)), ptrs: make([]any, len(cts))}
	for i, ct := range cts {
		dt := dataType(ct.DatabaseTypeName())
		if p, s, ok := ct.DecimalSize(); ok && dt.Class == model.TypeDecimal {
			dt.Precision, dt.Scale = int32(p), int32(s)
		}
		if n, ok := ct.Nullable(); ok {
			dt.Nullable = n
		}
		r.cols[i] = model.ColumnDef{Name: ct.Name(), Type: dt, Origin: origin}
		r.ptrs[i] = &r.vals[i]
	}
	return r, nil
}

func (r *rowStream) Columns() []model.ColumnDef  { return r.cols }
func (r *rowStream) Identity() model.RowIdentity { return r.id }

func (r *rowStream) Next(ctx context.Context) (model.Row, error) {
	if r.closed {
		return nil, io.EOF
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !r.rows.Next() {
		err := r.rows.Err()
		r.done = true
		r.Close()
		if err != nil {
			return nil, statementError(err, r.ctx)
		}
		if r.ctx != nil && r.ctx.Err() != nil {
			return nil, r.ctx.Err() // cut short by a stop: not the end of the data
		}
		return nil, io.EOF
	}
	if err := r.rows.Scan(r.ptrs...); err != nil {
		return nil, statementError(err, r.ctx)
	}
	row := make(model.Row, len(r.vals))
	for i, v := range r.vals {
		row[i] = normalize(v, r.cols[i].Type)
	}
	return row, nil
}

// Close releases the result. Safe to call more than once.
func (r *rowStream) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	if !r.done && r.abandon != nil {
		r.abandon()
	}
	err := r.rows.Close()
	if r.onClose != nil {
		r.onClose()
	}
	return err
}

// normalize narrows a scanned value to the model's set. go-sql-driver hands
// text, exact numerics and binary data over as bytes; the column's type says
// which they are.
func normalize(v any, dt model.DataType) any {
	switch x := v.(type) {
	case []byte:
		b := append([]byte(nil), x...) // the driver reuses its buffer
		switch dt.Class {
		case model.TypeDecimal:
			return model.Decimal(b)
		case model.TypeJSON:
			if json.Valid(b) {
				return model.JSON(b)
			}
			return string(b)
		case model.TypeString, model.TypeEnum, model.TypeTime, model.TypeUnknown:
			return string(b)
		case model.TypeInteger:
			if n, err := strconv.ParseInt(string(b), 10, 64); err == nil {
				return n
			}
			return model.Decimal(b)
		}
		return b
	case uint64:
		if x <= math.MaxInt64 {
			return int64(x)
		}
		return model.Decimal(strconv.FormatUint(x, 10)) // BIGINT UNSIGNED past int64
	case int32:
		return int64(x)
	case float32:
		return float64(x)
	}
	return v
}

// dataType classifies the type go-sql-driver reports for a result column.
func dataType(name string) model.DataType {
	u := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(name)), "UNSIGNED ")
	dt := model.DataType{Native: strings.ToLower(name), Length: -1, Nullable: true}
	switch u {
	case "TINYINT", "SMALLINT", "MEDIUMINT", "INT", "INTEGER", "BIGINT", "YEAR":
		dt.Class = model.TypeInteger
	case "DECIMAL", "NUMERIC":
		dt.Class = model.TypeDecimal
	case "FLOAT", "DOUBLE", "REAL":
		dt.Class = model.TypeFloat
	case "CHAR", "VARCHAR", "TEXT", "TINYTEXT", "MEDIUMTEXT", "LONGTEXT":
		dt.Class = model.TypeString
	case "BINARY", "VARBINARY", "BLOB", "TINYBLOB", "MEDIUMBLOB", "LONGBLOB":
		dt.Class = model.TypeBytes
	case "DATE":
		dt.Class = model.TypeDate
	case "DATETIME":
		dt.Class = model.TypeTimestamp
	case "TIMESTAMP":
		dt.Class, dt.TimeZone = model.TypeTimestamp, true // stored as an instant; read in UTC
	case "TIME":
		dt.Class = model.TypeTime
	case "JSON":
		dt.Class = model.TypeJSON
	case "ENUM", "SET":
		dt.Class = model.TypeEnum
	case "BIT":
		dt.Class = model.TypeBit
	case "GEOMETRY", "POINT", "LINESTRING", "POLYGON":
		dt.Class = model.TypeGeometry
	default:
		dt.Class = model.TypeUnknown
	}
	return dt
}
