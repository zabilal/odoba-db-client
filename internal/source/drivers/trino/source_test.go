package trino

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	tr "github.com/trinodb/trino-go-client/trino"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// What needs no Trino to be true: what a connection's settings become, what a
// statement is, what a type is, and what is written for the server to run.
// What the server then makes of it is the live suite's to say.

func kindOf(err error) source.ConnectKind {
	var ce *source.ConnectError
	if errors.As(err, &ce) {
		return ce.Kind
	}
	return 255 // no kind at all
}

func hintOf(err error) string {
	var ce *source.ConnectError
	if errors.As(err, &ce) {
		return ce.Hint
	}
	return ""
}

func TestTheDescriptorIsWhatTheFormNeeds(t *testing.T) {
	d := Driver{}.Describe()
	if d.ID != "trino" || d.Name != "Trino" || d.Paradigm != model.ParadigmRelational || d.DefaultPort != 8080 ||
		!reflect.DeepEqual(d.URLSchemes, []string{"trino"}) {
		t.Errorf("described as %+v", d)
	}
	fields := map[string]source.Field{}
	for _, f := range d.Fields {
		fields[f.Key] = f
	}
	if !fields["host"].Required || fields["port"].Default != "8080" || !fields["user"].Required {
		t.Errorf("the form asks for %+v", d.Fields)
	}
	// The catalog is what every other driver calls the database, so that a
	// connection string's path lands in it.
	if c := fields["database"]; c.Label != "Catalog" || c.Required {
		t.Errorf("the catalog is %+v", c)
	}
	if p := fields["password"]; !p.Secret || p.Kind != source.FieldPassword {
		t.Errorf("the password is %+v", p)
	}
	got, err := source.Lookup(driverID)
	if err != nil || got.Describe().ID != driverID {
		t.Errorf("it is not registered: %v", err)
	}
}

