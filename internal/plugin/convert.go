package plugin

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/plugin"
)

// Turning what a plugin says into what the application holds.
//
// Every value here is a plugin's, which is to say somebody else's: a kind
// nobody has, a path with nothing in it, a row longer than its columns. None of
// it is trusted to be right, and none of it can be more than wrong — a plugin
// saying nonsense makes an odd tree, not a broken application.

func paradigm(s string) model.Paradigm {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "document":
		return model.ParadigmDocument
	case "keyvalue", "key-value", "kv":
		return model.ParadigmKeyValue
	case "stream", "log":
		return model.ParadigmStream
	}
	return model.ParadigmRelational
}

func fieldKind(s string) source.FieldKind {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "number", "int":
		return source.FieldNumber
	case "password", "secret":
		return source.FieldPassword
	case "file", "path":
		return source.FieldFile
	case "choice", "select":
		return source.FieldSelect
	case "bool", "boolean", "check":
		return source.FieldBool
	}
	return source.FieldText
}

// connectError turns a plugin's refusal into the application's own, so that a
// plugin's failure to connect reads like every other driver's (FR-1.4).
func connectError(err error) error {
	var e *Error
	if !errors.As(err, &e) {
		return err
	}
	kind := source.ConnectUnknown
	switch e.Kind {
	case plugin.FailConfig:
		kind = source.ConnectConfig
	case plugin.FailAuth:
		kind = source.ConnectAuth
	case plugin.FailNetwork:
		kind = source.ConnectUnreachable
	case plugin.FailTLS:
		kind = source.ConnectTLS
	case plugin.FailNoDatabase:
		kind = source.ConnectNoDatabase
	case plugin.FailRefused:
		kind = source.ConnectRefused
	}
	return &source.ConnectError{Kind: kind, Hint: e.Text}
}

func wireRef(ref model.ObjectRef) plugin.Ref {
	return plugin.Ref{Kind: string(ref.Kind), Path: append([]string(nil), ref.Path...)}
}

func modelRef(r plugin.Ref) model.ObjectRef {
	return model.NewRef(model.ObjectKind(r.Kind), r.Path...)
}

func nodes(in []plugin.Node) []model.Node {
	out := make([]model.Node, 0, len(in))
	for _, n := range in {
		if len(n.Ref.Path) == 0 || n.Ref.Kind == "" {
			// A node nothing can address is a node nothing can open, and
			// would be a row in the tree that does nothing when clicked.
			continue
		}
		ref := modelRef(n.Ref)
		label := n.Label
		if label == "" {
			label = ref.Name()
		}
		node := model.Node{Ref: ref, Label: label,
			HasChildren: n.HasChildren, Browsable: n.Browsable}
		if n.Detail != "" {
			// The grey text beside a label is an attribute, which is how the
			// tree draws everything it does not know the meaning of.
			node.Attrs = map[string]string{"detail": n.Detail}
		}
		out = append(out, node)
	}
	return out
}

func columns(in []plugin.Column) []model.ColumnDef {
	out := make([]model.ColumnDef, len(in))
	for i, c := range in {
		out[i] = model.ColumnDef{Name: c.Name, Comment: c.Comment, Type: dataType(c)}
	}
	return out
}

func dataType(c plugin.Column) model.DataType {
	return model.DataType{Class: typeClass(c.Class), Native: c.Type,
		Nullable: c.Nullable, Length: -1}
}

func typeClass(s string) model.TypeClass {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "integer", "int":
		return model.TypeInteger
	case "float", "double", "real":
		return model.TypeFloat
	case "decimal", "numeric":
		return model.TypeDecimal
	case "boolean", "bool":
		return model.TypeBool
	case "date":
		return model.TypeDate
	case "time":
		return model.TypeTime
	case "timestamp", "datetime":
		return model.TypeTimestamp
	case "binary", "bytes":
		return model.TypeBytes
	case "json", "document":
		return model.TypeJSON
	case "uuid":
		return model.TypeUUID
	case "":
		return model.TypeUnknown
	}
	return model.TypeString
}

// object turns a described object into the type the application's structure
// view, DDL generator and comparison expect. Columns make it a table; a
// definition with no columns makes it a view.
func object(ref model.ObjectRef, o plugin.Object) any {
	if len(o.Columns) == 0 && o.Definition != "" {
		return &model.View{Name: ref.Name(), Definition: o.Definition, Comment: o.Comment}
	}
	t := &model.Table{Name: ref.Name(), Comment: o.Comment, RowsEstimate: -1}
	if o.Rows > 0 {
		t.RowsEstimate = o.Rows
	}
	for i, c := range o.Columns {
		t.Columns = append(t.Columns, model.Column{Name: c.Name, Position: i + 1,
			Type: dataType(c), Comment: c.Comment})
	}
	if len(o.PrimaryKey) > 0 {
		t.PrimaryKey = &model.PrimaryKey{Columns: append([]string(nil), o.PrimaryKey...)}
	}
	return t
}

// row turns a line of JSON values into a row. A row longer than its columns is
// cut and a shorter one is padded, because a grid with a ragged row in it is a
// grid that panics.
func row(raw []json.RawMessage, cols []model.ColumnDef) model.Row {
	out := make(model.Row, len(cols))
	for i := range cols {
		if i >= len(raw) {
			break
		}
		out[i] = value(raw[i], cols[i].Type.Class)
	}
	return out
}

// value reads one JSON value as what its column says it is. Numbers are the
// case that matters: JSON has one number type, and a count read as a float
// prints as 1e+06.
func value(raw json.RawMessage, class model.TypeClass) any {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	f, ok := v.(float64)
	if !ok {
		if _, isObject := v.(map[string]any); isObject {
			return model.JSON(raw)
		}
		if _, isArray := v.([]any); isArray {
			return model.JSON(raw)
		}
		return v
	}
	switch class {
	case model.TypeInteger:
		return int64(f)
	case model.TypeDecimal, model.TypeString:
		// A decimal must not become a float: the text as it was written is
		// what keeps every digit.
		return strings.TrimSpace(string(raw))
	}
	if f == float64(int64(f)) && class == model.TypeUnknown {
		return int64(f)
	}
	return f
}
