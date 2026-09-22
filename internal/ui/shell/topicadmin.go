package shell

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Making, unmaking and reshaping topics (FR-13.12).
//
// Each of these changes the cluster, so each is asked about before it happens
// — not only on a production connection, which is a second question on top.
// Deleting a topic takes everything in it, and adding partitions cannot be
// undone; both say so in the words somebody reads before saying yes.

// selectionTopicAdmin reports whether what is selected sits on a connection
// that can make and unmake topics.
func (s *Shell) selectionTopicAdmin() bool {
	conn, _, ok := s.Explorer.SelectedNode()
	if !ok {
		return false
	}
	live, ok := s.d.WS.Get(conn)
	return ok && live.Source.Capabilities().Stream.TopicAdmin
}

// selectionIsTopic reports whether a topic is selected on such a connection.
func (s *Shell) selectionIsTopic() bool {
	_, n, ok := s.Explorer.SelectedNode()
	return ok && n.Ref.Kind == model.KindTopic && s.selectionTopicAdmin()
}

func (s *Shell) newTopic() {
	conn, _, ok := s.Explorer.SelectedNode()
	if !ok {
		return
	}
	name := widget.NewEntry()
	name.SetPlaceHolder("what the topic is called")
	partitions := widget.NewEntry()
	partitions.SetText("1")
	replication := widget.NewEntry()
	replication.SetText("1")
	settings := widget.NewMultiLineEntry()
	settings.SetPlaceHolder("optional; one per line, written as name: value")

	form := widget.NewForm(
		widget.NewFormItem("Name", name),
		widget.NewFormItem("Partitions", partitions),
		widget.NewFormItem("Copies of each", replication),
		widget.NewFormItem("Settings", settings),
	)
	d := dialog.NewCustomConfirm("New Topic", "Create", "Cancel", form, func(ok bool) {
		if !ok {
			return
		}
		spec, err := topicFrom(name.Text, partitions.Text, replication.Text, settings.Text)
		if err != nil {
			s.showError(err)
			return
		}
		s.changeTopics(conn, fmt.Sprintf("create %s", spec.Name),
			func(src source.Source, confirmed bool) error {
				spec.Confirmed = confirmed
				return app.CreateTopic(s.ctx, src, spec)
			})
	}, s.win)
	d.Resize(fyne.NewSize(520, 360))
	d.Show()
}

func (s *Shell) deleteSelectedTopic() {
	conn, n, ok := s.Explorer.SelectedNode()
	if !ok || n.Ref.Kind != model.KindTopic {
		return
	}
	topic := n.Ref.Name()
	d := dialog.NewConfirm("Delete This Topic?",
		fmt.Sprintf("“%s” and every record in it go, and nothing brings them back.", topic),
		func(yes bool) {
			if !yes {
				return
			}
			s.changeTopics(conn, fmt.Sprintf("delete %s", topic),
				func(src source.Source, confirmed bool) error {
					return app.DeleteTopic(s.ctx, src, topic, confirmed)
				})
		}, s.win)
	d.SetConfirmText("Delete")
	d.SetDismissText("Cancel")
	d.SetConfirmImportance(widget.DangerImportance)
	d.Show()
}

func (s *Shell) addPartitionsToSelected() {
	conn, n, ok := s.Explorer.SelectedNode()
	if !ok || n.Ref.Kind != model.KindTopic {
		return
	}
	topic := n.Ref.Name()
	count := widget.NewEntry()
	count.SetText("1")
	note := widget.NewLabel("A topic cannot lose partitions again, and which partition a key lands in " +
		"changes for every record written afterwards: records already written stay where they are, " +
		"and new ones with the same key may not join them.")
	note.Wrapping = fyne.TextWrapWord
	body := container.NewVBox(widget.NewForm(widget.NewFormItem("Add", count)), note)

	d := dialog.NewCustomConfirm("Add Partitions to "+topic, "Add", "Cancel",
		body, func(ok bool) {
			if !ok {
				return
			}
			add, err := partitionsFrom(count.Text)
			if err != nil {
				s.showError(err)
				return
			}
			s.changeTopics(conn, fmt.Sprintf("add partitions to %s", topic),
				func(src source.Source, confirmed bool) error {
					return app.AddPartitions(s.ctx, src, topic, add, confirmed)
				})
		}, s.win)
	d.Resize(fyne.NewSize(520, 300))
	d.Show()
}

