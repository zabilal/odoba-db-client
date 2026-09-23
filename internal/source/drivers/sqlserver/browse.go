package sqlserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	mssql "github.com/microsoft/go-mssqldb"

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

// Browse opens an object's rows, ordered last by a unique key so that paging
// is deterministic.
//
// SQL Server has no row address to fall back on — %%physloc%% is where a row
// sits on a page and moves when it is updated — so a table with neither a
// primary key nor a NOT NULL unique index is ordered by every column:
// deterministic for rows that differ, and the best there is.
func (s *sqlServerSource) Browse(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (model.RowStream, error) {
	if !browsableKinds[ref.Kind] || len(ref.Path) < 3 {
		return nil, fmt.Errorf("sqlserver: %s is not browsable", ref)
	}
	db, err := s.pool(ctx, ref)
	if err != nil {
		return nil, err
	}
	id := identity{row: model.RowIdentity{Kind: model.IdentityNone}}
	if ref.Kind == model.KindTable {
		if id, err = s.identity(ctx, ref); err != nil {
			return nil, err
		}
		opt.Sorts = tiebreak(opt.Sorts, id.sort)
	}
	stmt, err := s.BuildBrowse(ref, opt)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, stmt.SQL, stmt.Args...)
	if err != nil {
		return nil, statementError(err, ctx, stmt.SQL)
	}
	st, err := newRowStream(rows, ref, id.row)
	if err != nil {
		return nil, statementError(err, ctx, stmt.SQL)
	}
	st.ctx = ctx
	return st, nil
}

// identity finds a table's key once and remembers it: the answer does not
// change while a connection is open, and paging asks for it on every page.
func (s *sqlServerSource) identity(ctx context.Context, ref model.ObjectRef) (identity, error) {
	key := strings.Join(ref.Path[:3], "\x00")
	s.idMu.Lock()
	id, ok := s.identities[key]
	s.idMu.Unlock()
	if ok {
		return id, nil
	}
	pk, err := s.keyColumns(ctx, ref)
	if err != nil {
		return id, err
	}
	switch {
	case len(pk.primary) > 0:
		id = identity{row: model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: pk.primary, Target: ref}, sort: pk.primary}
	case len(pk.unique) > 0:
		id = identity{row: model.RowIdentity{Kind: model.IdentityUniqueIndex, Columns: pk.unique, Target: ref}, sort: pk.unique}
	default:
		cols, err := s.columns(ctx, ref)
		if err != nil {
			return id, err
		}
		all := make([]string, 0, len(cols))
		for _, c := range cols {
			// A column no ORDER BY may name: a large object cannot be sorted
			// on, and asking would fail the whole read rather than page it.
			if sortable(c.Type) {
				all = append(all, c.Name)
			}
		}
		id = identity{row: model.RowIdentity{Kind: model.IdentityNone}, sort: all}
	}
	s.idMu.Lock()
	s.identities[key] = id
	s.idMu.Unlock()
	return id, nil
}

// sortable reports whether ORDER BY may name a column of this type. SQL
// Server refuses to sort on the large object types and on xml.
func sortable(dt model.DataType) bool {
	switch strings.ToLower(nameOf(dt.Native)) {
	case "text", "ntext", "image", "xml", "geography", "geometry", "hierarchyid", "sql_variant":
		return false
	}
	return dt.Length != -1 || dt.Class != model.TypeString && dt.Class != model.TypeBytes
}

// nameOf is a declared type without its length: "nvarchar(50)" is nvarchar.
func nameOf(native string) string {
	name, _, _ := strings.Cut(native, "(")
	return strings.TrimSpace(name)
}

type keys struct{ primary, unique []string }

