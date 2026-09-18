package cassandra

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

func TestTheKeyspacesACLusterKeepsForItselfAreItsOwn(t *testing.T) {
	for _, name := range []string{"system", "system_schema", "system_auth", "system_traces",
		"system_distributed", "system_views", "system_virtual_schema", "dse_system", "dse_perf"} {
		if !theServersOwn(name) {
			t.Errorf("%s is the cluster's own", name)
		}
	}
	for _, name := range []string{"ikigai_it", "shop", "systems", "my_system", "dsedata"} {
		if theServersOwn(name) {
			t.Errorf("%s is somebody's keyspace, not the cluster's", name)
		}
	}
}

func TestTheKeyspacesShownAreAPersonsOwnInNameOrder(t *testing.T) {
	// As a cluster hands them back: by the token of each name, which is no
	// order at all to read.
	read := []string{"system_auth", "shop", "system_schema", "ikigai_it", "system", "archive", "dse_perf"}
	if got := strings.Join(userKeyspaces(read), " "); got != "archive ikigai_it shop" {
		t.Errorf("the keyspaces read %q", got)
	}
	if got := userKeyspaces([]string{"system", "system_traces"}); got != nil {
		t.Errorf("a cluster holding only its own keyspaces shows %v", got)
	}
}

func TestAColumnIsRankedByWhatItIsForInTheKey(t *testing.T) {
	ranks := []string{partitionKey, clusteringKey, staticColumn, "regular"}
	for i := 1; i < len(ranks); i++ {
		if columnRank(ranks[i-1]) >= columnRank(ranks[i]) {
			t.Errorf("%s does not come before %s", ranks[i-1], ranks[i])
		}
	}
	// A kind nobody named is read as an ordinary column rather than as a key.
	if columnRank("whatever") != columnRank("regular") {
		t.Error("a kind nobody named is not an ordinary column")
	}
}

func TestAKeyIsThePartitionAndWhatOrdersItWithin(t *testing.T) {
	col := func(name, kind string) model.Column {
		return model.Column{Name: name, Attrs: map[string]string{"kind": kind}}
	}
	cols := []model.Column{
		col("country", partitionKey), col("id", clusteringKey),
		col("nickname", staticColumn), col("name", "regular"),
	}
	if got := strings.Join(primaryKey(cols), ", "); got != "country, id" {
		t.Errorf("the key is %q", got)
	}
	// A static column is a partition's, not a row's address, and neither is
	// an ordinary one.
	if got := primaryKey([]model.Column{col("name", "regular"), col("nickname", staticColumn)}); got != nil {
		t.Errorf("a table addressed by %v", got)
	}
}

func TestWhatATreeNodeOffersIsWhatItCanDo(t *testing.T) {
	tables := objectNodes(model.KindTable, "shop", []string{"orders", "people"}, true)
	if len(tables) != 2 || tables[0].Label != "orders" {
		t.Fatalf("the tables are %+v", tables)
	}
	for _, n := range tables {
		if !n.HasChildren {
			t.Errorf("%s holds columns", n.Label)
		}
		// Rows come with T2.51; until then nothing offers to read them.
		if n.Browsable {
			t.Errorf("%s offers rows it cannot read", n.Label)
		}
		if n.Ref.Kind != model.KindTable || n.Ref.Path[0] != "shop" {
			t.Errorf("%s is addressed as %s", n.Label, n.Ref)
		}
	}
	// A type holds nothing to open.
	types := objectNodes(model.KindUserType, "shop", []string{"address"}, false)
	if len(types) != 1 || types[0].HasChildren || types[0].Browsable {
		t.Errorf("the types are %+v", types)
	}
}
