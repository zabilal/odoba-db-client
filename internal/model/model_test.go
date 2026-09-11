package model

import "testing"

func TestObjectRefAddressing(t *testing.T) {
	ref := NewRef(KindTable, "sales", "public", "orders")

	if got := ref.Name(); got != "orders" {
		t.Errorf("Name() = %q, want orders", got)
	}
	if got := ref.String(); got != "table:sales.public.orders" {
		t.Errorf("String() = %q", got)
	}
	if parent := ref.Parent(KindSchema); parent.Name() != "public" {
		t.Errorf("Parent() = %v", parent)
	}
}

func TestObjectRefEqual(t *testing.T) {
	a := NewRef(KindTable, "db", "public", "t")

	if !a.Equal(NewRef(KindTable, "db", "public", "t")) {
		t.Error("identical refs should be equal")
	}
	if a.Equal(NewRef(KindView, "db", "public", "t")) {
		t.Error("differing kind should not be equal")
	}
	if a.Equal(NewRef(KindTable, "db", "other", "t")) {
		t.Error("differing path should not be equal")
	}
	if a.Equal(NewRef(KindTable, "db", "public")) {
		t.Error("differing path length should not be equal")
	}
}

func TestObjectRefZero(t *testing.T) {
	var zero ObjectRef
	if !zero.IsZero() || zero.String() != "" || zero.Name() != "" {
		t.Error("zero ref should be inert")
	}
	if !NewRef(KindTable, "solo").Parent(KindSchema).IsZero() {
		t.Error("parent of a root-level ref should be zero")
	}
}

func TestIdentityKindMutability(t *testing.T) {
	// A log record is addressable but append-only: browsable and exportable,
	// never editable. Getting this wrong would offer an edit affordance on
	// Kafka messages.
	if IdentityLogOffset.Mutable() {
		t.Error("log offsets must not be mutable")
	}
	if IdentityNone.Mutable() {
		t.Error("absent identity must not be mutable")
	}
	for _, k := range []IdentityKind{
		IdentityPrimaryKey, IdentityUniqueIndex, IdentityRowID,
		IdentityDocumentID, IdentityKeyName, IdentityChosen,
	} {
		if !k.Mutable() {
			t.Errorf("identity kind %d should be mutable", k)
		}
	}
}

func TestRowIdentityEditable(t *testing.T) {
	target := NewRef(KindTable, "db", "public", "t")

	ok := RowIdentity{Kind: IdentityPrimaryKey, Columns: []string{"id"}, Target: target}
	if !ok.Editable() {
		t.Error("complete primary-key identity should be editable")
	}

	for name, id := range map[string]RowIdentity{
		"no columns":  {Kind: IdentityPrimaryKey, Target: target},
		"no target":   {Kind: IdentityPrimaryKey, Columns: []string{"id"}},
		"log offset":  {Kind: IdentityLogOffset, Columns: []string{"offset"}, Target: target},
		"no identity": {Kind: IdentityNone, Columns: []string{"id"}, Target: target},
	} {
		if id.Editable() {
			t.Errorf("%s: should not be editable", name)
		}
	}
}

func TestTopicMessageCount(t *testing.T) {
	topic := Topic{Partitions: []Partition{
		{LowWatermark: 0, HighWatermark: 100},
		{LowWatermark: 50, HighWatermark: 150},
		{LowWatermark: 10, HighWatermark: 10}, // empty after retention
	}}
	if got := topic.MessageCount(); got != 200 {
		t.Errorf("MessageCount() = %d, want 200", got)
	}
}

func TestConsumerGroupTotalLagClampsNegatives(t *testing.T) {
	// Current and End are read at slightly different instants, so a busy
	// partition can momentarily report negative lag. It must not subtract
	// from the total and understate how far behind the group is.
	g := ConsumerGroup{Offsets: []GroupOffset{
		{Lag: 100}, {Lag: -5}, {Lag: 50}, {Lag: -1},
	}}
	if got := g.TotalLag(); got != 150 {
		t.Errorf("TotalLag() = %d, want 150", got)
	}
}

func TestConfigEntryIsOverride(t *testing.T) {
	if !(ConfigEntry{Source: "DYNAMIC_TOPIC_CONFIG"}).IsOverride() {
		t.Error("topic-level config should be an override")
	}
	for _, src := range []string{"DEFAULT_CONFIG", "STATIC_BROKER_CONFIG", ""} {
		if (ConfigEntry{Source: src}).IsOverride() {
			t.Errorf("%q should not be an override", src)
		}
	}
}

func TestTypeClassPredicates(t *testing.T) {
	for _, c := range []TypeClass{TypeInteger, TypeFloat, TypeDecimal} {
		if !c.Numeric() {
			t.Errorf("%v should be numeric", c)
		}
	}
	if TypeString.Numeric() || TypeTimestamp.Numeric() {
		t.Error("non-numeric class reported numeric")
	}
	if !TypeJSON.Structured() || !TypeArray.Structured() {
		t.Error("structured class not reported structured")
	}
	if !TypeTimestamp.Temporal() || TypeString.Temporal() {
		t.Error("temporal predicate wrong")
	}
}

func TestParadigmValid(t *testing.T) {
	for _, p := range []Paradigm{ParadigmRelational, ParadigmDocument, ParadigmKeyValue, ParadigmStream} {
		if !p.Valid() {
			t.Errorf("%v should be valid", p)
		}
	}
	if Paradigm("graph").Valid() {
		t.Error("unknown paradigm reported valid")
	}
}