// topicChange is one change to the cluster's topics, made with or without
// consent having been given for it.
type topicChange func(src source.Source, confirmed bool) error

// changeTopics makes the change, asks where the connection says to ask, and
// refreshes the tree where it worked.
func (s *Shell) changeTopics(connID, what string, change topicChange) {
	s.attemptTopicChange(connID, what, change, false)
}

func (s *Shell) attemptTopicChange(connID, what string, change topicChange, confirmed bool) {
	go func() {
		live, err := s.d.WS.Connect(s.ctx, connID)
		if err == nil {
			err = change(live.Source, confirmed)
		}
		s.d.Run(func() {
			switch {
			case errors.Is(err, source.ErrConfirmationRequired):
				s.askBeforeChangingTopics(connID, what, change)
			case errors.Is(err, source.ErrReadOnly):
				s.showError(fmt.Errorf("not done: this connection is read-only, and to %s would change the cluster", what))
			case err != nil:
				s.showError(fmt.Errorf("could not %s: %w", what, err))
			default:
				s.refreshSelected()
			}
		})
	}()
}

// askBeforeChangingTopics is the production guardrail. The driver refused
// before it dialled, so nothing has happened yet and asking is safe (FR-4.9).
func (s *Shell) askBeforeChangingTopics(connID, what string, change topicChange) {
	c, _ := s.d.Conns.Get(connID)
	d := dialog.NewConfirm("Change Structure on Production?",
		fmt.Sprintf("This would %s on “%s”, which is marked Production. Nothing has happened yet.", what, c.Name),
		func(yes bool) {
			if !yes {
				return
			}
			s.attemptTopicChange(connID, what, change, true)
		}, s.win)
	d.SetConfirmText("Continue")
	d.SetDismissText("Cancel")
	d.SetConfirmImportance(widget.DangerImportance)
	d.Show()
}

// topicFrom reads what was typed into the topic it describes.
func topicFrom(name, partitions, replication, settings string) (source.TopicSpec, error) {
	spec := source.TopicSpec{Name: strings.TrimSpace(name)}
	if spec.Name == "" {
		return source.TopicSpec{}, errors.New("a topic needs a name")
	}
	n, err := countFrom(partitions, "partitions")
	if err != nil {
		return source.TopicSpec{}, err
	}
	spec.Partitions = n
	r, err := countFrom(replication, "copies of each partition")
	if err != nil {
		return source.TopicSpec{}, err
	}
	spec.ReplicationFactor = int16(r)
	if spec.Config, err = settingsFrom(settings); err != nil {
		return source.TopicSpec{}, err
	}
	return spec, nil
}

// partitionsFrom reads how many partitions to add.
func partitionsFrom(text string) (int32, error) {
	return countFrom(text, "partitions to add")
}

// countFrom reads a count that has to be at least one, and says what it was
// counting when it is not.
func countFrom(text, what string) (int32, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(text), 10, 32)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("the %s is a whole number from 1 upwards, and %q is not", what, text)
	}
	return int32(n), nil
}

// settingsFrom reads a topic's settings, one per line.
func settingsFrom(text string) (map[string]string, error) {
	pairs, err := pairsFrom(text, "setting")
	if err != nil {
		return nil, err
	}
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		out[p[0]] = p[1]
	}
	return out, nil
}

// pairsFrom reads lines written as name: value, which is how both a record's
// headers and a topic's settings are typed.
//
// The first colon separates, because a value may hold one — a trace parent
// and a URL both do — and a name may not. what names the thing being read, so
// that a line which is not one says so in the reader's own words.
func pairsFrom(text, what string) ([][2]string, error) {
	var out [][2]string
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, value, found := strings.Cut(line, ":")
		if !found {
			return nil, fmt.Errorf("%s %d is %q, which is not a name and a value with a colon between them", what, i+1, line)
		}
		if name = strings.TrimSpace(name); name == "" {
			return nil, fmt.Errorf("%s %d has a value and no name to go with it", what, i+1)
		}
		out = append(out, [2]string{name, strings.TrimSpace(value)})
	}
	return out, nil
}
