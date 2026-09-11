package sqlscript

import (
	"fmt"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Inserts writes rows as INSERT statements into a table, one a row: the
// shape every SQL dialect shares. The dialect supplies the quoted table and
// column names and writes each value (source.RowScripter).
func Inserts(table string, names []string, cols []model.ColumnDef, rows []model.Row,
	literal func(any, model.DataType) (string, error)) (string, error) {
	head := "INSERT INTO " + table + " (" + strings.Join(names, ", ") + ") VALUES ("
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(head)
		for i, c := range cols {
			if i > 0 {
				b.WriteString(", ")
			}
			var v any
			if i < len(r) {
				v = r[i]
			}
			lit, err := literal(v, c.Type)
			if err != nil {
				return "", fmt.Errorf("column %s: %w", c.Name, err)
			}
			b.WriteString(lit)
		}
		b.WriteString(");\n")
	}
	return b.String(), nil
}
