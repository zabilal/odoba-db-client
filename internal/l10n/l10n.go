// Package l10n is the words the application says, in the language it says
// them in (FR-15.11). It draws nothing and imports nothing of this project's,
// so that every layer that says something to a person can say it through
// here (ARCH-1).
//
// English is the first language and the key. T("Save as Job…") is the
// translation of that English where the language in use has one, and the
// English where it has not — so a catalogue that is half finished is a window
// half translated, never a window with holes in it, and nothing has to be
// given a name before it can be said.
//
// A catalogue is a JSON object from English to translation, one file a
// language, compiled in. A translation that asks for different values than
// its English does — a %d where the English has a %s, or one more — is not
// used, and is reported when the catalogue is read: a translator's slip
// should cost a sentence its translation and not print "%!d(string=…)" in
// somebody's window.
package l10n

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"sync/atomic"
)

//go:embed locales
var compiled embed.FS

// Pseudo is a language nobody speaks, made from English as it is asked for:
// every letter given an accent and the whole put between brackets. Text that
// is still plain under it never went through T, and a label that is cut off
// under it has no room for German. It is how a window is checked for being
// translatable without waiting for a translation.
const Pseudo = "en-XA"

// catalogue is one language's translations, by their English.
type catalogue struct {
	tag    string
	words  map[string]string
	pseudo bool
}

// english is the catalogue of no translations: everything is said as written.
var english = &catalogue{tag: "en"}

var current atomic.Pointer[catalogue]

// T is s in the language in use, or s.
func T(s string) string {
	c := current.Load()
	switch {
	case c == nil, s == "":
		return s
	case c.pseudo:
		return pseudo(s)
	}
	if t, ok := c.words[s]; ok {
		return t
	}
	return s
}

// N marks s as something said later, somewhere it is not yet known in which
// language: in a table made when the package is, before any language is set.
// It is s. Whoever shows it says T of it then; marking it here is what puts
// it on the list a translator works from.
func N(s string) string { return s }

// F is a sentence with values in it: the format in the language in use,
// filled as fmt.Sprintf fills one. The whole sentence is one thing to
// translate, since where a value goes in it is the language's to say.
func F(format string, a ...any) string { return fmt.Sprintf(T(format), a...) }

// Language is the tag of the language in use: "en" where nothing else is.
func Language() string {
	if c := current.Load(); c != nil {
		return c.tag
	}
	return english.tag
}

// Use sets the language by its tag, as a person or their system gives it:
// "de", "de-DE", "de_DE.UTF-8". The nearest catalogue there is, is used — the
// tag itself, then its language alone — and English where there is none,
// which is not an error: most of the world's systems are in a language this
// does not have yet. What a catalogue had that could not be used is returned
// for whoever keeps a log.
func Use(tag string) []error { return use(compiled, "locales", tag) }

func use(files fs.FS, dir, tag string) []error {
	tag = normal(tag)
	if tag == strings.ToLower(Pseudo) {
		current.Store(&catalogue{tag: Pseudo, pseudo: true})
		return nil
	}
	for _, try := range []string{tag, strings.SplitN(tag, "-", 2)[0]} {
		if try == "" {
			break
		}
		raw, err := fs.ReadFile(files, path.Join(dir, try+".json"))
		if err != nil {
			continue
		}
		c, problems := read(try, raw)
		current.Store(c)
		return problems
	}
	current.Store(english)
	return nil
}

// normal is a tag as catalogues are named: lower case, hyphens, and without
// what a system adds after it ("de_DE.UTF-8@euro" is "de-de").
func normal(tag string) string {
	tag = strings.TrimSpace(tag)
	if i := strings.IndexAny(tag, ".@"); i >= 0 {
		tag = tag[:i]
	}
	return strings.ToLower(strings.ReplaceAll(tag, "_", "-"))
}

