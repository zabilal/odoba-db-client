package grid

import (
	"sync"
	_ "unsafe" // for go:linkname, and for nothing else

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// A small control in the grid — a column's filter, a cell being edited — is an
// entry drawn with the compact theme. Fyne's own way of saying that is
// container.NewThemeOverride, and it cannot be used here: every override is a
// scope of its own, a new one each time it is made, each time its renderer is
// and each time it is refreshed, and Fyne parses the theme's fonts again for
// every scope that measures text and keeps them until the theme changes
// (painter.CachedFontFace is keyed by the scope). Measured on v2.8.1 that is
// 6.4 MB kept and 13ms spent for every cell somebody starts editing and every
// time a header is refreshed. It is how the window's own tests came to hold
// four gigabytes, which is what found it.
//
// So every small control shares one scope: an object nothing draws is given
// the compact theme once, and each control is put into that object's scope
// rather than into one of its own. Fyne does exactly this for the rows of a
// list inside an override, with a function it does not export, which is the
// one reached for here. A Fyne that renames it fails this build at the link,
// which is the right place to find out.

//go:linkname overrideMatchingScope fyne.io/fyne/v2/internal/cache.OverrideThemeMatchingScope
func overrideMatchingScope(o, parent fyne.CanvasObject) bool

var (
	compactOnce   sync.Once
	compactAnchor fyne.CanvasObject
)

// small is content drawn with the compact theme, in the one scope every small
// control shares.
type small struct {
	widget.BaseWidget
	content fyne.CanvasObject
}

func newSmall(content fyne.CanvasObject) *small {
	s := &small{content: content}
	s.ExtendBaseWidget(s)
	s.adopt()
	content.Refresh() // it may have been drawn with the application's theme already
	return s
}

// adopt puts the content into the shared scope. More than once, because Fyne
// forgets a widget's scope when it lets go of its renderer, and because what a
// widget has made since the last time is not in any scope yet.
func (s *small) adopt() {
	compactOnce.Do(func() {
		compactAnchor = canvas.NewRectangle(nil)
		container.NewThemeOverride(compactAnchor, compact{})
	})
	overrideMatchingScope(s.content, compactAnchor)
}

func (s *small) CreateRenderer() fyne.WidgetRenderer {
	s.adopt()
	return widget.NewSimpleRenderer(container.NewStack(s.content))
}

func (s *small) Refresh() {
	s.adopt()
	s.BaseWidget.Refresh() // which refreshes the content
}
