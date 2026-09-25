package shell

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/chart"
	"github.com/ikigai-db/ikigai-db/internal/ui/geomap"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// The way onto a map (FR-11.5).
//
// The same shape as a chart's: the rows are read once into a tab of their own,
// and changing which columns hold the places redraws from those rows rather than
// asking the server again.
//
// There is no basemap, and the footer says so once rather than a label saying it
// for ever: the places are drawn on a graticule of degrees, which shows where
// they are in relation to each other and to nothing else (ADR-0163).

// mapRows is the most rows a map reads. Fewer than a chart's: a chart's marks
// are one raster, and a map's are shapes — a hundred thousand polygons would be
// a hundred thousand lines in the scene.
var mapRows = int64(20_000)

// mapReadTimeout bounds reading them.
const mapReadTimeout = 2 * time.Minute

// mapPanel is a map's state.
type mapPanel struct {
	s *Shell
	t *tab

	cols []model.ColumnDef
	rows []model.Row
	// short says the result had more rows than were read.
	short bool

	src   geomap.Source
	built geomap.Built

	w      *geomap.Widget
	holder *fyne.Container
	geoms  *widget.Select
	lats   *widget.Select
	lons   *widget.Select
	name   string
	// where is the coordinate the pointer is over, said in the footer so that
	// a map of a county can be read as places rather than as a shape.
	where string
}

func mapKey(of string) string { return "map:" + of }

// canMap reports whether the tab in front has rows with places in them.
//
// The rows are asked, not just the columns: a JSON column holds a place only if
// its values are places, and offering a map of a result with none would be an
// affordance that opens an empty picture (REQ-DB-2).
func (s *Shell) canMap() bool {
	t := s.activeTab()
	if t == nil || t.model == nil || len(t.model.Columns()) == 0 {
		return false
	}
	return geomap.Guess(t.model.Columns(), resident(t)).Found()
}

// resident is the first rows the grid already holds.
//
// A column holds a place or it does not, and the first row that is loaded says
// which; a few are taken rather than one because the first row's value may be
// null. Nothing is waited for: this is asked whenever a menu is built, and a
// menu that fetched rows would be a menu that hung.
func resident(t *tab) []model.Row {
	var out []model.Row
	for i := int64(0); i < residentEnough; i++ {
		row, loaded := t.model.Row(t.ctx, i)
		if !loaded || row == nil {
			break
		}
		out = append(out, row)
	}
	return out
}

// residentEnough is how many rows are looked at to decide what a column holds.
const residentEnough = 20

func (s *Shell) mapActive() {
	if t := s.activeTab(); t != nil && t.model != nil {
		s.OpenMap(t)
	}
}

// OpenMap draws the places in a tab's rows, or brings the map of them forward.
func (s *Shell) OpenMap(of *tab) *tab {
	if of.model == nil {
		return nil
	}
	key := mapKey(of.key)
	if t := s.tabFor(key); t != nil {
		s.selectTab(t)
		return t
	}
	label := chartLabel(of)
	ctx, cancel := context.WithCancel(s.ctx)
	t := &tab{key: key, connID: of.connID, ref: of.ref, label: label, structure: true,
		ctx: ctx, cancel: cancel,
		body: container.NewStack(quiet("Reading the rows…")), footer: widget.NewLabel("")}
	t.footer.Importance = widget.LowImportance
	t.item = container.NewTabItem("Map: "+label, container.NewBorder(nil, t.footer, nil, nil, t.body))
	s.open = append(s.open, t)
	s.addTab(t)
	s.sync()

	m := of.model
	cols := m.Columns()
	go func() {
		ctx, cancel := context.WithTimeout(ctx, mapReadTimeout)
		defer cancel()
		rows, err := m.Read(ctx, 0, mapRows)
		s.d.Run(func() {
			if t.ctx.Err() != nil {
				return
			}
			if err != nil {
				s.tabFailed(t, fmt.Errorf("could not read the rows to map: %w", err))
				return
			}
			s.showMap(t, label, cols, rows)
		})
	}()
	return t
}

// showMap finds the places and draws them.
func (s *Shell) showMap(t *tab, label string, cols []model.ColumnDef, rows []model.Row) {
	p := &mapPanel{s: s, t: t, cols: cols, rows: rows, name: label,
		short: int64(len(rows)) >= mapRows, src: geomap.Guess(cols, rows)}
	t.geomap = p
	p.holder = container.NewStack()
	t.body.Objects = []fyne.CanvasObject{
		container.NewBorder(p.toolbar(), nil, nil, nil, p.holder),
	}
	p.draw()
	t.body.Refresh()
}

// draw builds the picture from the rows already read.
func (p *mapPanel) draw() {
	p.built = geomap.Build(p.cols, p.rows, p.src)
	m := p.s.mapOf(p.built)
	if p.w == nil {
		p.w = geomap.New(m)
		p.w.OnPick = p.pick
		p.w.OnHover = p.hover
		p.holder.Objects = []fyne.CanvasObject{p.w}
		p.holder.Refresh()
	} else {
		p.w.SetMap(m)
	}
	p.say()
}

