package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

func TestAConnectErrorSaysWhatToFixAndHidesThePassword(t *testing.T) {
	inner := errors.New("dial postgres://admin:hunter2@db.example.com:5432/app: connection refused")
	e := &ConnectError{Kind: ConnectUnreachable, Hint: "the server could not be reached", Err: inner}
	msg := e.Error()
	if !strings.HasPrefix(msg, "the server could not be reached: ") {
		t.Errorf("message %q should lead with what to fix", msg)
	}
	if strings.Contains(msg, "hunter2") {
		t.Errorf("the password leaked into %q (NFR-S2)", msg)
	}
	if !errors.Is(e, inner) {
		t.Error("the original error should still be found through it")
	}
	var ce *ConnectError
	if !errors.As(fmt.Errorf("opening: %w", e), &ce) || ce.Kind != ConnectUnreachable {
		t.Error("a wrapped ConnectError should still say what kind it is")
	}
	if got := (&ConnectError{Hint: "the settings are incomplete"}).Error(); got != "the settings are incomplete" {
		t.Errorf("with nothing underneath, the hint alone: %q", got)
	}
}

func TestAStatementErrorNamesItsCodeAndUnwraps(t *testing.T) {
	inner := errors.New("pq: syntax error")
	e := &StatementError{Message: Message{Code: "42601", Text: `syntax error at or near "boom"`}, Err: inner}
	if got := e.Error(); got != `42601: syntax error at or near "boom"` {
		t.Errorf("message %q", got)
	}
	if !errors.Is(e, inner) {
		t.Error("the driver's error should still be found through it")
	}
	if got := (&StatementError{Message: Message{Text: "no code here"}}).Error(); got != "no code here" {
		t.Errorf("with no code, the text alone: %q", got)
	}
}

// rowsOf is a stream of fixed rows, then err or the end.
type rowsOf struct {
	rows []model.Row
	i    int
	err  error
}

func (r *rowsOf) Columns() []model.ColumnDef { return nil }
func (r *rowsOf) Close() error               { return nil }
func (r *rowsOf) Next(context.Context) (model.Row, error) {
	if r.i < len(r.rows) {
		r.i++
		return r.rows[r.i-1], nil
	}
	if r.err != nil {
		return nil, r.err
	}
	return nil, io.EOF
}

func TestReadDistinctReadsEachCountAsItCame(t *testing.T) {
	ctx := context.Background()
	got, err := ReadDistinct(ctx, &rowsOf{rows: []model.Row{
		{"a", int64(3)}, {nil, int32(1)}, {"b", "7"}, {"c", []byte("12")},
		{"d", uint64(2)}, {"e", 4}, {"f", 1.5}, {"g", "many"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{3, 1, 7, 12, 2, 4, -1, -1}
	if len(got) != len(want) {
		t.Fatalf("%d values, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Count != w {
			t.Errorf("value %v counted %d, want %d", got[i].Value, got[i].Count, w)
		}
	}
	if got[1].Value != nil {
		t.Errorf("NULL should stay NULL, got %v", got[1].Value)
	}
	if _, err := ReadDistinct(ctx, &rowsOf{rows: []model.Row{{"a", 1, "extra"}}}); err == nil || !strings.Contains(err.Error(), "3 values, want 2") {
		t.Errorf("a row of three values: %v", err)
	}
	broken := errors.New("the stream broke")
	if _, err := ReadDistinct(ctx, &rowsOf{err: broken}); !errors.Is(err, broken) {
		t.Errorf("a stream's failure should come back: %v", err)
	}
}

func TestAccessAndEnvironmentNames(t *testing.T) {
	if got := Access(99).String(); got != "unknown" {
		t.Errorf("an unknown access is named %q", got)
	}
	if got := AccessDDL.String(); got != "DDL" {
		t.Errorf("AccessDDL is named %q", got)
	}
	for _, e := range []Environment{EnvLocal, EnvDev, EnvStaging, EnvProduction} {
		if !e.Valid() || e.String() != string(e) {
			t.Errorf("%q should be a valid environment named as it is", e)
		}
	}
	for _, e := range []Environment{"prod", "", "Production"} {
		if e.Valid() {
			t.Errorf("%q is not an environment", e)
		}
	}
}
