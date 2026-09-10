package canvas

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/testutil/race"
)

// syntheticSchema builds a graph shaped like a real database schema: a few
// connected clusters plus a long tail of unreferenced tables. A uniformly
// connected graph would flatter the layout and tell us nothing.
func syntheticSchema(tables int) *Graph {
	var nodes []Node
	var edges []Edge

	clusters := []struct {
		name string
		size int
	}{
		{"sales", tables * 30 / 100},
		{"catalog", tables * 20 / 100},
		{"billing", tables * 15 / 100},
		{"logistics", tables * 10 / 100},
	}

	id := 0
	for _, c := range clusters {
		first := id
		for i := 0; i < c.size; i++ {
			nodes = append(nodes, table(fmt.Sprintf("%s.t%d", c.name, i), 4+i%9))
			if i > 0 {
				// Each table references an earlier one in the cluster, giving
				// a connected but not uniform shape.
				edges = append(edges, Edge{
					From:     nodes[first+i].ID,
					To:       nodes[first+(i*7)%i].ID,
					FromPort: 1, ToPort: 0,
					Cardinality: OneToMany,
				})
			}
			id++
		}
	}

	// A few cross-cluster relationships, as real schemas have.
	for i := 0; i+1 < len(clusters); i++ {
		a := fmt.Sprintf("%s.t1", clusters[i].name)
		b := fmt.Sprintf("%s.t2", clusters[i+1].name)
		edges = append(edges, Edge{From: a, To: b, FromPort: 2, ToPort: 0})
	}

	// The long tail: unreferenced tables, which every real schema has and
	// which a force layout handles badly without component packing.
	for id < tables {
		nodes = append(nodes, table(fmt.Sprintf("misc.t%d", id), 3+id%6))
		id++
	}

	return NewGraph(nodes, edges)
}

// centreOn positions the viewport so that a graph point sits at the centre of
// the screen. Pan is the graph point at the screen ORIGIN, so this is not just
// an assignment — getting it wrong points the viewport at empty space, which
// makes every culling measurement pass for the wrong reason.
func centreOn(v *Viewport, p Point) {
	v.Pan = Point{
		X: p.X - v.Screen.W/(2*v.Zoom),
		Y: p.Y - v.Screen.H/(2*v.Zoom),
	}
}

func table(id string, cols int) Node {
	ports := make([]Port, cols)
	ports[0] = Port{Label: "id", Detail: "bigint", Key: true}
	for i := 1; i < cols; i++ {
		ports[i] = Port{Label: fmt.Sprintf("col_%d", i), Detail: "text"}
	}
	return Node{ID: id, Title: id, Ports: ports}
}

func TestLayoutProducesNoOverlaps(t *testing.T) {
	// A diagram with overlapping tables is unreadable no matter how good the
	// underlying structure is.
	g := syntheticSchema(200)
	Layout(g, DefaultLayout())

	var worst float64
	overlaps := 0
	for i := 0; i < len(g.Nodes); i++ {
		for j := i + 1; j < len(g.Nodes); j++ {
			a, b := g.Nodes[i].Bounds(), g.Nodes[j].Bounds()
			if !a.Intersects(b) {
				continue
			}
			ca, cb := a.Center(), b.Center()
			ox := (a.Width()+b.Width())/2 - math.Abs(ca.X-cb.X)
			oy := (a.Height()+b.Height())/2 - math.Abs(ca.Y-cb.Y)
			if ox > 0 && oy > 0 {
				overlaps++
				if area := ox * oy; area > worst {
					worst = area
				}
			}
		}
	}
	if overlaps > 0 {
		t.Errorf("%d overlapping node pairs (worst overlap area %.0f)", overlaps, worst)
	}
}

func TestLayoutIsDeterministic(t *testing.T) {
	// A diagram that rearranges itself on every open is not a diagram.
	a := syntheticSchema(120)
	b := syntheticSchema(120)
	Layout(a, DefaultLayout())
	Layout(b, DefaultLayout())

	for i := range a.Nodes {
		if a.Nodes[i].Pos != b.Nodes[i].Pos {
			t.Fatalf("node %s moved between runs: %+v vs %+v",
				a.Nodes[i].ID, a.Nodes[i].Pos, b.Nodes[i].Pos)
		}
	}
}