// What a connection's settings become, before anything is dialled.
func TestWhatAConnectionBecomes(t *testing.T) {
	secret := func(string) (string, error) { return "s3cr@t", nil }
	none := func(string) (string, error) { return "", nil }

	c, err := config(source.ConnectionConfig{Host: " trino.example ", User: " ada ", Database: " hive ",
		Params: map[string]string{"schema": " web "}, Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	// Nothing said about encryption is encrypted and verified (NFR-S3), on
	// the port Trino is installed on, and the password is escaped into the
	// address rather than written into it.
	if c.ServerURI != "https://ada:s3cr%40t@trino.example:8080" || c.SSLVerification != tr.SSLVerificationFull {
		t.Errorf("it would dial %s, verifying %q", c.ServerURI, c.SSLVerification)
	}
	if c.Catalog != "hive" || c.Schema != "web" || c.Source != "ikigai-db" || c.TimeZone != "UTC" {
		t.Errorf("as %q in %s.%s, in %s", c.Source, c.Catalog, c.Schema, c.TimeZone)
	}

	for mode, want := range map[string]struct{ scheme, verify string }{
		"disable": {"http", ""}, "require": {"https", tr.SSLVerificationNone},
		"verify-ca": {"https", tr.SSLVerificationCA}, "verify-full": {"https", tr.SSLVerificationFull},
	} {
		c, err := config(source.ConnectionConfig{Host: "h", Port: 9, User: "u", Secret: none,
			TLS: source.TLSConfig{Mode: mode, CAFile: "ca.pem", CertFile: "c.pem", KeyFile: "k.pem"}})
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		if c.ServerURI != want.scheme+"://u@h:9" || c.SSLVerification != want.verify {
			t.Errorf("%s dials %s, verifying %q", mode, c.ServerURI, c.SSLVerification)
		}
		// Certificates are for an encrypted connection and say nothing to
		// one that is not.
		files := c.SSLCertPath + c.SSLClientCertPath + c.SSLClientKeyPath
		if (mode == "disable") != (files == "") || (mode != "disable" && files != "ca.pemc.pemk.pem") {
			t.Errorf("%s carries certificates %q", mode, files)
		}
	}

	// No host is this machine, as it is for every other driver.
	if c, err := config(source.ConnectionConfig{User: "u", TLS: source.TLSConfig{Mode: "disable"}}); err != nil || c.ServerURI != "http://u@localhost:8080" {
		t.Errorf("with no host: %v, %v", c, err)
	}

	for name, c := range map[string]struct {
		cfg  source.ConnectionConfig
		says string
	}{
		"no user":      {source.ConnectionConfig{Host: "h", User: "  "}, "user name"},
		"a wrong mode": {source.ConnectionConfig{Host: "h", User: "u", TLS: source.TLSConfig{Mode: "sometimes"}}, `"sometimes"`},
		"another name to verify": {source.ConnectionConfig{Host: "h", User: "u",
			TLS: source.TLSConfig{Mode: "verify-full", ServerName: "other"}}, "cannot be given another name"},
		"a password in the clear": {source.ConnectionConfig{Host: "h", User: "u", Secret: secret,
			TLS: source.TLSConfig{Mode: "disable"}}, "only over an encrypted connection"},
		"a password that cannot be read": {source.ConnectionConfig{Host: "h", User: "u",
			Secret: func(string) (string, error) { return "", errors.New("keychain locked") }}, "password"},
	} {
		_, err := config(c.cfg)
		if kindOf(err) != source.ConnectConfig || !strings.Contains(hintOf(err), c.says) {
			t.Errorf("%s: %v (%q), want a configuration error mentioning %q", name, err, hintOf(err), c.says)
		}
	}
	// And Open says the same without dialling anything.
	if _, err := (Driver{}).Open(context.Background(), source.ConnectionConfig{Host: "h"}); kindOf(err) != source.ConnectConfig {
		t.Errorf("Open with no user: %v", err)
	}
}

func TestAFailureToConnectIsCalledWhatItIs(t *testing.T) {
	server := func(name, msg string) error {
		return &tr.ErrQueryFailed{StatusCode: 200, Reason: &tr.ErrTrino{ErrorName: name, Message: msg}}
	}
	for name, c := range map[string]struct {
		err  error
		kind source.ConnectKind
		says string
	}{
		"no such catalog":        {server("CATALOG_NOT_FOUND", "Catalog 'x' not found"), source.ConnectNoDatabase, "catalog or schema"},
		"no such schema":         {server("SCHEMA_NOT_FOUND", "x"), source.ConnectNoDatabase, "catalog or schema"},
		"refused, who":           {&tr.ErrQueryFailed{StatusCode: 401, Reason: errors.New("Unauthorized")}, source.ConnectAuth, "user name or password"},
		"refused, forbidden":     {&tr.ErrQueryFailed{StatusCode: 403, Reason: errors.New("no")}, source.ConnectAuth, "user name or password"},
		"still starting":         {&tr.ErrQueryFailed{StatusCode: 503, Reason: errors.New("starting")}, source.ConnectRefused, "still be starting"},
		"a certificate":          {fmt.Errorf("dial: %w", &tls.CertificateVerificationError{Err: errors.New("unknown authority")}), source.ConnectTLS, "encrypted"},
		"not encrypted at all":   {errors.New(`Post "https://h:1/v1/statement": http: server gave HTTP response to HTTPS client`), source.ConnectTLS, "encrypted"},
		"not TLS at all":         {fmt.Errorf("dial: %w", tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}), source.ConnectTLS, "encrypted"},
		"no such host":           {fmt.Errorf("dial: %w", &net.DNSError{Name: "h", IsNotFound: true}), source.ConnectUnreachable, "host name"},
		"nothing listening":      {fmt.Errorf("dial: %w", syscall.ECONNREFUSED), source.ConnectUnreachable, "Nothing is listening"},
		"no answer":              {context.DeadlineExceeded, source.ConnectUnreachable, "in time"},
		"no answer, on the wire": {fmt.Errorf("read: %w", &net.OpError{Op: "read", Err: timeout{}}), source.ConnectUnreachable, "in time"},
		"something the server said": {server("PERMISSION_DENIED", "Access Denied: Cannot select\nat io.trino"), source.ConnectUnknown,
			"Access Denied: Cannot select"},
		"something else": {errors.New("odd"), source.ConnectUnknown, "The connection failed."},
	} {
		got := classifyConnectError(c.err)
		if kindOf(got) != c.kind || !strings.Contains(hintOf(got), c.says) || !errors.Is(got, c.err) {
			t.Errorf("%s: %v (%q), want kind %v mentioning %q", name, got, hintOf(got), c.kind, c.says)
		}
	}
	// What is already said is not said again.
	already := &source.ConnectError{Kind: source.ConnectConfig, Hint: "x", Err: errors.New("y")}
	if got := classifyConnectError(fmt.Errorf("wrapped: %w", already)); kindOf(got) != source.ConnectConfig || hintOf(got) != "x" {
		t.Errorf("an error already classified became %v", got)
	}
}

func TestAFailedStatementSaysWhatTheServerSaid(t *testing.T) {
	said := func(e tr.ErrTrino) error { return &tr.ErrQueryFailed{StatusCode: 200, Reason: &e} }
	byValue := &tr.ErrQueryFailed{StatusCode: 200, Reason: tr.ErrTrino{ErrorName: "DIVISION_BY_ZERO", Message: "Division by zero"}}
	live := context.Background()
	stopped, cancel := context.WithCancel(live)
	cancel()

	if statementError(nil, live) != nil {
		t.Error("no error became one")
	}
	var se *source.StatementError
	err := statementError(said(tr.ErrTrino{ErrorName: "SYNTAX_ERROR",
		Message: "line 1:1: mismatched input 'SELEC'. Expecting: 'ALTER', 'ANALYZE'\nmore"}), live)
	if !errors.As(err, &se) || se.Message.Code != "SYNTAX_ERROR" || se.Message.Level != source.MessageError ||
		se.Message.Text != "line 1:1: mismatched input 'SELEC'" {
		t.Errorf("a syntax error: %+v", se)
	}
	if err := statementError(byValue, live); !errors.As(err, &se) || se.Message.Code != "DIVISION_BY_ZERO" {
		t.Errorf("the server's error, held by value: %v", err)
	}
	if err := statementError(errors.New("connection reset"), live); !errors.As(err, &se) || se.Message.Text != "connection reset" || se.Message.Code != "" {
		t.Errorf("an error that is not the server's: %v", err)
	}
	// A stop is a stop, whoever noticed: the context, the client or the
	// server.
	for name, err := range map[string]error{
		"the context's own":         context.Canceled,
		"a deadline":                context.DeadlineExceeded,
		"the server, after a stop":  said(tr.ErrTrino{ErrorName: "GENERIC_INTERNAL_ERROR", Message: "x"}),
		"something else, after one": errors.New("EOF"),
	} {
		got := statementError(err, stopped)
		if !errors.Is(got, context.Canceled) && !errors.Is(got, context.DeadlineExceeded) || errors.As(got, &se) {
			t.Errorf("%s was reported as %v", name, got)
		}
	}
	if got := statementError(said(tr.ErrTrino{ErrorName: "USER_CANCELED", Message: "Query was canceled"}), live); got != context.Canceled {
		t.Errorf("a statement the server says was cancelled: %v", got)
	}
	if got := statementError(context.Canceled, nil); got != context.Canceled {
		t.Errorf("with no context to ask: %v", got)
	}

	// Where in a statement the server stopped reading.
	at := said(tr.ErrTrino{ErrorName: "TABLE_NOT_FOUND", Message: "x", ErrorLocation: tr.ErrorLocation{LineNumber: 2, ColumnNumber: 6}})
	if err := positioned(at, live, "SELECT é\nFROM nosuch"); !errors.As(err, &se) || se.Message.Position != 15 { // nine characters, of ten bytes, and six more
		t.Errorf("line 2, column 6 is at %d", se.Message.Position)
	}
	if err := positioned(errors.New("odd"), live, "SELECT 1"); !errors.As(err, &se) || se.Message.Position != 0 {
		t.Errorf("an error with no place is at %d", se.Message.Position)
	}
	if got := positioned(at, stopped, "x"); !errors.Is(got, context.Canceled) {
		t.Errorf("a stop with a place: %v", got)
	}
	for _, c := range []struct {
		text      string
		line, col int
		want      int
	}{
		{"abc", 1, 1, 1}, {"abc", 1, 3, 3}, {"a\nbc", 2, 2, 4}, {"a\n\nb", 3, 1, 4},
		{"abc", 0, 0, 0}, {"abc", 1, 0, 0}, {"abc", 0, 1, 0}, {"abc", 3, 1, 0}, // nowhere, or past the end
		{"a\nbc", 2, 0, 0}, {"a\nbc", 0, 2, 0},
		{"éé\nb", 2, 1, 4}, // counted in characters, as an editor counts
	} {
		if got := offsetOf(c.text, c.line, c.col); got != c.want {
			t.Errorf("offsetOf(%q, %d, %d) = %d, want %d", c.text, c.line, c.col, got, c.want)
		}
	}
	for msg, want := range map[string]string{
		"plain":                          "plain",
		"  spaced  ":                     "spaced",
		"first\nsecond":                  "first",
		"bad input. Expecting: 'A', 'B'": "bad input",
		"":                               "",
	} {
		if got := firstLine(msg); got != want {
			t.Errorf("firstLine(%q) = %q, want %q", msg, got, want)
		}
	}
}

func TestWhatATypeIs(t *testing.T) {
	type want struct {
		class     model.TypeClass
		length    int64
		precision int32
		scale     int32
		zone      bool
	}
	for native, w := range map[string]want{
		"boolean": {model.TypeBool, -1, 0, 0, false}, "BOOLEAN": {model.TypeBool, -1, 0, 0, false},
		"tinyint": {model.TypeInteger, -1, 0, 0, false}, "smallint": {model.TypeInteger, -1, 0, 0, false},
		"integer": {model.TypeInteger, -1, 0, 0, false}, "bigint": {model.TypeInteger, -1, 0, 0, false},
		"real": {model.TypeFloat, -1, 0, 0, false}, "double": {model.TypeFloat, -1, 0, 0, false},
		"decimal(12,2)": {model.TypeDecimal, -1, 12, 2, false}, "decimal(38, 0)": {model.TypeDecimal, -1, 38, 0, false},
		"DECIMAL": {model.TypeDecimal, -1, 0, 0, false}, "decimal(5)": {model.TypeDecimal, -1, 5, 0, false},
		"varchar": {model.TypeString, -1, 0, 0, false}, "varchar(25)": {model.TypeString, 25, 0, 0, false},
		"char(3)": {model.TypeString, 3, 0, 0, false}, "VARCHAR": {model.TypeString, -1, 0, 0, false},
		"varbinary": {model.TypeBytes, -1, 0, 0, false},
		"date":      {model.TypeDate, -1, 0, 0, false},
		"time(3)":   {model.TypeTime, -1, 3, 0, false}, "time(6) with time zone": {model.TypeTime, -1, 6, 0, true},
		"timestamp(6)":                {model.TypeTimestamp, -1, 6, 0, false},
		"timestamp(3) with time zone": {model.TypeTimestamp, -1, 3, 0, true},
		"TIMESTAMP WITH TIME ZONE":    {model.TypeTimestamp, -1, 0, 0, true},
		"TIMESTAMP":                   {model.TypeTimestamp, -1, 0, 0, false},
		"interval day to second":      {model.TypeInterval, -1, 0, 0, false},
		"INTERVAL YEAR TO MONTH":      {model.TypeInterval, -1, 0, 0, false},
		"uuid":                        {model.TypeUUID, -1, 0, 0, false}, "json": {model.TypeJSON, -1, 0, 0, false},
		"ipaddress":                    {model.TypeNetwork, -1, 0, 0, false},
		"map(varchar, array(integer))": {model.TypeStruct, -1, 0, 0, false},
		"row(a integer, b varchar(3))": {model.TypeStruct, -1, 0, 0, false},
		"Geometry":                     {model.TypeGeometry, -1, 0, 0, false},
		"HyperLogLog":                  {model.TypeUnknown, -1, 0, 0, false}, "": {model.TypeUnknown, -1, 0, 0, false},
		"  bigint  ": {model.TypeInteger, -1, 0, 0, false},
	} {
		got := dataType(native)
		if got.Class != w.class || got.Length != w.length || got.Precision != w.precision || got.Scale != w.scale ||
			got.TimeZone != w.zone || got.Native != strings.TrimSpace(native) {
			t.Errorf("%q is %+v, want %+v", native, got, w)
		}
	}
	// An array is an array of something, however deep.
	a := dataType("array(array(decimal(10,2)))")
	if a.Class != model.TypeArray || a.Element == nil || a.Element.Class != model.TypeArray ||
		a.Element.Element == nil || a.Element.Element.Class != model.TypeDecimal || a.Element.Element.Scale != 2 {
		t.Errorf("an array of arrays is %+v", a)
	}
	if got := dataType("ARRAY(ROW(INTEGER, VARCHAR(1)))"); got.Element == nil || got.Element.Class != model.TypeStruct {
		t.Errorf("an array of rows is %+v", got.Element)
	}
	// Brackets inside brackets close their own, and what follows them is
	// still read.
	if got := dataType("array(timestamp(3) with time zone)"); got.Element == nil || !got.Element.TimeZone || got.Element.Precision != 3 {
		t.Errorf("an array of zoned timestamps is of %+v", got.Element)
	}
	if got := dataType("array"); got.Class != model.TypeArray || got.Element != nil {
		t.Errorf("an array of nothing said is %+v", got)
	}
	// A type left open is read as far as it goes rather than not at all.
	if got := dataType("decimal(10,2"); got.Class != model.TypeDecimal || got.Precision != 10 || got.Scale != 2 {
		t.Errorf("an unclosed type is %+v", got)
	}
	if got := list("a, row(b integer, c varchar), d"); !reflect.DeepEqual(got, []string{"a", "row(b integer, c varchar)", "d"}) {
		t.Errorf("a type's arguments are %q", got)
	}

	for native, want := range map[string]bool{
		"bigint": true, "varchar": true, "double": true, "decimal(3,1)": true, "date": true, "time(3)": true,
		"timestamp(3)": true, "boolean": true, "varbinary": true, "uuid": true, "ipaddress": true,
		"interval day to second": true,
		"json":                   false, "map(varchar,integer)": false, "row(a integer)": false, "array(integer)": false,
		"geometry": false, "hyperloglog": false,
	} {
		if orderable(dataType(native)) != want {
			t.Errorf("orderable(%s) = %v", native, !want)
		}
	}
}

func TestWhatAStatementIs(t *testing.T) {
	for stmt, want := range map[string]source.Access{
		"SELECT 1":                                   source.AccessRead,
		"  with x as (select 1) select * from x":     source.AccessRead,
		"SHOW CATALOGS":                              source.AccessRead,
		"DESCRIBE tpch.tiny.nation":                  source.AccessRead,
		"DESC nation":                                source.AccessRead,
		"VALUES (1), (2)":                            source.AccessRead,
		"TABLE nation":                               source.AccessRead,
		"USE tpch.tiny":                              source.AccessRead,
		"SET SESSION x = 1":                          source.AccessRead,
		"RESET SESSION x":                            source.AccessRead,
		"START TRANSACTION":                          source.AccessRead,
		"COMMIT":                                     source.AccessRead,
		"ROLLBACK":                                   source.AccessRead,
		"PREPARE q FROM DELETE FROM t":               source.AccessRead, // names it; runs nothing
		"DEALLOCATE PREPARE q":                       source.AccessRead,
		"EXPLAIN DELETE FROM t":                      source.AccessRead, // a plan is read
		"EXPLAIN ANALYZE SELECT 1":                   source.AccessRead,
		"EXPLAIN ANALYZE VERBOSE SELECT * FROM t":    source.AccessRead,
		"EXPLAIN ANALYZE DELETE FROM t":              source.AccessWrite, // and an analysed one is run
		"EXPLAIN ANALYZE CREATE TABLE t AS SELECT 1": source.AccessDDL,
		"EXPLAIN ANALYZE CALL p()":                   source.AccessAdmin,
		"EXPLAIN ANALYZE MERGE INTO t USING s ON true WHEN MATCHED THEN DELETE": source.AccessWrite,
		"EXPLAIN ANALYZE INSERT INTO t SELECT comment FROM u":                   source.AccessDDL, // the worst word in it, wherever it is
		"DELETE FROM t WHERE a = 1; DROP TABLE t":                               source.AccessDDL, // and the worst statement, wherever it is
		"-- a note\nSELECT 1":      source.AccessRead,
		"SELECT 'delete from t'":   source.AccessRead,
		"":                         source.AccessRead,
		"INSERT INTO t VALUES (1)": source.AccessWrite,
		"DELETE FROM t":            source.AccessWrite,
		"UPDATE t SET a = 1":       source.AccessWrite,
		"MERGE INTO t USING s ON true WHEN MATCHED THEN DELETE": source.AccessWrite,
		"TRUNCATE TABLE t":                    source.AccessWrite,
		"EXECUTE q":                           source.AccessWrite, // runs what this cannot see
		"FROBNICATE t":                        source.AccessWrite, // and what it does not know
		"CREATE TABLE t (a bigint)":           source.AccessDDL,
		"DROP SCHEMA s":                       source.AccessDDL,
		"ALTER TABLE t RENAME TO u":           source.AccessDDL,
		"COMMENT ON TABLE t IS 'x'":           source.AccessDDL,
		"REFRESH MATERIALIZED VIEW v":         source.AccessDDL,
		"ANALYZE t":                           source.AccessDDL,
		"CALL system.runtime.kill_query('x')": source.AccessAdmin,
		"GRANT SELECT ON t TO u":              source.AccessAdmin,
		"REVOKE SELECT ON t FROM u":           source.AccessAdmin,
		"DENY SELECT ON t TO u":               source.AccessAdmin,
		"SELECT 1; DROP TABLE t; SELECT 2":    source.AccessDDL, // the worst of a script
		"SELECT 1; CALL p(); DELETE FROM t":   source.AccessAdmin,
	} {
		if got := classify(stmt); got != want {
			t.Errorf("%q is %v, want %v", stmt, got, want)
		}
		if d := (dialect{}); d.Classify(stmt) != want {
			t.Errorf("the dialect says otherwise of %q", stmt)
		}
	}
	for stmt, want := range map[string]bool{
		"SELECT 1": true, "with x as (select 1) select 1": true, "SHOW TABLES": true, "DESCRIBE t": true, "desc t": true,
		"EXPLAIN SELECT 1": true, "VALUES 1": true, "TABLE t": true, "EXECUTE q": true,
		"INSERT INTO t VALUES (1)": false, "USE a.b": false, "CREATE TABLE t (a int)": false, "": false, "-- nothing": false,
	} {
		if returnsRows(stmt) != want {
			t.Errorf("returnsRows(%q) = %v", stmt, !want)
		}
	}
	if u := unboundedIn("SELECT 1; DELETE FROM t"); u == nil {
		t.Error("a delete of every row, in a script, was not noticed")
	}
	if u := unboundedIn("DELETE FROM t WHERE a = 1; SELECT 'DELETE FROM t'"); u != nil {
		t.Errorf("a bounded delete was called unbounded: %v", u)
	}
	stmts := (dialect{}).SplitScript("SELECT ';'; -- a ; note\nSELECT 2")
	if len(stmts) != 2 || stmts[0].Text != "SELECT ';'" || !strings.HasSuffix(stmts[1].Text, "SELECT 2") {
		t.Errorf("a script is split into %+v", stmts)
	}
}

func TestNamesAreQuoted(t *testing.T) {
	d := dialect{}
	for name, want := range map[string]string{"a": `"a"`, `a"b`: `"a""b"`, "": `""`, "a.b": `"a.b"`, "Tab le": `"Tab le"`} {
		if got := d.QuoteIdentifier(name); got != want {
			t.Errorf("%q is written %s, want %s", name, got, want)
		}
	}
	if got := d.QualifyRef(table("c", "s", `t"x`)); got != `"c"."s"."t""x"` {
		t.Errorf("a table is named %s", got)
	}
	if got := d.QualifyRef(model.NewRef(model.KindColumn, "c", "s", "t", "col")); got != `"c"."s"."t"` {
		t.Errorf("a column's table is named %s", got)
	}
	if got := d.QualifyRef(model.NewRef(model.KindSchema, "c", "s")); got != `"c"."s"` {
		t.Errorf("a schema is named %s", got)
	}
	if d.Placeholder(1) != "?" || d.Placeholder(7) != "?" {
		t.Error("a placeholder is not a question mark")
	}
}

func table(parts ...string) model.ObjectRef { return model.NewRef(model.KindTable, parts...) }

// What a browse asks the server, in full.
func TestTheStatementABrowseRuns(t *testing.T) {
	d := dialect{}
	ref := table("c", "s", "t")
	when := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	for name, c := range map[string]struct {
		opt  source.BrowseOptions
		sql  string
		args []any
	}{
		"everything": {source.BrowseOptions{},
			`SELECT * FROM "c"."s"."t" LIMIT 1000`, nil},
		"a page": {source.BrowseOptions{Offset: 40, Limit: 20},
			`SELECT * FROM "c"."s"."t" OFFSET 40 LIMIT 20`, nil},
		"some columns": {source.BrowseOptions{Columns: []string{"a", `b"c`}, Limit: 5},
			`SELECT "a", "b""c" FROM "c"."s"."t" LIMIT 5`, nil},
		"sorted": {source.BrowseOptions{Limit: 5, Sorts: []source.Sort{{Column: "a"}, {Column: "b", Descending: true, NullsFirst: true}}},
			`SELECT * FROM "c"."s"."t" ORDER BY "a" NULLS LAST, "b" DESC NULLS FIRST LIMIT 5`, nil},
		"equal": {source.BrowseOptions{Limit: 5, Filters: []source.Filter{{Column: "a", Op: source.OpEqual, Values: []any{"x"}}}},
			`SELECT * FROM "c"."s"."t" WHERE "a" = ? LIMIT 5`, []any{"x"}},
		"equal to nothing": {source.BrowseOptions{Limit: 5, Filters: []source.Filter{{Column: "a", Op: source.OpEqual, Values: []any{nil}}}},
			`SELECT * FROM "c"."s"."t" WHERE "a" IS NULL LIMIT 5`, nil},
		"not equal to nothing": {source.BrowseOptions{Limit: 5, Filters: []source.Filter{{Column: "a", Op: source.OpNotEqual, Values: []any{nil}}}},
			`SELECT * FROM "c"."s"."t" WHERE "a" IS NOT NULL LIMIT 5`, nil},
		"compared": {source.BrowseOptions{Limit: 5, Filters: []source.Filter{
			{Column: "a", Op: source.OpNotEqual, Values: []any{int64(1)}}, {Column: "a", Op: source.OpLess, Values: []any{int64(2)}},
			{Column: "a", Op: source.OpLessEqual, Values: []any{int64(3)}}, {Column: "a", Op: source.OpGreater, Values: []any{int64(4)}},
			{Column: "a", Op: source.OpGreaterEqual, Values: []any{int64(5)}}}},
			`SELECT * FROM "c"."s"."t" WHERE "a" <> ? AND "a" < ? AND "a" <= ? AND "a" > ? AND "a" >= ? LIMIT 5`,
			[]any{int64(1), int64(2), int64(3), int64(4), int64(5)}},
		"like": {source.BrowseOptions{Limit: 5, Filters: []source.Filter{{Column: "a", Op: source.OpLike, Values: []any{"x%"}}}},
			`SELECT * FROM "c"."s"."t" WHERE CAST("a" AS varchar) LIKE ? ESCAPE '\' LIMIT 5`, []any{"x%"}},
		"not like": {source.BrowseOptions{Limit: 5, Filters: []source.Filter{{Column: "a", Op: source.OpNotLike, Values: []any{7}}}},
			`SELECT * FROM "c"."s"."t" WHERE CAST("a" AS varchar) NOT LIKE ? ESCAPE '\' LIMIT 5`, []any{"7"}},
		"contains": {source.BrowseOptions{Limit: 5, Filters: []source.Filter{{Column: "a", Op: source.OpContains, Values: []any{`50%_Of\X`}}}},
			`SELECT * FROM "c"."s"."t" WHERE lower(CAST("a" AS varchar)) LIKE ? ESCAPE '\' LIMIT 5`, []any{`%50\%\_of\\x%`}},
		"a regular expression": {source.BrowseOptions{Limit: 5, Filters: []source.Filter{{Column: "a", Op: source.OpRegex, Values: []any{"^x"}}}},
			`SELECT * FROM "c"."s"."t" WHERE regexp_like(CAST("a" AS varchar), ?) LIMIT 5`, []any{"^x"}},
		"null tests": {source.BrowseOptions{Limit: 5, Filters: []source.Filter{{Column: "a", Op: source.OpIsNull}, {Column: "b", Op: source.OpIsNotNull}}},
			`SELECT * FROM "c"."s"."t" WHERE "a" IS NULL AND "b" IS NOT NULL LIMIT 5`, nil},
		"between": {source.BrowseOptions{Limit: 5, Filters: []source.Filter{{Column: "a", Op: source.OpBetween, Values: []any{when, when}}}},
			`SELECT * FROM "c"."s"."t" WHERE "a" BETWEEN ? AND ? LIMIT 5`, []any{when, when}},
		"one of": {source.BrowseOptions{Limit: 5, Filters: []source.Filter{{Column: "a", Op: source.OpIn, Values: []any{"x", "y"}}}},
			`SELECT * FROM "c"."s"."t" WHERE "a" IN (?, ?) LIMIT 5`, []any{"x", "y"}},
		"one of, or nothing": {source.BrowseOptions{Limit: 5, Filters: []source.Filter{{Column: "a", Op: source.OpIn, Values: []any{"x", nil}}}},
			`SELECT * FROM "c"."s"."t" WHERE ("a" IN (?) OR "a" IS NULL) LIMIT 5`, []any{"x"}},
		"only nothing": {source.BrowseOptions{Limit: 5, Filters: []source.Filter{{Column: "a", Op: source.OpIn, Values: []any{nil}}}},
			`SELECT * FROM "c"."s"."t" WHERE "a" IS NULL LIMIT 5`, nil},
		"one of none": {source.BrowseOptions{Limit: 5, Filters: []source.Filter{{Column: "a", Op: source.OpIn}}},
			`SELECT * FROM "c"."s"."t" WHERE 1 = 0 LIMIT 5`, nil},
		"none of": {source.BrowseOptions{Limit: 5, Filters: []source.Filter{{Column: "a", Op: source.OpNotIn, Values: []any{"x"}}}},
			`SELECT * FROM "c"."s"."t" WHERE ("a" NOT IN (?) OR "a" IS NULL) LIMIT 5`, []any{"x"}},
		"none of, nor nothing": {source.BrowseOptions{Limit: 5, Filters: []source.Filter{{Column: "a", Op: source.OpNotIn, Values: []any{"x", nil}}}},
			`SELECT * FROM "c"."s"."t" WHERE "a" NOT IN (?) LIMIT 5`, []any{"x"}},
		"not nothing": {source.BrowseOptions{Limit: 5, Filters: []source.Filter{{Column: "a", Op: source.OpNotIn, Values: []any{nil}}}},
			`SELECT * FROM "c"."s"."t" WHERE "a" IS NOT NULL LIMIT 5`, nil},
		"none of none": {source.BrowseOptions{Limit: 5, Filters: []source.Filter{{Column: "a", Op: source.OpNotIn}}},
			`SELECT * FROM "c"."s"."t" WHERE 1 = 1 LIMIT 5`, nil},
		"negated": {source.BrowseOptions{Limit: 5, Filters: []source.Filter{{Column: "a", Op: source.OpEqual, Values: []any{"x"}, Negate: true}}},
			`SELECT * FROM "c"."s"."t" WHERE NOT ("a" = ?) LIMIT 5`, []any{"x"}},
		// An exact number is passed as one, or it would be compared as text.
		"an exact number": {source.BrowseOptions{Limit: 5, Filters: []source.Filter{{Column: "a", Op: source.OpEqual, Values: []any{model.Decimal("1.50")}}}},
			`SELECT * FROM "c"."s"."t" WHERE "a" = ? LIMIT 5`, []any{tr.Numeric("1.50")}},
		"a document": {source.BrowseOptions{Limit: 5, Filters: []source.Filter{{Column: "a", Op: source.OpEqual, Values: []any{model.JSON(`{"a":1}`)}}}},
			`SELECT * FROM "c"."s"."t" WHERE "a" = ? LIMIT 5`, []any{`{"a":1}`}},
		"a condition": {source.BrowseOptions{Limit: 5, Where: "a > 1 -- more",
			Filters: []source.Filter{{Column: "b", Op: source.OpIsNull}}, Sorts: []source.Sort{{Column: "a"}}, Offset: 2},
			"SELECT * FROM \"c\".\"s\".\"t\" WHERE \"b\" IS NULL AND (\na > 1 -- more\n) ORDER BY \"a\" NULLS LAST OFFSET 2 LIMIT 5", nil},
	} {
		got, err := d.BuildBrowse(ref, c.opt)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got.SQL != c.sql || !reflect.DeepEqual(got.Args, c.args) {
			t.Errorf("%s:\n got %s %#v\nwant %s %#v", name, got.SQL, got.Args, c.sql, c.args)
		}
	}

	for name, c := range map[string]struct {
		ref  model.ObjectRef
		opt  source.BrowseOptions
		says string
	}{
		"a schema":                  {model.NewRef(model.KindSchema, "c", "s"), source.BrowseOptions{}, "not browsable"},
		"a table short of a name":   {table("c", "t"), source.BrowseOptions{}, "not browsable"},
		"a table with one too many": {table("c", "s", "t", "u"), source.BrowseOptions{}, "not browsable"},
		"following":                 {ref, source.BrowseOptions{Follow: true}, "stream sources"},
		"seeking":                   {ref, source.BrowseOptions{Seek: &source.Seek{}}, "stream sources"},
		"a filter with no column":   {ref, source.BrowseOptions{Filters: []source.Filter{{Op: source.OpIsNull}}}, "needs a column"},
		"an operator nobody knows":  {ref, source.BrowseOptions{Filters: []source.Filter{{Column: "a", Op: "sounds like"}}}, "unsupported filter operator"},
		"ordered against nothing":   {ref, source.BrowseOptions{Filters: []source.Filter{{Column: "a", Op: source.OpGreaterEqual, Values: []any{nil}}}}, "with NULL"},
		"equal to two":              {ref, source.BrowseOptions{Filters: []source.Filter{{Column: "a", Op: source.OpEqual, Values: []any{1, 2}}}}, "takes 1 value(s), got 2"},
		"like none":                 {ref, source.BrowseOptions{Filters: []source.Filter{{Column: "a", Op: source.OpLike}}}, "takes 1 value(s), got 0"},
		"contains two":              {ref, source.BrowseOptions{Filters: []source.Filter{{Column: "a", Op: source.OpContains, Values: []any{1, 2}}}}, "takes 1 value(s), got 2"},
		"an expression of none":     {ref, source.BrowseOptions{Filters: []source.Filter{{Column: "a", Op: source.OpRegex}}}, "takes 1 value(s), got 0"},
		"null of something":         {ref, source.BrowseOptions{Filters: []source.Filter{{Column: "a", Op: source.OpIsNull, Values: []any{1}}}}, "takes 0 value(s), got 1"},
		"not null of something":     {ref, source.BrowseOptions{Filters: []source.Filter{{Column: "a", Op: source.OpIsNotNull, Values: []any{1}}}}, "takes 0 value(s), got 1"},
		"between one":               {ref, source.BrowseOptions{Filters: []source.Filter{{Column: "a", Op: source.OpBetween, Values: []any{1}}}}, "takes 2 value(s), got 1"},
		"two conditions":            {ref, source.BrowseOptions{Where: "1 = 1; DELETE FROM t"}, "trino:"},
		"a condition left open":     {ref, source.BrowseOptions{Where: "(a = 1"}, "trino:"},
	} {
		got, err := d.BuildBrowse(c.ref, c.opt)
		if err == nil {
			t.Errorf("%s became %s", name, got.SQL)
		} else if !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v, want it to mention %q", name, err, c.says)
		}
	}
}

