package l10n

import (
	"strings"
	"testing"
	"testing/fstest"
)

// catalogues are languages made for these tests: none is compiled in.
var catalogues = fstest.MapFS{
	"cat/de.json": {Data: []byte(`{
		"Save": "Speichern",
		"%d rows into %s": "%d Zeilen in %s",
		"Not yet": "",
		"%d of %s": "%s von %d",
		"%d from %s": "%[2]s, davon %[1]d",
		"%s again": "noch einmal",
		"100%% of %d": "%d, also 100%%",
		"one %s": "ein %s und %s"
	}`)},
	"cat/pt-br.json": {Data: []byte(`{"Save": "Salvar"}`)},
	"cat/xx.json":    {Data: []byte(`["not", "an", "object"]`)},
	"cat/zh.json":    {Data: []byte(`{"Save": "保存"}`)},
	"cat/zh-tw.json": {Data: []byte(`{"Save": "儲存"}`)},
}

func using(t *testing.T, tag string) []error {
	t.Helper()
	t.Cleanup(func() { current.Store(nil) })
	return use(catalogues, "cat", tag)
}

func TestEnglishIsTheKeyAndTheFallback(t *testing.T) {
	if T("Save") != "Save" || F("%d rows into %s", 3, "items") != "3 rows into items" || Language() != "en" {
		t.Errorf("with no language set, what is written is what is said: %q, %q, %s", T("Save"), F("%d rows into %s", 3, "items"), Language())
	}
	problems := using(t, "de")
	if T("Save") != "Speichern" || F("%d rows into %s", 3, "items") != "3 Zeilen in items" || Language() != "de" {
		t.Errorf("German: %q, %q, %s", T("Save"), F("%d rows into %s", 3, "items"), Language())
	}
	// Not in the catalogue, and in it with nothing against it: English.
	if T("Cancel") != "Cancel" || T("Not yet") != "Not yet" || T("") != "" {
		t.Errorf("what has no translation is said in English: %q, %q, %q", T("Cancel"), T("Not yet"), T(""))
	}
	// A translation that asks for other values than its English is not used,
	// and each is reported: values in another order, fewer, and more.
	if F("%d of %s", 2, "five") != "2 of five" || F("%s again", "that") != "that again" || F("one %s", "x") != "one x" {
		t.Errorf("a translation that would print wrongly was used: %q, %q, %q", F("%d of %s", 2, "five"), F("%s again", "that"), F("one %s", "x"))
	}
	if len(problems) != 3 {
		t.Fatalf("three translations cannot be used, and %d were reported: %v", len(problems), problems)
	}
	for i, want := range []string{`"%d of %s"`, `"%s again"`, `"one %s"`} {
		if msg := problems[i].Error(); !strings.Contains(msg, want) || !strings.Contains(msg, "de: ") || !strings.Contains(msg, "is not used") {
			t.Errorf("problem %d does not say which sentence, in which language: %s", i, msg)
		}
	}
	// Said in another order, by saying which value is meant, it is used.
	if got := F("%d from %s", 2, "five"); got != "five, davon 2" {
		t.Errorf("a sentence whose values change places: %q", got)
	}
	// A percent sign is not a value.
	if F("100%% of %d", 7) != "7, also 100%" {
		t.Errorf("%q", F("100%% of %d", 7))
	}
}

