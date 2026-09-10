package theme

import (
	"fyne.io/fyne/v2"
	ftheme "fyne.io/fyne/v2/theme"
)

// Object-kind icons for the explorer tree.
//
// Fyne's stock set covers actions (folder, document, settings) but has nothing
// for the things this application actually shows — tables, views, indexes,
// topics, partitions, consumer groups. SF Symbols would be the native answer
// but Apple's licence does not permit redistributing them in an application,
// so these are drawn to match: thin strokes, 16pt grid, rounded caps, the same
// optical weight as SF Symbols at the same size.
//
// Icons are monochrome and inherit the label colour, so they work unchanged in
// both appearances.

// Icon names for object kinds. These extend Fyne's ThemeIconName space; the
// prefix keeps them from colliding with Fyne's own additions.
const (
	IconNameDatabase      fyne.ThemeIconName = "ikigai:database"
	IconNameSchema        fyne.ThemeIconName = "ikigai:schema"
	IconNameTable         fyne.ThemeIconName = "ikigai:table"
	IconNameView          fyne.ThemeIconName = "ikigai:view"
	IconNameColumn        fyne.ThemeIconName = "ikigai:column"
	IconNameIndex         fyne.ThemeIconName = "ikigai:index"
	IconNamePrimaryKey    fyne.ThemeIconName = "ikigai:primarykey"
	IconNameForeignKey    fyne.ThemeIconName = "ikigai:foreignkey"
	IconNameRoutine       fyne.ThemeIconName = "ikigai:routine"
	IconNameCollection    fyne.ThemeIconName = "ikigai:collection"
	IconNameKey           fyne.ThemeIconName = "ikigai:key"
	IconNameTopic         fyne.ThemeIconName = "ikigai:topic"
	IconNamePartition     fyne.ThemeIconName = "ikigai:partition"
	IconNameConsumerGroup fyne.ThemeIconName = "ikigai:consumergroup"
	IconNameCluster       fyne.ThemeIconName = "ikigai:cluster"
)

// svgIcon wraps an SVG body in a 16x16 viewBox with shared stroke defaults.
func svgIcon(name, body string) fyne.Resource {
	const head = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" ` +
		`fill="none" stroke="currentColor" stroke-width="1.2" ` +
		`stroke-linecap="round" stroke-linejoin="round">`
	return fyne.NewStaticResource(name, []byte(head+body+`</svg>`))
}

