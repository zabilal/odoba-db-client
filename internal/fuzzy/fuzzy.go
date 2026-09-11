// Package fuzzy scores how well a short query matches a longer text, the
// way a person skims: its letters in order, whatever the case, preferring
// the starts of words and unbroken runs. The command palette ranks commands
// with it, and the explorer's filter ranks paths (FR-2.3, ADR-0018).
package fuzzy

import (
	"strings"
	"unicode"
)

const (
	scoreMatch     = 10
	bonusWordStart = 12
	bonusFirst     = 6
	bonusConsec    = 8
	penaltyGap     = 1
)

const impossible = -1 << 30

// Match reports whether query's runes appear in order in target,
// case-insensitively, and scores the best such placement.
//
// The best placement is found by dynamic programming, not taken greedily. A
// greedy match of "nc" in "Connection: New Connection" takes the first n and
// the next c, both mid-word, and scores badly. The placement on the word
// starts, New Connection, is the one a person means. A running maximum over
// earlier positions keeps it O(len(query) × len(target)).
func Match(query, target string) (int, []int, bool) {
	qs := []rune(strings.ToLower(query))
	ts := []rune(target)
	lt := make([]rune, len(ts))
	for i, r := range ts {
		lt[i] = unicode.ToLower(r)
	}
	nq, nt := len(qs), len(ts)
	if nq == 0 {
		return 0, nil, true
	}
	if nq > nt {
		return 0, nil, false
	}

	start := make([]int, nt)
	for j := range ts {
		start[j] = scoreMatch
		if isWordStart(ts, j) {
			start[j] += bonusWordStart
		}
		if j == 0 {
			start[j] += bonusFirst
		}
	}

	best := make([][]int, nq)
	back := make([][]int, nq)
	for i := range best {
		best[i] = make([]int, nt)
		back[i] = make([]int, nt)
		for j := range best[i] {
			best[i][j] = impossible
			back[i][j] = -1
		}
	}
	for j := 0; j < nt; j++ {
		if lt[j] == qs[0] {
			best[0][j] = start[j] - j/4 // a gentle preference for early matches
		}
	}
	for i := 1; i < nq; i++ {
		runMax, runArg := impossible, -1 // max over k <= j-2 of best[i-1][k] + k
		for j := i; j < nt; j++ {
			if k := j - 2; k >= 0 && best[i-1][k] != impossible && best[i-1][k]+k > runMax {
				runMax, runArg = best[i-1][k]+k, k
			}
			if lt[j] != qs[i] {
				continue
			}
			cand, arg := impossible, -1
			if p := best[i-1][j-1]; p != impossible {
				cand, arg = p+bonusConsec, j-1
			}
			if runMax != impossible {
				if gap := runMax + 1 - j*penaltyGap; gap > cand {
					cand, arg = gap, runArg
				}
			}
			if cand != impossible {
				best[i][j], back[i][j] = cand+start[j], arg
			}
		}
	}

	endScore, end := impossible, -1
	for j := 0; j < nt; j++ {
		if best[nq-1][j] > endScore {
			endScore, end = best[nq-1][j], j
		}
	}
	if end < 0 {
		return 0, nil, false
	}
	pos := make([]int, nq)
	for i, j := nq-1, end; i >= 0; i-- {
		pos[i] = j
		j = back[i][j]
	}
	return endScore - nt/8, pos, true // on a tie, the shorter label wins
}

func isWordStart(ts []rune, j int) bool {
	if j == 0 {
		return true
	}
	prev, cur := ts[j-1], ts[j]
	if unicode.IsSpace(prev) || strings.ContainsRune(":_-./()…", prev) {
		return true
	}
	return unicode.IsLower(prev) && unicode.IsUpper(cur) // camelCase
}
