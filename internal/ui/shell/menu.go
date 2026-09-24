package shell

import (
	"errors"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"

	"github.com/ikigai-db/ikigai-db/internal/ui/commands"
)

// Command IDs. They are stable: custom key bindings will be keyed on them
// (FR-15.4).
const (
	cmdPalette         = "palette.show"
	cmdShortcuts       = "help.shortcuts"
	cmdConnNew         = "connection.new"
	cmdConnEdit        = "connection.edit"
	cmdConnDup         = "connection.duplicate"
	cmdConnDelete      = "connection.delete"
	cmdDisconnect      = "connection.disconnect"
	cmdOpen            = "object.open"
	cmdStructure       = "object.structure"
	cmdProduce         = "object.produce"
	cmdTopicNew        = "topic.new"
	cmdTopicDelete     = "topic.delete"
	cmdTopicPartition  = "topic.addPartitions"
	cmdTopicConfig     = "topic.settings"
	cmdGroupReset      = "group.resetOffsets"
	cmdScriptSelect    = "object.script.select"
	cmdScriptInsert    = "object.script.insert"
	cmdScriptUpdate    = "object.script.update"
	cmdScriptCreate    = "object.script.create"
	cmdScriptSchema    = "object.script.schema"
	cmdCompare         = "schema.compare"
	cmdSaveModel       = "schema.saveModel"
	cmdDiagram         = "schema.diagram"
	cmdFavorite        = "explorer.favorite"
	cmdRefresh         = "explorer.refresh"
	cmdReload          = "tab.reload"
	cmdSidebar         = "view.sidebar"
	cmdFilterObjects   = "explorer.filter"
	cmdSearchStructure = "explorer.searchStructure"
	cmdMark            = "explorer.mark"
	cmdMarksClear      = "explorer.clearMarks"
	cmdMarksSelect     = "explorer.scriptMarkedSelect"
	cmdMarksCreate     = "explorer.scriptMarkedCreate"
	cmdMarksDrop       = "explorer.scriptMarkedDrop"
	cmdMarksExport     = "explorer.exportMarked"
	cmdCopyRows        = "explorer.copyRows"
	cmdTabClose        = "tab.close"
	cmdTabNext         = "tab.next"
	cmdTabPrev         = "tab.previous"
	cmdMoveTabLeft     = "tab.moveLeft"
	cmdMoveTabRight    = "tab.moveRight"
	cmdPinTab          = "tab.pin"
	cmdTasks           = "window.tasks"
	cmdNewWindow       = "window.new"
	cmdSplitRight      = "window.splitRight"
	cmdSplitDown       = "window.splitDown"
	cmdMoveToPane      = "window.moveToOtherPane"
	cmdJoinPanes       = "window.joinPanes"
	cmdQueryNew        = "query.new"
	cmdQueryComplete   = "query.complete"
	cmdQueryRun        = "query.run"
	cmdQueryRunAll     = "query.runAll"
	cmdQueryStop       = "query.stop"
	cmdHistory         = "query.history"
	cmdQuerySave       = "query.save"
	cmdQuerySaveAs     = "query.saveAs"
	cmdOpenSaved       = "query.openSaved"
	cmdExport          = "data.export"
	cmdImport          = "data.import"
	cmdChart           = "data.chart"
	cmdExplain         = "query.explain"
	cmdExplainMeasure  = "query.explainMeasure"
	cmdTxBegin         = "query.begin"
	cmdTxCommit        = "query.commit"
	cmdTxRollback      = "query.rollback"
	cmdFormatSQL       = "query.format"
	cmdColumnStats     = "data.columnStats"
	cmdAggregates      = "data.aggregates"
	cmdFilterValues    = "data.filterValues"
	cmdWhere           = "data.where"
	cmdCopyCells       = "grid.copy"
	cmdPasteCells      = "grid.paste"
	cmdSelectRow       = "grid.selectRow"
	cmdSelectColumn    = "grid.selectColumn"
	cmdSelectAllCells  = "grid.selectAll"
	cmdEditCell        = "grid.editCell"
	cmdEditValue       = "grid.editValue"
	cmdSetNull         = "grid.setNull"
	cmdSetValue        = "grid.setValue"
	cmdInsertRow       = "grid.insertRow"
	cmdDuplicateRows   = "grid.duplicateRows"
	cmdDeleteRows      = "grid.deleteRows"
	cmdDesign          = "object.design"
	cmdEditSource      = "object.editSource"
	cmdRename          = "object.rename"
	cmdReviewChanges   = "data.reviewChanges"
	cmdRevertCells     = "data.revertCells"
	cmdRevertRows      = "data.revertRows"
	cmdDiscardAll      = "data.discardAll"
	cmdChooseKey       = "data.chooseKey"
	cmdCopyCSV         = "grid.copyCSV"
	cmdCopyJSON        = "grid.copyJSON"
	cmdCopyMarkdown    = "grid.copyMarkdown"
	cmdCopyInsert      = "grid.copyInsert"
	cmdCellViewer      = "grid.viewer"
	cmdFormView        = "grid.form"
	cmdJSONView        = "grid.json"
	cmdRecordView      = "grid.record"
	cmdPipeline        = "data.pipeline"
	cmdGoToReferenced  = "data.goToReferenced"
	cmdOpenRowObject   = "data.openRowObject"
	cmdShowReferring   = "data.showReferring"
	cmdDetail          = "data.detail"
	cmdSaveView        = "data.saveView"
	cmdWorkspaceSave   = "file.saveWorkspace"
	cmdWorkspaces      = "file.workspaces"
	cmdViews           = "data.views"
	cmdHideColumn      = "grid.hideColumn"
	cmdShowColumns     = "grid.showColumns"
	cmdMoveLeft        = "grid.moveLeft"
	cmdMoveRight       = "grid.moveRight"
	cmdFreeze          = "grid.freeze"
	cmdUnfreeze        = "grid.unfreeze"
	cmdFind            = "edit.find"
	cmdFindReplace     = "edit.findReplace"
	cmdFindNext        = "edit.findNext"
	cmdFindPrev        = "edit.findPrevious"
	cmdAppearSystem    = "appearance.system"
	cmdAppearLight     = "appearance.light"
	cmdAppearDark      = "appearance.dark"
)

