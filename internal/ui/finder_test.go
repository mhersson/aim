package ui

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/mhersson/aim/internal/md"
)

// rowRels lists the rels of rows, each with a leading kind mark so a test reads
// the order and the kinds at once: ^ up, ~ jump, / folder, - file.
func rowRels(rows []finderRow) []string {
	out := make([]string, len(rows))

	for i, r := range rows {
		switch {
		case r.kind == finderUp:
			out[i] = "^" + r.rel
		case r.kind == finderJump:
			out[i] = "~" + r.rel
		case r.dir:
			out[i] = "/" + r.rel
		default:
			out[i] = "-" + r.rel
		}
	}

	return out
}

func TestFinderRows(t *testing.T) {
	t.Parallel()

	listing := []md.FolderEntry{
		{Name: ".config", Dir: true},
		{Name: "docs", Dir: true},
		{Name: "notes", Dir: true},
		{Name: ".hidden.md"},
		{Name: "README.md"},
		{Name: "todo.md"},
	}
	walked := []md.WalkEntry{
		{Rel: "docs", Dir: true},
		{Rel: "notes", Dir: true},
		{Rel: "README.md"},
		{Rel: "docs/guide.md"},
		{Rel: "notes/ideas.md"},
		{Rel: ".config", Dir: true},
		{Rel: ".config/aim.md"},
	}

	tests := []struct {
		name  string
		in    finderInput
		want  []string
		exact bool // want is the whole list; else every wanted row leads the list in order
	}{
		{
			name: "nothing typed: up, folders, files; hidden left out",
			in:   finderInput{parent: true, listing: listing},
			want: []string{"^..", "/docs", "/notes", "-README.md", "-todo.md"}, exact: true,
		},
		{
			name: "nothing typed at the top has no up row",
			in:   finderInput{listing: listing},
			want: []string{"/docs", "/notes", "-README.md", "-todo.md"}, exact: true,
		},
		{
			name: "a dot lists this folder's hidden entries, no up row",
			in:   finderInput{parent: true, listing: listing, query: "."},
			want: []string{"/.config", "-.hidden.md"}, exact: true,
		},
		{
			name: "a tilde is one jump row",
			in:   finderInput{parent: true, listing: listing, query: "~"},
			want: []string{"~~/"}, exact: true,
		},
		{
			name: "two dots are one up row",
			in:   finderInput{parent: true, listing: listing, query: ".."},
			want: []string{"^.."}, exact: true,
		},
		{
			name: "two dots at the top are nothing",
			in:   finderInput{listing: listing, query: ".."},
			want: []string{}, exact: true,
		},
		{
			name: "a query searches the walk, folders included, no up row",
			in:   finderInput{parent: true, listing: listing, query: "notes", entries: walked},
			want: []string{"/notes", "-notes/ideas.md"},
		},
		{
			name: "a dotted query reaches hidden entries of the walk",
			in:   finderInput{parent: true, listing: listing, query: ".co", entries: walked},
			want: []string{"/.config", "-.config/aim.md"},
		},
		{
			name: "no walk yet, no rows",
			in:   finderInput{parent: true, listing: listing, query: "doc"},
			want: []string{}, exact: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := rowRels(finderRows(tt.in))
			if !tt.exact && len(got) > len(tt.want) {
				got = got[:len(tt.want)]
			}

			if !slices.Equal(got, tt.want) {
				t.Errorf("rows = %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("a search leaves out what does not match and keeps walk order on a tie", func(t *testing.T) {
		t.Parallel()

		in := finderInput{query: "x", entries: []md.WalkEntry{{Rel: "a/x.md"}, {Rel: "b.md"}, {Rel: "c/x.md"}}}

		if got, want := rowRels(finderRows(in)), []string{"-a/x.md", "-c/x.md"}; !slices.Equal(got, want) {
			t.Errorf("rows = %q, want %q", got, want)
		}
	})

	t.Run("the matched bytes of the path are the hits", func(t *testing.T) {
		t.Parallel()

		rows := finderRows(finderInput{query: "do", entries: []md.WalkEntry{{Rel: "docs", Dir: true}}})
		if len(rows) != 1 || !slices.Equal(rows[0].hits, []int{0, 1}) {
			t.Errorf("rows = %+v, want one row with hits 0 and 1", rows)
		}
	})
}

func TestFinderQuery(t *testing.T) {
	t.Parallel()

	tests := []struct{ raw, want string }{
		{"", ""},
		{"a b", "ab"},
		{" . ", "."},
		{"\t~", "~"},
	}

	for _, tt := range tests {
		if got := finderQuery(tt.raw); got != tt.want {
			t.Errorf("finderQuery(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestFinderWalkNeed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		query  string
		search bool
		hidden bool
	}{
		{"", false, false},
		{".", false, false},
		{"~", false, false},
		{"..", false, false},
		{"a", true, false},
		{".c", true, true},
		{"...", true, true},
	}

	for _, tt := range tests {
		if got := finderSearches(tt.query); got != tt.search {
			t.Errorf("finderSearches(%q) = %v, want %v", tt.query, got, tt.search)
		}

		if tt.search && finderHidden(tt.query) != tt.hidden {
			t.Errorf("finderHidden(%q) = %v, want %v", tt.query, !tt.hidden, tt.hidden)
		}
	}
}

func TestFinderCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		sel, n int
		walk   *finderWalk
		want   string
	}{
		{name: "no walk", sel: 1, n: 10, want: "2 of 10"},
		{name: "nothing listed", sel: 0, n: 0, want: "0 of 0"},
		{name: "a walk that finished", sel: 2, n: 37, walk: &finderWalk{}, want: "3 of 37"},
		{name: "a walk running", sel: 2, n: 37, walk: &finderWalk{running: true}, want: "3 of 37 · " + finderSearching},
		{name: "the file limit", sel: 2, n: 37, walk: &finderWalk{stop: md.WalkFileLimit}, want: "3 of 37 · " + fmt.Sprintf(finderStoppedFiles, thousands(md.WalkMaxFiles))},
		{name: "the time limit", sel: 2, n: 37, walk: &finderWalk{stop: md.WalkTimeLimit}, want: "3 of 37 · " + fmt.Sprintf(finderStoppedTime, int(md.WalkMaxTime/time.Second))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := finderCountText(tt.sel, tt.n, tt.walk.tail()); got != tt.want {
				t.Errorf("count = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFinderHints(t *testing.T) {
	t.Parallel()

	keys := func(pairs [][2]string) []string {
		var out []string
		for _, p := range pairs {
			out = append(out, p[0]+" "+p[1])
		}

		return out
	}

	tests := []struct {
		name  string
		enter string
		empty bool
		width int
		want  []string
	}{
		{
			name: "an empty prompt offers backspace", enter: "go in", empty: true, width: 200,
			want: []string{"up/down move", "enter go in", "backspace up", "ctrl+d/u preview", "esc close"},
		},
		{
			name: "a typed prompt does not", enter: "open", width: 200,
			want: []string{"up/down move", "enter open", "ctrl+d/u preview", "esc close"},
		},
		{
			name: "backspace is dropped first when the row is narrow", enter: "go up", empty: true, width: 60,
			want: []string{"up/down move", "enter go up", "ctrl+d/u preview", "esc close"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := keys(finderHints(tt.enter, tt.empty, tt.width)); !slices.Equal(got, tt.want) {
				t.Errorf("hints = %q, want %q", got, tt.want)
			}
		})
	}
}
