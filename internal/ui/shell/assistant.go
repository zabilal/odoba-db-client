package shell

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/assistant"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
)

// The assistant, in the window (FR-14).
//
// Two dialogs and a panel. The dialogs are where somebody says what may
// happen — which provider, which model, and whether this connection may be
// asked about at all — and the panel is where they ask. Nothing about what is
// allowed is decided here: internal/assistant decides, from the settings and
// the connection, and this asks it (ADR-0159).
//
// What the window adds is the saying-so. Every place the assistant appears says
// what it may see and what answered, because somebody about to send their
// schema to a company should not have to open Settings to find out that they
// are (FR-14.5).

// assistantVaultID is the name the provider keys are kept under. Not a
// connection's ID: a key that shared a namespace with a connection's password
// would be deleted with that connection.
const assistantVaultID = "assistant"

// assistantSecrets reads a provider's key from the vault, which is where every
// credential in this application lives (FR-1.5, NFR-S1).
func (s *Shell) assistantSecrets() assistant.Secrets {
	return func(name string) (string, error) {
		return s.d.Conns.Vault().Get(assistantVaultID, name)
	}
}

// assistantSettings are the settings as they stand, or nil where nobody has
// configured one — and nil too where there is no settings file, a window
// without one having nowhere to keep a provider.
func (s *Shell) assistantSettings() *store.Assistant {
	if s.d.Settings == nil {
		return nil
	}
	return s.d.Settings.Get().Assistant
}

// canSetUpAssistant reports whether the assistant can be configured: there has
// to be a settings file to write the provider to, and the vault has to be open,
// a key not being saveable into a shut one.
func (s *Shell) canSetUpAssistant() bool {
	return s.d.Settings != nil && s.d.Conns.VaultOpen()
}

// setUpAssistant is where somebody says which model to ask and turns it on.
func (s *Shell) setUpAssistant() {
	was := s.assistantSettings()
	if was == nil {
		was = &store.Assistant{}
	}
	providers := assistant.Builtin()
	names := make([]string, len(providers))
	at := 0
	for i, p := range providers {
		names[i] = p.Name
		if strings.EqualFold(p.ID, was.Provider) {
			at = i
		}
	}
	pick := widget.NewSelect(names, nil)
	model := widget.NewEntry()
	model.SetText(was.Model)
	endpoint := widget.NewEntry()
	endpoint.SetText(was.Endpoint)
	header := widget.NewEntry()
	header.SetText(was.Header)
	key := newSecretEntry()
	on := widget.NewCheck("Turn the assistant on", nil)
	on.SetChecked(was.Enabled)

	// The placeholders follow the provider, because the model's name and the
	// address are each that provider's own and a blank field says nothing.
	pick.OnChanged = func(string) {
		p := providers[max(pick.SelectedIndex(), 0)]
		endpoint.SetPlaceHolder(p.Endpoint)
		switch p.ID {
		case "openai":
			model.SetPlaceHolder("gpt-4o-mini")
		case "anthropic":
			model.SetPlaceHolder("claude-sonnet-4-5")
		default:
			model.SetPlaceHolder("the name your server gives it")
		}
		if p.Secret == "" {
			key.SetPlaceHolder("usually none")
			return
		}
		key.SetPlaceHolder("kept in the keychain")
	}
	pick.SetSelectedIndex(at)

	items := []*widget.FormItem{
		widget.NewFormItem("Provider", pick),
		widget.NewFormItem("Model", model),
		widget.NewFormItem("Address", endpoint),
		widget.NewFormItem("Key", key),
		widget.NewFormItem("Key header", header),
		widget.NewFormItem("", on),
		widget.NewFormItem("", quiet(
			"What it may see is set per connection, and is nothing until you say otherwise. "+
				"A key is kept in the keychain, never in the settings file. "+
				"Plain http:// is allowed only to a model on this machine.")),
	}
	items[2].HintText = "Leave empty for the provider's own. A model of your own goes here."
	items[4].HintText = "Only for a gateway that wants the key in a header of its own."

	d := dialog.NewForm("The Assistant", "Save", "Cancel", items, func(ok bool) {
		if !ok {
			return
		}
		s.saveAssistant(providers[max(pick.SelectedIndex(), 0)], store.Assistant{
			Enabled:  on.Checked,
			Provider: providers[max(pick.SelectedIndex(), 0)].ID,
			Model:    strings.TrimSpace(model.Text),
			Endpoint: strings.TrimSpace(endpoint.Text),
			Header:   strings.TrimSpace(header.Text),
		}, key.Text)
	}, s.win)
	d.Resize(fyne.NewSize(560, d.MinSize().Height))
	d.Show()
}

