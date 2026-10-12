// Package job is an export or an import written down to be run again
// (FR-10.11, ADR-0181): the file, and the words it is written in. It holds no
// UI and runs nothing (ARCH-1); the command line runs a job, and both it and
// the window write one.
//
// A job is a command's flags in a file. Not a second description of an export
// that has to be kept in step with the first: the flags a command takes are
// the only definition there is of what it can be told, so a job is those, by
// their names, and running one is running the command with them.
//
// The file is JSON, with its keys in the alphabet's order, so that it reads
// the same whoever wrote it and a change to it is a line in a diff. It is made
// to be committed.
//
// Three things are never in one:
//
//   - A connection string. It may hold a password, and a file made to be
//     committed is the last place for one. A job names a saved connection, or
//     a driver and a database; a pipeline passes --url, or sets IKIGAI_URL,
//     when it runs the job.
//   - Consent. --confirm is what somebody says when they run a thing, about
//     that run. Written into the file it would be said by nobody, every time.
//   - Where the job itself is kept.
//
// A file a job names — what it reads, what it writes — is kept as a path from
// the job's own directory, so that a job and its files can be moved, or
// checked out somewhere else, together.
package job

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/transfer"
)

// Version is the one shape of job file there is.
const Version = 1

// File is a job as it is kept.
type File struct {
	Version int            `json:"ikigai_job"`
	Command string         `json:"command"`
	Flags   map[string]any `json:"flags"`
}

// SaveFlag is the flag that writes a command down instead of running it.
const SaveFlag = "save-job"

// Commands are the commands a job can be of: the two FR-10.11 names.
var Commands = map[string]bool{"export": true, "import": true}

// NeverSaved are the flags a job does not keep, each with why, as a person is
// told it; a flag with no why is left out without being refused.
var NeverSaved = map[string]string{
	"url":     "a connection string may hold a password, and a job is a file made to be shared; name a saved connection with --connection, or pass --url when the job is run",
	"confirm": "",
}

// PathFlags are the flags that name a file, which a job keeps as a path from
// its own directory.
var PathFlags = map[string]bool{"out": true, "from": true, "file": true}

// Write keeps a command and its flags as the job at path. A flag that names a
// file is given as the path it was told, and kept as the way to it from the
// job. A flag a job never keeps is an error here, whoever asks: the rule is
// the file's, not one caller's.
func Write(path, command string, flags map[string]any) error {
	if !Commands[command] {
		return fmt.Errorf("a job is an export or an import, not %q", command)
	}
	dir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return err
	}
	f := File{Version: Version, Command: command, Flags: make(map[string]any, len(flags))}
	for name, v := range flags {
		if _, never := NeverSaved[name]; never || name == SaveFlag {
			return fmt.Errorf("a job does not keep --%s", name)
		}
		if s, ok := v.(string); ok && PathFlags[name] {
			v = pathFrom(dir, s)
		}
		f.Flags[name] = v
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(f); err != nil {
		return err
	}
	return os.WriteFile(path, b.Bytes(), 0o644)
}

// pathFrom is a file's path as a job keeps it: from the job's directory, with
// the slashes every system reads. A file that cannot be reached from there —
// on another volume — is kept as where it is.
func pathFrom(dir, path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	rel, err := filepath.Rel(dir, abs)
	if err != nil {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(rel)
}

// Read is the job in a file, or what is wrong with the file.
func Read(path string) (File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return File{}, err
	}
	var f File
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields() // a key nobody reads is a mistake nobody sees
	dec.UseNumber()
	if err := dec.Decode(&f); err != nil {
		return File{}, fmt.Errorf("%s is not a job: %w", path, err)
	}
	switch {
	case f.Version == 0:
		return File{}, fmt.Errorf("%s is not a job: it has no \"ikigai_job\"", path)
	case f.Version != Version:
		return File{}, fmt.Errorf("%s is a job of version %d, and this reads version %d", path, f.Version, Version)
	case !Commands[f.Command]:
		return File{}, fmt.Errorf("%s: a job is an export or an import, not %q", path, f.Command)
	}
	return f, nil
}

// Values is a flag's value from a job as the flag is told it: once, or once
// for each of a list, which is a flag said more than once. A flag that names a
// file is told where the file is, found from dir, the job's own directory.
func Values(name string, v any, dir string) ([]string, error) {
	list, ok := v.([]any)
	if !ok {
		list = []any{v}
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		s, err := text(item)
		if err != nil {
			return nil, err
		}
		if PathFlags[name] && s != "" && !filepath.IsAbs(filepath.FromSlash(s)) {
			s = filepath.Join(dir, filepath.FromSlash(s))
		}
		out = append(out, s)
	}
	return out, nil
}