func TestPinnedNodesSurviveRelayout(t *testing.T) {
	// FR-8.2: a manual arrangement must not be undone by a re-layout.
	g := syntheticSchema(60)
	Layout(g, DefaultLayout())

	g.Nodes[3].Pinned = true
	g.Nodes[3].Pos = Point{X: 1234, Y: 5678}
	want := g.Nodes[3].Pos

	Layout(g, DefaultLayout())

	if g.Nodes[3].Pos != want {
		t.Errorf("pinned node moved: %+v, want %+v", g.Nodes[3].Pos, want)
	}
}

func TestComponentsFindTheLongTail(t *testing.T) {
	g := syntheticSchema(200)
	comps := g.Components()

	singles := 0
	for _, c := range comps {
		if len(c) == 1 {
			singles++
		}
	}
	if singles == 0 {
		t.Error("no isolated tables found; the fixture is not schema-shaped")
	}
	total := 0
	for _, c := range comps {
		total += len(c)
	}
	if total != len(g.Nodes) {
		t.Errorf("components cover %d nodes, graph has %d", total, len(g.Nodes))
	}
}

func TestWithinDegreeNarrowsALargeSchema(t *testing.T) {
	// FR-8.5 is what makes a 200-table schema readable at all.
	g := syntheticSchema(200)
	root := g.Nodes[0].ID

	one := g.WithinDegree(root, 1)
	two := g.WithinDegree(root, 2)

	if len(one) < 2 {
		t.Errorf("1-degree neighbourhood has %d nodes, expected the root plus neighbours", len(one))
	}
	if len(two) < len(one) {
		t.Error("2-degree neighbourhood is smaller than 1-degree")
	}
	if len(two) >= len(g.Nodes) {
		t.Error("2-degree neighbourhood covers the whole schema; it narrows nothing")
	}
}

func TestZoomAtKeepsPointUnderCursor(t *testing.T) {
	// Zooming toward the cursor is what makes the gesture feel controlled.
	v := NewViewport(Size{1000, 800})
	v.Pan = Point{100, 100}

	cursor := Point{640, 480}
	before := v.ToGraph(cursor)
	v.ZoomAt(cursor, 1.8)
	after := v.ToGraph(cursor)

	if math.Abs(before.X-after.X) > 1e-9 || math.Abs(before.Y-after.Y) > 1e-9 {
		t.Errorf("point under cursor drifted: %+v -> %+v", before, after)
	}
}

func TestZoomClamps(t *testing.T) {
	v := NewViewport(Size{1000, 800})
	for i := 0; i < 200; i++ {
		v.ZoomAt(Point{0, 0}, 0.5)
	}
	if v.Zoom < MinZoom {
		t.Errorf("zoom %v fell below MinZoom %v", v.Zoom, MinZoom)
	}
	for i := 0; i < 200; i++ {
		v.ZoomAt(Point{0, 0}, 2)
	}
	if v.Zoom > MaxZoom {
		t.Errorf("zoom %v exceeded MaxZoom %v", v.Zoom, MaxZoom)
	}
}

func TestFitToFramesTheWholeGraph(t *testing.T) {
	g := syntheticSchema(200)
	Layout(g, DefaultLayout())

	v := NewViewport(Size{1440, 900})
	v.FitTo(g.Bounds(), 40)

	vis := v.VisibleRect()
	b := g.Bounds()
	if !vis.Intersects(b) {
		t.Fatal("fit produced a viewport that does not see the graph")
	}
	// Every node should be inside the visible rectangle after a fit.
	outside := 0
	for _, n := range g.Nodes {
		if !vis.Expand(1).Intersects(n.Bounds()) {
			outside++
		}
	}
	if outside > 0 {
		t.Errorf("%d nodes outside the viewport after FitTo", outside)
	}
}

