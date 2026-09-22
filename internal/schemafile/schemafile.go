// Package schemafile keeps a database's structure on disk as a tree of
// files, one per object (FR-7.6), so that a live database can be compared
// against a model somebody saved (FR-7.1).
//
// The point of one file per object is version control. A schema in a single
// file produces one enormous diff for one added column, and a review of that
// is nobody reading it. One file per object means a commit shows the objects
// that changed, and a review is possible.
//
// What is written is structure and not statistics. A row count changes every
// day and is never a difference worth committing, so it is not saved; the
// comparison ignores it for the same reason (ADR-0119).
package schemafile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/diff"
	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Format is what this writes, and what it will read. It is in the file so
// that a model saved by a later version says so rather than being read
// wrongly by an earlier one.
const Format = "ikigai-schema/1"

// dbFile names the file at the root of a saved model. Its presence is what
// says a directory holds one.
const dbFile = "database.json"

// schemaFile names the file holding a schema's own properties.
const schemaFile = "schema.json"

// classDir is the directory each kind of object is written into. The names
// are the plurals the explorer uses, because somebody reading the tree in a
// repository is reading the same words they read in the window.
var classDir = []struct {
	kind model.ObjectKind
	dir  string
}{
	{model.KindTable, "tables"},
	{model.KindView, "views"},
	{model.KindRoutine, "routines"},
	{model.KindSequence, "sequences"},
	{model.KindUserType, "types"},
}

// database is the root file: the database's own properties and the schemas
// it holds, which is the list a reader walks.
type database struct {
	Format  string   `json:"format"`
	Name    string   `json:"name"`
	Charset string   `json:"charset,omitempty"`
	Collate string   `json:"collate,omitempty"`
	Comment string   `json:"comment,omitempty"`
	Schemas []string `json:"schemas"`

	// Ignore is what a comparison against this model leaves out (FR-7.5).
	//
	// It lives here because it is part of the same agreement the model is:
	// a team decides once that the audit schema is nobody's to deploy, and
	// the decision travels in version control beside the objects it is
	// about. Keeping it with the connection instead would make a comparison
	// mean something different on each person's machine.
	Ignore *diff.Options `json:"ignore,omitempty"`
}

// schemaOwn is a schema's own properties, without the objects in it: those
// are files of their own.
type schemaOwn struct {
	Name    string            `json:"name"`
	Owner   string            `json:"owner,omitempty"`
	Comment string            `json:"comment,omitempty"`
	Attrs   map[string]string `json:"attrs,omitempty"`
}

// ErrNotAModel is a directory that holds something other than a saved model.
var ErrNotAModel = errors.New("schemafile: this directory does not hold a saved schema")

// Write saves a database's structure as a tree of files under dir.
//
// The whole tree is built beside dir and swapped into place, so a reader or
// a crash sees the model that was there or the model that replaced it and
// never half of each. Writing object by object into the live directory would
// leave a model that is neither, and a comparison against it would be wrong
// in a way nobody could see.
func Write(dir string, db *model.Database) (err error) {
	if db == nil {
		return errors.New("schemafile: there is no model to save")
	}
	if err := checkModel(db); err != nil {
		return err
	}
	if err := refuseIfNotOurs(dir); err != nil {
		return err
	}

	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, "."+filepath.Base(dir)+".writing-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(staging)
		}
	}()
	// Saving a model again keeps the rules agreed about it: they are an
	// agreement about what to compare, not a fact about the database, and
	// reading the database again is no reason to throw them away.
	if err = writeTree(staging, db, keeping(dir)); err != nil {
		return err
	}
	return swap(dir, staging)
}

// checkModel refuses a model that cannot be written as files before any of
// it is, rather than leaving a half-written tree behind.
func checkModel(db *model.Database) error {
	if err := checkNames("schema", names(db.Schemas, func(s model.Schema) string { return s.Name })); err != nil {
		return err
	}
	for _, s := range db.Schemas {
		for _, c := range []struct {
			kind  string
			names []string
		}{
			{"table", names(s.Tables, func(t model.Table) string { return t.Name })},
			{"view", names(s.Views, func(v model.View) string { return v.Name })},
			{"routine", names(s.Routines, func(r model.Routine) string { return r.Name })},
			{"sequence", names(s.Sequences, func(q model.Sequence) string { return q.Name })},
			{"type", names(s.UserTypes, func(u model.UserType) string { return u.Name })},
		} {
			if err := checkNames(c.kind, c.names); err != nil {
				return fmt.Errorf("in schema %s: %w", s.Name, err)
			}
		}
	}
	return nil
}