var customIcons = map[fyne.ThemeIconName]fyne.Resource{
	// Disclosure chevrons, as macOS draws them, rather than arrows
	// (ADR-0006). Fyne's tree asks the theme for these two.
	ftheme.IconNameNavigateNext: svgIcon("chevron-right.svg", `<path d="M6.2 3.4 10.8 8l-4.6 4.6"/>`),
	ftheme.IconNameMoveDown:     svgIcon("chevron-down.svg", `<path d="M3.4 6.2 8 10.8l4.6-4.6"/>`),

	// A cylinder, the universal database mark.
	IconNameDatabase: svgIcon("database.svg",
		`<ellipse cx="8" cy="3.8" rx="5" ry="2.1"/>`+
			`<path d="M3 3.8v8.4c0 1.16 2.24 2.1 5 2.1s5-.94 5-2.1V3.8"/>`+
			`<path d="M3 8c0 1.16 2.24 2.1 5 2.1s5-.94 5-2.1"/>`),

	// Nested brackets: a namespace containing objects.
	IconNameSchema: svgIcon("schema.svg",
		`<path d="M6 2.5H4.2A1.2 1.2 0 003 3.7v8.6a1.2 1.2 0 001.2 1.2H6"/>`+
			`<path d="M10 2.5h1.8A1.2 1.2 0 0113 3.7v8.6a1.2 1.2 0 01-1.2 1.2H10"/>`+
			`<path d="M8 5.5v5"/>`),

	// A grid with a header row — the table itself.
	IconNameTable: svgIcon("table.svg",
		`<rect x="2.5" y="3" width="11" height="10" rx="1.2"/>`+
			`<path d="M2.5 6.2h11"/><path d="M6.4 6.2V13"/><path d="M2.5 9.6h11"/>`),

	// A table outline with a dashed body: a projection, not storage.
	IconNameView: svgIcon("view.svg",
		`<rect x="2.5" y="3" width="11" height="10" rx="1.2"/>`+
			`<path d="M2.5 6.2h11"/>`+
			`<path d="M5 9h2.4M9 9h2.4M5 11.2h2.4M9 11.2h2.4" stroke-dasharray="0.1 2.2"/>`),

	// A single vertical band — one column of the grid.
	IconNameColumn: svgIcon("column.svg",
		`<rect x="5.6" y="2.6" width="4.8" height="10.8" rx="1.1"/>`+
			`<path d="M5.6 5.8h4.8"/>`),

	// Stacked sort lines: an ordered access path.
	IconNameIndex: svgIcon("index.svg",
		`<path d="M3.2 4.4h9.6"/><path d="M3.2 8h6.4"/><path d="M3.2 11.6h3.6"/>`+
			`<path d="M11.4 9.6l1.6 2 1.6-2"/>`),

	// A key, for the primary key.
	IconNamePrimaryKey: svgIcon("primarykey.svg",
		`<circle cx="5.6" cy="6.4" r="2.6"/>`+
			`<path d="M7.4 8.2l4.6 4.6"/><path d="M10.6 11.4l1.4-1.4"/>`),

	// A link between two nodes, for the foreign key.
	IconNameForeignKey: svgIcon("foreignkey.svg",
		`<path d="M6.6 9.4a2.6 2.6 0 010-3.7l1.5-1.5a2.6 2.6 0 013.7 3.7l-.8.8"/>`+
			`<path d="M9.4 6.6a2.6 2.6 0 010 3.7l-1.5 1.5a2.6 2.6 0 01-3.7-3.7l.8-.8"/>`),

	// Angle brackets: executable code.
	IconNameRoutine: svgIcon("routine.svg",
		`<path d="M5.8 5L3 8l2.8 3"/><path d="M10.2 5L13 8l-2.8 3"/>`),

	// Stacked documents, for a document collection.
	IconNameCollection: svgIcon("collection.svg",
		`<rect x="2.6" y="4.6" width="8.4" height="8.8" rx="1.2"/>`+
			`<path d="M5.2 4.6V3.8a1.2 1.2 0 011.2-1.2h6a1.2 1.2 0 011.2 1.2v6a1.2 1.2 0 01-1.2 1.2h-.8"/>`),

	// A tag, for a key-value entry.
	IconNameKey: svgIcon("key.svg",
		`<path d="M8.4 2.6H13v4.6l-6 6a1.4 1.4 0 01-2 0L2.6 9.2a1.4 1.4 0 010-2z"/>`+
			`<circle cx="10.6" cy="5" r="0.9"/>`),

	// A log: parallel ordered lanes with a leading edge.
	IconNameTopic: svgIcon("topic.svg",
		`<path d="M2.6 4.6h10.8"/><path d="M2.6 8h10.8"/><path d="M2.6 11.4h10.8"/>`+
			`<path d="M11 2.9l2.4 1.7L11 6.3"/>`),

	// One lane of a log.
	IconNamePartition: svgIcon("partition.svg",
		`<rect x="2.6" y="6.2" width="10.8" height="3.6" rx="1.1"/>`+
			`<path d="M6.2 6.2v3.6"/><path d="M9.8 6.2v3.6"/>`),

	// Readers following a log.
	IconNameConsumerGroup: svgIcon("consumergroup.svg",
		`<circle cx="5.4" cy="5.6" r="2"/>`+
			`<path d="M2.4 12.4a3.2 3.2 0 016 0"/>`+
			`<circle cx="11.2" cy="6.4" r="1.6"/>`+
			`<path d="M9 12.4a2.6 2.6 0 014.6-1.5"/>`),

	// Connected brokers.
	IconNameCluster: svgIcon("cluster.svg",
		`<circle cx="8" cy="3.6" r="1.8"/>`+
			`<circle cx="3.8" cy="11.6" r="1.8"/>`+
			`<circle cx="12.2" cy="11.6" r="1.8"/>`+
			`<path d="M6.8 5.2l-2 4.8"/><path d="M9.2 5.2l2 4.8"/><path d="M5.6 11.6h4.8"/>`),
}
