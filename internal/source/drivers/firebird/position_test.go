package firebird

import "testing"

// Where an error was (FR-5.10). Firebird says it in prose, so this is the one
// place in the driver reading a number out of a sentence, and the failure is
// chosen to match: anything that does not parse points nowhere rather than
// somewhere wrong.

func TestWhereTheServerSaysTheErrorWas(t *testing.T) {
	const stmt = `SELECT * FROM NOPE`
	// Column 15 of that statement is the N of NOPE, which is the token the
	// server is complaining about.
	msg := "Dynamic SQL Error\nSQL error code = -204\nTable unknown\nNOPE\nAt line 1, column 15\n"
	if got := positionIn(msg, stmt); got != 15 {
		t.Errorf("it points at %d, want 15", got)
	}
	if stmt[14] != 'N' {
		t.Fatalf("this test is about the wrong character: %q", stmt[14])
	}
}

func TestAPositionOnALaterLine(t *testing.T) {
	stmt := "SELECT *\nFROM NOPE"
	// Line 2, column 6 is the N: 9 characters of the first line including its
	// newline, then five more.
	if got := positionIn("At line 2, column 6", stmt); got != 15 {
		t.Errorf("it points at %d, want 15", got)
	}
}

func TestAPositionCountsCharactersRatherThanBytes(t *testing.T) {
	// The comment holds a character that is two bytes, so a count in bytes
	// would point one past where an editor puts the caret.
	stmt := "-- café\nSELECT X"
	got := positionIn("At line 2, column 8", stmt)
	if got != 16 {
		t.Errorf("it points at %d, want 16", got)
	}
}

func TestAMessagePointingNowhereIsNoPosition(t *testing.T) {
	const stmt = "SELECT 1\nFROM RDB$DATABASE"
	for name, msg := range map[string]string{
		"nothing about a place":        "violation of PRIMARY or UNIQUE KEY constraint",
		"a line and no column":         "At line 4 of the procedure",
		"a line the statement has not": "At line 9, column 1",
		"a column the line has not":    "At line 1, column 400",
		"a line before the first":      "At line 0, column 1",
		"a column before the first":    "At line 1, column 0",
		// And on a later line, whose first character is not the statement's:
		// a column of zero there would point at that character rather than
		// nowhere.
		"a column before a later line's first": "At line 2, column 0",
		// A number with prose after it, which is how a message names a place
		// inside something larger.
		"prose after the column":    "At line 1, column 400 of the statement",
		"words where a number goes": "At line one, column two",
		"nothing at all":            "",
	} {
		t.Run(name, func(t *testing.T) {
			if got := positionIn(msg, stmt); got != 0 {
				t.Errorf("it points at %d, and should point nowhere", got)
			}
		})
	}
}

// One past the last character is a place: an error at the end of a statement
// is reported at the column after it, and that is where a caret goes.
func TestOnePastTheEndIsAPlace(t *testing.T) {
	const stmt = "SELECT"
	if got := positionIn("At line 1, column 7", stmt); got != 7 {
		t.Errorf("it points at %d, want 7", got)
	}
	if got := positionIn("At line 1, column 8", stmt); got != 0 {
		t.Errorf("two past the end points at %d, and should point nowhere", got)
	}
}

// A failure inside a procedure body names the outer statement first and the
// place in the body after it. The nearer of the two is the second.
func TestTheLastPlaceNamedWins(t *testing.T) {
	stmt := "EXECUTE\nPROCEDURE P"
	// Line 2 begins at character 9, so its eleventh character is the 19th —
	// the P of the procedure's name, which is what went wrong.
	msg := "At line 1, column 1\nAt line 2, column 11"
	if got := positionIn(msg, stmt); got != 19 {
		t.Errorf("it points at %d, want 19", got)
	}
	if []rune(stmt)[18] != 'P' {
		t.Fatalf("this test is about the wrong character: %q", []rune(stmt)[18])
	}
}

// And a second place that does not parse leaves the first standing, rather
// than throwing away a position that was read successfully.
func TestAnUnreadableSecondPlaceLeavesTheFirst(t *testing.T) {
	if got := positionIn("At line 1, column 3\nAt line 2, column", "SELECT"); got != 3 {
		t.Errorf("it points at %d, want 3", got)
	}
}