func names[T any](list []T, name func(T) string) []string {
	out := make([]string, len(list))
	for i, v := range list {
		out[i] = name(v)
	}
	return out
}

// refuseIfNotOurs will not overwrite a directory that holds something else.
//
// Saving a model replaces everything under the directory, so pointing it at
// the wrong one would delete somebody's work. An empty directory, or one
// that has a model in it already, is fair game; anything else is a mistake
// worth stopping.
func refuseIfNotOurs(dir string) error {
	entries, err := os.ReadDir(dir)
	switch {
	case os.IsNotExist(err):
		return nil
	case err != nil:
		return err
	case len(entries) == 0:
		return nil
	}
	if _, err := os.Stat(filepath.Join(dir, dbFile)); err == nil {
		return nil
	}
	return fmt.Errorf("schemafile: %s holds something already, and saving a model replaces "+
		"everything in it: %w", dir, ErrNotAModel)
}

// swap puts the staged tree where the model goes, and takes the old one out
// of the way first so the replacement is one rename rather than a delete and
// a hope.
func swap(dir, staging string) error {
	aside := ""
	if _, err := os.Stat(dir); err == nil {
		aside = dir + ".replaced"
		os.RemoveAll(aside)
		if err := os.Rename(dir, aside); err != nil {
			return err
		}
	}
	if err := os.Rename(staging, dir); err != nil {
		if aside != "" {
			os.Rename(aside, dir) // put back what was there
		}
		return err
	}
	if aside != "" {
		os.RemoveAll(aside)
	}
	return nil
}

func writeTree(dir string, db *model.Database, keep *diff.Options) error {
	root := database{Format: Format, Name: db.Name, Charset: db.Charset,
		Collate: db.Collate, Comment: db.Comment, Ignore: keep}
	for _, s := range db.Schemas {
		root.Schemas = append(root.Schemas, s.Name)
	}
	if err := writeJSON(filepath.Join(dir, dbFile), root); err != nil {
		return err
	}
	for _, s := range db.Schemas {
		at := filepath.Join(dir, fileName(s.Name))
		if err := writeJSON(filepath.Join(at, schemaFile),
			schemaOwn{Name: s.Name, Owner: s.Owner, Comment: s.Comment, Attrs: s.Attrs}); err != nil {
			return err
		}
		if err := writeObjects(at, s); err != nil {
			return err
		}
	}
	return nil
}

func writeObjects(at string, s model.Schema) error {
	for _, t := range s.Tables {
		// The structure, not the statistics: a row count changes every day
		// and would put a diff in the repository for nothing.
		saved := t
		saved.RowsEstimate = 0
		if err := writeJSON(objectPath(at, model.KindTable, t.Name), saved); err != nil {
			return err
		}
	}
	for _, v := range s.Views {
		if err := writeJSON(objectPath(at, model.KindView, v.Name), v); err != nil {
			return err
		}
	}
	for _, r := range s.Routines {
		if err := writeJSON(objectPath(at, model.KindRoutine, r.Name), r); err != nil {
			return err
		}
	}
	for _, q := range s.Sequences {
		if err := writeJSON(objectPath(at, model.KindSequence, q.Name), q); err != nil {
			return err
		}
	}
	for _, u := range s.UserTypes {
		if err := writeJSON(objectPath(at, model.KindUserType, u.Name), u); err != nil {
			return err
		}
	}
	return nil
}

func objectPath(at string, kind model.ObjectKind, name string) string {
	return filepath.Join(at, dirOf(kind), fileName(name)+".json")
}

func dirOf(kind model.ObjectKind) string {
	for _, c := range classDir {
		if c.kind == kind {
			return c.dir
		}
	}
	return string(kind)
}

// writeJSON writes one object, indented and newline-terminated, because what
// this is for is being read in a diff.
func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