// keyColumns finds the primary key, and failing that the first unique index
// whose columns are all NOT NULL: a unique index over nullable columns does
// not tell two rows of NULLs apart — and on SQL Server it does not even try,
// because it holds one NULL row at most.
//
// The indexes come in the order the catalogue numbers them, the key among
// them rather than ahead of them: which one it is, is read from the row
// rather than from where it is in the list.
func (s *sqlServerSource) keyColumns(ctx context.Context, ref model.ObjectRef) (keys, error) {
	db, err := s.pool(ctx, ref)
	if err != nil {
		return keys{}, err
	}
	rows, err := db.QueryContext(ctx, `SELECT i.index_id, i.is_primary_key, c.name, c.is_nullable
		FROM sys.indexes i
		JOIN sys.objects o ON o.object_id = i.object_id
		JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id
		JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
		WHERE o.schema_id = SCHEMA_ID(@p1) AND o.name = @p2
		  AND i.is_unique = 1 AND ic.is_included_column = 0
		ORDER BY i.index_id, ic.key_ordinal`, ref.Path[1], ref.Path[2])
	if err != nil {
		return keys{}, statementError(err, ctx, "")
	}
	defer rows.Close()
	var k keys
	cols := map[int64][]string{}
	nullable := map[int64]bool{}
	var order []int64
	primary := map[int64]bool{}
	for rows.Next() {
		var id int64
		var isPrimary, isNullable bool
		var col string
		if err := rows.Scan(&id, &isPrimary, &col, &isNullable); err != nil {
			return keys{}, err
		}
		if _, seen := cols[id]; !seen {
			order = append(order, id)
		}
		cols[id] = append(cols[id], col)
		nullable[id] = nullable[id] || isNullable
		primary[id] = isPrimary
	}
	for _, id := range order {
		switch {
		case primary[id]:
			k.primary = cols[id]
		case k.unique == nil && !nullable[id]:
			k.unique = cols[id]
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
	ctx     context.Context // the statement's, to tell a stop from a failure
	onClose func()
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
		dt.Length = -1
		if n, ok := ct.Length(); ok {
			dt.Length = n
		}
		if p, sc, ok := ct.DecimalSize(); ok && dt.Class == model.TypeDecimal {
			dt.Precision, dt.Scale = int32(p), int32(sc)
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
		r.Close()
		if err != nil {
			return nil, statementError(err, r.ctx, "")
		}
		if r.ctx != nil && r.ctx.Err() != nil {
			return nil, r.ctx.Err() // cut short by a stop: not the end of the data
		}
		return nil, io.EOF
	}
	if err := r.rows.Scan(r.ptrs...); err != nil {
		return nil, statementError(err, r.ctx, "")
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
	err := r.rows.Close()
	if r.onClose != nil {
		r.onClose()
	}
	return err
}

// normalize narrows a scanned value to the model's set.
//
// go-mssqldb hands exact numerics, binary data and uniqueidentifiers over as
// bytes; the column's type says which they are, and a value read as bytes
// that is not bytes would be shown as a hex dump of itself.
func normalize(v any, dt model.DataType) any {
	switch x := v.(type) {
	case []byte:
		b := append([]byte(nil), x...) // the driver reuses its buffer
		switch dt.Class {
		case model.TypeDecimal:
			return model.Decimal(b)
		case model.TypeUUID:
			var u mssql.UniqueIdentifier
			if err := u.Scan(b); err == nil {
				return u.String()
			}
			return b
		case model.TypeJSON:
			if json.Valid(b) {
				return model.JSON(b)
			}
			return string(b)
		case model.TypeString, model.TypeXML:
			return string(b)
		}
		return b
	case string:
		if dt.Class == model.TypeDecimal {
			// A money column arrives as its own text; reading it as a number
			// would round it to the nearest ten-thousandth of a float.
			return model.Decimal(x)
		}
		return x
	case int32:
		return int64(x)
	case float32:
		return float64(x)
	}
	return v
}

// dataType classifies a SQL Server type by name, whether it came from the
// catalogue or from a result column.
func dataType(name string) model.DataType {
	n := strings.ToLower(nameOf(name))
	dt := model.DataType{Native: n, Length: -1, Nullable: true}
	switch n {
	case "bit":
		dt.Class = model.TypeBool
	case "tinyint", "smallint", "int", "bigint":
		dt.Class = model.TypeInteger
	case "decimal", "numeric", "money", "smallmoney":
		dt.Class = model.TypeDecimal
	case "float", "real":
		dt.Class = model.TypeFloat
	case "char", "varchar", "text", "nchar", "nvarchar", "ntext", "sysname":
		dt.Class = model.TypeString
	case "binary", "varbinary", "image", "timestamp", "rowversion":
		dt.Class = model.TypeBytes
	case "date":
		dt.Class = model.TypeDate
	case "time":
		dt.Class = model.TypeTime
	case "datetime", "datetime2", "smalldatetime":
		dt.Class = model.TypeTimestamp
	case "datetimeoffset":
		dt.Class, dt.TimeZone = model.TypeTimestamp, true
	case "uniqueidentifier":
		dt.Class = model.TypeUUID
	case "json":
		// SQL Server 2025's own type. Every version before it keeps JSON in
		// an nvarchar, where it is text and is shown as text.
		dt.Class = model.TypeJSON
	case "xml":
		dt.Class = model.TypeXML
	case "geography", "geometry":
		dt.Class = model.TypeGeometry
	default:
		dt.Class = model.TypeUnknown
	}
	return dt
}

// wideTypes hold two bytes to the character, so what the catalogue reports
// in bytes is half that many characters.
var wideTypes = map[string]bool{"nchar": true, "nvarchar": true, "ntext": true, "sysname": true}

// sized are the types written with a length, and lengthy the ones whose
// length may be "max".
var sized = map[string]bool{"char": true, "varchar": true, "nchar": true, "nvarchar": true,
	"binary": true, "varbinary": true}

// scaled are the types written with a fractional-seconds scale.
var scaled = map[string]bool{"time": true, "datetime2": true, "datetimeoffset": true}

// nativeType writes the type as somebody would declare it, out of the parts
// the catalogue keeps separately.
func nativeType(name string, maxLen, precision, scale int64) string {
	n := strings.ToLower(name)
	switch {
	case sized[n] && maxLen < 0:
		return n + "(max)"
	case sized[n]:
		if wideTypes[n] {
			maxLen /= 2
		}
		return n + "(" + strconv.FormatInt(maxLen, 10) + ")"
	case n == "decimal" || n == "numeric":
		return n + "(" + strconv.FormatInt(precision, 10) + "," + strconv.FormatInt(scale, 10) + ")"
	case scaled[n]:
		return n + "(" + strconv.FormatInt(scale, 10) + ")"
	}
	return n
}

// declaredLength is the length the column was declared with, in characters
// for text and bytes for binary, or -1 where a type has no length. A "max"
// column reports -1 as well: it has no declared length to report.
func declaredLength(name string, maxLen int64) int64 {
	n := strings.ToLower(name)
	if !sized[n] || maxLen < 0 {
		return -1
	}
	if wideTypes[n] {
		return maxLen / 2
	}
	return maxLen
}