// menuEntry is one menu-bar item: a command, a separator (the zero value), or
// a submenu of commands.
type menuEntry struct {
	cmd  string
	sub  string
	cmds []string
}

func item(id string) menuEntry { return menuEntry{cmd: id} }

var separator = menuEntry{}

func submenu(title string, ids ...string) menuEntry { return menuEntry{sub: title, cmds: ids} }

// menuBar is the menu bar, left to right.
//
// Every command with a shortcut must appear here, because a menu item's
// shortcut is the only kind Fyne delivers while a text field has focus: the
// window tries the main menu before the focused widget, and a focused Entry
// swallows every shortcut it does not own. The same shortcut is therefore
// never also registered on the canvas, which is also what keeps it from
// firing twice. TestEveryShortcutIsOnTheMenuBar holds the line.
//
// Fyne adds the application menu and Quit itself (natively on macOS, into the
// first menu elsewhere), so neither is listed.
var menuBar = []struct {
	title   string
	entries []menuEntry
}{
	{"File", []menuEntry{
		item(cmdConnNew), item(cmdQueryNew), item(cmdOpenSaved), separator,
		submenu("Workspace", cmdWorkspaceSave, cmdWorkspaces), separator,
		item(cmdQuerySave), item(cmdQuerySaveAs), separator,
		item(cmdExport), item(cmdImport), separator,
		item(cmdTabClose),
	}},
	{"Edit", []menuEntry{
		item(cmdFind), item(cmdFindReplace), separator, item(cmdFindNext), item(cmdFindPrev), separator,
		item(cmdCopyCells), submenu("Copy As", cmdCopyCSV, cmdCopyJSON, cmdCopyMarkdown, cmdCopyInsert), item(cmdPasteCells), item(cmdSelectRow), item(cmdSelectColumn), item(cmdSelectAllCells), separator,
		item(cmdEditCell), item(cmdEditValue), item(cmdSetNull), item(cmdSetValue), separator,
		item(cmdInsertRow), item(cmdDuplicateRows), item(cmdDeleteRows), separator,
		item(cmdDesign), item(cmdEditSource), item(cmdRename), item(cmdSaveModel), item(cmdCompare), item(cmdDiagram), separator,
		item(cmdReviewChanges), item(cmdRevertCells), item(cmdRevertRows), item(cmdDiscardAll), separator,
		item(cmdChooseKey),
	}},
	{"View", []menuEntry{
		item(cmdPalette), separator,
		item(cmdSidebar), item(cmdFilterObjects), item(cmdSearchStructure), item(cmdRefresh), item(cmdReload), item(cmdFilterValues), item(cmdColumnStats), item(cmdAggregates), item(cmdWhere), item(cmdPipeline), item(cmdCellViewer), item(cmdFormView), item(cmdJSONView), item(cmdRecordView), item(cmdGoToReferenced), item(cmdOpenRowObject), item(cmdShowReferring), item(cmdDetail), item(cmdChart), separator,
		item(cmdSaveView), item(cmdViews),
		submenu("Columns", cmdHideColumn, cmdShowColumns, cmdMoveLeft, cmdMoveRight, cmdFreeze, cmdUnfreeze), separator,
		submenu("Appearance", cmdAppearSystem, cmdAppearLight, cmdAppearDark),
		submenu("Accent Colour", accentIDs()...),
	}},
	{"Connection", []menuEntry{
		item(cmdOpen), item(cmdStructure), item(cmdFavorite),
		submenu("Script As", cmdScriptSelect, cmdScriptInsert, cmdScriptUpdate, cmdScriptCreate, cmdScriptSchema), separator,
		item(cmdConnEdit), item(cmdConnDup), item(cmdConnDelete), separator,
		item(cmdCopyRows), separator,
		submenu("Batch", cmdMark, cmdMarksClear, cmdMarksSelect, cmdMarksCreate, cmdMarksDrop, cmdMarksExport), separator,
		item(cmdFolderNew), item(cmdFolderEdit), item(cmdFolderDelete), separator,
		item(cmdReconnect), item(cmdDisconnect),
	}},
	{"Query", []menuEntry{item(cmdQueryRun), item(cmdQueryRunAll), separator, item(cmdQueryStop), separator,
		item(cmdExplain), item(cmdExplainMeasure), separator,
		item(cmdTxBegin), item(cmdTxCommit), item(cmdTxRollback), separator,
		item(cmdFormatSQL), item(cmdQueryComplete), separator, item(cmdHistory)}},
	{"Window", []menuEntry{item(cmdTabNext), item(cmdTabPrev), separator,
		item(cmdMoveTabLeft), item(cmdMoveTabRight), item(cmdPinTab), separator,
		item(cmdNewWindow), separator, item(cmdSplitRight), item(cmdSplitDown), item(cmdMoveToPane), item(cmdJoinPanes), separator, item(cmdTasks)}},
	{"Help", []menuEntry{item(cmdShortcuts)}},
}

