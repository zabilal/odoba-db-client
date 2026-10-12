package shell

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
)

// Who may do what to a cluster (T5.13, FR-13.15).
//
// The permissions are read where the thing they are about is, each of them is a
// sentence, and the sentence is what somebody agrees to before it is granted or
// taken away.

// clusterRef is the fake's cluster, which every permission of its own hangs on.
var clusterRef = model.NewRef(model.KindCluster, "cluster")

// aclFixture opens a connection of the given host's shape, loads its tree and
// selects one of its nodes, which is what the Permissions command works from.
func aclFixture(t *testing.T, host string, ref model.ObjectRef) (*fixture, *recordSource) {
	t.Helper()
	fx := newFixture(t)
	c, err := fx.conns.Create(store.SavedConnection{Name: "kafka1", Driver: "recordfake",
		Host: host}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fx.s.Explorer.Refresh(explorer.RootID)
	loaded(t, fx, explorer.RootID)
	loaded(t, fx, view.ConnectionID(c.ID))
	loaded(t, fx, view.NodeID(c.ID, clusterRef))
	fx.s.Explorer.Tree.Select(view.NodeID(c.ID, ref))
	fx.s.sync()
	return fx, recordSourceOf(t, fx, c.ID)
}

// readable is one permission, for a test to hold against what is shown.
func readable(principal, topic string) model.ACL {
	return model.ACL{Principal: principal, Host: "*", Operation: model.ACLRead,
		Resource: model.ACLResource{Kind: model.ACLTopic, Name: topic}}
}

// openPermissions opens the list and waits for it to have read them.
func openPermissions(t *testing.T, fx *fixture) *permissions {
	t.Helper()
	conn, n, ok := fx.s.Explorer.SelectedNode()
	if !ok {
		t.Fatal("nothing is selected")
	}
	about, said, okAbout := permissionAbout(n.Ref)
	if !okAbout {
		t.Fatalf("%s has no permissions", n.Ref)
	}
	p := &permissions{s: fx.s, connID: conn, about: about, said: said, chosen: -1}
	p.show()
	pump(t, fx.q, func() bool { return p.note.Text != "" })
	return p
}

// stacked is how many dialogs are open. The permissions list is one of them, so
// a question asked over it is the next one up rather than the top one being
// there at all.
func stacked(fx *fixture) int { return len(fx.s.win.Canvas().Overlays().List()) }

// A permission is read about the thing it is about: the topic, the group, every
// object of a kind where the class is what is selected, and everything the
// cluster holds where the cluster is.
func TestWhatAPermissionIsAskedAbout(t *testing.T) {
	for _, c := range []struct {
		ref   model.ObjectRef
		about model.ACLFilter
		said  string
	}{
		{topicRef, model.ACLFilter{Kind: model.ACLTopic, Name: "events"}, "the topic events"},
		{groupRef, model.ACLFilter{Kind: model.ACLGroup, Name: "readers"}, "the group readers"},
		{clusterRef, model.ACLFilter{}, "this cluster"},
		{model.ClassRef(clusterRef, model.KindTopic), model.ACLFilter{Kind: model.ACLTopic}, "topics"},
		{model.ClassRef(clusterRef, model.KindConsumerGroup), model.ACLFilter{Kind: model.ACLGroup}, "consumer groups"},
	} {
		about, said, ok := permissionAbout(c.ref)
		if !ok {
			t.Errorf("%s has no permissions", c.ref)
			continue
		}
		if about != c.about {
			t.Errorf("%s asks about %+v, want %+v", c.ref, about, c.about)
		}
		if said != c.said {
			t.Errorf("%s reads as %q, want %q", c.ref, said, c.said)
		}
	}
	// And a thing no permission can be about says so rather than opening an
	// empty list: a partition is part of a topic, and a subject belongs to the
	// registry rather than to the cluster.
	for _, ref := range []model.ObjectRef{
		model.NewRef(model.KindPartition, "cluster", "events", "0"),
		model.ClassRef(clusterRef, model.KindSubject),
		model.NewRef(model.KindTable, "shop", "orders"),
	} {
		if _, _, ok := permissionAbout(ref); ok {
			t.Errorf("%s was asked about", ref)
		}
	}
}

// The list shows each of them as the sentence it is, and the filter it asked
// with is the one the selected object needs.
func TestPermissionsAreShownAsSentences(t *testing.T) {
	fx, src := aclFixture(t, "kafka1", topicRef)
	src.acls = []model.ACL{readable("User:alice", "events"),
		{Principal: "User:bob", Host: "10.0.0.1", Operation: model.ACLWrite, Deny: true,
			Resource: model.ACLResource{Kind: model.ACLTopic, Name: "events"}}}
	p := openPermissions(t, fx)

	asks := src.aclAsks()
	if len(asks) != 1 || asks[0] != (model.ACLFilter{Kind: model.ACLTopic, Name: "events"}) {
		t.Fatalf("it asked %+v", asks)
	}
	// Drawn as the sentences, which is what the rows of the list are.
	for i, want := range []string{
		"User:alice may read the topic events, from anywhere",
		"User:bob may not write the topic events, from 10.0.0.1",
	} {
		item := p.list.CreateItem()
		p.list.UpdateItem(i, item)
		if got := item.(*widget.Label).Text; got != want {
			t.Errorf("row %d reads %q,\n          want %q", i, got, want)
		}
	}
	// And the line underneath says how many there are and the one thing about
	// them that is not in any single sentence.
	if !strings.Contains(p.note.Text, "2 permissions") ||
		!strings.Contains(p.note.Text, "refusal beats a permission") {
		t.Errorf("it says %q", p.note.Text)
	}
}

// An empty list says what it does not mean. A cluster with nothing written
// about it either refuses everybody or allows everybody, and which of those is
// a broker's setting rather than anything shown here.
func TestAnEmptyListSaysWhatItDoesNotMean(t *testing.T) {
	fx, _ := aclFixture(t, "kafka1", topicRef)
	p := openPermissions(t, fx)
	for _, want := range []string{"names anybody", "brokers"} {
		if !strings.Contains(p.note.Text, want) {
			t.Errorf("it says %q, which does not mention %q", p.note.Text, want)
		}
	}
	if p.note.Importance == widget.DangerImportance {
		t.Error("an empty list is shown as a failure")
	}
}

// A cluster that keeps no permissions at all says so where the list would be,
// in its own words: an empty list would read as "nobody may do anything", and
// the truth is the opposite.
func TestAClusterThatKeepsNoPermissionsSaysSoWhereTheListWouldBe(t *testing.T) {
	fx, src := aclFixture(t, "kafka1", clusterRef)
	src.aclErr = errors.New("kafka: this cluster keeps no permissions: no authorizer is configured")
	p := openPermissions(t, fx)
	if !strings.Contains(p.note.Text, "no authorizer") {
		t.Errorf("it says %q", p.note.Text)
	}
	if p.note.Importance != widget.DangerImportance {
		t.Error("a cluster that answers nothing is not shown as a problem")
	}
}

// A connection that may see permissions and not change them is offered neither
// Grant nor Revoke: a control that could only fail is worse than none.
func TestWhatCannotBeChangedOffersNoGrant(t *testing.T) {
	fx, src := aclFixture(t, "unmanaged", topicRef)
	src.acls = []model.ACL{readable("User:alice", "events")}
	p := openPermissions(t, fx)
	if p.manages() {
		t.Fatal("the connection claims to manage permissions")
	}
	if buttons := buttonLabels(p); len(buttons) != 0 {
		t.Errorf("it offers %v", buttons)
	}
	// And where it can, both are there.
	fx2, src2 := aclFixture(t, "kafka1", topicRef)
	src2.acls = src.acls
	p2 := openPermissions(t, fx2)
	if got := buttonLabels(p2); !slices.Contains(got, "Grant…") || !slices.Contains(got, "Revoke") {
		t.Errorf("it offers %v", got)
	}
}

// buttonLabels are the buttons the list offers, which is none where nothing can
// be changed.
func buttonLabels(p *permissions) []string {
	var out []string
	for _, o := range p.buttons.Objects {
		if b, ok := o.(*widget.Button); ok {
			out = append(out, b.Text)
		}
	}
	return out
}

// What was typed becomes the permission it describes, and what cannot be
// granted is refused rather than granted as something else.
func TestAPermissionIsReadFromWhatWasTyped(t *testing.T) {
	acl, err := aclFrom("User:alice", allowSaid, "read", "topic", "orders", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if acl.String() != "User:alice may read the topic orders, from anywhere" {
		t.Errorf("it reads as %q", acl)
	}
	// A name with no kind in front of it is a user, which is what nearly every
	// principal is: Kafka will not take it without one.
	acl, err = aclFrom("alice", allowSaid, "read", "topic", "orders", false, "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if acl.Principal != "User:alice" || acl.Host != "10.0.0.1" {
		t.Errorf("it reads as %+v", acl)
	}
	// A refusal is the other thing a permission can be.
	acl, err = aclFrom("User:bob", denySaid, "write", "group", "", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if !acl.Deny || acl.Resource.Kind != model.ACLGroup || acl.Resource.Name != "" {
		t.Errorf("a refusal reads as %+v", acl)
	}
	// A prefix is a name, so it needs one.
	if _, err := aclFrom("User:bob", allowSaid, "read", "topic", "", true, ""); err == nil {
		t.Error("a permission about names beginning with nothing was accepted")
	}
	if acl, err = aclFrom("User:ci", allowSaid, "read", "topic", " staging. ", true, ""); err != nil ||
		!acl.Resource.Prefixed || acl.Resource.Name != "staging." {
		t.Errorf("a prefix reads as %+v: %v", acl, err)
	}
	// The cluster is not named, because there is one.
	if _, err := aclFrom("User:bob", allowSaid, "alter", "cluster", "ours", false, ""); err == nil {
		t.Error("the cluster was given a name")
	}
	if _, err := aclFrom("User:bob", allowSaid, "alter", "cluster", "", false, ""); err != nil {
		t.Errorf("a permission about the cluster: %v", err)
	}
	// And what is missing or unknown is refused, in words about what to do.
	for _, c := range []struct {
		who, may, op, kind, says string
	}{
		{"", allowSaid, "read", "topic", "Who is the permission"},
		{"   ", allowSaid, "read", "topic", "Who is the permission"},
		{"User:a", "perhaps", "read", "topic", "allow or refuse"},
		{"User:a", allowSaid, "sniff", "topic", "What may they do"},
		{"User:a", allowSaid, "read", "wormhole", "What is the permission about"},
	} {
		_, err := aclFrom(c.who, c.may, c.op, c.kind, "x", false, "")
		if err == nil {
			t.Errorf("%+v was accepted", c)
			continue
		}
		if !strings.Contains(err.Error(), c.says) {
			t.Errorf("it says %q, which does not mention %q", err, c.says)
		}
	}
}

// Granting shows the sentence first, grants it when that is agreed to, and then
// reads the list again: what is shown is what the cluster says now.
func TestGrantingShowsTheSentenceAndReadsAgain(t *testing.T) {
	fx, src := aclFixture(t, "kafka1", topicRef)
	p := openPermissions(t, fx)
	acl := readable("User:alice", "events")

	open := stacked(fx)
	p.confirm("Grant This Permission?", acl.String()+".", "Grant", acl,
		func(s source.Source, confirmed bool) error {
			return app.GrantACL(fx.s.ctx, s, acl, confirmed)
		})
	pump(t, fx.q, func() bool { return stacked(fx) > open })
	// The sentence is what is agreed to, not "a permission will be granted".
	if text := labelText(fx.s.win.Canvas().Overlays().Top()); !strings.Contains(text, acl.String()) {
		t.Errorf("it asks %q", text)
	}
	tapOnTop(t, fx, "Cancel")
	if len(src.grants()) != 0 {
		t.Fatalf("saying no granted %v", src.grants())
	}

	p.confirm("Grant This Permission?", acl.String()+".", "Grant", acl,
		func(s source.Source, confirmed bool) error {
			return app.GrantACL(fx.s.ctx, s, acl, confirmed)
		})
	pump(t, fx.q, func() bool { return stacked(fx) > open })
	tapOnTop(t, fx, "Grant")
	pump(t, fx.q, func() bool { return len(src.grants()) == 1 })
	// And the list was read again, so what is on the screen is what happened.
	pump(t, fx.q, func() bool { return len(p.acls) == 1 })
	if p.acls[0] != acl {
		t.Errorf("the list holds %+v", p.acls)
	}
	if len(src.aclAsks()) < 2 {
		t.Errorf("it read the permissions %d times", len(src.aclAsks()))
	}
}

// Revoking takes away the one that is selected, and nothing at all where
// nothing is: a Revoke that guessed which one would be the worst kind of
// convenience.
func TestRevokingTakesTheChosenOne(t *testing.T) {
	fx, src := aclFixture(t, "kafka1", topicRef)
	src.acls = []model.ACL{readable("User:alice", "events"), readable("User:bob", "events")}
	p := openPermissions(t, fx)
	if len(p.acls) != 2 {
		t.Fatalf("it read %d permissions", len(p.acls))
	}
	// Nothing chosen, nothing asked: the only dialog open is the list itself.
	open := stacked(fx)
	p.revokeChosen()
	if stacked(fx) != open {
		t.Fatalf("it asked about revoking nothing: %q", labelText(fx.s.win.Canvas().Overlays().Top()))
	}
	// The second one, which is not the first: an off-by-one here revokes
	// somebody else's permission.
	p.list.Select(1)
	if p.chosen != 1 {
		t.Fatalf("choosing the second selected %d", p.chosen)
	}
	p.revokeChosen()
	pump(t, fx.q, func() bool { return stacked(fx) > open })
	text := labelText(fx.s.win.Canvas().Overlays().Top())
	if !strings.Contains(text, "User:bob") || strings.Contains(text, "User:alice") {
		t.Errorf("it asks about revoking %q", text)
	}
	tapOnTop(t, fx, "Revoke")
	pump(t, fx.q, func() bool { return len(src.revokes()) == 1 })
	if src.revokes()[0] != readable("User:bob", "events") {
		t.Errorf("it revoked %+v", src.revokes()[0])
	}
	// The list was read again and holds what is left, with nothing selected:
	// the row numbers have moved, and a selection that survived would point at
	// a different permission than the one somebody was looking at.
	pump(t, fx.q, func() bool { return len(p.acls) == 1 })
	if p.acls[0] != readable("User:alice", "events") {
		t.Errorf("what is left is %+v", p.acls)
	}
	if p.chosen != -1 {
		t.Errorf("row %d is still chosen", p.chosen)
	}
}

// A production connection asks before a permission changes, and nothing changes
// until it is answered.
func TestChangingAPermissionOnProductionAsksFirst(t *testing.T) {
	fx, src := aclFixture(t, "kafka1", topicRef)
	p := openPermissions(t, fx)
	src.refuses(fmt.Errorf("%w: changing permissions on a production connection",
		source.ErrConfirmationRequired))
	acl := readable("User:alice", "events")

	open := stacked(fx)
	fx.s.changeClusterThen(p.connID, "grant “"+acl.String()+"”",
		func(s source.Source, confirmed bool) error { return app.GrantACL(fx.s.ctx, s, acl, confirmed) },
		p.reload)
	pump(t, fx.q, func() bool { return stacked(fx) > open })
	if text := labelText(fx.s.win.Canvas().Overlays().Top()); !strings.Contains(text, "marked Production") ||
		!strings.Contains(text, acl.String()) {
		t.Errorf("it asks %q", text)
	}
	if len(src.grants()) != 0 {
		t.Fatalf("it granted %v before asking", src.grants())
	}
	typeOnTop(t, fx, "kafka1")
	tapOnTop(t, fx, "Continue")
	pump(t, fx.q, func() bool { return len(src.grants()) == 1 })
}

// The menu offers permissions on the things that have them, and the command is
// not offered where a source keeps none.
func TestPermissionsAreOfferedWhereThereAreSome(t *testing.T) {
	fx, _ := aclFixture(t, "kafka1", topicRef)
	conn, _, _ := fx.s.Explorer.SelectedNode()
	for _, ref := range []model.ObjectRef{topicRef, groupRef, clusterRef} {
		got := labels(fx.s.explorerMenu(view.NodeID(conn, ref)))
		if !slices.Contains(got, titleOf(fx, cmdPermissions)) {
			t.Errorf("%s does not offer permissions: %q", ref, got)
		}
	}
	if !fx.s.canReadPermissions() {
		t.Error("a topic on a cluster cannot read permissions")
	}
}

// The form offers every permission there is to grant: an operation or a kind
// the window left out would be one nobody could grant through it, and the list
// the model keeps is the one thing that says what there is.
func TestTheGrantFormOffersEveryPermissionThereIs(t *testing.T) {
	fx, _ := aclFixture(t, "kafka1", topicRef)
	p := openPermissions(t, fx)
	open := stacked(fx)
	p.grant()
	pump(t, fx.q, func() bool { return stacked(fx) > open })
	picks := selectsIn(fx.s.win.Canvas().Overlays().Top())
	if len(picks) != 3 {
		t.Fatalf("the form offers %d things to choose from", len(picks))
	}
	// May, do, to a: the three that are chosen rather than typed.
	if got := strings.Join(picks[0].Options, " · "); got != allowSaid+" · "+denySaid {
		t.Errorf("it offers %q as what a permission does", got)
	}
	ops := strings.Join(picks[1].Options, " · ")
	for _, op := range model.ACLOperations {
		if !strings.Contains(ops, string(op)) {
			t.Errorf("it does not offer %q: %s", op, ops)
		}
	}
	kinds := strings.Join(picks[2].Options, " · ")
	for _, k := range model.ACLResourceKinds {
		if !strings.Contains(kinds, string(k)) {
			t.Errorf("it does not offer %q: %s", k, kinds)
		}
	}
	// And it starts where the list was opened from, so granting a permission
	// on this topic is the thing the form is already about.
	if picks[2].Selected != string(model.ACLTopic) {
		t.Errorf("it is about a %q", picks[2].Selected)
	}
	if entries := entriesIn(fx.s.win.Canvas().Overlays().Top()); len(entries) < 3 ||
		entries[1].Text != "events" {
		t.Errorf("the name it is about is %+v", entries)
	}
}

// Permissions are not offered where a cluster keeps none, and not on a thing
// that cannot have any: a command that could only fail is worse than one that
// is not there.
func TestPermissionsAreNotOfferedWhereThereAreNone(t *testing.T) {
	fx, _ := aclFixture(t, "noacl", topicRef)
	if fx.s.canReadPermissions() {
		t.Error("a cluster that keeps no permissions offers to show them")
	}
	// And on a cluster that does keep them, not on a partition: a partition is
	// part of a topic, and permissions are about the topic.
	fx2, _ := aclFixture(t, "kafka1", topicRef)
	conn, _, _ := fx2.s.Explorer.SelectedNode()
	loaded(t, fx2, view.NodeID(conn, topicRef))
	fx2.s.Explorer.Tree.Select(view.NodeID(conn, partitionRef))
	fx2.s.sync()
	if _, n, ok := fx2.s.Explorer.SelectedNode(); !ok || n.Ref.Kind != model.KindPartition {
		t.Fatalf("the partition is not what is selected: %+v", n)
	}
	if fx2.s.canReadPermissions() {
		t.Error("a partition offers permissions of its own")
	}
	// Nothing selected at all is nothing to ask about.
	fx2.s.Explorer.Tree.UnselectAll()
	fx2.s.sync()
	if fx2.s.canReadPermissions() {
		t.Error("with nothing selected it offers permissions")
	}
}

// The form's own path: what was typed becomes the sentence that is agreed to,
// and then the permission that is granted. Nothing between the two is a guess.
func TestGrantingFromTheFormSaysWhatItWouldGrant(t *testing.T) {
	fx, src := aclFixture(t, "kafka1", topicRef)
	p := openPermissions(t, fx)
	open := stacked(fx)
	p.grant()
	pump(t, fx.q, func() bool { return stacked(fx) > open })
	form := fx.s.win.Canvas().Overlays().Top()
	entries := entriesIn(form)
	if len(entries) < 3 {
		t.Fatalf("the form has %d boxes", len(entries))
	}
	// Who, and nothing else: the form is already about this topic.
	entries[0].SetText("alice")
	tapOnTop(t, fx, "Continue")
	pump(t, fx.q, func() bool { return stacked(fx) > open })

	// The sentence, with the kind of principal filled in, is what is agreed to.
	want := "User:alice may read the topic events, from anywhere"
	if text := labelText(fx.s.win.Canvas().Overlays().Top()); !strings.Contains(text, want) {
		t.Errorf("it asks %q,\n     want it to say %q", text, want)
	}
	tapOnTop(t, fx, "Grant")
	pump(t, fx.q, func() bool { return len(src.grants()) == 1 })
	if got := src.grants()[0].String(); got != want {
		t.Errorf("it granted %q", got)
	}
	// And the list read itself again, so the screen shows what happened.
	pump(t, fx.q, func() bool { return len(p.acls) == 1 })
}
