# Architecture Decision Records

One file per architectural decision, numbered sequentially and never renumbered.
A superseded ADR is kept and marked, not deleted — the reasoning is the point.

| ADR | Title | Status |
|---|---|---|
| [0001](0001-fyne-ui-toolkit.md) | Fyne as the UI toolkit | Accepted |
| [0002](0002-data-grid-approach.md) | `widget.Table` is the data grid, provisionally | Accepted |
| [0003](0003-hand-written-sql-lexer.md) | A hand-written SQL lexer, not Chroma | Accepted |
| [0004](0004-chart-rendering.md) | Charts — native marks below a threshold, raster above | Accepted |
| [0005](0005-browser-as-required-data-path.md) | Browse, not Query, is the required data path | Accepted |
| [0006](0006-macos-design-system.md) | macOS Human Interface Guidelines as the design system | Accepted |
| [0007](0007-node-canvas.md) | Node canvas — force-directed layout with culling and LOD | Accepted |
| [0008](0008-postgresql-driver.md) | The PostgreSQL driver, and what the first real driver changed | Accepted |
| [0009](0009-local-store.md) | The local store — settings, secrets, history | Accepted |
| [0010](0010-connection-management.md) | Connection management | Accepted |
| [0011](0011-shell-and-ui-threads.md) | The shell, where UI work runs, and tables without a row count | Accepted |
| [0012](0012-running-queries.md) | Running queries | Accepted |
| [0013](0013-export.md) | Export | Accepted |
| [0014](0014-sqlite-driver.md) | The SQLite driver | Accepted |
| [0015](0015-mysql-mariadb-driver.md) | The MySQL and MariaDB driver | Accepted |
| [0016](0016-grid-interaction.md) | The grid's interaction model | Accepted |
| [0017](0017-driver-panics-contained.md) | A driver's panic is contained | Accepted |
| [0018](0018-explorer-filter.md) | The explorer's filter matches paths, over what is loaded | Accepted |
| [0019](0019-favourites.md) | Favourites live in the settings file | Accepted |
| [0020](0020-explorer-badges.md) | Badges are read as their rows are drawn | Accepted |
| [0021](0021-performance-gates.md) | Performance gates measure what CI can see | Accepted |
| [0022](0022-one-copy-per-data.md) | One copy to a data directory | Accepted |
| [0023](0023-native-file-dialogs-and-notifications.md) | The platform's own file dialogs, and notifications from the background | Accepted |
| [0024](0024-object-classes.md) | Object classes are the model's | Accepted |
| [0025](0025-connection-folders.md) | Connection folders | Accepted |
| [0026](0026-query-parameters.md) | Query parameters | Accepted |
| [0027](0027-pending-changes.md) | Pending changes | Accepted |
| [0028](0028-pending-changes-in-the-grid.md) | Pending changes in the grid | Accepted |
| [0029](0029-cells-edited-in-place.md) | Cells edited in place | Accepted |
| [0030](0030-new-rows-duplicates-and-deletions.md) | New rows, duplicates and deletions | Accepted |
| [0031](0031-changes-planned-and-applied.md) | Changes planned as SQL and applied in one transaction | Accepted |
| [0032](0032-reviewing-and-committing-changes.md) | Reviewing and committing changes | Accepted |
| [0033](0033-reverting-changes.md) | Reverting changes, and asking before they are lost | Accepted |
| [0034](0034-row-identity.md) | Row identity | Accepted |
| [0035](0035-where-a-querys-columns-come-from.md) | Where a query's columns come from | Accepted |
| [0036](0036-editing-a-querys-result.md) | Editing a query's result | Accepted |
| [0037](0037-pasting-a-block.md) | Pasting a block of cells | Accepted |
| [0038](0038-a-value-set-across-a-selection.md) | A value set across a selection | Accepted |
| [0039](0039-the-form-view.md) | The form view | Accepted |
| [0040](0040-going-to-a-referenced-row.md) | Going to the row a foreign key refers to | Accepted |
| [0041](0041-rows-that-refer-to-a-row.md) | Rows that refer to a row | Accepted |
| [0042](0042-a-keys-value-shown-with-its-label.md) | A foreign key's value shown with its row's label | Accepted |
| [0043](0043-master-and-detail.md) | Master and detail | Accepted |
| [0044](0044-reading-the-files-an-import-takes.md) | Reading the files an import takes | Accepted |
| [0045](0045-finding-what-a-file-is.md) | Finding what a file is | Accepted |
