package sqlscript

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

const paramScript = `SELECT :id, ':not' AS lit, x::int -- :nor
FROM t WHERE a = :name /* :neither */ AND b = :id AND c = $1 AND d = ?`

func TestNamesAreThoseUsedInOrderOnce(t *testing.T) {
	got := Names(sqllex.PostgreSQL, paramScript)
	if fmt.Sprint(got) != "[id name]" {
		t.Errorf("Names = %q; a string, a comment, a cast or a positional marker is no named parameter", got)
	}
	if got := Names(sqllex.MySQL, "SELECT 1"); got != nil {
		t.Errorf("a statement with none has %q", got)
	}
	if got := Names(sqllex.PostgreSQL, "SELECT 1 /* a comment\n on :hidden lines */, :shown"); fmt.Sprint(got) != "[shown]" {
		t.Errorf("Names = %q; a comment carried to the next line still hides a name", got)
	}
}

func TestBindingReusesANumberedPlaceholder(t *testing.T) {
	dollar := func(n int) string { return "$" + strconv.Itoa(n) }
	sql, args, err := BindNamed(sqllex.PostgreSQL, paramScript, map[string]any{"id": 7, "name": "ann", "unused": 1}, dollar, true)
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT $1, ':not' AS lit, x::int -- :nor
FROM t WHERE a = $2 /* :neither */ AND b = $1 AND c = $1 AND d = ?`
	if sql != want || fmt.Sprint(args) != "[7 ann]" {
		t.Errorf("got %q with %v\nwant %q with [7 ann]", sql, args, want)
	}
}

func TestBindingGivesEachUseItsOwnQuestionMark(t *testing.T) {
	q := func(int) string { return "?" }
	sql, args, err := BindNamed(sqllex.MySQL, "SELECT :a, :b, :a", map[string]any{"a": 1, "b": 2}, q, false)
	if err != nil || sql != "SELECT ?, ?, ?" || fmt.Sprint(args) != "[1 2 1]" {
		t.Errorf("got %q with %v, %v", sql, args, err)
	}
}

func TestANameWithNoValueIsRefused(t *testing.T) {
	q := func(int) string { return "?" }
	_, _, err := BindNamed(sqllex.MySQL, "SELECT :a, :b, :c", map[string]any{"a": 1}, q, false)
	if err == nil || !strings.Contains(err.Error(), ":b") {
		t.Errorf("err %v; want the first name without a value named", err)
	}
}
