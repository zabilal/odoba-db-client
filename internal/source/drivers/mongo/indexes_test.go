package mongo

import (
	"context"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

func TestAnIndexIsPlannedAsTheCallItIs(t *testing.T) {
	s := &mongoSource{}
	ctx := context.Background()
	plan, err := s.PlanIndex(ctx, peopleRef, model.DocumentIndex{
		Name: "name_score", Unique: true,
		Keys: []model.IndexColumn{{Name: "name"}, {Name: "score", Descending: true}},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Statements) != 1 || len(plan.Descriptions) != 1 {
		t.Fatalf("a plan of %d statements", len(plan.Statements))
	}
	want := `db.people.createIndex({"name":1,"score":-1}, {"name":"name_score","unique":true})`
	if got := plan.Statements[0].SQL; got != want {
		t.Errorf("the call is\n%s\nwant\n%s", got, want)
	}
	if got := plan.Descriptions[0]; got != "Make an index on name, score descending (unique)" {
		t.Errorf("it says %q", got)
	}
	if plan.Statements[0].Op == nil {
		t.Error("the plan carries no call to make")
	}
	if plan.Atomic {
		t.Error("one call claims a transaction")
	}
	// An index of a kind, and one that expires.
	plan, _ = s.PlanIndex(ctx, peopleRef, model.DocumentIndex{
		Keys: []model.IndexColumn{{Name: "body", Expression: "text"}}}, false)
	if got := plan.Statements[0].SQL; got != `db.people.createIndex({"body":"text"})` {
		t.Errorf("a text index is %s", got)
	}
	plan, _ = s.PlanIndex(ctx, peopleRef, model.DocumentIndex{
		Keys: []model.IndexColumn{{Name: "seen"}}, TTL: 3600, Sparse: true}, false)
	if got := plan.Statements[0].SQL; !strings.Contains(got, `"expireAfterSeconds":3600`) || !strings.Contains(got, `"sparse":true`) {
		t.Errorf("an expiring sparse index is %s", got)
	}
	if got := plan.Descriptions[0]; !strings.Contains(got, "expiring after 3600s") {
		t.Errorf("it says %q", got)
	}
}

func TestDroppingAnIndexIsPlannedToo(t *testing.T) {
	s := &mongoSource{}
	ctx := context.Background()
	plan, err := s.PlanDropIndex(ctx, peopleRef, "name_score", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Statements[0].SQL; got != `db.people.dropIndex("name_score")` {
		t.Errorf("the call is %s", got)
	}
	if got := plan.Descriptions[0]; got != "Drop the index name_score" {
		t.Errorf("it says %q", got)
	}
}

func TestAnIndexThatCannotBePlannedIsRefused(t *testing.T) {
	s := &mongoSource{}
	ctx := context.Background()
	cases := []struct {
		what string
		says string
		run  func() error
	}{
		{"an index on nothing", "at least one field", func() error {
			_, err := s.PlanIndex(ctx, peopleRef, model.DocumentIndex{}, false)
			return err
		}},
		{"a field with no name", "no name", func() error {
			_, err := s.PlanIndex(ctx, peopleRef, model.DocumentIndex{
				Keys: []model.IndexColumn{{Name: "  "}}}, false)
			return err
		}},
		{"a field twice", "twice", func() error {
			_, err := s.PlanIndex(ctx, peopleRef, model.DocumentIndex{
				Keys: []model.IndexColumn{{Name: "a"}, {Name: "a", Descending: true}}}, false)
			return err
		}},
		{"an index on something that is not a collection", "no indexes of its own", func() error {
			_, err := s.PlanIndex(ctx, model.NewRef(model.KindDatabase, "shop"), model.DocumentIndex{
				Keys: []model.IndexColumn{{Name: "a"}}}, false)
			return err
		}},
		{"a drop with no name", "by name", func() error {
			_, err := s.PlanDropIndex(ctx, peopleRef, "  ", false)
			return err
		}},
		{"a drop of every index at once", "one index at a time", func() error {
			_, err := s.PlanDropIndex(ctx, peopleRef, "*", false)
			return err
		}},
		{"a drop of the identifier's index", "cannot be dropped", func() error {
			_, err := s.PlanDropIndex(ctx, peopleRef, "_id_", false)
			return err
		}},
		{"a drop on something that is not a collection", "no indexes of its own", func() error {
			_, err := s.PlanDropIndex(ctx, model.NewRef(model.KindIndex, "shop", "people", "x"), "x", false)
			return err
		}},
	}
	for _, tc := range cases {
		err := tc.run()
		if err == nil {
			t.Errorf("%s was planned", tc.what)
			continue
		}
		if !strings.Contains(err.Error(), tc.says) {
			t.Errorf("%s is refused with %q, want it to say %q", tc.what, err, tc.says)
		}
	}
}

func TestAPlanMadeElsewhereChangesNoIndex(t *testing.T) {
	s := &mongoSource{}
	out, err := s.ApplyIndex(context.Background(), &source.WritePlan{Target: peopleRef,
		Statements: []source.Statement{{SQL: "db.people.dropIndex(\"x\")"}}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Err == nil || out.FailedAt != 0 {
		t.Errorf("a plan made elsewhere: %+v", out)
	}
}