func TestTheStatementAPicklistRuns(t *testing.T) {
	d := dialect{}
	got, err := d.buildDistinct(table("c", "s", "t"), `a"b`, source.BrowseOptions{
		Filters: []source.Filter{{Column: "x", Op: source.OpEqual, Values: []any{"v"}}}, Where: "y > 1"}, 10)
	want := "SELECT \"a\"\"b\", count(*) FROM \"c\".\"s\".\"t\" WHERE \"x\" = ? AND (\ny > 1\n) " +
		`GROUP BY "a""b" ORDER BY count(*) DESC, "a""b" NULLS LAST LIMIT 10`
	if err != nil || got.SQL != want || !reflect.DeepEqual(got.Args, []any{"v"}) {
		t.Errorf("\n got %s %v, %v\nwant %s", got.SQL, got.Args, err, want)
	}
	for name, c := range map[string]struct {
		ref   model.ObjectRef
		opt   source.BrowseOptions
		limit int
	}{
		"of a schema":        {model.NewRef(model.KindSchema, "c", "s"), source.BrowseOptions{}, 10},
		"with no limit":      {table("c", "s", "t"), source.BrowseOptions{}, 0},
		"under a bad filter": {table("c", "s", "t"), source.BrowseOptions{Filters: []source.Filter{{Op: source.OpIsNull}}}, 10},
	} {
		if got, err := d.buildDistinct(c.ref, "a", c.opt, c.limit); err == nil {
			t.Errorf("a picklist %s became %s", name, got.SQL)
		}
	}
}

