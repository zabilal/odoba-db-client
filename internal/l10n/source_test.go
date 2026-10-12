package l10n

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// What holds "no hardcoded UI strings" (FR-15.11): the code that draws is
// read, and every piece of text it gives a widget directly has to have come
// through T or F. The same reading lists every sentence there is to
// translate, and that list is kept in a file a translator starts from.
//
// Two flags do the work the test would otherwise only complain of:
//
//	go test ./internal/l10n -run TestSource -l10n.fix      wraps what is bare
//	go test ./internal/l10n -run TestSource -l10n.update   rewrites messages.json
var (
	fix    = flag.Bool("l10n.fix", false, "wrap bare UI text in l10n.T and l10n.F, in place")
	update = flag.Bool("l10n.update", false, "rewrite messages.json from the code")
)

// drawn are the directories whose code says things in a window, from the
// module's root.
var drawn = []string{"internal/ui", "cmd/ikigai"}

// labelled are the functions and methods whose arguments are text a person
// reads, each with which of its arguments are.
var labelled = map[string][]int{
	"NewLabel": {0}, "NewLabelWithStyle": {0}, "NewButton": {0}, "NewButtonWithIcon": {0}, "NewCheck": {0},
	"NewFormItem": {0}, "NewTabItem": {0}, "NewTabItemWithIcon": {0}, "NewMenuItem": {0}, "NewMenu": {0},
	"NewHyperlink": {0}, "NewCard": {0, 1}, "NewAccordionItem": {0},
	"NewConfirm": {0, 1}, "NewInformation": {0, 1}, "NewForm": {0, 1, 2}, "NewCustom": {0, 1},
	"NewCustomConfirm": {0, 1, 2}, "NewCustomWithoutButtons": {0},
	"SetText": {0}, "SetPlaceHolder": {0}, "SetConfirmText": {0}, "SetDismissText": {0}, "SetTitle": {0},
	"formError": {0},
}

// fields are the fields of a struct written out that are text a person
// reads.
var fields = map[string]bool{"Title": true, "Category": true, "Message": true, "Accept": true, "Label": true, "PlaceHolder": true}

// said reports whether a literal is something said to a person, rather than
// a name the program uses: it has a letter in it, and is not one lower-case
// word, which is what an identifier, a host or a format's name is.
func said(s string) bool {
	letter, word := false, true
	for _, r := range s {
		if unicode.IsLetter(r) {
			letter = true
		}
		if !unicode.IsLower(r) && !unicode.IsDigit(r) && r != '-' && r != '_' && r != '.' {
			word = false
		}
	}
	return letter && !word
}

// bare is a piece of text that goes to a widget as it was written.
type bare struct {
	file      string
	line      int
	from, to  int    // the bytes to replace
	with      string // what with
	text      string
	atPackage bool // in a declaration that is made before any language is set
}

// reading is what one file says: what is bare in it, and what it sends
// through T and F already.
type reading struct {
	bare     []bare
	messages []string
	imports  bool // l10n already
}

func read1(t *testing.T, fset *token.FileSet, path string, src []byte) reading {
	t.Helper()
	f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var r reading
	for _, imp := range f.Imports {
		if strings.HasSuffix(strings.Trim(imp.Path.Value, `"`), "/internal/l10n") {
			r.imports = true
		}
	}
	offset := func(p token.Pos) int { return fset.Position(p).Offset }
	literal := func(e ast.Expr) (string, bool) {
		lit, ok := e.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(lit.Value)
		return s, err == nil
	}
	// wrap notes an expression that is text a person reads: a literal, or a
	// sentence made by fmt.Sprintf from one.
	wrap := func(e ast.Expr, atPackage bool) {
		if s, ok := literal(e); ok {
			if said(s) {
				r.bare = append(r.bare, bare{file: path, line: fset.Position(e.Pos()).Line, from: offset(e.Pos()), to: offset(e.End()),
					with: "l10n.T(" + string(src[offset(e.Pos()):offset(e.End())]) + ")", text: s, atPackage: atPackage})
			}
			return
		}
		if sum, ok := e.(*ast.BinaryExpr); ok && sum.Op == token.ADD {
			// Text joined to values: one sentence, with a place for each
			// value, since where they go is the language's to say.
			var parts []ast.Expr
			var flatten func(e ast.Expr)
			flatten = func(e ast.Expr) {
				if b, ok := e.(*ast.BinaryExpr); ok && b.Op == token.ADD {
					flatten(b.X)
					flatten(b.Y)
					return
				}
				parts = append(parts, e)
			}
			flatten(sum)
			format, args, any := "", "", false
			for _, part := range parts {
				if s, ok := literal(part); ok {
					format += strings.ReplaceAll(s, "%", "%%")
					any = any || said(s)
					continue
				}
				format += "%s"
				args += ", " + string(src[offset(part.Pos()):offset(part.End())])
			}
			if any {
				r.bare = append(r.bare, bare{file: path, line: fset.Position(e.Pos()).Line, from: offset(e.Pos()), to: offset(e.End()),
					with: "l10n.F(" + strconv.Quote(format) + args + ")", text: format, atPackage: atPackage})
			}
			return
		}
		call, ok := e.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Sprintf" {
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "fmt" {
				if s, ok := literal(call.Args[0]); ok && said(s) {
					r.bare = append(r.bare, bare{file: path, line: fset.Position(e.Pos()).Line, from: offset(call.Fun.Pos()), to: offset(call.Fun.End()),
						with: "l10n.F", text: s, atPackage: atPackage})
				}
			}
		}
	}
	var walk func(n ast.Node, atPackage bool)
	walk = func(n ast.Node, atPackage bool) {
		ast.Inspect(n, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.FuncLit:
				if atPackage {
					walk(x.Body, false) // run later, when a language may be set
					return false
				}
			case *ast.CallExpr:
				name := ""
				switch fun := x.Fun.(type) {
				case *ast.SelectorExpr:
					name = fun.Sel.Name
					if pkg, ok := fun.X.(*ast.Ident); ok && pkg.Name == "l10n" && (name == "T" || name == "F" || name == "N") && len(x.Args) > 0 {
						if s, ok := literal(x.Args[0]); ok {
							r.messages = append(r.messages, s)
						}
						return true
					}
				case *ast.Ident:
					name = fun.Name
				}
				for _, i := range labelled[name] {
					if i < len(x.Args) {
						wrap(x.Args[i], atPackage)
					}
				}
			case *ast.KeyValueExpr:
				if key, ok := x.Key.(*ast.Ident); ok && fields[key.Name] {
					wrap(x.Value, atPackage)
				}
			}
			return true
		})
	}
	for _, d := range f.Decls {
		_, isFunc := d.(*ast.FuncDecl)
		walk(d, !isFunc)
	}
	return r
}