// saveAssistant writes the settings and the key, refusing a provider that could
// not be asked anything.
//
// Refused here rather than at the first question, because a setting saved and
// then rejected is a setting somebody thinks they made.
func (s *Shell) saveAssistant(p assistant.Provider, want store.Assistant, key string) {
	ready := p
	ready.Model = want.Model
	if want.Endpoint != "" {
		ready.Endpoint = want.Endpoint
	}
	if want.Enabled {
		if err := ready.Valid(); err != nil {
			s.showError(fmt.Errorf("the assistant could not be turned on: %w", err))
			return
		}
	}
	if k := strings.TrimSpace(key); k != "" && p.Secret != "" {
		if err := s.d.Conns.Vault().Set(assistantVaultID, p.Secret, k); err != nil {
			s.showError(fmt.Errorf("the key could not be saved: %w", err))
			return
		}
	}
	if err := s.d.Settings.Update(func(st *store.Settings) error {
		st.Assistant = &want
		return nil
	}); err != nil {
		s.showError(fmt.Errorf("the settings could not be saved: %w", err))
		return
	}
	// The menus first, and what happened after: sync writes the status line
	// from the selection, so a message set before it would be replaced by it.
	s.sync()
	if want.Enabled {
		s.status.SetText("The assistant is on, with " + p.Name + " and " + want.Model +
			". Each connection decides what it may see.")
	} else {
		s.status.SetText("The assistant is off.")
	}
}

// canAskAssistant reports whether there is a connection the assistant may be
// asked about. A command offered where it cannot work is worse than one that is
// not there (REQ-DB-2).
func (s *Shell) canAskAssistant() bool {
	c, ok := s.assistantConnection()
	return ok && assistant.Ready(s.assistantSettings(), c)
}

// assistantConnection is the connection a question would be about: the tab in
// front, or else the explorer's selection.
//
// The tab wins because a question asked from a query tab is about that query,
// and the explorer's selection may be somewhere else entirely. The selection is
// read as the connection a row belongs to rather than as a selected object, so
// that a question can be asked with nothing but the connection picked out --
// which is the state somebody is in before they have opened anything.
func (s *Shell) assistantConnection() (*store.SavedConnection, bool) {
	id := ""
	if t := s.activeTab(); t != nil {
		id = t.connID
	}
	if id == "" {
		id, _ = view.ConnectionOf(s.Explorer.Selected())
	}
	if id == "" {
		return nil, false
	}
	c, ok := s.d.Conns.Get(id)
	if !ok {
		return nil, false
	}
	return &c, true
}

// askAssistant is the way in: a question, what it may see said beside it, and
// the answer.
func (s *Shell) askAssistant() {
	c, ok := s.assistantConnection()
	if !ok {
		s.showError(formError("Open a connection to ask the assistant about it."))
		return
	}
	st := s.assistantSettings()
	p, hasProvider := assistant.ProviderFor(st)
	if !hasProvider {
		s.showError(formError("No assistant provider is set up. Choose one in the Assistant settings."))
		return
	}
	consent := assistant.ConsentFor(st, c, false)
	if err := consent.Allow(false); err != nil {
		s.showError(fmt.Errorf("the assistant cannot be asked about %s: %w", c.Name, err))
		return
	}
	question := widget.NewMultiLineEntry()
	question.SetPlaceHolder("How many orders did each person place last month?")
	question.Wrapping = fyne.TextWrapWord
	// The rows switch is offered only where the connection allows it, and says
	// what it would do: a tick that did nothing would read as one that did.
	withRows := widget.NewCheck("Send a few rows as well as the schema", nil)
	if !consent.Data {
		withRows.Disable()
	}
	items := []*widget.FormItem{
		widget.NewFormItem("Question", question),
		widget.NewFormItem("", withRows),
		widget.NewFormItem("", quiet(p.Name+" · "+p.Model+" — "+consent.Describe())),
	}
	d := dialog.NewForm("Ask the Assistant", "Ask", "Cancel", items, func(ok bool) {
		if !ok {
			return
		}
		s.runAsk(c, p, assistant.KindQuery, strings.TrimSpace(question.Text), withRows.Checked)
	}, s.win)
	d.Resize(fyne.NewSize(600, d.MinSize().Height))
	d.Show()
}

