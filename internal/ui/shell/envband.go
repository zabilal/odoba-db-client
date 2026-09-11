package shell

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/ui/tabbar"
	uitheme "github.com/ikigai-db/ikigai-db/internal/ui/theme"
)

// The environment on every tab (FR-1.7, UX principle 8, ADR-0011 §20). A tab
// on a connection with an environment carries its word on the tab bar, in the
// environment's colours; a production tab also has a band across the top of
// its content, saying in words what working there means. Both are read from
// the connection as they are drawn, so an environment saved later, or a
// change of appearance, shows at the next redraw.

// envOf is a tab's connection's environment, and whether it is read-only.
func (s *Shell) envOf(t *tab) (env string, readOnly bool) {
	c, _ := s.d.Conns.Get(t.connID)
	return c.Environment, c.ReadOnly
}

// tint is an environment's treatment in the appearance on show.
func (s *Shell) tint(env string) uitheme.EnvironmentTint {
	return s.d.Theme.Environment(env, s.d.Theme.IsDark(s.app.Settings().ThemeVariant()))
}

// tabMark is a tab's mark on the tab bar: its connection's environment, or
// none for a connection with none.
func (s *Shell) tabMark(it *container.TabItem) *tabbar.Mark {
	t := s.tabOf(it)
	if t == nil {
		return nil
	}
	env, _ := s.envOf(t)
	if env == "" {
		return nil
	}
	e := s.tint(env)
	return &tabbar.Mark{Label: e.Label, Fill: e.Accent, Text: e.OnAccent}
}

// markTabs redraws every tab's environment. A connection saved with another
// can have tabs open: one that failed to connect keeps its tab.
func (s *Shell) markTabs() {
	for _, p := range s.panes {
		p.Refresh()
	}
	for _, t := range s.open {
		if t.band != nil {
			t.band.update()
		}
	}
}

// envBand is the band across the top of a production tab's content.
type envBand struct {
	widget.BaseWidget
	s *Shell
	t *tab
}

func newEnvBand(s *Shell, t *tab) *envBand {
	b := &envBand{s: s, t: t}
	b.ExtendBaseWidget(b)
	b.update()
	return b
}

// text is what the band says, or "" for a tab that has no band: only
// production shouts, or none would.
func (b *envBand) text() string {
	env, readOnly := b.s.envOf(b.t)
	switch {
	case env == "" || !b.s.tint(env).Emphatic:
		return ""
	case readOnly:
		return "Production, opened read-only: statements that change data are refused."
	}
	return "Production: statements run here change live data."
}

// update shows the band if the tab has one, and redraws it.
func (b *envBand) update() {
	setShown(b, b.text() != "")
	b.Refresh()
}

func (b *envBand) CreateRenderer() fyne.WidgetRenderer {
	r := &envBandRenderer{b: b, bg: canvas.NewRectangle(color.Transparent), pill: canvas.NewRectangle(color.Transparent),
		word: canvas.NewText("", color.Transparent), msg: widget.NewLabel("")}
	r.word.TextStyle.Bold = true
	r.word.Alignment = fyne.TextAlignCenter
	r.msg.Truncation = fyne.TextTruncateEllipsis
	r.Refresh()
	return r
}

type envBandRenderer struct {
	b        *envBand
	bg, pill *canvas.Rectangle
	word     *canvas.Text
	msg      *widget.Label
	pillW    float32
}

func (r *envBandRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.bg, r.pill, r.word, r.msg}
}

func (r *envBandRenderer) Destroy() {}

func (r *envBandRenderer) MinSize() fyne.Size {
	pad := r.b.Theme().Size(fynetheme.SizeNamePadding)
	return fyne.NewSize(pad+r.pillW+pad+r.msg.MinSize().Width, r.msg.MinSize().Height)
}

func (r *envBandRenderer) Layout(size fyne.Size) {
	pad := r.b.Theme().Size(fynetheme.SizeNamePadding)
	r.bg.Resize(size)
	h := r.word.MinSize().Height + 2
	at := fyne.NewPos(pad, (size.Height-h)/2)
	r.pill.Resize(fyne.NewSize(r.pillW, h))
	r.pill.Move(at)
	r.word.Resize(fyne.NewSize(r.pillW, h))
	r.word.Move(at)
	x := pad + r.pillW + pad
	r.msg.Resize(fyne.NewSize(max(0, size.Width-x), size.Height))
	r.msg.Move(fyne.NewPos(x, 0))
}

func (r *envBandRenderer) Refresh() {
	th := r.b.Theme()
	env, _ := r.b.s.envOf(r.b.t)
	e := r.b.s.tint(env)
	r.bg.FillColor = e.Subtle
	r.pill.FillColor, r.pill.CornerRadius = e.Accent, th.Size(fynetheme.SizeNameSelectionRadius)
	r.word.Text, r.word.Color, r.word.TextSize = e.Label, e.OnAccent, th.Size(fynetheme.SizeNameCaptionText)
	r.pillW = fyne.MeasureText(e.Label, r.word.TextSize, r.word.TextStyle).Width + th.Size(fynetheme.SizeNameInnerPadding)
	r.msg.SetText(r.b.text())
	r.bg.Refresh()
	r.pill.Refresh()
	r.word.Refresh()
	r.Layout(r.b.Size())
}
