// Package localdb is the application's own SQLite database: query history,
// saved queries and session state (FR-17.3, FR-17.4).
//
// modernc.org/sqlite is pure Go, so this database needs no C library of its
// own, even though the application as a whole links Fyne through CGO.
//
// This package must not import any UI package (ARCH-1).
package localdb

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	_ "modernc.org/sqlite"

	"github.com/ikigai-db/ikigai-db/internal/redact"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

var (
	// ErrNewerSchema: the database was written by a newer build. Opening it
	// anyway would let this build write rows the newer schema does not expect.
	ErrNewerSchema = errors.New("localdb: the database was written by a newer version of Ikigai DB")
	// ErrNameTaken: a saved query with that name already exists in the folder.
	ErrNameTaken = errors.New("localdb: a saved query with that name already exists in this folder")
)

const (
	// MaxHistory bounds the history table. Disk is cheaper than memory, but
	// not free, and history nobody can scroll back to is not worth keeping.
	MaxHistory = 50_000
	pruneEvery = 500
)

// migrations are applied in order; migration i brings the schema to version
// i+1. The list is APPEND-ONLY. An applied migration is never edited, because
// a database that has already run it would never see the edit.
var migrations = []string{
	// 1: history (with full-text search), saved queries, session state.
	`
	CREATE TABLE history (
	    id            INTEGER PRIMARY KEY,
	    connection_id TEXT    NOT NULL,
	    database      TEXT    NOT NULL DEFAULT '',
	    statement     TEXT    NOT NULL,
	    started_at    INTEGER NOT NULL,           -- unix milliseconds, UTC
	    duration_ms   INTEGER NOT NULL DEFAULT 0,
	    rows          INTEGER NOT NULL DEFAULT -1, -- -1: unknown
	    error         TEXT    NOT NULL DEFAULT ''
	);
	CREATE INDEX history_by_connection ON history (connection_id, started_at DESC);
	CREATE INDEX history_by_time       ON history (started_at DESC);

	-- Underscore is a token character so an identifier such as customer_id is
	-- one word, the way someone searching their SQL thinks of it.
	CREATE VIRTUAL TABLE history_fts USING fts5 (
	    statement, content = 'history', content_rowid = 'id',
	    tokenize = "unicode61 tokenchars '_'"
	);
	CREATE TRIGGER history_ai AFTER INSERT ON history BEGIN
	    INSERT INTO history_fts (rowid, statement) VALUES (new.id, new.statement);
	END;
	CREATE TRIGGER history_ad AFTER DELETE ON history BEGIN
	    INSERT INTO history_fts (history_fts, rowid, statement) VALUES ('delete', old.id, old.statement);
	END;

	CREATE TABLE saved_queries (
	    id            TEXT    PRIMARY KEY,
	    folder        TEXT    NOT NULL DEFAULT '',
	    name          TEXT    NOT NULL,
	    connection_id TEXT    NOT NULL DEFAULT '',
	    body          TEXT    NOT NULL,
	    created_at    INTEGER NOT NULL,
	    updated_at    INTEGER NOT NULL,
	    UNIQUE (folder, name)
	);

	CREATE TABLE kv (
	    key        TEXT    PRIMARY KEY,
	    value      BLOB    NOT NULL,
	    updated_at INTEGER NOT NULL
	);
	`,
}

// DB is the local database. Safe for concurrent use.
type DB struct {
	db         *sql.DB
	maxHistory int
}

// Open opens, creating if needed, and migrates the database at path.
func Open(ctx context.Context, path string) (*DB, error) {
	// SQLite creates its file under the process umask, typically 0644. History
	// holds everything the user has run, so the file is created private
	// first. SQLite gives the -wal and -shm files the main file's mode.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("localdb: %w", err)
	}
	f.Close()

	// Pragmas go in the DSN, not an Exec after opening, because database/sql
	// pools connections and busy_timeout and foreign_keys are per connection.
	// WAL lets a history search read while a statement is being recorded;
	// _txlock=immediate takes the write lock at BEGIN, so two writers never
	// deadlock trying to upgrade a read lock.
	esc := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(path)
	dsn := "file:" + esc + "?_txlock=immediate" +
		"&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("localdb: %w", err)
	}
	db.SetMaxOpenConns(4)

	d := &DB{db: db, maxHistory: MaxHistory}
	if err := d.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return d, nil
}