// text is one value from a job as a flag reads one.
func text(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case bool:
		return fmt.Sprint(x), nil
	case json.Number:
		return x.String(), nil
	}
	return "", errors.New("a flag's value is text, a number, or true or false, or a list of those")
}

// The words a command line, and so a job, uses for the formats a file is
// read as, for how a text file's characters are written, and for the
// delimiters that are hard to say as themselves.
var (
	ReadFormats = map[string]transfer.Format{
		"csv":     transfer.CSV,
		"tsv":     transfer.TSV,
		"json":    transfer.JSON,
		"ndjson":  transfer.NDJSON,
		"xlsx":    transfer.XLSX,
		"yaml":    transfer.YAML,
		"dbf":     transfer.DBF,
		"parquet": transfer.Parquet,
	}
	Encodings = map[string]transfer.Encoding{
		"utf-8":        transfer.UTF8,
		"utf-16le":     transfer.UTF16LE,
		"utf-16be":     transfer.UTF16BE,
		"windows-1252": transfer.Windows1252,
	}
	Delimiters = map[string]rune{"comma": ',', "tab": '\t', "semicolon": ';', "pipe": '|'}
)

// wordFor is the word a table has for a value.
func wordFor[T comparable](words map[string]T, v T) (string, bool) {
	for w, x := range words {
		if x == v {
			return w, true
		}
	}
	return "", false
}

// Import is an import as the window knows one, to be written as the flags of
// the command that does the same.
type Import struct {
	Connection string   // a saved connection's name
	Into       []string // the table, as the path of its name
	From       string   // the file
	Read       transfer.Options
	Pairs      []transfer.Pair
	Columns    []string // the file's columns' names, by their place
	Load       transfer.LoadOptions
}

// Flags is the import as `ikigai import` is told it. Everything about how the
// file is read and where its columns go is said, and nothing is left to be
// found out again: a job does what was seen, not what would be detected on a
// file that has changed since.
func (i Import) Flags() (map[string]any, error) {
	format, ok := wordFor(ReadFormats, i.Read.Format)
	if !ok {
		return nil, fmt.Errorf("a job cannot say the format %v", i.Read.Format)
	}
	for _, part := range i.Into {
		if part == "" || strings.Contains(part, ".") {
			return nil, fmt.Errorf("a job names a table by its dotted name, and %q cannot be part of one", part)
		}
	}
	flags := map[string]any{
		"connection": i.Connection,
		"into":       strings.Join(i.Into, "."),
		"from":       i.From,
		"format":     format,
	}
	switch i.Read.Format {
	case transfer.CSV, transfer.TSV:
		flags["header"] = i.Read.Header
		if i.Read.Comma != 0 {
			flags["delimiter"] = delimiterWord(i.Read.Comma)
		}
	case transfer.XLSX:
		flags["header"] = i.Read.Header
		if i.Read.Sheet != "" {
			flags["sheet"] = i.Read.Sheet
		}
	}
	if i.Read.Format != transfer.XLSX && i.Read.Format != transfer.Parquet {
		enc, ok := wordFor(Encodings, i.Read.Encoding)
		if !ok {
			return nil, fmt.Errorf("a job cannot say the encoding %v", i.Read.Encoding)
		}
		flags["encoding"] = enc
	}
	pairs := make([]string, 0, len(i.Pairs))
	for _, p := range i.Pairs {
		if p.From < 0 || p.From >= len(i.Columns) {
			return nil, fmt.Errorf("the file has no column %d to fill %s", p.From+1, p.To)
		}
		if strings.Contains(p.To, "=") {
			return nil, fmt.Errorf("a job pairs columns as \"file=table\", and the table's column %q cannot be said that way", p.To)
		}
		pairs = append(pairs, i.Columns[p.From]+"="+p.To)
	}
	flags["map"] = pairs
	if i.Load.Replace {
		flags["replace"] = true
	}
	if len(i.Load.Keys) > 0 {
		for _, k := range i.Load.Keys {
			if strings.ContainsAny(k, ",.") {
				return nil, fmt.Errorf("a job names key columns in a list, and %q cannot be in one", k)
			}
		}
		flags["keys"] = strings.Join(i.Load.Keys, ",")
	}
	if i.Load.Batch > 0 {
		flags["batch"] = i.Load.Batch
	}
	if i.Load.OnError != "" {
		flags["on-error"] = i.Load.OnError
		if i.Load.OnError == "collect" {
			flags["max-errors"] = i.Load.MaxErrors
		}
	}
	return flags, nil
}

// delimiterWord is a delimiter as a flag says it: by its name where it has
// one, and as itself otherwise.
func delimiterWord(r rune) string {
	if w, ok := wordFor(Delimiters, r); ok {
		return w
	}
	return string(r)
}
