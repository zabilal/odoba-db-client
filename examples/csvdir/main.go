// Command csvdir is a worked example of an Ikigai DB plugin (FR-16.2): a folder
// of CSV files, browsed as a database.
//
// It is here to be read. Everything a plugin has to do is in it and nothing
// else is: say what it is, open a connection, answer a tree, describe an object,
// and write rows. The whole of the protocol is on the other side of
// github.com/ikigai-db/ikigai-db/plugin, which is the only thing this imports.
//
// Build it and put it where the application looks:
//
//	go build -o ~/.local/share/ikigai-db/plugins/csvdir/plugin ./examples/csvdir
//
// then turn plugins on in the settings. The connection form asks for a folder;
// each .csv file in it is a table, and its first line is the column names.
package main

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/ikigai-db/ikigai-db/plugin"
)

func main() {
	if err := plugin.Serve(&csvdir{open: map[string]string{}}); err != nil {
		fmt.Fprintln(os.Stderr, "csvdir:", err)
		os.Exit(1)
	}
}

// csvdir is the plugin. Its state is the folders it has been opened on, by
// handle: one process serves every connection somebody makes to it.
type csvdir struct {
	mu   sync.Mutex
	n    int
	open map[string]string // handle -> folder
}

func (c *csvdir) Hello() plugin.Hello {
	return plugin.Hello{
		ID: "csvdir", Name: "CSV folder", Version: "1",
		Fields: []plugin.Field{{
			Key: "database", Label: "Folder", Kind: "file", Required: true,
			Help: "A folder of .csv files. Each file is a table.",
		}},
		Capabilities: plugin.Capabilities{
			Objects: []string{"table"},
			// No schemas, one database, and no filtering or sorting of its
			// own: the application does those over what it reads, which is
			// what those switches are for.
		},
	}
}

func (c *csvdir) Open(ctx context.Context, cfg plugin.Config) (string, error) {
	dir := strings.TrimSpace(cfg.Database)
	if dir == "" {
		return "", plugin.Failed(plugin.FailConfig, errors.New("choose a folder of CSV files"))
	}
	fi, err := os.Stat(dir)
	switch {
	case err != nil:
		return "", plugin.Failed(plugin.FailNoDatabase, err)
	case !fi.IsDir():
		return "", plugin.Failed(plugin.FailConfig, errors.New("that is a file, not a folder"))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	handle := strconv.Itoa(c.n)
	c.open[handle] = dir
	return handle, nil
}

func (c *csvdir) Close(handle string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.open, handle)
	return nil
}

func (c *csvdir) dir(handle string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	dir, ok := c.open[handle]
	if !ok {
		return "", errors.New("that connection is closed")
	}
	return dir, nil
}

func (c *csvdir) Ping(ctx context.Context, handle string) error {
	dir, err := c.dir(handle)
	if err != nil {
		return err
	}
	_, err = os.Stat(dir)
	return err
}

func (c *csvdir) Info(ctx context.Context, handle string) (plugin.Info, error) {
	dir, err := c.dir(handle)
	if err != nil {
		return plugin.Info{}, err
	}
	return plugin.Info{Product: "CSV folder", Version: "1", Database: filepath.Base(dir)}, nil
}

// Root is the class folders of the one database this has. A source with one
// database has no node above them.
func (c *csvdir) Root(ctx context.Context, handle string) ([]plugin.Node, error) {
	tables, err := c.tables(handle)
	if err != nil {
		return nil, err
	}
	return []plugin.Node{{
		Ref:         plugin.Ref{Kind: "folder", Path: []string{"csv", "table"}},
		Label:       "Tables",
		Detail:      strconv.Itoa(len(tables)),
		HasChildren: len(tables) > 0,
	}}, nil
}

func (c *csvdir) Children(ctx context.Context, handle string, ref plugin.Ref) ([]plugin.Node, error) {
	if ref.Kind != "folder" {
		return nil, nil
	}
	tables, err := c.tables(handle)
	if err != nil {
		return nil, err
	}
	out := make([]plugin.Node, 0, len(tables))
	for _, t := range tables {
		out = append(out, plugin.Node{
			Ref:       plugin.Ref{Kind: "table", Path: []string{"csv", t}},
			Label:     t,
			Browsable: true,
		})
	}
	return out, nil
}

func (c *csvdir) Describe(ctx context.Context, handle string, ref plugin.Ref) (plugin.Object, error) {
	cols, _, err := c.head(handle, ref)
	if err != nil {
		return plugin.Object{}, err
	}
	return plugin.Object{Ref: ref, Columns: cols, Rows: -1}, nil
}

// Browse writes the file's rows, a row at a time, so that a large file costs no
// more memory than a small one.
func (c *csvdir) Browse(ctx context.Context, handle string, ref plugin.Ref,
	opt plugin.BrowseOptions, w plugin.RowWriter) error {
	cols, path, err := c.head(handle, ref)
	if err != nil {
		return err
	}
	if err := w.Columns(cols); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	if _, err := r.Read(); err != nil { // the names
		return nil
	}
	var at int64
	for {
		if err := ctx.Err(); err != nil {
			return err // given up on: stop reading rather than finish the file
		}
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if at < opt.Offset {
			at++
			continue
		}
		values := make([]any, len(cols))
		for i := range cols {
			if i < len(rec) {
				values[i] = rec[i]
			}
		}
		if err := w.Row(values...); err != nil {
			return err
		}
		at++
		if opt.Limit > 0 && at-opt.Offset >= opt.Limit {
			return nil
		}
	}
}

// tables are the .csv files in the folder, by name without the extension.
func (c *csvdir) tables(handle string) ([]string, error) {
	dir, err := c.dir(handle)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".csv") {
			out = append(out, strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())))
		}
	}
	sort.Strings(out)
	return out, nil
}

// head is a file's columns and its path. Every column is text, because a CSV
// file says nothing about what its values are.
func (c *csvdir) head(handle string, ref plugin.Ref) ([]plugin.Column, string, error) {
	dir, err := c.dir(handle)
	if err != nil {
		return nil, "", err
	}
	if len(ref.Path) == 0 {
		return nil, "", errors.New("that is not a table")
	}
	name := ref.Path[len(ref.Path)-1]
	if strings.ContainsAny(name, `/\`) || name == ".." {
		// A name is a file in this folder and nothing else: a path somebody
		// sent could otherwise read a file the folder does not hold.
		return nil, "", errors.New("that is not a table")
	}
	path := filepath.Join(dir, name+".csv")
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	names, err := r.Read()
	if errors.Is(err, io.EOF) {
		return nil, path, nil // an empty file is a table with no columns
	}
	if err != nil {
		return nil, "", err
	}
	cols := make([]plugin.Column, len(names))
	for i, n := range names {
		cols[i] = plugin.Column{Name: n, Type: "text", Class: "string", Nullable: true}
	}
	return cols, path, nil
}