// Close closes the database.
func (d *DB) Close() error { return d.db.Close() }

// BackupTo writes a whole copy of the database to a file that does not
// exist yet (FR-17.5).
//
// VACUUM INTO does it: it runs while the database is open and in use, and
// what it writes is the database as of when it ran, with no -wal beside it
// to carry. Copying the file by hand would copy whatever was mid-write.
func (d *DB) BackupTo(ctx context.Context, path string) error {
	if _, err := d.db.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		return fmt.Errorf("localdb: copying the database: %w", err)
	}
	return nil
}

// SchemaVersion returns the schema version on disk.
func (d *DB) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	err := d.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v)
	return v, err
}

func (d *DB) migrate(ctx context.Context) error {
	v, err := d.SchemaVersion(ctx)
	if err != nil {
		return fmt.Errorf("localdb: reading schema version: %w", err)
	}
	if v > len(migrations) {
		return fmt.Errorf("%w (schema %d; this build understands %d)", ErrNewerSchema, v, len(migrations))
	}
	for ; v < len(migrations); v++ {
		if err := d.apply(ctx, v); err != nil {
			return fmt.Errorf("localdb: migration %d: %w", v+1, err)
		}
	}
	return nil
}

// apply runs one migration and records its version in the same transaction.
// SQLite's DDL is transactional, so a migration that fails part-way leaves
// the schema exactly as it was: history is never half-migrated.
func (d *DB) apply(ctx context.Context, i int) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
		return err
	}
	return tx.Commit()
}

// --- history (FR-5.8) ----------------------------------------------------------

// HistoryEntry is one executed statement.
type HistoryEntry struct {
	ID           int64
	ConnectionID string
	Database     string
	// Language selects the lexer used to redact the statement; see
	// capability.Query.Language. Empty means PostgreSQL's rules.
	Language  string
	Statement string
	StartedAt time.Time
	Duration  time.Duration
	Rows      int64 // -1 when unknown
	Error     string
}

// AddHistory records a statement, redacted.
func (d *DB) AddHistory(ctx context.Context, e HistoryEntry) (int64, error) {
	res, err := d.db.ExecContext(ctx, `
		INSERT INTO history (connection_id, database, statement, started_at, duration_ms, rows, error)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.ConnectionID, e.Database, RedactStatement(e.Language, e.Statement),
		e.StartedAt.UTC().UnixMilli(), e.Duration.Milliseconds(), e.Rows, redact.String(e.Error))
	if err != nil {
		return 0, fmt.Errorf("localdb: recording history: %w", err)
	}
	id, _ := res.LastInsertId()
	if id%pruneEvery == 0 {
		d.PruneHistory(ctx, d.maxHistory)
	}
	return id, nil
}

// HistoryQuery filters history.
type HistoryQuery struct {
	// Text is words to find. Each must appear, and each matches as a prefix,
	// so "cust ord" finds "SELECT customer_id FROM orders".
	Text         string
	ConnectionID string
	FailedOnly   bool
	Limit        int
}

// SearchHistory returns matching entries, newest first.
func (d *DB) SearchHistory(ctx context.Context, q HistoryQuery) ([]HistoryEntry, error) {
	limit := q.Limit
	if limit <= 0 || limit > 5000 {
		limit = 200
	}
	from := "history h"
	var where []string
	var args []any
	if m := ftsQuery(q.Text); m != "" {
		from = "history h JOIN history_fts ON history_fts.rowid = h.id"
		where = append(where, "history_fts MATCH ?")
		args = append(args, m)
	}
	if q.ConnectionID != "" {
		where = append(where, "h.connection_id = ?")
		args = append(args, q.ConnectionID)
	}
	if q.FailedOnly {
		where = append(where, "h.error <> ''")
	}
	query := "SELECT h.id, h.connection_id, h.database, h.statement, h.started_at, h.duration_ms, h.rows, h.error FROM " + from
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY h.started_at DESC, h.id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("localdb: searching history: %w", err)
	}
	defer rows.Close()
	var out []HistoryEntry
	for rows.Next() {
		var e HistoryEntry
		var started, dur int64
		if err := rows.Scan(&e.ID, &e.ConnectionID, &e.Database, &e.Statement, &started, &dur, &e.Rows, &e.Error); err != nil {
			return nil, err
		}
		e.StartedAt = time.UnixMilli(started).UTC()
		e.Duration = time.Duration(dur) * time.Millisecond
		out = append(out, e)
	}
	return out, rows.Err()
}

// ftsQuery turns what the user typed into a safe FTS5 query: every word
// quoted, as a prefix, all required.
//
// FTS5's query syntax has operators — OR, NOT, NEAR, column filters, ^ — so
// passing user text through turns a search for "order-by", or a stray quote,
// into a syntax error or a different query entirely.
func ftsQuery(text string) string {
	var terms []string
	for _, w := range strings.Fields(text) {
		if strings.IndexFunc(w, isTokenRune) < 0 {
			continue // nothing searchable; an empty phrase is an FTS5 syntax error
		}
		terms = append(terms, `"`+strings.ReplaceAll(w, `"`, `""`)+`"*`)
	}
	return strings.Join(terms, " ")
}

func isTokenRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// PruneHistory keeps the newest keep entries. The delete trigger keeps the
// search index in step.
func (d *DB) PruneHistory(ctx context.Context, keep int) (int64, error) {
	res, err := d.db.ExecContext(ctx, `
		DELETE FROM history WHERE id NOT IN (
		    SELECT id FROM history ORDER BY started_at DESC, id DESC LIMIT ?)`, keep)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// RedactStatement removes credentials from a statement before it is written
// to history, which is a plaintext record of everything the user has run.
//
// It masks the literal that follows PASSWORD (CREATE/ALTER ROLE, MySQL's SET
// PASSWORD), the literal or bare name after IDENTIFIED BY or IDENTIFIED WITH
// ... AS (MySQL, Oracle), and any DSN-style secret inside a string, as in
// dblink_connect('... password=...'). It works through the lexer, so a word
// PASSWORD inside a comment or string arms nothing, and a column named
// password_hash is not the keyword PASSWORD.
//
// Saved queries are deliberately NOT redacted. The user saved them on
// purpose, and silently rewriting their script would corrupt their work.
func RedactStatement(language, stmt string) string {
	const maskString, maskName = "'[REDACTED]'", "[REDACTED]"

	lx := sqllex.NewLexer(sqllex.DialectFor(language))
	var st sqllex.State
	var sb strings.Builder
	sb.Grow(len(stmt))

	const (
		idle            = iota
		afterPassword   // mask the next string literal
		afterIdentified // mask the next string literal or bare name
	)
	armed := idle
	sawIdentified := false
	swallow := false // inside a masked string that spans lines

	for li, line := range strings.Split(stmt, "\n") {
		if li > 0 {
			sb.WriteByte('\n')
		}
		toks, next := lx.LexLine(line, st)
		for ti, tk := range toks {
			text := line[tk.Start:tk.End]

			if swallow {
				if tk.Kind == sqllex.TokString && tk.Start == 0 {
					// The rest of a masked multi-line literal: drop it, and stop
					// swallowing once the literal closes on this line.
					swallow = ti == len(toks)-1 && next.Kind != sqllex.StateNormal
					continue
				}
				swallow = false
			}

			switch tk.Kind {
			case sqllex.TokString:
				if armed != idle {
					sb.WriteString(maskString)
					armed = idle
					swallow = ti == len(toks)-1 && next.Kind != sqllex.StateNormal
					continue
				}
				sb.WriteString(redact.String(text))
				continue

			case sqllex.TokIdentifier, sqllex.TokQuotedIdent:
				if armed == afterIdentified {
					sb.WriteString(maskName)
					armed = idle
					continue
				}

			case sqllex.TokPunctuation:
				if text == ";" {
					armed, sawIdentified = idle, false
				}
			}

			switch tk.Kind {
			case sqllex.TokKeyword, sqllex.TokIdentifier, sqllex.TokFunction:
				switch w := strings.ToLower(text); {
				case w == "password" || w == "passwd":
					armed = afterPassword
				case w == "identified":
					sawIdentified = true
				case sawIdentified && (w == "by" || w == "as"):
					armed = afterIdentified
				}
			}
			sb.WriteString(text)
		}
		st = next
	}
	return sb.String()
}

// --- saved queries (FR-5.9) ----------------------------------------------------

// SavedQuery is a script the user chose to keep.
type SavedQuery struct {
	ID           string
	Folder       string
	Name         string
	ConnectionID string
	Body         string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// SaveQuery creates or updates a saved query, verbatim.
func (d *DB) SaveQuery(ctx context.Context, q SavedQuery) (SavedQuery, error) {
	if strings.TrimSpace(q.Name) == "" {
		return SavedQuery{}, errors.New("localdb: a saved query needs a name")
	}
	now := time.Now().UTC().UnixMilli()
	if q.ID == "" {
		q.ID = newID()
	}
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO saved_queries (id, folder, name, connection_id, body, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
		    folder = excluded.folder, name = excluded.name,
		    connection_id = excluded.connection_id, body = excluded.body,
		    updated_at = excluded.updated_at`,
		q.ID, q.Folder, q.Name, q.ConnectionID, q.Body, now, now)
	if err != nil {
		// SQLite's wording for this has been stable for over a decade.
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return SavedQuery{}, ErrNameTaken
		}
		return SavedQuery{}, fmt.Errorf("localdb: saving query: %w", err)
	}
	return d.savedQuery(ctx, q.ID)
}

func (d *DB) savedQuery(ctx context.Context, id string) (SavedQuery, error) {
	var q SavedQuery
	var created, updated int64
	err := d.db.QueryRowContext(ctx, `
		SELECT id, folder, name, connection_id, body, created_at, updated_at
		FROM saved_queries WHERE id = ?`, id).
		Scan(&q.ID, &q.Folder, &q.Name, &q.ConnectionID, &q.Body, &created, &updated)
	q.CreatedAt, q.UpdatedAt = time.UnixMilli(created).UTC(), time.UnixMilli(updated).UTC()
	return q, err
}

// SavedQueries lists saved queries, by folder then name.
func (d *DB) SavedQueries(ctx context.Context) ([]SavedQuery, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT id, folder, name, connection_id, body, created_at, updated_at
		FROM saved_queries ORDER BY folder, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SavedQuery
	for rows.Next() {
		var q SavedQuery
		var created, updated int64
		if err := rows.Scan(&q.ID, &q.Folder, &q.Name, &q.ConnectionID, &q.Body, &created, &updated); err != nil {
			return nil, err
		}
		q.CreatedAt, q.UpdatedAt = time.UnixMilli(created).UTC(), time.UnixMilli(updated).UTC()
		out = append(out, q)
	}
	return out, rows.Err()
}

// DeleteQuery removes a saved query. Deleting a missing one is not an error.
func (d *DB) DeleteQuery(ctx context.Context, id string) error {
	_, err := d.db.ExecContext(ctx, "DELETE FROM saved_queries WHERE id = ?", id)
	return err
}

// --- session state (FR-15.2, NFR-R3) --------------------------------------------

// Put stores a value under a key: open tabs, layout, scratch buffers.
func (d *DB) Put(ctx context.Context, key string, value []byte) error {
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO kv (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, time.Now().UTC().UnixMilli())
	return err
}

// Get returns the value under a key, and whether one was set.
func (d *DB) Get(ctx context.Context, key string) ([]byte, bool, error) {
	var v []byte
	err := d.db.QueryRowContext(ctx, "SELECT value FROM kv WHERE key = ?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return v, err == nil, err
}

// Delete removes a key.
func (d *DB) Delete(ctx context.Context, key string) error {
	_, err := d.db.ExecContext(ctx, "DELETE FROM kv WHERE key = ?", key)
	return err
}

func newID() string {
	var b [12]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