// mapOf dresses a reading in the window's colours, which the map carries rather
// than reading a theme as it draws.
func (s *Shell) mapOf(b geomap.Built) geomap.Map {
	pal := s.colours()
	return geomap.Map{
		Built:      b,
		Colours:    chart.SeriesColours(s.d.Theme.IsDark(s.app.Settings().ThemeVariant())),
		Axis:       pal.Label,
		Grid:       pal.Separator,
		Background: pal.ContentBackground,
		Highlight:  -1,
	}
}

// say tells the footer what is drawn, what is not, and where the pointer is.
func (p *mapPanel) say() {
	said := fmt.Sprintf("%s, %s.", nounCount(len(p.rows), "row"),
		nounCount(len(p.built.Places), "place"))
	if p.short {
		said = "The first " + said
	}
	if p.built.Skipped > 0 {
		said += fmt.Sprintf(" %s left out: no place could be read from it.",
			nounCount(p.built.Skipped, "row"))
	}
	said += " " + p.src.Describe(p.cols)
	if !p.built.Ok {
		said += " There is nothing to draw."
	}
	if p.where != "" {
		said += " " + p.where
	}
	p.t.footer.SetText(said)
}

// hover follows the pointer: the coordinates under it, and the row where it is
// over a place.
func (p *mapPanel) hover(r geomap.Reading) {
	p.where = ""
	if r.Row >= 0 || r.X != 0 || r.Y != 0 {
		p.where = "At " + coordinate(r.Y, "N", "S") + " " + coordinate(r.X, "E", "W")
		if r.Row >= 0 && r.Row < len(p.rows) {
			p.where += " · row " + strconv.Itoa(r.Row+1)
		}
	}
	p.say()
}

// coordinate writes one number as a bearing, which is how a coordinate is read
// aloud: 51.5°N rather than 51.5.
func coordinate(v float64, pos, neg string) string {
	side := pos
	if v < 0 {
		side, v = neg, -v
	}
	return strconv.FormatFloat(v, 'f', 4, 64) + "°" + side
}

// pick acts on a click: the row it was on, shown in the grid it came from.
func (p *mapPanel) pick(r geomap.Reading) {
	if r.Row < 0 || r.Row >= len(p.rows) {
		p.t.footer.SetText("There is no place there. " + p.src.Describe(p.cols))
		return
	}
	of := p.s.tabFor(strings.TrimPrefix(p.t.key, "map:"))
	if of == nil || of.grid == nil {
		p.t.footer.SetText("The rows this was drawn from are no longer open.")
		return
	}
	of.grid.GoTo(grid.CellID{Row: r.Row, Col: 0})
	p.s.selectTab(of)
}

// toolbar is what a map can be told to draw from.
func (p *mapPanel) toolbar() fyne.CanvasObject {
	p.geoms = widget.NewSelect(p.choices(), func(name string) {
		p.src.Geometry = p.columnAt(name)
		if p.src.Geometry != geomap.None {
			// One or the other: a geometry column and a pair of numbers are
			// two answers to the same question.
			p.src.Lat, p.src.Lon = geomap.None, geomap.None
			p.lats.SetSelected(nothingName)
			p.lons.SetSelected(nothingName)
		}
		p.draw()
	})
	p.geoms.Selected = p.columnName(p.src.Geometry)
	p.lats = widget.NewSelect(p.choices(), func(name string) {
		p.src.Lat = p.columnAt(name)
		if p.src.Lat != geomap.None {
			p.src.Geometry = geomap.None
			p.geoms.SetSelected(nothingName)
		}
		p.draw()
	})
	p.lats.Selected = p.columnName(p.src.Lat)
	p.lons = widget.NewSelect(p.choices(), func(name string) {
		p.src.Lon = p.columnAt(name)
		if p.src.Lon != geomap.None {
			p.src.Geometry = geomap.None
			p.geoms.SetSelected(nothingName)
		}
		p.draw()
	})
	p.lons.Selected = p.columnName(p.src.Lon)

	return container.NewHBox(
		widget.NewLabel("Geometry"), p.geoms,
		widget.NewLabel("or latitude"), p.lats,
		widget.NewLabel("and longitude"), p.lons,
	)
}

func (p *mapPanel) choices() []string {
	out := []string{nothingName}
	for _, c := range p.cols {
		out = append(out, c.Name)
	}
	return out
}

func (p *mapPanel) columnAt(name string) int {
	for i, c := range p.cols {
		if c.Name == name {
			return i
		}
	}
	return geomap.None
}

func (p *mapPanel) columnName(at int) string {
	if at < 0 || at >= len(p.cols) {
		return nothingName
	}
	return p.cols[at].Name
}