func TestTheNearestCatalogueIsUsed(t *testing.T) {
	for tag, want := range map[string]string{
		"de": "de", "DE": "de", "de-AT": "de", "de_DE.UTF-8": "de", "de_DE@euro": "de", " de ": "de",
		"pt-BR": "pt-br", "pt_BR.UTF-8": "pt-br", "de@euro": "de",
		// Its own before its language's, where there are both.
		"zh-TW": "zh-tw", "zh_TW.UTF-8": "zh-tw", "zh-CN": "zh", "zh": "zh",
		// A language with no catalogue, and its speakers' own dialect of one
		// that has only another's: English, and not somebody else's Portuguese.
		"fr": "en", "pt": "en", "pt-PT": "en", "": "en", "en": "en", "en-GB": "en", "C": "en", "-": "en",
	} {
		if using(t, tag); Language() != want {
			t.Errorf("%q is spoken as %s, not %s", tag, want, Language())
		}
	}
	// English after another language is English again.
	using(t, "de")
	using(t, "fr")
	if T("Save") != "Save" {
		t.Errorf("after German, an unknown language still says %q", T("Save"))
	}
	// A catalogue that is not one is said, and nothing of it is used.
	problems := using(t, "xx")
	if len(problems) != 1 || !strings.Contains(problems[0].Error(), "the xx catalogue is not a JSON object") || T("Save") != "Save" {
		t.Errorf("%v; %q", problems, T("Save"))
	}
	// What is compiled in has no catalogue yet, and says so by offering
	// English alone.
	if got := Languages(); len(got) != 1 || got[0] != "en" {
		t.Errorf("the languages there are: %v", got)
	}
	t.Cleanup(func() { current.Store(nil) })
	if problems := Use("de"); problems != nil || Language() != "en" {
		t.Errorf("German, which is not compiled in: %s, %v", Language(), problems)
	}
}

func TestThePseudoLanguageMarksWhatWentThroughIt(t *testing.T) {
	using(t, "en-xa")
	if Language() != Pseudo {
		t.Fatalf("%s", Language())
	}
	for en, want := range map[string]string{
		"Save as Job…":       "[Sávé ás Jób…]",
		"CANCEL":             "[ÇÁÑÇÉL]",
		"":                   "",
		"%d rows into %s":    "[%d róws íñtó %s]",
		"%5.2e%% of %[1]d a": "[%5.2e%% óf %[1]d á]",
		"%c of %e in %U":     "[%c óf %e íñ %U]",
		"ends in a %":        "[éñds íñ á %]",
	} {
		if got := T(en); got != want {
			t.Errorf("%q is %q, not %q", en, want, got)
		}
	}
	// A sentence with values is still filled, and the values are not accented.
	if got := F("%d rows into %s", 3, "items"); got != "[3 róws íñtó items]" {
		t.Errorf("%q", got)
	}
	using(t, "EN_XA.UTF-8")
	if Language() != Pseudo {
		t.Errorf("however it is written: %s", Language())
	}
}

func TestWhatAFormatAsksFor(t *testing.T) {
	for format, want := range map[string]string{
		"": "", "plain": "", "%d": "d", "%s and %d": "sd", "100%%": "", "%5.2f": "f", "%-10s|%+d": "sd",
		"%[2]d %[1]s": "sd", "%q%v": "qv", "%": "", "%%%s": "s", "% d": "d", "%#x": "x", "%*d": "d",
		"%[1]s %[1]s": "s", "%[3]d": "  d", "%[2]s %d": " sd", "%[x]d": "!", "%[1x]d": "!", "%[0]d": "!", "%[2": "!", "%[99]d": "!", "%d %[1]q": "q",
	} {
		if got := string(verbs(format)); got != want {
			t.Errorf("%q asks for %q, not %q", format, want, got)
		}
	}
	if !sameValues("%d of %s", "%d von %s") || sameValues("%d of %s", "%s von %d") || sameValues("%d", "%d %d") || sameValues("%d %d", "%d") ||
		!sameValues("%d of %s", "%[2]s von %[1]d") || sameValues("%d of %s", "%[2]d von %[1]s") {
		t.Error("the same values as the same kinds of thing, in whatever order they are said")
	}
}

func TestTheSystemsLanguage(t *testing.T) {
	for _, c := range []struct{ all, messages, lang, want string }{
		{"", "", "", ""},
		{"", "", "de_DE.UTF-8", "de-de"},
		{"", "fr_FR", "de_DE.UTF-8", "fr-fr"},
		{"pt_BR", "fr_FR", "de_DE.UTF-8", "pt-br"},
		{"C", "fr_FR", "de_DE", ""},
		{"", "POSIX", "de_DE", ""},
		{"", "", "C.UTF-8", ""},
	} {
		t.Setenv("LC_ALL", c.all)
		t.Setenv("LC_MESSAGES", c.messages)
		t.Setenv("LANG", c.lang)
		if got := System(); got != c.want {
			t.Errorf("LC_ALL=%q LC_MESSAGES=%q LANG=%q is %q, not %q", c.all, c.messages, c.lang, c.want, got)
		}
	}
}
