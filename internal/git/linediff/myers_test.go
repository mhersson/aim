package linediff

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
)

func TestDiff(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a, b []string
		want []Op
	}{
		{"both empty", nil, nil, nil},
		{"all added", nil, []string{"a", "b"}, []Op{{'+', "a"}, {'+', "b"}}},
		{"all removed", []string{"a", "b"}, nil, []Op{{'-', "a"}, {'-', "b"}}},
		{
			"one line changed",
			[]string{"a", "b", "c"}, []string{"a", "x", "c"},
			[]Op{{' ', "a"}, {'-', "b"}, {'+', "x"}, {' ', "c"}},
		},
		{
			"a line inserted",
			[]string{"a", "c"}, []string{"a", "b", "c"},
			[]Op{{' ', "a"}, {'+', "b"}, {' ', "c"}},
		},
		{
			"nothing in common",
			[]string{"a", "b"}, []string{"c", "d"},
			[]Op{{'-', "a"}, {'-', "b"}, {'+', "c"}, {'+', "d"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := Diff(tt.a, tt.b); !slices.Equal(got, tt.want) {
				t.Errorf("Diff(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// lcsLen is the length of a longest common subsequence of a and b.
func lcsLen(a, b []string) int {
	row := make([]int, len(b)+1)

	for i := range a {
		diag := 0

		for j := range b {
			up := row[j+1]
			if a[i] == b[j] {
				row[j+1] = diag + 1
			} else {
				row[j+1] = max(row[j+1], row[j])
			}

			diag = up
		}
	}

	return row[len(b)]
}

// checkScript fails t unless ops turns a into b, keeps n lines and puts the
// removed lines of each run of changes before its added ones.
func checkScript(t *testing.T, a, b []string, ops []Op, n int) {
	t.Helper()

	var gotA, gotB []string

	kept, added := 0, false

	for _, op := range ops {
		switch op.Op {
		case ' ':
			gotA, gotB = append(gotA, op.Line), append(gotB, op.Line)
			kept, added = kept+1, false
		case '-':
			gotA = append(gotA, op.Line)

			if added {
				t.Fatalf("a removed line follows an added one in a run: %v", ops)
			}
		case '+':
			gotB = append(gotB, op.Line)
			added = true
		default:
			t.Fatalf("op %q", op.Op)
		}
	}

	if !slices.Equal(gotA, a) || !slices.Equal(gotB, b) {
		t.Fatalf("%v turns %q into %q, want %q into %q", ops, gotA, gotB, a, b)
	}

	if kept != n {
		t.Fatalf("%v keeps %d lines, want %d", ops, kept, n)
	}
}

func TestDiffIsAShortestScript(t *testing.T) {
	t.Parallel()

	r := rand.New(rand.NewPCG(5, 6))
	lines := func() []string {
		out := make([]string, r.IntN(40))
		for i := range out {
			out[i] = string(rune('a' + r.IntN(4)))
		}

		return out
	}

	for range 300 {
		a, b := lines(), lines()
		checkScript(t, a, b, Diff(a, b), lcsLen(a, b))
	}
}

func TestDiffOfABigFileChangedAtBothEnds(t *testing.T) {
	t.Parallel()

	a := make([]string, 3000)
	for i := range a {
		a[i] = fmt.Sprintf("line %d", i+1)
	}

	b := slices.Clone(a)
	b[1], b[2998] = "two edited", "end edited"
	b = slices.Insert(slices.Delete(b, 2989, 2990), 10, "inserted")

	ops := Diff(a, b)
	checkScript(t, a, b, ops, 2997)
}

func TestDiffPastTheCap(t *testing.T) {
	t.Parallel()

	// The lines between "s" and "e" are 6 and 4 of them change: 24 units of work.
	a := []string{"s", "a1", "m", "a2", "e"}
	b := []string{"s", "b1", "m", "b2", "e"}

	shortest := []Op{{' ', "s"}, {'-', "a1"}, {'+', "b1"}, {' ', "m"}, {'-', "a2"}, {'+', "b2"}, {' ', "e"}}
	replaced := []Op{{' ', "s"}, {'-', "a1"}, {'-', "m"}, {'-', "a2"}, {'+', "b1"}, {'+', "m"}, {'+', "b2"}, {' ', "e"}}

	if got := diff(a, b, 24); !slices.Equal(got, shortest) {
		t.Errorf("diff at the cap = %v, want %v", got, shortest)
	}

	if got := diff(a, b, 23); !slices.Equal(got, replaced) {
		t.Errorf("diff past the cap = %v, want %v", got, replaced)
	}
}

// Not parallel: AllocsPerRun counts the process's allocations.
func TestDiffPastTheCapAllocatesOnlyTheScript(t *testing.T) {
	// The middles differ by 999 lines, so the edit distance is at least 999 and
	// the cap is passed before any search.
	a := []string{"s", "x", "e"}

	b := []string{"s"}
	for i := range 1000 {
		b = append(b, fmt.Sprint(i))
	}

	b = append(b, "e")

	if n := testing.AllocsPerRun(10, func() { diff(a, b, 1000) }); n > 1 {
		t.Errorf("diff past the cap made %v allocations, want only the script's", n)
	}
}
