package sqllex

import "testing"

func TestKnown(t *testing.T) {
	for name, want := range map[string]bool{
		"postgresql": true, "PostgreSQL": true, "mysql": true, "cql": true,
		"clickhouse": true, "ClickHouse": true, "sqlserver": true,
		"sql": false, "": false, "postgre": false,
	} {
		if got := Known(name); got != want {
			t.Errorf("Known(%q) = %v, want %v", name, got, want)
		}
	}
}