// root is the module's directory, from this package's.
func root(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		t.Fatalf("the module is not where this test thinks it is: %v", err)
	}
	return dir
}

func sources(t *testing.T) []string {
	t.Helper()
	var files []string
	for _, dir := range drawn {
		err := filepath.WalkDir(filepath.Join(root(t), dir), func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
				files = append(files, path)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(files)
	return files
}

func TestSourceSaysNothingBare(t *testing.T) {
	base := root(t)
	var found []bare
	messages := map[string]bool{}
	for _, path := range sources(t) {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		r := read1(t, token.NewFileSet(), path, src)
		if *fix && len(r.bare) > 0 {
			src = fixed(src, r)
			if err := os.WriteFile(path, src, 0o644); err != nil {
				t.Fatal(err)
			}
			r = read1(t, token.NewFileSet(), path, src)
		}
		found = append(found, r.bare...)
		for _, m := range r.messages {
			messages[m] = true
		}
	}
	for _, b := range found {
		rel, _ := filepath.Rel(base, b.file)
		why := "is given to a widget as it is written; say it through l10n.T, or l10n.F where it has values in it"
		if b.atPackage {
			why = "is said where the package is made, before any language is set; say it in a function, through l10n.T"
		}
		t.Errorf("%s:%d: %q %s", filepath.ToSlash(rel), b.line, b.text, why)
	}
	if len(messages) == 0 {
		t.Fatal("no text was found going through l10n.T at all: this test is reading the wrong thing")
	}

	// The list a translator starts from: every sentence, in the alphabet's
	// order, each with nothing against it.
	list := make(map[string]string, len(messages))
	for m := range messages {
		list[m] = ""
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(list); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, "internal", "l10n", "messages.json")
	if *update {
		if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	kept, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v; run: go test ./internal/l10n -run TestSource -l10n.update", err)
	}
	if !bytes.Equal(bytes.ReplaceAll(kept, []byte("\r\n"), []byte("\n")), b.Bytes()) {
		t.Errorf("messages.json is not what the code says (%d sentences); run: go test ./internal/l10n -run TestSource -l10n.update", len(messages))
	}
}

// fixed is a file with what is bare in it sent through l10n, and l10n
// imported. What is said where the package is made is left: it needs moving,
// which is a person's to do.
func fixed(src []byte, r reading) []byte {
	sort.Slice(r.bare, func(i, j int) bool { return r.bare[i].from > r.bare[j].from })
	changed := false
	for _, b := range r.bare {
		if b.atPackage {
			continue
		}
		src = append(src[:b.from:b.from], append([]byte(b.with), src[b.to:]...)...)
		changed = true
	}
	if !changed || r.imports {
		return src
	}
	const imp = "\t\"github.com/ikigai-db/ikigai-db/internal/l10n\"\n"
	// Among this project's own imports where there are any, which is where
	// gofmt would keep it; else as an import of its own.
	if i := bytes.Index(src, []byte("\t\"github.com/ikigai-db/ikigai-db/")); i >= 0 {
		// before the first of them that sorts after it
		at := i
		for {
			end := bytes.IndexByte(src[at:], '\n')
			line := string(src[at : at+end+1])
			if !strings.HasPrefix(line, "\t\"github.com/ikigai-db/ikigai-db/") || line > imp {
				break
			}
			at += end + 1
		}
		return append(src[:at:at], append([]byte(imp), src[at:]...)...)
	}
	if i := bytes.Index(src, []byte("import (\n")); i >= 0 {
		end := i + bytes.Index(src[i:], []byte("\n)"))
		return append(src[:end+1:end+1], append([]byte("\n"+imp), src[end+1:]...)...)
	}
	i := bytes.Index(src, []byte("\nimport "))
	if i < 0 {
		i = bytes.IndexByte(src, '\n') // after the package clause
		return append(src[:i+1:i+1], append([]byte(fmt.Sprintf("\nimport (\n%s)\n", imp)), src[i+1:]...)...)
	}
	return append(src[:i+1:i+1], append([]byte("import "+strings.TrimSpace(imp)+"\n"), src[i+1:]...)...)
}
