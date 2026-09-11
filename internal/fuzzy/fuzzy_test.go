package fuzzy

import "testing"

func TestMatchPrefersWordStarts(t *testing.T) {
	// Greedy matching would take the first n and c, mid-word in "Connection".
	score, pos, ok := Match("nc", "Connection: New Connection")
	if !ok {
		t.Fatal("no match")
	}
	want := []int{12, 16} // New, Connection
	if pos[0] != want[0] || pos[1] != want[1] {
		t.Errorf("positions %v, want %v (word starts)", pos, want)
	}
	if other, _, _ := Match("nc", "Syncing"); other >= score {
		t.Errorf("a mid-word match (%d) scored at least the word-start one (%d)", other, score)
	}
}

func TestMatchBasics(t *testing.T) {
	if _, _, ok := Match("xyz", "New Connection"); ok {
		t.Error("non-subsequence matched")
	}
	if _, _, ok := Match("NEWCONN", "new connection"); !ok {
		t.Error("matching must be case-insensitive")
	}
	if _, _, ok := Match("", "anything"); !ok {
		t.Error("empty query should match")
	}
	if _, _, ok := Match("toolong", "short"); ok {
		t.Error("longer query than target matched")
	}
	// Consecutive runs beat scattered letters.
	tight, _, _ := Match("run", "Query: Run")
	loose, _, _ := Match("run", "Query: Refresh Unused Nodes")
	if tight <= loose {
		t.Errorf("consecutive %d should beat scattered %d", tight, loose)
	}
	// Unicode survives.
	if _, pos, ok := Match("ü", "Grüße"); !ok || pos[0] != 2 {
		t.Errorf("unicode: %v %v", pos, ok)
	}
}
