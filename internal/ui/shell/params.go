package shell

import (
	"context"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
)

// Query parameters (FR-5.7, ADR-0026). A script with :name parameters asks
// for their values before it runs, in a panel beside the editor rather than
// over it: each name with the value last given on the connection, or NULL.
// The values go to the server bound, never written into the statement, and
// as typed: the server makes of each what its place needs. History keeps
// the script as written, so no value is recorded there.

const panelParams = "params"

// paramsTimeout bounds reading or saving remembered values.
const paramsTimeout = 2 * time.Second

// paramsPanel asks for a script's parameter values.
type paramsPanel struct {
	s      *Shell
	t      *tab
	script string
	base   int
	names  []string
	values []*panelEntry
	nulls  []*widget.Check
	run    *widget.Button
	cancel *widget.Button
}

// askParams opens the panel for a script about to run.
func (s *Shell) askParams(t *tab, script string, base int, names []string) *paramsPanel {
	p := &paramsPanel{s: s, t: t, script: script, base: base, names: names}
	form := &widget.Form{}
	for _, name := range names {
		e := s.newPanelEntry()
		e.OnSubmitted = func(string) { p.runIt() }
		null := widget.NewCheck("NULL", nil)
		null.OnChanged = func(on bool) {
			if on {
				e.Disable()
			} else {
				e.Enable()
			}
		}
		if v, ok := s.remembered(t.connID, name); ok {
			e.SetText(v.Text)
			null.SetChecked(v.Null)
		}
		p.values, p.nulls = append(p.values, e), append(p.nulls, null)
		form.Append(":"+name, container.NewBorder(nil, nil, nil, null, e))
	}
	p.run = widget.NewButton("Run", p.runIt)
	p.run.Importance = widget.HighImportance
	p.cancel = widget.NewButton("Cancel", s.closePanel)
	note := widget.NewLabel("Values are sent as typed, and the server reads each as its place needs.")
	note.Wrapping = fyne.TextWrapWord
	note.Importance = widget.LowImportance
	body := container.NewBorder(nil, container.NewHBox(layout.NewSpacer(), p.cancel, p.run), nil, nil,
		container.NewVScroll(container.NewVBox(form, note)))
	var focus fyne.Focusable
	if len(p.values) > 0 {
		focus = p.values[0]
	}
	s.openPanel(panelParams, "Parameters", body, focus)
	s.lastParams = p
	return p
}

// named is the values as given: the text typed, or nil for NULL.
func (p *paramsPanel) named() map[string]any {
	out := make(map[string]any, len(p.names))
	for i, name := range p.names {
		if p.nulls[i].Checked {
			out[name] = nil
		} else {
			out[name] = p.values[i].Text
		}
	}
	return out
}

// runIt remembers the values and runs the script with them.
func (p *paramsPanel) runIt() {
	for i, name := range p.names {
		p.s.remember(p.t.connID, name, localdb.ParamValue{Text: p.values[i].Text, Null: p.nulls[i].Checked})
	}
	named := p.named()
	p.s.closePanel()
	p.s.execute(p.t, p.script, p.base, source.ScriptOptions{Named: named})
}

// remembered is the value last given to a parameter on a connection.
func (s *Shell) remembered(connID, name string) (localdb.ParamValue, bool) {
	if s.d.Params == nil {
		return localdb.ParamValue{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), paramsTimeout)
	defer cancel()
	v, ok, err := s.d.Params.Param(ctx, connID, name)
	if err != nil {
		s.d.Log.Warn("reading a parameter's value", "err", err)
	}
	return v, ok && err == nil
}

// remember keeps the value given to a parameter on a connection. A value
// that cannot be kept is only not offered next time.
func (s *Shell) remember(connID, name string, v localdb.ParamValue) {
	if s.d.Params == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), paramsTimeout)
	defer cancel()
	if err := s.d.Params.PutParam(ctx, connID, name, v); err != nil {
		s.d.Log.Warn("keeping a parameter's value", "err", err)
	}
}
