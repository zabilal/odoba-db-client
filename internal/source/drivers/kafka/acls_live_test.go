//go:build conformance

package kafka

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// A cluster's permissions against real brokers (T5.13, FR-13.15).
//
// It takes two of them. Permissions only exist where a broker has an authorizer
// configured, and the plain broker has none — which is itself worth a test,
// because a cluster that keeps no permissions has to say so rather than answer
// an empty list. The SASL broker has one, with ikigai a super user so that
// everything already proven against it still passes.

// aclCluster is a connection to the broker that keeps permissions.
func aclCluster(t *testing.T, g source.Guard) source.Source {
	t.Helper()
	cfg := saslConfig("PLAIN", saslUser, saslPassword)
	cfg.Guard = g
	src := liveSASL(t, cfg)
	if !src.Capabilities().Stream.ACLs {
		t.Fatal("the driver reads permissions and does not claim to")
	}
	return src
}

// reads is the interface for reading permissions.
func reads(t *testing.T, src source.Source) source.ACLInspector {
	t.Helper()
	a, ok := src.(source.ACLInspector)
	if !ok {
		t.Fatal("claims Stream.ACLs but does not implement ACLInspector")
	}
	return a
}

// changes is the interface for granting and revoking them.
func changes(t *testing.T, src source.Source) source.ACLAdmin {
	t.Helper()
	if !src.Capabilities().Stream.ManageACLs {
		t.Fatal("the driver changes permissions and does not claim to")
	}
	a, ok := src.(source.ACLAdmin)
	if !ok {
		t.Fatal("claims Stream.ManageACLs but does not implement ACLAdmin")
	}
	return a
}

// held is the permissions matching a filter, as sentences, so that a test says
// what a person would read rather than comparing structs.
func held(t *testing.T, a source.ACLInspector, of model.ACLFilter) []string {
	t.Helper()
	acls, err := a.ACLs(context.Background(), of)
	if err != nil {
		t.Fatalf("reading the permissions: %v", err)
	}
	said := make([]string, 0, len(acls))
	for _, acl := range acls {
		said = append(said, acl.String())
	}
	return said
}

// has reports whether one of them is the sentence given.
func has(said []string, want string) bool {
	for _, s := range said {
		if s == want {
			return true
		}
	}
	return false
}

