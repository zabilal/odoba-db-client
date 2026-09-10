package theme

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"fyne.io/fyne/v2"
)

// System font resolution.
//
// UX principle 11 asks for native feel, and on macOS that means SF Pro. There
// is a real constraint in the way:
//
// Modern macOS ships SF as a small number of VARIABLE fonts —
// /System/Library/Fonts/SFNS.ttf carries every weight on a `wght` axis rather
// than as separate files. Fyne's text stack renders a variable font's default
// instance, which is Regular, so loading SFNS.ttf would give us regular
// glyphs for bold text. Half-applied typography looks worse than a consistent
// fallback, so resolution here is all-or-nothing: unless distinct regular and
// bold faces are found, we return nil and Fyne's bundled font is used
// throughout.
//
// In practice this succeeds for anyone who has installed SF Pro as static
// weights from Apple's developer downloads (they land in ~/Library/Fonts),
// and degrades cleanly for everyone else. It is deliberately not a hard
// requirement: a missing font must never be a startup failure.

type fontSet struct {
	regular    fyne.Resource
	bold       fyne.Resource
	italic     fyne.Resource
	boldItalic fyne.Resource
	mono       fyne.Resource
	monoBold   fyne.Resource
}

var (
	fontOnce sync.Once
	fonts    *fontSet // nil when the system font is unusable
)

// systemFont returns the platform system font for a style, or nil to fall back.
func systemFont(style fyne.TextStyle) fyne.Resource {
	fontOnce.Do(func() { fonts = resolveFontSet() })
	if fonts == nil {
		return nil
	}

	if style.Monospace {
		if style.Bold && fonts.monoBold != nil {
			return fonts.monoBold
		}
		return fonts.mono
	}
	switch {
	case style.Bold && style.Italic:
		return fonts.boldItalic
	case style.Bold:
		return fonts.bold
	case style.Italic:
		return fonts.italic
	default:
		return fonts.regular
	}
}

// candidate names for each face, tried in order within each search directory.
var faceCandidates = map[string][]string{
	"regular":    {"SF-Pro-Text-Regular.otf", "SF-Pro-Display-Regular.otf", "SFProText-Regular.otf"},
	"bold":       {"SF-Pro-Text-Bold.otf", "SF-Pro-Display-Bold.otf", "SFProText-Bold.otf"},
	"italic":     {"SF-Pro-Text-RegularItalic.otf", "SF-Pro-Text-Italic.otf", "SFProText-Italic.otf"},
	"boldItalic": {"SF-Pro-Text-BoldItalic.otf", "SFProText-BoldItalic.otf"},
	"mono":       {"SF-Mono-Regular.otf", "SFMono-Regular.otf"},
	"monoBold":   {"SF-Mono-Bold.otf", "SFMono-Bold.otf"},
}

func searchDirs() []string {
	if runtime.GOOS != "darwin" {
		return nil
	}
	dirs := []string{
		"/System/Library/Fonts",
		"/System/Library/Fonts/Supplemental",
		"/Library/Fonts",
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append([]string{filepath.Join(home, "Library", "Fonts")}, dirs...)
	}
	return dirs
}

// resolveFontSet locates the system font faces, or returns nil.
func resolveFontSet() *fontSet {
	dirs := searchDirs()
	if len(dirs) == 0 {
		return nil
	}

	find := func(face string) fyne.Resource {
		for _, dir := range dirs {
			for _, name := range faceCandidates[face] {
				path := filepath.Join(dir, name)
				data, err := os.ReadFile(path)
				if err != nil || len(data) == 0 {
					continue
				}
				return fyne.NewStaticResource(name, data)
			}
		}
		return nil
	}

	regular, bold := find("regular"), find("bold")
	if regular == nil || bold == nil {
		// All-or-nothing: see the note at the top of this file.
		return nil
	}

	set := &fontSet{regular: regular, bold: bold}

	// Italics are optional; Fyne synthesises a slant when absent, which is
	// acceptable because italic is rare in this UI. Weight is not
	// synthesisable, which is why bold above is mandatory.
	if set.italic = find("italic"); set.italic == nil {
		set.italic = regular
	}
	if set.boldItalic = find("boldItalic"); set.boldItalic == nil {
		set.boldItalic = bold
	}

	// Monospace matters more than usual here: it is the SQL editor and the
	// grid's numeric columns. Falling back to Fyne's mono is fine.
	set.mono = find("mono")
	set.monoBold = find("monoBold")

	return set
}