func (s *Shell) buildMenu() *fyne.MainMenu {
	menus := make([]*fyne.Menu, 0, len(menuBar))
	for _, m := range menuBar {
		menus = append(menus, fyne.NewMenu(m.title, s.menuItemsFor(m.entries)...))
	}
	return fyne.NewMainMenu(menus...)
}

func (s *Shell) menuItemsFor(entries []menuEntry) []*fyne.MenuItem {
	out := make([]*fyne.MenuItem, 0, len(entries))
	for _, e := range entries {
		switch {
		case e.sub != "":
			sub := make([]menuEntry, len(e.cmds))
			for i, id := range e.cmds {
				sub[i] = item(id)
			}
			it := fyne.NewMenuItem(e.sub, nil)
			it.ChildMenu = fyne.NewMenu("", s.menuItemsFor(sub)...)
			out = append(out, it)
		case e.cmd == "":
			out = append(out, fyne.NewMenuItemSeparator())
		default:
			c, ok := s.reg.Get(e.cmd)
			if !ok {
				panic("shell: the menu bar names an unregistered command: " + e.cmd)
			}
			id := e.cmd
			it := fyne.NewMenuItem(c.Title, func() { s.run(id) })
			it.Shortcut = fyneShortcut(c.Shortcut)
			s.menuItems[id] = it
			out = append(out, it)
		}
	}
	return out
}

// run is how every menu item, shortcut and button invokes a command. It goes
// through the registry, which re-checks Enabled: Fyne's own shortcut matching
// fires a menu item's action even while the item is disabled.
func (s *Shell) run(id string) {
	s.followFocus() // a command acts in the pane the focus is in (panes.go)
	if err := s.reg.Run(id); err != nil && !errors.Is(err, commands.ErrDisabled) {
		s.d.Log.Error("running command", "id", id, "err", err)
	}
}

// fyneShortcut converts a chord for a menu item. KeyModifierShortcutDefault
// is ⌘ on macOS and Control elsewhere, so ModControl folding into it off
// macOS matches what commands.Shortcut.Label shows.
func fyneShortcut(sc commands.Shortcut) fyne.Shortcut {
	if sc.IsZero() {
		return nil
	}
	var m fyne.KeyModifier
	if sc.Mods&commands.ModShortcut != 0 {
		m |= fyne.KeyModifierShortcutDefault
	}
	if sc.Mods&commands.ModControl != 0 {
		m |= fyne.KeyModifierControl
	}
	if sc.Mods&commands.ModAlt != 0 {
		m |= fyne.KeyModifierAlt
	}
	if sc.Mods&commands.ModShift != 0 {
		m |= fyne.KeyModifierShift
	}
	key := sc.Key
	if len(key) == 1 {
		key = strings.ToUpper(key)
	}
	return &desktop.CustomShortcut{KeyName: fyne.KeyName(key), Modifier: m}
}
