package source

import (
	"context"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

type fakeDriver struct{ desc Descriptor }

func (f fakeDriver) Describe() Descriptor { return f.desc }
func (f fakeDriver) Open(context.Context, ConnectionConfig) (Source, error) {
	return nil, nil
}

func TestRegisterAndLookup(t *testing.T) {
	t.Cleanup(reset)
	reset()

	Register(fakeDriver{Descriptor{ID: "pg", Name: "PostgreSQL",
		Paradigm: model.ParadigmRelational, URLSchemes: []string{"postgres", "postgresql"}}})

	if _, err := Lookup("pg"); err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if _, err := Lookup("missing"); err == nil {
		t.Error("expected error for unregistered driver")
	}
}

func TestDriversSortedByName(t *testing.T) {
	t.Cleanup(reset)
	reset()

	Register(fakeDriver{Descriptor{ID: "sqlite", Name: "SQLite"}})
	Register(fakeDriver{Descriptor{ID: "kafka", Name: "Apache Kafka"}})
	Register(fakeDriver{Descriptor{ID: "pg", Name: "PostgreSQL"}})

	got := Drivers()
	want := []string{"Apache Kafka", "PostgreSQL", "SQLite"}
	if len(got) != len(want) {
		t.Fatalf("got %d drivers, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Name != want[i] {
			t.Errorf("position %d: got %q want %q", i, got[i].Name, want[i])
		}
	}
}

func TestLookupSchemeIsCaseAndColonInsensitive(t *testing.T) {
	t.Cleanup(reset)
	reset()

	Register(fakeDriver{Descriptor{ID: "pg", Name: "PostgreSQL",
		URLSchemes: []string{"postgres", "postgresql"}}})

	for _, in := range []string{"postgres", "POSTGRES", "postgresql:", "PostgreSQL"} {
		if _, ok := LookupScheme(in); !ok {
			t.Errorf("scheme %q not resolved", in)
		}
	}
	if _, ok := LookupScheme("mysql"); ok {
		t.Error("unclaimed scheme resolved")
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	t.Cleanup(reset)
	reset()

	Register(fakeDriver{Descriptor{ID: "pg", Name: "PostgreSQL"}})

	defer func() {
		if recover() == nil {
			t.Error("expected panic on duplicate registration")
		}
	}()
	Register(fakeDriver{Descriptor{ID: "pg", Name: "Other"}})
}

func TestRegisterEmptyIDPanics(t *testing.T) {
	t.Cleanup(reset)
	reset()

	defer func() {
		if recover() == nil {
			t.Error("expected panic on empty driver ID")
		}
	}()
	Register(fakeDriver{Descriptor{Name: "Nameless"}})
}