// canExplainWithAssistant reports whether there is a statement to explain.
func (s *Shell) canExplainWithAssistant() bool {
	if !s.canAskAssistant() {
		return false
	}
	t := s.activeTab()
	return t != nil && t.query != nil && strings.TrimSpace(t.query.editor.Document().Text()) != ""
}

// explainWithAssistant asks what the statement in the editor does (FR-14.2).
func (s *Shell) explainWithAssistant() {
	t := s.activeTab()
	if t == nil || t.query == nil {
		return
	}
	c, ok := s.assistantConnection()
	if !ok {
		return
	}
	p, hasProvider := assistant.ProviderFor(s.assistantSettings())
	if !hasProvider {
		s.showError(formError("No assistant provider is set up. Choose one in the Assistant settings."))
		return
	}
	statement := strings.TrimSpace(statementInEditor(t))
	if statement == "" {
		s.showError(formError("There is nothing in the editor to explain."))
		return
	}
	s.runAsk(c, p, assistant.KindExplain, statement, false)
}

// runAsk gathers the grounding, asks, and shows the answer. It is a task, so it
// says it is happening and can be given up on.
func (s *Shell) runAsk(c *store.SavedConnection, p assistant.Provider,
	kind assistant.Kind, question string, withRows bool) {
	if question == "" {
		s.showError(formError("There is no question to ask."))
		return
	}
	k := s.startTask(nil, "Asking "+p.Name, nil)
	connID := c.ID
	saved := *c
	settings := s.assistantSettings()
	client := &assistant.Client{Secret: s.assistantSecrets()}
	go func() {
		ctx, cancel := context.WithTimeout(s.ctx, assistantTimeout)
		defer cancel()
		g, err := s.grounding(ctx, connID, withRows)
		var answer *assistant.Answer
		if err == nil {
			answer, err = client.Ask(ctx, p, assistant.ConsentFor(settings, &saved, false),
				assistant.Request{Kind: kind, Question: question, Grounding: g})
		}
		s.d.Run(func() {
			if s.ctx.Err() != nil {
				return
			}
			if err != nil {
				s.endTask(k, taskFailed, err.Error())
				s.showError(fmt.Errorf("the assistant could not answer: %w", err))
				return
			}
			s.endTask(k, taskDone, "Answered")
			s.showAnswer(connID, answer)
		})
	}()
}

// assistantTimeout bounds one question. A model that has not answered in two
// minutes is not going to, and a window waiting on one for longer is a window
// that looks broken.
const assistantTimeout = 2 * time.Minute

// grounding is what the model is told about a connection: its schema, and a few
// rows where that was asked for and allowed.
func (s *Shell) grounding(ctx context.Context, connID string, withRows bool) (assistant.Grounding, error) {
	live, err := s.d.WS.Connect(ctx, connID)
	if err != nil {
		return assistant.Grounding{}, err
	}
	info, _ := live.Source.Info(ctx)
	dialect := live.Source.Capabilities().Query.Language
	db, err := app.Snapshot(ctx, live.Source, "")
	if err != nil {
		return assistant.Grounding{}, fmt.Errorf("the schema could not be read: %w", err)
	}
	g := assistant.FromSchema(db, info.Product, dialect, nil)
	if !withRows {
		return g, nil
	}
	// Rows are read only where they were asked for: gathering them and then
	// being refused would be reading somebody's data for nothing.
	sample, err := s.sampleRows(ctx, live, db)
	if err != nil {
		return g, nil // the schema alone is still an answerable question
	}
	if len(sample.Rows) > 0 {
		g.Rows = []assistant.Sample{sample}
	}
	return g, nil
}