// Ignore is what a comparison against the model in dir leaves out.
//
// A directory that holds no model, or a model that says nothing about it,
// answers no rules — which is not an error: most models have none.
func Ignore(dir string) (diff.Options, error) {
	var root database
	if err := readJSON(filepath.Join(dir, dbFile), &root); err != nil {
		if os.IsNotExist(err) {
			return diff.Options{}, fmt.Errorf("schemafile: %s: %w", dir, ErrNotAModel)
		}
		return diff.Options{}, err
	}
	if root.Ignore == nil {
		return diff.Options{}, nil
	}
	return *root.Ignore, nil
}

// SetIgnore writes what a comparison against this model should leave out,
// keeping everything else about it exactly as it is.
//
// Only the root file is rewritten: the rules are an agreement about the
// model and not a change to any object in it, so a commit that changes them
// should touch one file and say so.
func SetIgnore(dir string, opt diff.Options) error {
	if err := opt.Check(); err != nil {
		return err
	}
	var root database
	if err := readJSON(filepath.Join(dir, dbFile), &root); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("schemafile: %s: %w", dir, ErrNotAModel)
		}
		return err
	}
	root.Ignore = nil
	if opt.Any() {
		root.Ignore = &opt
	}
	return writeJSON(filepath.Join(dir, dbFile), root)
}

// Read loads a model saved by Write.
func Read(dir string) (*model.Database, error) {
	var root database
	if err := readJSON(filepath.Join(dir, dbFile), &root); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("schemafile: %s: %w", dir, ErrNotAModel)
		}
		return nil, err
	}
	if root.Format != Format {
		return nil, fmt.Errorf("schemafile: %s was saved as %q and this reads %q",
			dir, root.Format, Format)
	}
	db := &model.Database{Name: root.Name, Charset: root.Charset,
		Collate: root.Collate, Comment: root.Comment}
	for _, name := range root.Schemas {
		s, err := readSchema(filepath.Join(dir, fileName(name)))
		if err != nil {
			return nil, err
		}
		db.Schemas = append(db.Schemas, s)
	}
	return db, nil
}

func readSchema(at string) (model.Schema, error) {
	var own schemaOwn
	if err := readJSON(filepath.Join(at, schemaFile), &own); err != nil {
		return model.Schema{}, err
	}
	s := model.Schema{Name: own.Name, Owner: own.Owner, Comment: own.Comment, Attrs: own.Attrs}
	if err := readInto(at, model.KindTable, &s.Tables); err != nil {
		return s, err
	}
	// A table read back says it does not know how many rows it has, which is
	// the truth: a file does not.
	for i := range s.Tables {
		s.Tables[i].RowsEstimate = -1
	}
	if err := readInto(at, model.KindView, &s.Views); err != nil {
		return s, err
	}
	if err := readInto(at, model.KindRoutine, &s.Routines); err != nil {
		return s, err
	}
	if err := readInto(at, model.KindSequence, &s.Sequences); err != nil {
		return s, err
	}
	return s, readInto(at, model.KindUserType, &s.UserTypes)
}

// readInto reads every object of one kind, in the order the names sort, so
// that reading a model twice gives the same model.
func readInto[T any](at string, kind model.ObjectKind, into *[]T) error {
	dir := filepath.Join(at, dirOf(kind))
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	// os.ReadDir hands back its entries sorted by name, which is the order
	// this wants and the reason nothing sorts them again: reading a model
	// twice has to give the same model, and a second sort over an already
	// sorted list would be a line no test could tell the absence of.
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			files = append(files, e.Name())
		}
	}
	for _, f := range files {
		var v T
		if err := readJSON(filepath.Join(dir, f), &v); err != nil {
			return err
		}
		*into = append(*into, v)
	}
	return nil
}

func readJSON(path string, into any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, into); err != nil {
		return fmt.Errorf("schemafile: %s: %w", path, err)
	}
	return nil
}

// keeping is the rules a model already carries, or none where there is no
// model there yet.
func keeping(dir string) *diff.Options {
	opt, err := Ignore(dir)
	if err != nil || !opt.Any() {
		return nil
	}
	return &opt
}