// settledACLs waits for the permissions matching of to be as want says.
//
// A change is accepted by the controller before the broker's own authorizer has
// heard of it, so reading back at once can see what was there before. That is
// the cluster catching up rather than the change being wrong, and waiting for
// the condition is honest where a fixed pause would only be lucky (settled).
func settledACLs(t *testing.T, a source.ACLInspector, of model.ACLFilter,
	what string, want func([]string) bool) []string {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		said := held(t, a, of)
		if want(said) {
			return said
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: the permissions are %v", what, said)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// waitHas waits for a permission to be among them, and waitGone for it to be
// gone.
func waitHas(t *testing.T, a source.ACLInspector, of model.ACLFilter, want string) []string {
	t.Helper()
	return settledACLs(t, a, of, want+" never arrived", func(said []string) bool {
		return has(said, want)
	})
}

func waitGone(t *testing.T, a source.ACLInspector, of model.ACLFilter, want string) []string {
	t.Helper()
	return settledACLs(t, a, of, want+" is still there", func(said []string) bool {
		return !has(said, want)
	})
}

// A cluster with no authorizer keeps no permissions, and says what that means:
// an empty list would read as "nobody may do anything", where the truth is that
// everybody who can reach it may do everything.
func TestLiveAClusterWithNoAuthorizerKeepsNoPermissions(t *testing.T) {
	src := producing(t, source.Guard{})
	_, err := reads(t, src).ACLs(context.Background(), model.ACLFilter{})
	if err == nil {
		t.Fatal("a cluster with no authorizer answered a list of permissions")
	}
	says := func(err error, what string) {
		t.Helper()
		if err == nil {
			t.Fatalf("a cluster with no authorizer answered %s", what)
		}
		for _, want := range []string{"no authorizer", "everything is allowed"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: it says %q, which does not mention %q", what, err, want)
			}
		}
	}
	says(err, "a list of permissions")
	// And so does trying to change them, in the same words: somebody granting a
	// permission on an unsecured cluster is owed the reason it cannot be done
	// rather than a refusal in the protocol's own numbers.
	w := changes(t, src)
	acl := model.ACL{Principal: "User:ikigai_acl_none", Host: "*", Operation: model.ACLRead,
		Resource: model.ACLResource{Kind: model.ACLTopic, Name: "ikigai_it_acl"}}
	says(w.GrantACL(context.Background(), acl, false), "a grant")
	says(w.RevokeACL(context.Background(), acl, false), "a revoke")
}

// A permission is granted, read back as the sentence it was granted as, and
// taken away again.
func TestLiveAPermissionIsGrantedReadBackAndRevoked(t *testing.T) {
	src := aclCluster(t, source.Guard{})
	a, w := reads(t, src), changes(t, src)
	ctx := context.Background()
	// Granted with nowhere named, which means anywhere: a permission tied to no
	// host is what nearly every permission is, and the cluster writes it as a
	// star whether or not somebody typed one.
	acl := model.ACL{Principal: "User:ikigai_acl_one", Operation: model.ACLRead,
		Resource: model.ACLResource{Kind: model.ACLTopic, Name: "ikigai_it_acl"}}
	t.Cleanup(func() { w.RevokeACL(context.Background(), acl, false) })

	if err := w.GrantACL(ctx, acl, false); err != nil {
		t.Fatalf("granting: %v", err)
	}
	want := "User:ikigai_acl_one may read the topic ikigai_it_acl, from anywhere"
	// Found by asking about the topic it is about…
	about := model.ACLFilter{Kind: model.ACLTopic, Name: "ikigai_it_acl"}
	waitHas(t, a, about, want)
	// …and the cluster holds it as the star that means anywhere, which is what
	// it was granted as although nobody typed one.
	got, err := a.ACLs(ctx, about)
	if err != nil {
		t.Fatal(err)
	}
	for _, held := range got {
		if held.Principal == acl.Principal && held.Host != "*" {
			t.Errorf("it was granted from %q", held.Host)
		}
	}
	// …by asking about the principal, which answers that principal's and
	// nobody else's: a filter that answered everybody would be a list of the
	// wrong thing, and a long one on a real cluster.
	other := model.ACL{Principal: "User:ikigai_acl_other", Host: "*", Operation: model.ACLRead,
		Resource: model.ACLResource{Kind: model.ACLTopic, Name: "ikigai_it_acl"}}
	t.Cleanup(func() { w.RevokeACL(context.Background(), other, false) })
	if err := w.GrantACL(ctx, other, false); err != nil {
		t.Fatal(err)
	}
	// Both are there before the question is asked, so that the answer not
	// holding the other one is an answer rather than a race.
	waitHas(t, a, model.ACLFilter{}, other.String())
	said := waitHas(t, a, model.ACLFilter{Principal: acl.Principal}, want)
	if has(said, other.String()) {
		t.Errorf("asking about one principal answered another's: %v", said)
	}
	// …and in the whole cluster's, which is what the zero filter asks for.
	waitHas(t, a, model.ACLFilter{}, want)

	if err := w.RevokeACL(ctx, acl, false); err != nil {
		t.Fatalf("revoking: %v", err)
	}
	waitGone(t, a, about, want)
	// And revoking it again says so rather than reporting success for a
	// permission that was never there.
	err = w.RevokeACL(ctx, acl, false)
	if err == nil {
		t.Fatal("revoking a permission that is not there succeeded")
	}
	if !strings.Contains(err.Error(), "no such permission") {
		t.Errorf("it says %q", err)
	}
}

// Everything that reaches one topic is what asking about that topic answers:
// the permission written about its name, the one written about every topic, and
// any prefix of it. All three let somebody read it, so a list that showed only
// the first would be a list that lied about who can.
func TestLiveEverythingThatReachesATopicIsShown(t *testing.T) {
	src := aclCluster(t, source.Guard{})
	a, w := reads(t, src), changes(t, src)
	ctx := context.Background()
	exact := model.ACL{Principal: "User:ikigai_acl_exact", Host: "*", Operation: model.ACLRead,
		Resource: model.ACLResource{Kind: model.ACLTopic, Name: "ikigai_it_acl.eu"}}
	prefix := model.ACL{Principal: "User:ikigai_acl_prefix", Host: "*", Operation: model.ACLRead,
		Resource: model.ACLResource{Kind: model.ACLTopic, Name: "ikigai_it_acl.", Prefixed: true}}
	every := model.ACL{Principal: "User:ikigai_acl_every", Host: "*", Operation: model.ACLRead,
		Resource: model.ACLResource{Kind: model.ACLTopic}}
	for _, acl := range []model.ACL{exact, prefix, every} {
		acl := acl
		t.Cleanup(func() { w.RevokeACL(context.Background(), acl, false) })
		if err := w.GrantACL(ctx, acl, false); err != nil {
			t.Fatalf("granting %s: %v", acl, err)
		}
	}
	for _, acl := range []model.ACL{exact, prefix, every} {
		waitHas(t, a, model.ACLFilter{Kind: model.ACLTopic, Name: "ikigai_it_acl.eu"}, acl.String())
	}
	// A prefix that does not cover the topic is not among them: MATCH is not
	// "everything of this kind".
	other := model.ACL{Principal: "User:ikigai_acl_other", Host: "*", Operation: model.ACLRead,
		Resource: model.ACLResource{Kind: model.ACLTopic, Name: "somethingelse.", Prefixed: true}}
	t.Cleanup(func() { w.RevokeACL(context.Background(), other, false) })
	if err := w.GrantACL(ctx, other, false); err != nil {
		t.Fatal(err)
	}
	// Waited for where it does belong, so that its not being here is an answer.
	waitHas(t, a, model.ACLFilter{Kind: model.ACLTopic, Name: "somethingelse.x"}, other.String())
	if said := held(t, a, model.ACLFilter{Kind: model.ACLTopic, Name: "ikigai_it_acl.eu"}); has(said, other.String()) {
		t.Errorf("a prefix about other topics is shown as this one's: %v", said)
	}
}

// A deny is shown beside the allow it overrules. A list of only what is allowed
// would say somebody may do something the cluster refuses them.
func TestLiveADenyIsShownBesideTheAllowItOverrules(t *testing.T) {
	src := aclCluster(t, source.Guard{})
	a, w := reads(t, src), changes(t, src)
	ctx := context.Background()
	allow := model.ACL{Principal: "User:ikigai_acl_both", Host: "*", Operation: model.ACLRead,
		Resource: model.ACLResource{Kind: model.ACLTopic, Name: "ikigai_it_acl_deny"}}
	deny := allow
	deny.Deny = true
	for _, acl := range []model.ACL{allow, deny} {
		acl := acl
		t.Cleanup(func() { w.RevokeACL(context.Background(), acl, false) })
		if err := w.GrantACL(ctx, acl, false); err != nil {
			t.Fatalf("granting %s: %v", acl, err)
		}
	}
	about := model.ACLFilter{Kind: model.ACLTopic, Name: "ikigai_it_acl_deny"}
	waitHas(t, a, about, allow.String())
	waitHas(t, a, about, deny.String())
	// Revoking the allow leaves the deny: they are two permissions, not one
	// with a switch in it.
	if err := w.RevokeACL(ctx, allow, false); err != nil {
		t.Fatal(err)
	}
	said := waitGone(t, a, about, allow.String())
	if !has(said, deny.String()) {
		t.Errorf("revoking the allow took the refusal with it: %v", said)
	}
}

// A permission about the cluster itself is about a thing with no name, and is
// read and written as one.
func TestLiveAPermissionAboutTheClusterItself(t *testing.T) {
	src := aclCluster(t, source.Guard{})
	a, w := reads(t, src), changes(t, src)
	ctx := context.Background()
	acl := model.ACL{Principal: "User:ikigai_acl_cluster", Host: "*", Operation: model.ACLDescribe,
		Resource: model.ACLResource{Kind: model.ACLClusterItself}}
	t.Cleanup(func() { w.RevokeACL(context.Background(), acl, false) })
	if err := w.GrantACL(ctx, acl, false); err != nil {
		t.Fatalf("granting: %v", err)
	}
	want := "User:ikigai_acl_cluster may describe the cluster, from anywhere"
	waitHas(t, a, model.ACLFilter{Kind: model.ACLClusterItself}, want)
	// The name Kafka keeps for the cluster is not shown as a name a person
	// gave, because nobody gave it.
	acls, err := a.ACLs(ctx, model.ACLFilter{Kind: model.ACLClusterItself})
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range acls {
		if got.Resource.Name != "" {
			t.Errorf("the cluster is named %q", got.Resource.Name)
		}
	}
}

// Reading permissions is not a change, so a read-only connection reads them.
// Changing one is, and it is refused before a broker is asked anything.
func TestLiveAReadOnlyConnectionChangesNoPermission(t *testing.T) {
	src := aclCluster(t, source.Guard{ReadOnly: true})
	a, w := reads(t, src), changes(t, src)
	ctx := context.Background()
	if _, err := a.ACLs(ctx, model.ACLFilter{}); err != nil {
		t.Errorf("a read-only connection cannot read permissions: %v", err)
	}
	acl := model.ACL{Principal: "User:ikigai_acl_readonly", Host: "*", Operation: model.ACLRead,
		Resource: model.ACLResource{Kind: model.ACLTopic, Name: "ikigai_it_acl"}}
	if err := w.GrantACL(ctx, acl, false); !errors.Is(err, source.ErrReadOnly) {
		t.Errorf("a read-only connection granted a permission, or refused in other words: %v", err)
	}
	// Consent cannot buy its way past read-only: that is a setting about the
	// connection, not a question about this permission.
	if err := w.GrantACL(ctx, acl, true); !errors.Is(err, source.ErrReadOnly) {
		t.Errorf("consent bought its way past read-only: %v", err)
	}
	if err := w.RevokeACL(ctx, acl, true); !errors.Is(err, source.ErrReadOnly) {
		t.Errorf("a read-only connection revoked a permission: %v", err)
	}
	// And nothing was granted: the refusal came before the broker was asked.
	if said := held(t, a, model.ACLFilter{Principal: acl.Principal}); len(said) != 0 {
		t.Errorf("it granted %v", said)
	}
}

// A production connection asks first, every time, and the consent is for that
// one act rather than for the connection.
func TestLiveAProductionConnectionAsksBeforeChangingPermissions(t *testing.T) {
	src := aclCluster(t, source.Guard{Environment: source.EnvProduction})
	a, w := reads(t, src), changes(t, src)
	ctx := context.Background()
	acl := model.ACL{Principal: "User:ikigai_acl_prod", Host: "*", Operation: model.ACLRead,
		Resource: model.ACLResource{Kind: model.ACLTopic, Name: "ikigai_it_acl"}}
	t.Cleanup(func() { w.RevokeACL(context.Background(), acl, true) })

	if err := w.GrantACL(ctx, acl, false); !errors.Is(err, source.ErrConfirmationRequired) {
		t.Fatalf("a production connection granted a permission unasked: %v", err)
	}
	if said := held(t, a, model.ACLFilter{Principal: acl.Principal}); len(said) != 0 {
		t.Fatalf("it granted %v before asking", said)
	}
	if err := w.GrantACL(ctx, acl, true); err != nil {
		t.Fatalf("granting with consent: %v", err)
	}
	waitHas(t, a, model.ACLFilter{Principal: acl.Principal}, acl.String())
	// Revoking is its own act, and asks again.
	if err := w.RevokeACL(ctx, acl, false); !errors.Is(err, source.ErrConfirmationRequired) {
		t.Errorf("revoking did not ask: %v", err)
	}
	if err := w.RevokeACL(ctx, acl, true); err != nil {
		t.Errorf("revoking with consent: %v", err)
	}
}
