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
| [0046](0046-mapping-a-files-columns-to-a-tables.md) | Mapping a file's columns to a table's | Accepted |
| [0047](0047-the-import-panel.md) | The import panel | Accepted |
| [0048](0048-the-dry-run.md) | The dry run | Accepted |
| [0049](0049-writing-an-imports-rows.md) | Writing an import's rows | Accepted |
| [0050](0050-loading-rows-in-bulk.md) | Loading rows in bulk | Accepted |
| [0051](0051-adding-or-replacing-a-tables-rows.md) | Adding or replacing a table's rows | Accepted |
| [0052](0052-updating-rows-by-key.md) | Updating rows by key | Accepted |
| [0053](0053-leaving-refused-rows-out.md) | Leaving refused rows out | Accepted |
| [0054](0054-how-an-import-writes-its-rows.md) | How an import writes its rows | Accepted |
| [0055](0055-exporting-an-excel-workbook.md) | Exporting an Excel workbook | Accepted |
| [0056](0056-sql-html-and-xml-exports.md) | SQL, HTML and XML exports | Accepted |
| [0057](0057-what-can-be-typed-next.md) | What can be typed next | Accepted |
| [0058](0058-the-tables-a-statement-reads.md) | The tables a statement reads | Accepted |
| [0059](0059-the-completion-popup.md) | The completion popup | Accepted |
| [0060](0060-what-completion-knows-about-a-schema.md) | What completion knows about a schema | Accepted |
| [0061](0061-snippets.md) | Snippets | Accepted |
| [0062](0062-the-mongodb-connection.md) | The MongoDB connection | Accepted |
| [0063](0063-the-mongodb-tree.md) | The MongoDB tree | Accepted |
| [0064](0064-a-shape-read-from-documents.md) | A shape read from documents | Accepted |
| [0065](0065-a-collections-documents-in-the-grid.md) | A collection's documents, in the grid and as JSON | Accepted |
| [0066](0066-writing-a-document.md) | Writing a document | Accepted |
| [0067](0067-editing-a-document.md) | Editing a document | Accepted |
| [0068](0068-the-aggregation-pipeline.md) | The aggregation pipeline | Accepted |
| [0069](0069-making-and-unmaking-indexes.md) | Making and unmaking indexes | Accepted |
| [0070](0070-the-command-console.md) | The command console | Accepted |
| [0071](0071-a-suite-with-two-paradigms.md) | A conformance suite with two paradigms | Accepted |
| [0072](0072-the-redis-connection.md) | The Redis connection | Accepted |
| [0073](0073-the-key-browser.md) | The key browser | Accepted |
| [0074](0074-what-a-key-holds.md) | What a key holds, and a row that is an object | Accepted |
| [0075](0075-a-log-and-a-document.md) | A log and a document, among a key's kinds | Accepted |
| [0076](0076-a-key-itself.md) | A key itself: how long it has left, and what it is called | Accepted |
| [0077](0077-the-redis-console.md) | The Redis console | Accepted |
| [0078](0078-what-a-server-says-about-itself.md) | What a server says about itself | Accepted |
| [0079](0079-a-keyspace-in-the-suite.md) | A keyspace in the conformance suite | Accepted |
| [0080](0080-the-cassandra-connection.md) | The Cassandra connection | Accepted |
| [0081](0081-the-cassandra-tree.md) | The Cassandra tree | Accepted |
| [0082](0082-the-cql-dialect.md) | The CQL dialect | Accepted |
| [0083](0083-running-cql.md) | Running CQL | Accepted |
| [0084](0084-paging-a-cassandra-table.md) | Paging a Cassandra table | Accepted |
| [0085](0085-the-level-a-read-is-answered-at.md) | The level a read is answered at | Accepted |
| [0086](0086-the-kafka-connection.md) | The Kafka connection | Accepted |
| [0087](0087-proving-who-you-are-to-a-broker.md) | Proving who you are to a broker | Accepted |
| [0088](0088-a-token-instead-of-a-password.md) | A token instead of a password | Accepted |
| [0089](0089-signing-in-to-a-managed-cluster.md) | Signing in to a managed cluster | Accepted |
| [0090](0090-a-certificate-as-the-way-in.md) | A certificate as the way in | Accepted |
| [0091](0091-the-cluster-in-the-tree.md) | The cluster in the tree | Accepted |
| [0092](0092-what-a-topic-list-costs.md) | What a topic list costs | Accepted |
| [0093](0093-what-a-partition-says.md) | What a partition says | Accepted |
| [0094](0094-what-hangs-under-what.md) | What hangs under what | Accepted |
| [0095](0095-reading-a-log-as-it-stands.md) | Reading a log as it stands | Accepted |
| [0096](0096-where-a-read-begins.md) | Where a read begins | Accepted |
| [0097](0097-a-read-with-no-end.md) | A read with no end | Accepted |
| [0098](0098-what-a-record-says-of-itself.md) | What a record says of itself | Accepted |
| [0099](0099-filtering-what-has-been-read.md) | Filtering what has been read | Accepted |
| [0100](0100-what-bytes-can-be-read-as.md) | What bytes can be read as | Accepted |
| [0101](0101-a-second-server-that-says-what-records-mean.md) | A second server that says what records mean | Accepted |
| [0102](0102-reading-a-record-by-its-schema.md) | Reading a record by its schema | Accepted |
| [0103](0103-compiling-a-schema-to-read-a-record.md) | Compiling a schema to read a record | Accepted |
| [0104](0104-a-document-with-five-bytes-in-front.md) | A document with five bytes in front | Accepted |
| [0105](0105-comparing-two-versions-of-a-schema.md) | Comparing two versions of a schema | Accepted |
| [0106](0106-a-description-is-not-rows.md) | A description is not rows | Accepted |
| [0107](0107-inspecting-a-group-is-not-administering-one.md) | Inspecting a group is not administering one | Accepted |
| [0108](0108-writing-a-record-and-what-is-checked-first.md) | Writing a record, and what is checked first | Accepted |
| [0109](0109-what-a-tunnel-proves-before-it-carries-anything.md) | What a tunnel proves before it carries anything | Accepted |
| [0110](0110-an-identity-nobody-typed.md) | An identity nobody typed | Accepted |
| [0111](0111-what-an-import-brings-and-what-it-leaves.md) | What an import brings, and what it leaves | Accepted |
| [0112](0112-a-connection-set-as-a-file-somebody-keeps.md) | A connection set as a file somebody keeps | Accepted |
| [0113](0113-a-production-write-is-typed-not-clicked.md) | A production write is typed, not clicked | Accepted |
| [0114](0114-a-table-changed-on-paper-first.md) | A table changed on paper first | Accepted |
| [0115](0115-what-is-read-is-what-runs.md) | What is read is what runs | Accepted |