func TestCullingIsBoundedByViewportGeometry(t *testing.T) {
	// The property that matters is not "a bigger schema shows proportionally
	// fewer nodes" — that just reflects how dense a particular layout is. It
	// is that the visible count can never exceed what the viewport
	// geometrically holds, however large the schema grows. That is what makes
	// pan cost independent of schema size, the same guarantee as the data
	// grid (NFR-P13).
	screen := Size{1440, 900}

	for _, tables := range []int{50, 200, 400, 800} {
		g := syntheticSchema(tables)
		Layout(g, DefaultLayout())

		v := NewViewport(screen)
		v.Zoom = 1.0
		// Centre on a real node. A schema packs as one large cluster beside
		// rows of isolated tables, so the centre of the bounding box
		// legitimately falls in a gap between them, and a viewport pointed at
		// a gap makes this test pass for the wrong reason.
		centreOn(v, g.Nodes[0].Bounds().Center())

		visible := len(v.VisibleNodes(g, nil))
		if visible == 0 {
			t.Fatalf("%d tables: no nodes visible over a node's own centre", tables)
		}

		// VisibleNodes expands the rectangle by NodeWidth on every side so
		// nodes do not pop in at the edge, so the bound must account for that.
		r := v.VisibleRect().Expand(NodeWidth)
		capacity := int((r.Width() / NodeWidth) * (r.Height() / NodeMinHeight))

		t.Logf("%3d tables: %2d visible, geometric capacity %d", tables, visible, capacity)

		if visible > capacity {
			t.Errorf("%d tables: %d nodes visible exceeds the viewport's "+
				"geometric capacity of %d; culling is not bounding the frame",
				tables, visible, capacity)
		}
	}
}

func TestDetailLevelDropsWhenZoomedOut(t *testing.T) {
	// The whole-schema overview is the one view where every node is visible,
	// so LOD is what makes culling meaningful at that zoom.
	v := NewViewport(Size{1440, 900})

	v.Zoom = 1.0
	if v.Detail() != DetailFull {
		t.Error("expected full detail at zoom 1.0")
	}
	v.Zoom = 0.4
	if v.Detail() != DetailTitle {
		t.Error("expected title-only detail at zoom 0.4")
	}
	v.Zoom = 0.15
	if v.Detail() != DetailBox {
		t.Error("expected box-only detail at zoom 0.15")
	}
}

func TestRouteIsOrthogonal(t *testing.T) {
	a := Node{ID: "a", Pos: Point{0, 0}, Size: Size{NodeWidth, 120}, Ports: make([]Port, 5)}
	b := Node{ID: "b", Pos: Point{500, 300}, Size: Size{NodeWidth, 120}, Ports: make([]Port, 5)}

	r := RouteEdge(a, b, 1, 2)
	if len(r.Points) < 2 {
		t.Fatal("route has no segments")
	}
	for i := 1; i < len(r.Points); i++ {
		p, q := r.Points[i-1], r.Points[i]
		horizontal := math.Abs(p.Y-q.Y) < 1e-9
		vertical := math.Abs(p.X-q.X) < 1e-9
		if !horizontal && !vertical {
			t.Errorf("segment %d is diagonal: %+v -> %+v", i, p, q)
		}
	}
}

func TestRouteAttachesToPortRows(t *testing.T) {
	// An edge must point at the column it refers to, not at the box.
	a := Node{ID: "a", Pos: Point{0, 0}, Size: Size{NodeWidth, 200}, Ports: make([]Port, 6)}
	b := Node{ID: "b", Pos: Point{600, 0}, Size: Size{NodeWidth, 200}, Ports: make([]Port, 6)}

	r0 := RouteEdge(a, b, 0, 0)
	r3 := RouteEdge(a, b, 3, 3)

	if r0.Points[0].Y == r3.Points[0].Y {
		t.Error("routes for different ports start at the same height")
	}
	if want := portY(a, 3); math.Abs(r3.Points[0].Y-want) > 1e-9 {
		t.Errorf("route starts at y=%v, want port row y=%v", r3.Points[0].Y, want)
	}
}

func TestHitTestPrefersTopmost(t *testing.T) {
	g := NewGraph([]Node{
		{ID: "under", Pos: Point{0, 0}, Size: Size{100, 100}},
		{ID: "over", Pos: Point{50, 50}, Size: Size{100, 100}},
	}, nil)

	v := NewViewport(Size{500, 500})
	if got := v.HitTest(g, Point{75, 75}); got != 1 {
		t.Errorf("hit test returned %d, want the topmost node (1)", got)
	}
	if got := v.HitTest(g, Point{400, 400}); got != -1 {
		t.Errorf("hit test on empty space returned %d, want -1", got)
	}
}

