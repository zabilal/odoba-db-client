package kafka

import (
	"context"
	"strings"
	"testing"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kmsg"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// The permissions a cluster holds, on the way in and out (FR-13.15). What
// needs a broker is in acls_live_test.go; this is the mapping between the words
// a person reads and the ones the protocol carries.

// Every kind and operation a window offers is one this driver can write, and
// every one it can write reads back as the same word. A table that had drifted
// from the one the window offers from would be a choice that failed when it was
// taken.
func TestEveryPermissionAWindowOffersCanBeWritten(t *testing.T) {
	for _, k := range model.ACLResourceKinds {
		wire, err := kindWire(k)
		if err != nil {
			t.Errorf("a permission cannot be about a %s: %v", k, err)
			continue
		}
		if back := kindSaid(wire); back != k {
			t.Errorf("%s goes out as %s and comes back as %s", k, wire, back)
		}
	}
	for _, op := range model.ACLOperations {
		wire, err := opWire(op)
		if err != nil {
			t.Errorf("%s cannot be allowed: %v", op, err)
			continue
		}
		if back := opSaid(wire); back != op {
			t.Errorf("%s goes out as %s and comes back as %s", op, wire, back)
		}
	}
}

// A kind or an operation this application has no word for is still shown, in
// the protocol's own word: a permission nobody can see is worse than one named
// oddly, because it is one nobody can take away either.
func TestAPermissionWithNoWordForItIsStillShown(t *testing.T) {
	if got := kindSaid(kmsg.ACLResourceTypeUnknown); got != "unknown" {
		t.Errorf("an unknown resource reads as %q", got)
	}
	// CREATE_TOKENS is a real operation this application has no word for.
	if got := opSaid(kmsg.ACLOperationCreateTokens); got != "create tokens" {
		t.Errorf("an operation with no word reads as %q", got)
	}
	// And it cannot be written, because writing the wrong one would grant
	// something nobody asked for.
	if _, err := opWire("create tokens"); err == nil {
		t.Error("an operation with no word was written anyway")
	}
	if _, err := kindWire("wormhole"); err == nil {
		t.Error("a permission was written about a wormhole")
	}
}

// A described permission reads as the sentence somebody is about to agree to.
func TestAPermissionReadsAsASentence(t *testing.T) {
	for _, c := range []struct {
		acl  model.ACL
		want string
	}{
		{model.ACL{Principal: "User:alice", Host: "*", Operation: model.ACLRead,
			Resource: model.ACLResource{Kind: model.ACLTopic, Name: "orders"}},
			"User:alice may read the topic orders, from anywhere"},
		{model.ACL{Principal: "User:bob", Host: "10.0.0.1", Operation: model.ACLWrite, Deny: true,
			Resource: model.ACLResource{Kind: model.ACLTopic}},
			"User:bob may not write any topic, from 10.0.0.1"},
		{model.ACL{Principal: "User:ci", Operation: model.ACLAll,
			Resource: model.ACLResource{Kind: model.ACLTopic, Name: "staging.", Prefixed: true}},
			"User:ci may do anything to topics beginning with staging., from anywhere"},
		{model.ACL{Principal: "User:ops", Operation: model.ACLDescribe,
			Resource: model.ACLResource{Kind: model.ACLClusterItself}},
			"User:ops may describe the cluster, from anywhere"},
	} {
		if got := c.acl.String(); got != c.want {
			t.Errorf("it reads as %q,\n          want %q", got, c.want)
		}
	}
}

// What the protocol says becomes what this application holds: the star that
// means every object of a kind is not a name, and neither is the name Kafka
// gives the cluster itself.
func TestWhatTheProtocolSaysBecomesAPermission(t *testing.T) {
	got := aclSaid(kadm.DescribedACL{Principal: "User:alice", Host: "*",
		Type: kmsg.ACLResourceTypeTopic, Name: "*", Pattern: kadm.ACLPatternLiteral,
		Operation: kadm.OpRead, Permission: kmsg.ACLPermissionTypeAllow})
	if got.Resource.Name != "" || got.Resource.Prefixed || got.Deny {
		t.Errorf("every topic reads as %+v", got)
	}
	got = aclSaid(kadm.DescribedACL{Principal: "User:ops", Host: "*",
		Type: kmsg.ACLResourceTypeCluster, Name: clusterResource, Pattern: kadm.ACLPatternLiteral,
		Operation: kadm.OpAlter, Permission: kmsg.ACLPermissionTypeDeny})
	if got.Resource.Name != "" || got.Resource.Kind != model.ACLClusterItself || !got.Deny {
		t.Errorf("the cluster reads as %+v", got)
	}
	got = aclSaid(kadm.DescribedACL{Principal: "User:ci", Host: "10.0.0.2",
		Type: kmsg.ACLResourceTypeGroup, Name: "ci.", Pattern: kadm.ACLPatternPrefixed,
		Operation: kadm.OpDescribe, Permission: kmsg.ACLPermissionTypeAllow})
	if !got.Resource.Prefixed || got.Resource.Name != "ci." || got.Host != "10.0.0.2" {
		t.Errorf("a prefix reads as %+v", got)
	}
}

// A permission is written exactly as it was given, never as a pattern: a
// revoke by pattern would take away permissions nobody named.
func TestAPermissionIsWrittenExactly(t *testing.T) {
	b, err := aclExactly(model.ACL{Principal: "User:alice", Operation: model.ACLRead,
		Resource: model.ACLResource{Kind: model.ACLTopic, Name: "orders"}})
	if err != nil {
		t.Fatal(err)
	}
	// Nothing about it is a wildcard: kadm calls that opting into a wide glob,
	// and it is the difference between revoking one permission and many.
	if b.HasAnyFilter() {
		t.Error("it would match more permissions than the one it names")
	}
	// A prefix is a pattern, and that one is asked for rather than assumed.
	b, err = aclExactly(model.ACL{Principal: "User:ci", Operation: model.ACLWrite,
		Resource: model.ACLResource{Kind: model.ACLTopic, Name: "staging.", Prefixed: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.ValidateCreate(); err != nil {
		t.Errorf("a prefixed permission cannot be created: %v", err)
	}
}

// What cannot be written is refused before a broker is asked, in words about
// what was missing.
func TestAPermissionThatCannotBeWritten(t *testing.T) {
	for _, c := range []struct {
		acl  model.ACL
		says string
	}{
		{model.ACL{Operation: model.ACLRead, Resource: model.ACLResource{Kind: model.ACLTopic}},
			"somebody it is about"},
		{model.ACL{Principal: "  ", Operation: model.ACLRead,
			Resource: model.ACLResource{Kind: model.ACLTopic}}, "somebody it is about"},
		{model.ACL{Principal: "User:alice", Operation: "sniff",
			Resource: model.ACLResource{Kind: model.ACLTopic}}, "not something a permission can allow"},
		{model.ACL{Principal: "User:alice", Operation: model.ACLRead,
			Resource: model.ACLResource{Kind: "wormhole"}}, "cannot be about"},
		{model.ACL{Principal: "User:alice", Operation: model.ACLRead,
			Resource: model.ACLResource{Kind: model.ACLClusterItself, Prefixed: true}},
			"there is one cluster"},
		{model.ACL{Principal: "User:alice", Operation: model.ACLRead,
			Resource: model.ACLResource{Kind: model.ACLUserPrincipal, Name: "bob"}},
			"cannot ask about permissions on a user"},
	} {
		_, err := aclExactly(c.acl)
		if err == nil {
			t.Errorf("%+v was written anyway", c.acl)
			continue
		}
		if !strings.Contains(err.Error(), c.says) {
			t.Errorf("it says %q, which does not mention %q", err, c.says)
		}
	}
}

// A filter asks for everything that reaches the object named — the permission
// written about its name, the one about every name of its kind, and any prefix
// of it — and for anything at all where nothing is named.
func TestAFilterAsksForEverythingThatReaches(t *testing.T) {
	b, err := aclFilter(model.ACLFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.ValidateFilter(); err != nil {
		t.Errorf("the empty filter is invalid: %v", err)
	}
	if !b.HasAnyFilter() {
		t.Error("the empty filter does not ask for everything")
	}
	if _, err := aclFilter(model.ACLFilter{Kind: model.ACLTopic, Name: "orders"}); err != nil {
		t.Fatal(err)
	}
	if _, err := aclFilter(model.ACLFilter{Kind: model.ACLClusterItself}); err != nil {
		t.Fatal(err)
	}
	// A kind kadm cannot build a question about says so rather than asking a
	// different question.
	if _, err := aclFilter(model.ACLFilter{Kind: model.ACLUserPrincipal, Name: "bob"}); err == nil {
		t.Error("it asked about permissions on a user")
	}
	// The zero filter is the one that asks for everything, and says so.
	if !(model.ACLFilter{}).Anything() {
		t.Error("the zero filter does not say it asks for everything")
	}
	if (model.ACLFilter{Kind: model.ACLTopic}).Anything() {
		t.Error("a filter about topics says it asks for everything")
	}
}

// They come back in an order that does not move between two readings of the
// same cluster: a list that reordered itself would be a list nobody could
// point at.
func TestPermissionsComeBackInAnOrder(t *testing.T) {
	acls := []model.ACL{
		{Principal: "User:bob", Operation: model.ACLRead, Host: "*",
			Resource: model.ACLResource{Kind: model.ACLTopic, Name: "orders"}},
		{Principal: "User:alice", Operation: model.ACLWrite, Host: "*",
			Resource: model.ACLResource{Kind: model.ACLTopic, Name: "orders"}},
		{Principal: "User:alice", Operation: model.ACLRead, Host: "*",
			Resource: model.ACLResource{Kind: model.ACLTopic, Name: "orders"}},
		{Principal: "User:alice", Operation: model.ACLRead, Host: "*", Deny: true,
			Resource: model.ACLResource{Kind: model.ACLTopic, Name: "orders"}},
		{Principal: "User:alice", Operation: model.ACLRead, Host: "*",
			Resource: model.ACLResource{Kind: model.ACLGroup, Name: "readers"}},
		{Principal: "User:alice", Operation: model.ACLRead, Host: "*",
			Resource: model.ACLResource{Kind: model.ACLTopic, Name: "orders", Prefixed: true}},
		{Principal: "User:alice", Operation: model.ACLRead, Host: "*",
			Resource: model.ACLResource{Kind: model.ACLTopic, Name: "invoices"}},
		{Principal: "User:alice", Operation: model.ACLRead, Host: "10.0.0.1",
			Resource: model.ACLResource{Kind: model.ACLTopic, Name: "invoices"}},
	}
	sortACLs(acls)
	var said []string
	for _, a := range acls {
		said = append(said, string(a.Resource.Kind)+"/"+a.Resource.Name+" "+
			a.Principal+" "+string(a.Operation))
	}
	want := []string{
		"group/readers User:alice read",
		// Two names, so that the order of names is an order and not an
		// accident: invoices before orders.
		"topic/invoices User:alice read",
		"topic/invoices User:alice read",
		"topic/orders User:alice read",
		"topic/orders User:alice read",
		"topic/orders User:alice write",
		"topic/orders User:bob read",
		"topic/orders User:alice read",
	}
	for i := range want {
		if said[i] != want[i] {
			t.Errorf("row %d is %q, want %q", i, said[i], want[i])
		}
	}
	// Where everything else is equal, the host is what is left to order by.
	if acls[1].Host != "*" || acls[2].Host != "10.0.0.1" {
		t.Errorf("two permissions differing only in host are ordered %q then %q",
			acls[1].Host, acls[2].Host)
	}
	// An allow comes before the deny that overrules it, and a name before the
	// prefix that also covers it.
	if acls[3].Deny || !acls[4].Deny {
		t.Errorf("the deny is not beside its allow: %+v", acls[3:5])
	}
	if !acls[7].Resource.Prefixed {
		t.Errorf("a prefix is not last: %+v", acls[7])
	}
}

// A cluster with no authorizer says what that means: an empty list would read
// as "nobody may do anything", where the truth is the opposite.
func TestAClusterWithNoAuthorizerSaysSo(t *testing.T) {
	err := aclError(kerr.SecurityDisabled)
	if err == nil {
		t.Fatal("it said nothing")
	}
	for _, want := range []string{"no authorizer", "everything is allowed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("it says %q, which does not mention %q", err, want)
		}
	}
	// Anything else is passed along as it was: a refusal this does not
	// recognise is not one it should rewrite.
	other := context.Canceled
	if got := aclError(other); got != other {
		t.Errorf("it turned %v into %v", other, got)
	}
	if aclError(nil) != nil {
		t.Error("it invented an error out of nothing")
	}
}
