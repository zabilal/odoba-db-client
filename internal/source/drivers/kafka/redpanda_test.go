package kafka

import (
	"testing"

	"github.com/twmb/franz-go/pkg/kadm"
)

// Redpanda is told from Kafka by the one thing it says of itself.
func TestWhichClusterIsRedpanda(t *testing.T) {
	for id, want := range map[string]bool{
		"redpanda.99e59dad-3997-44e5-b183-5207596ccd94": true,
		"redpanda.":              true,
		"5L6g3nShT-eMCtK--X86sw": false, // Kafka's are a UUID in base64
		"":                       false,
		"my-redpanda.cluster":    false, // somebody's name for a Kafka cluster
		"Redpanda.x":             false,
		"redpanda":               false,
	} {
		if isRedpanda(id) != want {
			t.Errorf("isRedpanda(%q) = %v", id, !want)
		}
	}
}

// A group described without complaint may still not be there: dead, with
// nobody in it and nothing committed. Anything less than all three is a group.
func TestAGroupThatIsNotThere(t *testing.T) {
	committed := kadm.GroupLag{"orders": {0: kadm.GroupMemberLag{Topic: "orders"}}}
	member := []kadm.DescribedGroupMember{{MemberID: "m"}}
	for name, c := range map[string]struct {
		l    kadm.DescribedGroupLag
		want bool
	}{
		"dead, empty, nothing committed": {kadm.DescribedGroupLag{State: "Dead"}, true},
		"empty, but known":               {kadm.DescribedGroupLag{State: "Empty"}, false},
		"stable":                         {kadm.DescribedGroupLag{State: "Stable", Members: member}, false},
		"no state said":                  {kadm.DescribedGroupLag{}, false},
		"dead, with commits still held":  {kadm.DescribedGroupLag{State: "Dead", Lag: committed}, false},
		"dead, with somebody in it":      {kadm.DescribedGroupLag{State: "Dead", Members: member}, false},
	} {
		if missing(c.l) != c.want {
			t.Errorf("%s: missing = %v", name, !c.want)
		}
	}
}
