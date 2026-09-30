package shell

import (
	"errors"
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Who may do what to a cluster (FR-13.15, T5.13).
//
// Permissions are read where the thing they are about is: on a topic, on a
// consumer group, on the class either of those hangs under, and on the cluster
// itself, which is where every permission it holds can be seen at once. Asking
// about one object answers everything that reaches it — the permission written
// about its name, the one written about every name of its kind, and any prefix
// of it — because all three let somebody in, and a list showing only the first
// would be a list that lied about who can.
//
// Each of them is a sentence, and the sentence is what somebody agrees to
// before it is granted or taken away: "User:alice may read the topic orders,
// from anywhere". A grid of five columns would be the same facts with the
// meaning taken out.
//
// Granting and revoking are AccessAdmin, and the driver refuses before it dials
// where the connection is read-only or wants asking (FR-4.9). Nothing here
// relaxes that.

// permissionAbout is the resource a node's permissions are about, and how to
// say it. False where a node is nothing a permission can be about.
func permissionAbout(ref model.ObjectRef) (model.ACLFilter, string, bool) {
	switch ref.Kind {
	case model.KindTopic:
		return model.ACLFilter{Kind: model.ACLTopic, Name: ref.Name()}, "the topic " + ref.Name(), true
	case model.KindConsumerGroup:
		return model.ACLFilter{Kind: model.ACLGroup, Name: ref.Name()}, "the group " + ref.Name(), true
	case model.KindCluster:
		// Everything the cluster holds, which is the only place to see it.
		return model.ACLFilter{}, "this cluster", true
	case model.KindFolder:
		// The class a kind of object hangs under: every permission about that
		// kind, which is the question "who may read any topic".
		switch k, ok := model.ClassOf(ref); {
		case ok && k == model.KindTopic:
			return model.ACLFilter{Kind: model.ACLTopic}, "topics", true
		case ok && k == model.KindConsumerGroup:
			return model.ACLFilter{Kind: model.ACLGroup}, "consumer groups", true
		}
	}
	return model.ACLFilter{}, "", false
}

// canReadPermissions reports whether what is selected has permissions to show.
func (s *Shell) canReadPermissions() bool {
	conn, n, ok := s.Explorer.SelectedNode()
	if !ok {
		return false
	}
	if _, _, about := permissionAbout(n.Ref); !about {
		return false
	}
	live, ok := s.d.WS.Get(conn)
	return ok && live.Source.Capabilities().Stream.ACLs
}

// permissions is the open list of them for one object.
type permissions struct {
	s      *Shell
	connID string
	about  model.ACLFilter
	said   string

	acls    []model.ACL
	chosen  int // -1 when no row is selected
	list    *widget.List
	note    *widget.Label
	revoke  *widget.Button
	buttons *fyne.Container
}

// showPermissions opens the list for what is selected.
func (s *Shell) showPermissions() {
	conn, n, ok := s.Explorer.SelectedNode()
	if !ok {
		return
	}
	about, said, okAbout := permissionAbout(n.Ref)
	if !okAbout {
		return
	}
	p := &permissions{s: s, connID: conn, about: about, said: said, chosen: -1}
	p.show()
}

// manages reports whether this connection can change what it is showing.
func (p *permissions) manages() bool {
	live, ok := p.s.d.WS.Get(p.connID)
	return ok && live.Source.Capabilities().Stream.ManageACLs
}

func (p *permissions) show() {
	p.list = widget.NewList(
		func() int { return len(p.acls) },
		func() fyne.CanvasObject {
			l := widget.NewLabel("")
			l.Truncation = fyne.TextTruncateEllipsis
			return l
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i < len(p.acls) {
				o.(*widget.Label).SetText(p.acls[i].String())
			}
		})
	p.list.OnSelected = func(i widget.ListItemID) {
		p.chosen = i
		p.revoke.Enable()
	}
	p.list.OnUnselected = func(widget.ListItemID) {
		p.chosen = -1
		p.revoke.Disable()
	}
	p.note = widget.NewLabel("")
	p.note.Wrapping = fyne.TextWrapWord

	p.revoke = widget.NewButton("Revoke", p.revokeChosen)
	p.revoke.Disable()
	p.buttons = container.NewHBox()
	if p.manages() {
		// Only where this connection can change them: a Grant that could only
		// fail is worse than no Grant at all.
		p.buttons.Add(widget.NewButton("Grant…", p.grant))
		p.buttons.Add(p.revoke)
	}
	body := container.NewBorder(nil, container.NewVBox(p.note, p.buttons), nil, nil, p.list)
	d := dialog.NewCustom("Permissions on "+p.said, "Close", body, p.s.win)
	d.Resize(fyne.NewSize(680, 460))
	d.Show()
	p.reload()
}

// reload reads them again. What is shown is what the cluster says now, so a
// grant or a revoke is followed by this rather than by a guess at the result.
func (p *permissions) reload() {
	go func() {
		live, err := p.s.d.WS.Connect(p.s.ctx, p.connID)
		var acls []model.ACL
		if err == nil {
			acls, err = app.ACLs(p.s.ctx, live.Source, p.about)
		}
		p.s.d.Run(func() {
			p.acls = acls
			// Unselecting is what puts the choice back: the rows have moved,
			// and a selection that survived would point at a different
			// permission than the one somebody was looking at.
			p.list.UnselectAll()
			p.list.Refresh()
			p.revoke.Disable()
			p.say(err)
		})
	}()
}

// say puts what the list means underneath it: a refusal in the cluster's own
// terms, or what an empty list does and does not tell somebody.
func (p *permissions) say(err error) {
	switch {
	case err != nil:
		p.note.Importance = widget.DangerImportance
		p.note.SetText(err.Error())
	case len(p.acls) == 0:
		p.note.Importance = widget.MediumImportance
		p.note.SetText("No permission here names anybody. What that means is the brokers' " +
			"setting rather than something written here: a cluster either refuses everybody " +
			"it has not been told about, or allows them.")
	default:
		p.note.Importance = widget.MediumImportance
		p.note.SetText(fmt.Sprintf("%s. A refusal beats a permission wherever both reach.",
			nounCount(len(p.acls), "permission")))
	}
	p.note.Refresh()
}

// grant asks what to grant, then shows the sentence it would add.
func (p *permissions) grant() {
	principal := widget.NewEntry()
	principal.SetPlaceHolder("User:alice")
	allow := widget.NewSelect([]string{allowSaid, denySaid}, nil)
	allow.Selected = allowSaid
	op := widget.NewSelect(choices(model.ACLOperations), nil)
	op.Selected = string(model.ACLRead)
	kind := widget.NewSelect(choices(model.ACLResourceKinds), nil)
	kind.Selected = string(model.ACLTopic)
	if p.about.Kind != "" {
		kind.Selected = string(p.about.Kind)
	}
	name := widget.NewEntry()
	name.SetText(p.about.Name)
	name.SetPlaceHolder("every one of them")
	prefixed := widget.NewCheck("the name written is the beginning of a name", nil)
	host := widget.NewEntry()
	host.SetPlaceHolder("anywhere")

	form := widget.NewForm(
		widget.NewFormItem("Who", principal),
		widget.NewFormItem("May", allow),
		widget.NewFormItem("Do", op),
		widget.NewFormItem("To a", kind),
		widget.NewFormItem("Called", name),
		widget.NewFormItem("", prefixed),
		widget.NewFormItem("From", host),
	)
	form.Items[0].HintText = "as the cluster writes it; a name with no kind is a user"
	form.Items[4].HintText = "leave it empty for every one of them"

	d := dialog.NewCustomConfirm("Grant a Permission", "Continue", "Cancel", form, func(ok bool) {
		if !ok {
			return
		}
		acl, err := aclFrom(principal.Text, allow.Selected, op.Selected, kind.Selected,
			name.Text, prefixed.Checked, host.Text)
		if err != nil {
			p.s.showError(err)
			return
		}
		p.confirm("Grant This Permission?", acl.String()+".", "Grant", acl,
			func(src source.Source, confirmed bool) error {
				return app.GrantACL(p.s.ctx, src, acl, confirmed)
			})
	}, p.s.win)
	d.Resize(fyne.NewSize(560, 420))
	d.Show()
}

// revokeChosen takes away the one that is selected.
func (p *permissions) revokeChosen() {
	if p.chosen < 0 {
		return
	}
	acl := p.acls[p.chosen]
	p.confirm("Revoke This Permission?", acl.String()+" — and will not, once this is done.",
		"Revoke", acl, func(src source.Source, confirmed bool) error {
			return app.RevokeACL(p.s.ctx, src, acl, confirmed)
		})
}

// confirm shows the sentence and makes the change if it is agreed to.
func (p *permissions) confirm(title, body, verb string, acl model.ACL, change clusterChange) {
	d := dialog.NewConfirm(title, body, func(yes bool) {
		if !yes {
			return
		}
		p.s.changeClusterThen(p.connID, strings.ToLower(verb)+" “"+acl.String()+"”", change, p.reload)
	}, p.s.win)
	d.SetConfirmText(verb)
	d.SetDismissText("Cancel")
	if verb == "Revoke" {
		d.SetConfirmImportance(widget.DangerImportance)
	}
	d.Show()
}

// The two words for what a permission does, as a person chooses between them.
const (
	allowSaid = "may"
	denySaid  = "may not"
)

// choices is a list of words a window offers, as a Select takes them.
func choices[T ~string](of []T) []string {
	out := make([]string, 0, len(of))
	for _, v := range of {
		out = append(out, string(v))
	}
	return out
}

// aclFrom reads what was typed into the permission it describes, refusing what
// cannot be granted rather than granting something else.
func aclFrom(principal, may, op, kind, name string, prefixed bool, host string) (model.ACL, error) {
	who := strings.TrimSpace(principal)
	if who == "" {
		return model.ACL{}, formError("Who is the permission about? A principal, as the cluster writes it.")
	}
	if !strings.Contains(who, ":") {
		// Kafka writes a principal as its kind and its name. A name on its own
		// is a user, which is what nearly every principal is.
		who = "User:" + who
	}
	if may != allowSaid && may != denySaid {
		return model.ACL{}, formError("Does this permission allow or refuse?")
	}
	acl := model.ACL{Principal: who, Host: strings.TrimSpace(host), Deny: may == denySaid}
	for _, o := range model.ACLOperations {
		if string(o) == op {
			acl.Operation = o
		}
	}
	if acl.Operation == "" {
		return model.ACL{}, formError("What may they do? Choose one of the operations.")
	}
	for _, k := range model.ACLResourceKinds {
		if string(k) == kind {
			acl.Resource.Kind = k
		}
	}
	if acl.Resource.Kind == "" {
		return model.ACL{}, formError("What is the permission about? Choose one of the kinds.")
	}
	acl.Resource.Name = strings.TrimSpace(name)
	acl.Resource.Prefixed = prefixed
	if acl.Resource.Prefixed && acl.Resource.Name == "" {
		return model.ACL{}, formError("A permission about names beginning with something needs " +
			"the something. Leave the box unticked for every one of them.")
	}
	if acl.Resource.Kind == model.ACLClusterItself && acl.Resource.Name != "" {
		return model.ACL{}, errors.New("there is one cluster, and it is not named: leave Called empty")
	}
	return acl, nil
}