// showAnswer puts an answer in front of somebody: the statement in an editor if
// there is one, and the words otherwise.
//
// Nothing is run. A generated statement lands in the editor for review, which is
// FR-14.6 and is why this calls openScript and not anything that executes
// (ADR-0011 §15).
func (s *Shell) showAnswer(connID string, a *assistant.Answer) {
	if a == nil {
		return
	}
	statement, ok := assistant.Statement(a.Text)
	if !ok {
		// An answer with no statement in it is an answer: a model saying the
		// question cannot be answered from the schema is telling somebody
		// something, and guessing a statement out of it would put something
		// nobody asked for in their editor.
		s.showAnswerText(a)
		return
	}
	t := s.openScript(connID, "-- "+a.Summary()+"\n"+
		"-- Nothing here has run. Read it before you do.\n\n"+statement+"\n")
	if t != nil {
		s.status.SetText("The assistant answered with a statement. It has not run — " + a.Summary() + ".")
	}
}

// showAnswerText shows words rather than a statement, in a window of their own
// with what answered at the top.
func (s *Shell) showAnswerText(a *assistant.Answer) {
	body := widget.NewMultiLineEntry()
	body.Wrapping = fyne.TextWrapWord
	body.SetText(a.Text)
	body.Disable()
	said := widget.NewLabel(a.Summary())
	said.Importance = widget.LowImportance
	content := container.NewBorder(said, nil, nil, nil,
		container.NewGridWrap(fyne.NewSize(620, 320), body))
	d := dialog.NewCustom("The Assistant", "Close", content, s.win)
	d.Resize(fyne.NewSize(660, 420))
	d.Show()
	s.status.SetText("The assistant answered — " + a.Summary() + ".")
}

// statementInEditor is what a question about "this statement" is about: the
// selection where there is one, and the whole script otherwise — the same
// reading Run makes, so that Explain explains what Run would run.
func statementInEditor(t *tab) string {
	doc := t.query.editor.Document()
	if _, _, sel := doc.Selection(); sel {
		return doc.SelectedText()
	}
	return doc.Text()
}

// sampleRows is a few rows of one table, for a question that asked for them.
//
// The first table the schema holds, because a question about a schema is about
// the schema and one table's rows are there to show a model what a value looks
// like — not to answer the question. Which table is not worth a dialog, and
// sending every table's rows would be sending the database.
func (s *Shell) sampleRows(ctx context.Context, live *app.Live, db *model.Database) (assistant.Sample, error) {
	ref, ok := firstTable(db)
	if !ok {
		return assistant.Sample{}, errNoAssistantRows
	}
	b, err := app.NewBrowseSource(ctx, live.Source, ref, source.BrowseOptions{Limit: assistantSampleRows})
	if err != nil {
		return assistant.Sample{}, err
	}
	rs := b.Rows()
	defer rs.Close()
	var rows []model.Row
	for len(rows) < assistantSampleRows {
		row, err := rs.Next(ctx)
		if err != nil {
			break
		}
		rows = append(rows, row)
	}
	return assistant.SampleOf(strings.Join(ref.Path[1:], "."), rs.Columns(), rows), nil
}

// assistantSampleRows is how many rows a sample asks for. Few: the model needs
// to see what a value looks like, not to read the table.
const assistantSampleRows = 10

// firstTable is the first table a schema holds, in the order the snapshot has
// them.
func firstTable(db *model.Database) (model.ObjectRef, bool) {
	if db == nil {
		return model.ObjectRef{}, false
	}
	for _, sc := range db.Schemas {
		for _, t := range sc.Tables {
			if sc.Name == "" {
				return model.NewRef(model.KindTable, db.Name, t.Name), true
			}
			return model.NewRef(model.KindTable, db.Name, sc.Name, t.Name), true
		}
	}
	return model.ObjectRef{}, false
}

var errNoAssistantRows = errors.New("shell: this database has no table to sample")
