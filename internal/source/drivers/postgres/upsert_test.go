package postgres

import "testing"

func TestAnUpsertIsWrittenOnConflict(t *testing.T) {
	if got := (dialect{}).UpsertClause([]string{"id"}, []string{"id", "name"}); got != ` ON CONFLICT ("id") DO UPDATE SET "name" = EXCLUDED."name"` {
		t.Errorf("%s", got)
	}
}
