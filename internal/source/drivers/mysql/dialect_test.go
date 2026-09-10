package mysql

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

func TestClassify(t *testing.T) {
	for stmt, want := range map[string]source.Access{
		"SELECT * FROM t":                       source.AccessRead,
		"select * from t for update":            source.AccessRead,
		"SELECT * FROM t INTO OUTFILE '/tmp/x'": source.AccessAdmin,
		"SELECT load_file('/etc/passwd')":       source.AccessAdmin,
		"WITH x AS (SELECT 1) SELECT * FROM x":  source.AccessRead,
		"WITH x AS (SELECT 1) DELETE FROM t":    source.AccessWrite,
		"SHOW TABLES":                           source.AccessRead,
		"DESCRIBE t":                            source.AccessRead,
		"USE sales":                             source.AccessRead,
		"START TRANSACTION":                     source.AccessRead,
		"START REPLICA":                         source.AccessAdmin,
		"SET @x = 5":                            source.AccessRead,
		"SET NAMES utf8mb4":                     source.AccessRead,
		"SET SESSION transaction_read_only = 0": source.AccessAdmin,
		"SET @@session.tx_read_only = 0":        source.AccessAdmin,
		"INSERT INTO t VALUES (1)":              source.AccessWrite,
		"CALL refresh_totals()":                 source.AccessWrite,
		"TRUNCATE TABLE t":                      source.AccessDDL,
		"CREATE TABLE t (a INT)":                source.AccessDDL,
		"GRANT ALL ON *.* TO x":                 source.AccessAdmin,
		"KILL 42":                               source.AccessAdmin,
		"ANALYZE TABLE t":                       source.AccessAdmin,
		"ANALYZE DELETE FROM t":                 source.AccessWrite,
		"ANALYZE SELECT * FROM t":               source.AccessRead,
		"SELECT 'DELETE FROM t'":                source.AccessRead,
		"# DROP TABLE t\nSELECT 1":              source.AccessRead,
		"SELECT 1; DELETE FROM t":               source.AccessWrite,
		"frobnicate everything":                 source.AccessWrite,
	} {
		if got := classify(stmt); got != want {
			t.Errorf("%q: %v, want %v", stmt, got, want)
		}
	}
}

func TestQuotingAndBrowseSQL(t *testing.T) {
	var d dialect
	if got := d.QuoteIdentifier("we`ird"); got != "`we``ird`" {
		t.Errorf("%s", got)
	}
	ref := model.NewRef(model.KindTable, "sales", "orders")
	st, err := d.BuildBrowse(ref, source.BrowseOptions{
		Sorts:   []source.Sort{{Column: "total", Descending: true}},
		Filters: []source.Filter{{Column: "note", Op: source.OpContains, Values: []any{"50%_off!"}}, {Column: "code", Op: source.OpRegex, Values: []any{"^A"}}},
		Limit:   10, Offset: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "SELECT * FROM `sales`.`orders` WHERE CAST(`note` AS CHAR) LIKE ? ESCAPE '!' AND CAST(`code` AS CHAR) REGEXP ? " +
		"ORDER BY `total` IS NULL ASC, `total` DESC LIMIT ? OFFSET ?"
	if st.SQL != want {
		t.Errorf("\n got %s\nwant %s", st.SQL, want)
	}
	if st.Args[0] != "%50!%!_off!!%" {
		t.Errorf("escaped needle %q", st.Args[0])
	}
}

func TestDataTypes(t *testing.T) {
	for name, want := range map[string]model.TypeClass{
		"INT": model.TypeInteger, "UNSIGNED BIGINT": model.TypeInteger, "DECIMAL": model.TypeDecimal,
		"VARCHAR": model.TypeString, "JSON": model.TypeJSON, "DATETIME": model.TypeTimestamp,
		"BLOB": model.TypeBytes, "ENUM": model.TypeEnum, "WHATEVER": model.TypeUnknown,
	} {
		if got := dataType(name).Class; got != want {
			t.Errorf("%s: %v, want %v", name, got, want)
		}
	}
	if !dataType("TIMESTAMP").TimeZone || dataType("DATETIME").TimeZone {
		t.Error("TIMESTAMP is an instant, DATETIME a wall-clock time")
	}
	if v := normalize([]byte("12345678901234567890.1234567890"), model.DataType{Class: model.TypeDecimal}); v != model.Decimal("12345678901234567890.1234567890") {
		t.Errorf("decimal %v", v)
	}
	if v := normalize(uint64(1<<63), model.DataType{Class: model.TypeInteger}); v != model.Decimal("9223372036854775808") {
		t.Errorf("unsigned past int64: %v", v)
	}
}

func TestSplitScriptKeepsRoutineBodies(t *testing.T) {
	var d dialect
	got := d.SplitScript("DELIMITER //\nCREATE PROCEDURE p() BEGIN SELECT 1; SELECT 2; END //\nDELIMITER ;\nCALL p();")
	if len(got) != 2 || !strings.Contains(got[0].Text, "SELECT 2; END") {
		t.Errorf("%+v", got)
	}
}