// What breaks a tie: every column with an order, after what was asked for and
// without saying any of it twice.
func TestWhatBreaksATie(t *testing.T) {
	cols := []model.Column{
		{Name: "id", Type: dataType("bigint")}, {Name: "doc", Type: dataType("json")},
		{Name: "name", Type: dataType("varchar")}, {Name: "tags", Type: dataType("array(varchar)")},
		{Name: "Name", Type: dataType("varchar")},
	}
	got := tiebreak([]source.Sort{{Column: "name", Descending: true}}, cols)
	want := []source.Sort{{Column: "name", Descending: true}, {Column: "id"}, {Column: "Name"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("after a sort by name: %+v", got)
	}
	if got := tiebreak(nil, cols); len(got) != 3 || got[0].Column != "id" {
		t.Errorf("with no sort: %+v", got)
	}
	// What somebody asked for is theirs: with room after it, what is added
	// goes somewhere else.
	asked := make([]source.Sort, 1, 8)
	asked[0] = source.Sort{Column: "id"}
	if tiebreak(asked, cols); asked[:2][1].Column != "" {
		t.Errorf("the sort somebody asked for was written after: %+v", asked[:4])
	}
}

func TestAValueIsNarrowedToTheModels(t *testing.T) {
	utc := time.Date(2026, 3, 4, 5, 6, 7, 123456000, time.UTC)
	lagos := time.FixedZone("WAT", 3600)
	var nilPointer *int32
	seven := int32(7)
	for name, c := range map[string]struct {
		in   any
		typ  string
		want any
	}{
		"nothing":                      {nil, "bigint", nil},
		"a boolean":                    {true, "boolean", true},
		"a whole number":               {int64(9007199254740993), "bigint", int64(9007199254740993)},
		"a fraction":                   {1.5, "double", 1.5},
		"bytes":                        {[]byte("ab"), "varbinary", []byte("ab")},
		"text":                         {"x", "varchar", "x"},
		"an exact number":              {"12.340", "decimal(5,3)", model.Decimal("12.340")},
		"a document":                   {`{"a":1}`, "json", model.JSON(`{"a":1}`)},
		"a document that is not one":   {`{broken`, "json", `{broken`},
		"text that looks exact":        {"12.340", "varchar", "12.340"},
		"a uuid":                       {"12151fd2-7586-11e9-8f9e-2a86e4085a59", "uuid", "12151fd2-7586-11e9-8f9e-2a86e4085a59"},
		"a time of day":                {time.Date(0, 1, 1, 1, 2, 3, 456000000, time.UTC), "time(3)", "01:02:03.456"},
		"a time of day, on the second": {time.Date(0, 1, 1, 1, 2, 3, 0, time.UTC), "time(3)", "01:02:03"},
		"a moment":                     {utc, "timestamp(6)", utc},
		"a moment somewhere":           {utc.In(lagos), "timestamp(3) with time zone", utc},
		"a date":                       {time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC), "date", time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)},
		"digits, whole":                {json.Number("7"), "", int64(7)},
		"digits, too long":             {json.Number("18446744073709551616"), "", model.Decimal("18446744073709551616")},
		"digits, a fraction":           {json.Number("2.5"), "", 2.5},
		"digits, an exponent":          {json.Number("1e3"), "", 1000.0},
		"digits, too large":            {json.Number("1e999"), "", model.Decimal("1e999")},
		"a list":                       {[]any{json.Number("1"), nil, "x"}, "array(integer)", []any{int64(1), nil, "x"}},
		"a list of exact numbers":      {[]any{"1.50"}, "array(decimal(3,2))", []any{model.Decimal("1.50")}},
		"a list of nothing said":       {[]any{"1.50"}, "", []any{"1.50"}},
		"a map": {map[string]any{"k": json.Number("1"), "in": map[string]any{"f": json.Number("0.5")}}, "map(varchar,integer)",
			map[string]any{"k": int64(1), "in": map[string]any{"f": 0.5}}},
		"a narrower integer":            {int32(7), "integer", int64(7)},
		"a narrower float":              {float32(1.5), "real", 1.5},
		"a pointer":                     {&seven, "integer", int64(7)},
		"a pointer to nothing":          {nilPointer, "integer", nil},
		"a typed list":                  {[]int32{1, 2}, "array(integer)", []any{int64(1), int64(2)}},
		"a typed list of exact numbers": {[]string{"1.5"}, "array(decimal(2,1))", []any{model.Decimal("1.5")}},
		"a typed map":                   {map[int]string{1: "a"}, "map(integer,varchar)", map[string]any{"1": "a"}},
		"something else":                {struct{ A int }{1}, "", "{1}"},
	} {
		if got := normalize(c.in, dataType(c.typ)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %#v (%T), want %#v (%T)", name, got, got, c.want, c.want)
		}
	}
	// A row that is not there is nothing, and not an empty one.
	if got := normalize(tr.Row{}, dataType("row(a integer)")); got != nil {
		t.Errorf("a row that is NULL is %#v", got)
	}
}

