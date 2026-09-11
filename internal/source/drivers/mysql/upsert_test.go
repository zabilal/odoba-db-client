package mysql

import "testing"

func TestAnUpsertIsWrittenOnDuplicateKey(t *testing.T) {
	if got := (dialect{}).UpsertClause([]string{"id"}, []string{"id", "name", "n"}); got != " ON DUPLICATE KEY UPDATE `name` = VALUES(`name`), `n` = VALUES(`n`)" {
		t.Errorf("%s", got)
	}
	if got := (dialect{}).UpsertClause([]string{"id"}, []string{"id"}); got != " ON DUPLICATE KEY UPDATE `id` = `id`" {
		t.Errorf("a row of its key alone left as it is: %s", got)
	}
}