// read is a catalogue from its file, without the translations that cannot be
// used, each of which is reported.
func read(tag string, raw []byte) (*catalogue, []error) {
	c := &catalogue{tag: tag, words: map[string]string{}}
	var words map[string]string
	if err := json.Unmarshal(raw, &words); err != nil {
		return c, []error{fmt.Errorf("l10n: the %s catalogue is not a JSON object of text: %w", tag, err)}
	}
	var problems []error
	for en, t := range words {
		switch {
		case t == "":
			continue // not translated yet: said in English
		case !sameValues(en, t):
			problems = append(problems, fmt.Errorf("l10n: %s: the translation of %q asks for different values (%q) and is not used", tag, en, t))
			continue
		}
		c.words[en] = t
	}
	sort.Slice(problems, func(i, j int) bool { return problems[i].Error() < problems[j].Error() })
	return c, problems
}

// sameValues reports whether two formats ask for the same values as the same
// kinds of thing. A translation may put them in another order, by saying
// which it means — "%[2]s of %[1]d" — since where a value goes in a sentence
// is the language's to say; it may not ask for a number where the English
// gives text, nor for a value the English does not give.
func sameValues(a, b string) bool {
	va, vb := verbs(a), verbs(b)
	if len(va) != len(vb) {
		return false
	}
	for i := range va {
		if va[i] != vb[i] {
			return false
		}
	}
	return true
}

// verbs are the values a format asks for, each by its letter, in the order
// they are given to it and not the order it uses them: the second value's
// letter is second, wherever "%[2]s" stands. A value used twice is one value,
// one never used is a space, and a doubled percent sign asks for nothing.
func verbs(format string) []byte {
	var out []byte
	next := 0
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		i++
		for i < len(format) && strings.IndexByte("+-# 0123456789.*", format[i]) >= 0 {
			i++
		}
		if i < len(format) && format[i] == '[' {
			end := strings.IndexByte(format[i:], ']')
			if end < 0 {
				return append(out, '!') // not a format anybody can fill
			}
			n := 0
			for _, d := range format[i+1 : i+end] {
				if d < '0' || d > '9' {
					return append(out, '!')
				}
				n = n*10 + int(d-'0')
			}
			if n < 1 || n > 64 {
				return append(out, '!')
			}
			next, i = n-1, i+end+1
		}
		if i >= len(format) || format[i] == '%' {
			continue
		}
		for len(out) <= next {
			out = append(out, ' ')
		}
		out[next] = format[i]
		next++
	}
	return out
}

// accents are the letters of the pseudo-language.
var accents = strings.NewReplacer(
	"a", "á", "e", "é", "i", "í", "o", "ó", "u", "ú", "y", "ý", "c", "ç", "n", "ñ",
	"A", "Á", "E", "É", "I", "Í", "O", "Ó", "U", "Ú", "Y", "Ý", "C", "Ç", "N", "Ñ",
)

// pseudo is s in the pseudo-language. What a format asks for is left as it
// is, so that a sentence with values in it is still filled.
func pseudo(s string) string {
	var b strings.Builder
	b.WriteString("[")
	for i := 0; i < len(s); {
		if s[i] != '%' {
			j := i
			for j < len(s) && s[j] != '%' {
				j++
			}
			b.WriteString(accents.Replace(s[i:j]))
			i = j
			continue
		}
		j := i + 1
		for j < len(s) && strings.IndexByte("+-# 0123456789.[]*", s[j]) >= 0 {
			j++
		}
		if j < len(s) {
			j++
		}
		b.WriteString(s[i:j])
		i = j
	}
	b.WriteString("]")
	return b.String()
}

// Languages are the languages there are catalogues for, by their tags, with
// English first: what a person can be offered.
func Languages() []string {
	out := []string{english.tag}
	entries, _ := fs.ReadDir(compiled, "locales")
	for _, e := range entries {
		if name := e.Name(); strings.HasSuffix(name, ".json") {
			out = append(out, strings.TrimSuffix(name, ".json"))
		}
	}
	return out
}

// System is the language a person's system says it is in, from the
// environment, or "" where it says nothing: LC_ALL, then LC_MESSAGES, then
// LANG, as a C library reads them. "C" and "POSIX" are no language.
func System() string {
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := normal(os.Getenv(name)); v != "" {
			if v == "c" || v == "posix" {
				return ""
			}
			return v
		}
	}
	return ""
}