func TestParametersAreBoundByPlace(t *testing.T) {
	text, args, err := bindNamed(source.Statement{SQL: "SELECT ?, ?", Args: []any{model.Decimal("1.5"), "x"}})
	if err != nil || text != "SELECT ?, ?" || !reflect.DeepEqual(args, []any{tr.Numeric("1.5"), "x"}) {
		t.Errorf("positional: %s %#v, %v", text, args, err)
	}
	text, args, err = bindNamed(source.Statement{SQL: "SELECT :a, ':a', :b, :a",
		Named: map[string]any{"a": model.Decimal("2"), "b": int64(3)}})
	if err != nil || text != "SELECT ?, ':a', ?, ?" || !reflect.DeepEqual(args, []any{tr.Numeric("2"), int64(3), tr.Numeric("2")}) {
		t.Errorf("named: %s %#v, %v", text, args, err)
	}
	if text, args, err := bindNamed(source.Statement{SQL: "SELECT 1"}); err != nil || text != "SELECT 1" || len(args) != 0 {
		t.Errorf("none: %s %#v, %v", text, args, err)
	}
	if _, _, err := bindNamed(source.Statement{SQL: "SELECT :a, ?", Args: []any{1}, Named: map[string]any{"a": 1}}); err == nil ||
		!strings.Contains(err.Error(), "cannot mix") {
		t.Errorf("both kinds: %v", err)
	}
	if _, _, err := bindNamed(source.Statement{SQL: "SELECT :missing", Named: map[string]any{"a": 1}}); err == nil ||
		!strings.HasPrefix(err.Error(), "trino: ") {
		t.Errorf("a name with no value: %v", err)
	}
}

