package schemafile

import (
	"fmt"
	"strings"
	"unicode"
)

// Turning an object's name into a file name.
//
// The name a file is called is a label, not the name: the object's real name
// is inside the file, so reading a model never decodes a path. That is what
// lets this be readable rather than reversible — a table called "order
// totals" is order-totals.json, and the file says what it is really called.
//
// What it does have to be is safe and stable. Safe on every filesystem this
// runs on, including a Windows one where NUL and COM1 are devices and a name
// cannot end in a dot; and stable, so that saving the same schema twice
// produces the same tree and version control sees no change.

// reserved are the names Windows will not give a file, whatever the
// extension. A table called "con" is not unusual.
var reserved = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// fileName is what to call the file holding an object.
//
// Anything that is not a letter, a digit, a dot, a dash or an underscore
// becomes a dash, and runs of dashes collapse — a path separator, a colon, a
// quote and a space all go the same way. What is left is trimmed of the dots
// and spaces Windows will not end a name with.
func fileName(name string) string {
	var b strings.Builder
	dashed := false
	for _, r := range name {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '-' || r == '_':
			b.WriteRune(r)
			dashed = false
		case !dashed:
			b.WriteByte('-')
			dashed = true
		}
	}
	out := strings.Trim(b.String(), "-. ")
	if out == "" || reserved[strings.ToLower(out)] {
		// A name that came to nothing, or one a filesystem keeps for itself.
		// The object's real name is in the file either way.
		out = "_" + out
	}
	return out
}

// checkNames reports two objects that would be written to the same file.
//
// It compares without case, because a name safe on Linux collides on macOS
// and Windows, and a model saved on one and read on the other would quietly
// be missing an object. Disambiguating instead was the alternative and it is
// worse: a suffix that depends on what else is in the schema changes when
// something unrelated is dropped, and every file after it moves.
func checkNames(kind string, names []string) error {
	seen := map[string]string{}
	for _, n := range names {
		f := strings.ToLower(fileName(n))
		if first, clash := seen[f]; clash {
			return fmt.Errorf("schemafile: %s %q and %q would both be written to %s.json; "+
				"rename one, or save them to different directories", kind, first, n, f)
		}
		seen[f] = n
	}
	return nil
}
