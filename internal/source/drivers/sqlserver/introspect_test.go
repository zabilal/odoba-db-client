package sqlserver

import (
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Reading the catalogue, and writing back what it says.

// The server keeps a stored expression wrapped in brackets it added itself.
// What is shown is what was written.
func TestAStoredExpressionIsShownAsItWasWritten(t *testing.T) {
	cases := map[string]string{
		"((0))":                              "0",
		"('none')":                           "'none'",
		"(getdate())":                        "getdate()",
		"(NEXT VALUE FOR [dbo].[writes_id])": "NEXT VALUE FOR [dbo].[writes_id]",
		"((a)+(b))":                          "(a)+(b)",
		"([a]>(0) AND [b]<(9))":              "[a]>(0) AND [b]<(9)",
		"":                                   "",
		"(":                                  "(",
		")":                                  ")",
		"(a))+((b)":                          "(a))+((b)",
	}
	for def, want := range cases {
		if got := trimDefault(def); got != want {
			t.Errorf("trimDefault(%q) = %q, want %q", def, got, want)
		}
	}
}

// Brackets are balanced when every one is closed after it was opened; the
// outer pair of "(a) + (b)" is two pairs and not a wrapper.
func TestWhenBracketsAreBalanced(t *testing.T) {
	cases := map[string]bool{
		"":      true,
		"a":     true,
		"(a)":   true,
		"(a)+(": false,
		")a(":   false,
		"((a)":  false,
		"(a))":  false,
	}
	for s, want := range cases {
		if got := balanced(s); got != want {
			t.Errorf("balanced(%q) = %v, want %v", s, got, want)
		}
	}
}

// The four databases a server makes for itself are marked as its own.
func TestTheServersOwnDatabasesAreKnown(t *testing.T) {
	for id := int64(1); id <= 4; id++ {
		if !systemDatabase(id) {
			t.Errorf("database %d is one of the server's own", id)
		}
	}
	if systemDatabase(5) {
		t.Error("the first database somebody made was called the server's")
	}
}

// A foreign key's behaviour reads as the model spells it, not as the
// catalogue does.
func TestAForeignKeysBehaviourReadsAsTheModelSpellsIt(t *testing.T) {
	cases := map[string]model.ReferentialAction{
		"NO_ACTION":   model.ActionNoAction,
		"CASCADE":     model.ActionCascade,
		"SET_NULL":    model.ActionSetNull,
		"SET_DEFAULT": model.ActionSetDefault,
	}
	for desc, want := range cases {
		if got := referentialAction(desc); got != want {
			t.Errorf("referentialAction(%q) = %q, want %q", desc, got, want)
		}
	}
}

// A badge has room for a few characters, so a count is abbreviated.
func TestACountIsAbbreviatedForABadge(t *testing.T) {
	cases := map[int64]string{
		0: "0", 1: "1", 999: "999", 1000: "1K", 1234: "1.2K", 999999: "1000K",
		1_000_000: "1M", 12_300_000: "12.3M", 1_000_000_000: "1B", 2_500_000_000: "2.5B",
	}
	for n, want := range cases {
		if got := humanCount(n); got != want {
			t.Errorf("humanCount(%d) = %q, want %q", n, got, want)
		}
	}
}

// A declared type without its length is the type's name.
func TestATypesNameWithoutItsLength(t *testing.T) {
	cases := map[string]string{
		"nvarchar(50)":   "nvarchar",
		"decimal(30,10)": "decimal",
		"int":            "int",
		" int ":          "int",
		"":               "",
	}
	for native, want := range cases {
		if got := nameOf(native); got != want {
			t.Errorf("nameOf(%q) = %q, want %q", native, got, want)
		}
	}
}