// What it claims is what it does: it reads and runs statements, and edits
// nothing from a grid.
func TestWhatItClaims(t *testing.T) {
	caps := (&trinoSource{}).Capabilities()
	if caps.Paradigm != model.ParadigmRelational || !caps.Structure.MultipleDatabases || !caps.Structure.Schemas {
		t.Errorf("it is shaped %+v", caps.Structure)
	}
	if q := caps.Query; !q.Supported || q.Language != "trino" || !q.MultiStatement || !q.Parameters || q.Cancel || q.Explain || q.Transactions {
		t.Errorf("of statements it claims %+v", q)
	}
	if d := caps.Data; !d.ServerSort || !d.ServerFilter || !d.DistinctValues || !d.ApproximateCount ||
		d.ExactCount || d.Insert || d.Update || d.Delete || d.BulkLoad {
		t.Errorf("of data it claims %+v", d)
	}
	for _, k := range []model.ObjectKind{model.KindDatabase, model.KindSchema, model.KindFolder, model.KindTable, model.KindView, model.KindColumn} {
		if !caps.Supports(k) {
			t.Errorf("it does not hold a %s", k)
		}
	}
	var src any = &trinoSource{}
	if _, ok := src.(source.Writer); ok {
		t.Error("it implements Writer")
	}
	if _, ok := src.(source.Killer); ok {
		t.Error("it implements Killer, and claims no Cancel")
	}
	if _, ok := src.(source.Countable); ok {
		t.Error("it counts exactly, and claims it does not")
	}
	if (&session{}).Handle() != "" {
		t.Error("a session has a name to be stopped by")
	}
}