// --- GATE G0-3 -------------------------------------------------------------

func TestGateG0_3(t *testing.T) {
	race.SkipTimingGate(t)
	const tables = 200
	g := syntheticSchema(tables)

	start := time.Now()
	Layout(g, DefaultLayout())
	layoutTime := time.Since(start)

	t.Logf("layout of %d tables (%d edges): %v",
		len(g.Nodes), len(g.Edges), layoutTime.Round(time.Millisecond))

	// Layout runs once when the diagram opens, behind a progress indicator if
	// needed, so the budget is a user-perceptible pause rather than a frame.
	const layoutBudget = 2 * time.Second
	if layoutTime > layoutBudget {
		t.Errorf("G0-3: layout took %v, exceeding %v", layoutTime, layoutBudget)
	}

	// Pan cost at three zooms. The overview is the demanding one: every node
	// is visible, so it is LOD rather than culling that carries it.
	v := NewViewport(Size{1440, 900})
	var nodeBuf, edgeBuf []int

	for _, tc := range []struct {
		name string
		fit  bool
		zoom float64
	}{
		{"fit to window (overview)", true, 0},
		{"zoom 0.5", false, 0.5},
		{"zoom 1.0 (reading)", false, 1.0},
	} {
		if tc.fit {
			v.FitTo(g.Bounds(), 40)
		} else {
			v.Zoom = tc.zoom
			centreOn(v, g.Bounds().Center())
		}
		home := v.Pan

		const frames = 300
		start := time.Now()
		var visN, visE, sumN int
		for i := 0; i < frames; i++ {
			// Sweep across the graph rather than drifting off it. Panning in
			// one direction for 300 frames leaves the diagram entirely and
			// measures empty space, which is the cheap case.
			b := g.Bounds()
			t := float64(i) / float64(frames)
			v.Pan = Point{
				X: home.X + (b.Width()-v.Screen.W/v.Zoom)*t,
				Y: home.Y + (b.Height()-v.Screen.H/v.Zoom)*math.Sin(t*math.Pi),
			}
			nodeBuf = v.VisibleNodes(g, nodeBuf)
			edgeBuf = v.VisibleEdges(g, nodeBuf, edgeBuf)
			for _, ei := range edgeBuf {
				e := g.Edges[ei]
				from, to := g.NodeByID(e.From), g.NodeByID(e.To)
				if from != nil && to != nil {
					_ = RouteEdge(*from, *to, e.FromPort, e.ToPort)
				}
			}
			visN, visE = len(nodeBuf), len(edgeBuf)
			sumN += visN
		}
		perFrame := time.Since(start) / frames
		avgN := sumN / frames

		t.Logf("%-26s zoom %.2f  detail=%d  avg %3d nodes visible (peak %3d, %3d edges)  %v/frame",
			tc.name, v.Zoom, v.Detail(), avgN, visN, visE, perFrame.Round(time.Microsecond))

		if avgN == 0 {
			t.Errorf("G0-3: %s saw no nodes across the sweep; the measurement "+
				"is of empty space, not of the diagram", tc.name)
		}

		// A quarter of the 16.7ms frame for culling and routing, leaving the
		// rest for layout and paint.
		const budget = 4 * time.Millisecond
		if perFrame > budget {
			t.Errorf("G0-3: %s cost %v per frame, exceeding %v", tc.name, perFrame, budget)
		}
	}
}

func BenchmarkLayout(b *testing.B) {
	for _, n := range []int{50, 200, 500} {
		b.Run(fmt.Sprintf("%dtables", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				g := syntheticSchema(n)
				Layout(g, DefaultLayout())
			}
		})
	}
}

func BenchmarkPanFrame(b *testing.B) {
	g := syntheticSchema(200)
	Layout(g, DefaultLayout())
	v := NewViewport(Size{1440, 900})
	v.Zoom = 1.0
	v.Pan = g.Bounds().Center()

	var nodeBuf, edgeBuf []int
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v.PanBy(2, 1)
		nodeBuf = v.VisibleNodes(g, nodeBuf)
		edgeBuf = v.VisibleEdges(g, nodeBuf, edgeBuf)
	}
}
