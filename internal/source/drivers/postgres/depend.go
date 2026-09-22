package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// What names an object, and what a rename does to it (FR-6.6).
//
// The useful question is not "what refers to this" but "what would a rename
// cost", and on PostgreSQL those have very different answers. A view holds
// its base table by OID in a parse tree, so renaming the table carries the
// view and pg_get_viewdef prints the new name afterwards. The same is true
// of a foreign key, an index, a trigger and a column default naming a
// sequence. Listing those as casualties would be a warning about nothing.
//
// What actually breaks is text the engine never resolved: a PL/pgSQL body,
// or a SQL function whose body is a string, both of which look the name up
// when they run. A function written with PostgreSQL 14's BEGIN ATOMIC body
// is parsed at creation and so follows, which is why the two are separated
// here rather than lumped together as "routines".
//
// All of this was settled against a live server, not read off a manual.

// Dependents lists what names the object at ref.
func (s *pgSource) Dependents(ctx context.Context, ref model.ObjectRef) ([]model.Dependent, error) {
	if len(ref.Path) < 3 {
		return nil, fmt.Errorf("postgres: incomplete reference %s", ref)
	}
	db, schema, name := ref.Path[0], ref.Path[1], ref.Path[2]

	// Which queries to run is decided before a pool is opened: a kind
	// nothing can name is answered with nothing, and opening a connection to
	// find that out would be a connection opened for no reason.
	var relation bool
	switch ref.Kind {
	case model.KindTable, model.KindView, model.KindMaterializedView, model.KindSequence:
		relation = true
	case model.KindRoutine, model.KindTrigger, model.KindIndex:
	default:
		return nil, nil
	}

	p, err := s.poolFor(ctx, ref)
	if err != nil {
		return nil, err
	}
	if relation {
		return s.relationDependents(ctx, p, db, schema, name)
	}
	// Nothing in PostgreSQL holds one of these by name from elsewhere: a
	// trigger and an index are named only by the table they sit on, and a
	// routine is called from bodies this can only guess at.
	return s.mentionedIn(ctx, p, db, schema, bareName(name))
}

// relationDependents is what names a table, a view or a sequence.
func (s *pgSource) relationDependents(ctx context.Context, p *pgxpool.Pool, db, schema, name string) ([]model.Dependent, error) {
	var out []model.Dependent

	// Views and materialized views selecting from it. pg_rewrite holds the
	// rule, and pg_depend holds the rule's reference to this relation.
	rows, err := p.Query(ctx, `
		SELECT DISTINCT n.nspname, c.relname, c.relkind
		FROM pg_depend d
		JOIN pg_rewrite r ON r.oid = d.objid AND d.classid = 'pg_rewrite'::regclass
		JOIN pg_class c ON c.oid = r.ev_class
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE d.refclassid = 'pg_class'::regclass
		  AND d.refobjid = to_regclass(quote_ident($1) || '.' || quote_ident($2))
		  AND c.oid <> to_regclass(quote_ident($1) || '.' || quote_ident($2))
		ORDER BY 1, 2`, schema, name)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading what selects from %s.%s: %w", schema, name, err)
	}
	if err := scanInto(rows, &out, func(ns, rel, kind string) model.Dependent {
		what := "A view"
		k := model.KindView
		if kind == "m" {
			what, k = "A materialized view", model.KindMaterializedView
		}
		return model.Dependent{
			Ref:   model.NewRef(k, db, ns, rel),
			Label: ns + "." + rel,
			Note:  what + " that selects from it. PostgreSQL holds it by identity, so the rename carries it.",
		}
	}); err != nil {
		return nil, err
	}

	// Foreign keys pointing at it, and triggers on it. Both follow, but both
	// keep a name that will now say something that is not true.
	rows, err = p.Query(ctx, `
		SELECT n.nspname, c.relname, con.conname
		FROM pg_constraint con
		JOIN pg_class c ON c.oid = con.conrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE con.contype = 'f'
		  AND con.confrelid = to_regclass(quote_ident($1) || '.' || quote_ident($2))
		ORDER BY 1, 2, 3`, schema, name)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading what refers to %s.%s: %w", schema, name, err)
	}
	if err := scanInto(rows, &out, func(ns, rel, con string) model.Dependent {
		return model.Dependent{
			Ref:   model.NewRef(model.KindTable, db, ns, rel),
			Label: ns + "." + rel + " · " + con,
			Note:  "A foreign key pointing at it. It follows the rename, but keeps the name it has.",
		}
	}); err != nil {
		return nil, err
	}

	// Routines whose body mentions the name. This is the part that breaks,
	// and the part nothing can be sure of: the body is text and this is
	// looking for a word in it.
	guesses, err := s.mentionedIn(ctx, p, db, schema, name)
	if err != nil {
		return nil, err
	}
	return append(out, guesses...), nil
}

// mentionedIn finds routines whose body holds the name as a word, in any
// schema of the database.
//
// It is deliberately a search and not a lookup. A body in PL/pgSQL resolves
// its names when it runs, so PostgreSQL has nothing recorded to ask; the
// alternative to guessing is saying nothing, and saying nothing about the
// only thing that breaks would make the warning worthless.
//
// Routines PostgreSQL does know about — those with a BEGIN ATOMIC body,
// parsed when they were created — fall out of this for free rather than
// being excluded by a column: their prosrc is empty, because the body they
// were written with is kept as a parse tree instead. Asking for
// prosqlbody IS NULL would say the same thing and would not run at all
// before PostgreSQL 14.
func (s *pgSource) mentionedIn(ctx context.Context, p *pgxpool.Pool, db, schema, name string) ([]model.Dependent, error) {
	rows, err := p.Query(ctx, `
		SELECT n.nspname,
		       p.proname || '(' || pg_get_function_identity_arguments(p.oid) || ')',
		       l.lanname
		FROM pg_proc p
		JOIN pg_namespace n ON n.oid = p.pronamespace
		JOIN pg_language l ON l.oid = p.prolang
		WHERE p.prokind IN ('f', 'p')
		  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
		  AND p.prosrc ~* ('(^|[^a-zA-Z0-9_])' || $1 || '($|[^a-zA-Z0-9_])')
		ORDER BY 1, 2`, regexpQuote(name))
	if err != nil {
		return nil, fmt.Errorf("postgres: looking for %s in routine bodies: %w", name, err)
	}
	var out []model.Dependent
	if err := scanInto(rows, &out, func(ns, sig, lang string) model.Dependent {
		return model.Dependent{
			Ref:    model.NewRef(model.KindRoutine, db, ns, sig),
			Label:  ns + "." + sig,
			Note:   "Its " + lang + " body names it in text, which PostgreSQL never resolved. The rename will not reach it.",
			Breaks: true,
		}
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// scanInto reads three strings a row and appends what make builds.
func scanInto(rows pgx.Rows, out *[]model.Dependent, make func(a, b, c string) model.Dependent) error {
	defer rows.Close()
	for rows.Next() {
		var a, b, c string
		if err := rows.Scan(&a, &b, &c); err != nil {
			return err
		}
		*out = append(*out, make(a, b, c))
	}
	return rows.Err()
}

// regexpQuote escapes what a POSIX regular expression would read as syntax,
// so a table called "a.b" is looked for as itself.
func regexpQuote(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`\.+*?()|[]{}^$`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// bareName is a routine's name without its arguments, for looking it up in
// the text of another routine, where it is called by name alone.
func bareName(name string) string {
	if before, _, found := strings.Cut(name, "("); found {
		return before
	}
	return name
}