func TestABufferedResult(t *testing.T) {
	ctx := context.Background()
	rows := func(n int) *sliceStream {
		s := &sliceStream{cols: []model.ColumnDef{{Name: "n"}}}
		for i := 0; i < n; i++ {
			s.rows = append(s.rows, model.Row{int64(i)})
		}
		return s
	}
	got, truncated, err := buffer(ctx, rows(3), 5)
	if err != nil || truncated {
		t.Fatalf("three of five: %v, %v", truncated, err)
	}
	if r, _ := got.Next(ctx); r[0] != int64(0) || len(got.Columns()) != 1 {
		t.Errorf("its first row is %v", r)
	}
	// Exactly as many as fit is all of them, and one more is not.
	if _, truncated, _ := buffer(ctx, rows(5), 5); truncated {
		t.Error("five of five was called cut short")
	}
	got, truncated, err = buffer(ctx, rows(6), 5)
	n := 0
	for {
		if _, e := got.Next(ctx); e != nil {
			break
		}
		n++
	}
	if err != nil || !truncated || n != 5 {
		t.Errorf("six of five: %d rows, %v, %v", n, truncated, err)
	}
	stopped, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := buffer(stopped, rows(3), 5); !errors.Is(err, context.Canceled) {
		t.Errorf("a stopped read was buffered: %v", err)
	}
	if got.Close() != nil {
		t.Error("closing a buffered result failed")
	}
}

// timeout is a network error that is one.
type timeout struct{}

func (timeout) Error() string   { return "i/o timeout" }
func (timeout) Timeout() bool   { return true }
func (timeout) Temporary() bool { return true }
