package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/mhersson/aim/internal/git"
)

func paths(ps ...string) []git.FileDiff {
	out := make([]git.FileDiff, len(ps))
	for i, p := range ps {
		out[i] = git.FileDiff{Path: p, Status: git.Modified}
	}

	return out
}

func TestTreeItemsJoinChains(t *testing.T) {
	t.Parallel()

	files := paths(
		"README.md",
		"internal/git/testdata/b.golden",
		"internal/git/testdata/a.golden",
		"internal/git/diff.go",
		"internal/ui/x.go",
		"cmd/main.go",
		"docs/a/b/c/only.md",
		"zz.txt",
	)

	items := treeItems(files)

	type want struct {
		dir   bool
		name  string
		depth int
		file  int
	}

	wants := []want{
		{true, "cmd", 0, -1},
		{false, "main.go", 1, 5},
		{true, "docs/a/b/c", 0, -1},
		{false, "only.md", 1, 6},
		{true, "internal", 0, -1},
		{true, "git", 1, -1},
		{true, "testdata", 2, -1},
		{false, "a.golden", 3, 2},
		{false, "b.golden", 3, 1},
		{false, "diff.go", 2, 3},
		{true, "ui", 1, -1},
		{false, "x.go", 2, 4},
		{false, "README.md", 0, 0},
		{false, "zz.txt", 0, 7},
	}

	if len(items) != len(wants) {
		t.Fatalf("got %d items %+v, want %d", len(items), items, len(wants))
	}

	for i, w := range wants {
		g := items[i]
		if g.dir != w.dir || g.name != w.name || g.depth != w.depth || g.file != w.file {
			t.Errorf("item %d = %+v, want %+v", i, g, w)
		}
	}
}

func TestInTreeOrder(t *testing.T) {
	t.Parallel()

	renamed := paths("z.go", "a.go")
	renamed[0].Status, renamed[0].OldPath = git.Renamed, "dir/old.go"

	tests := []struct {
		name  string
		files []git.FileDiff
		want  []string
	}{
		{
			"folders before files at each level, by name",
			paths("README.md", "b.go", "cmd/main.go", "internal/b.go", "internal/z/x.go"),
			[]string{"cmd/main.go", "internal/z/x.go", "internal/b.go", "README.md", "b.go"},
		},
		{"a folder before a file of its name", paths("a", "a/b"), []string{"a/b", "a"}},
		{"a renamed file under its new path", renamed, []string{"a.go", "z.go"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got []string
			for _, f := range inTreeOrder(tt.files) {
				got = append(got, f.Path)
			}

			if !slices.Equal(got, tt.want) {
				t.Errorf("inTreeOrder = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTreeItemsJoinsOnlyDirChains(t *testing.T) {
	t.Parallel()

	items := treeItems(paths("internal/git/testdata/a.golden", "internal/git/testdata/b.golden"))
	if len(items) != 3 || items[0].name != "internal/git/testdata" || !items[0].dir {
		t.Fatalf("items = %+v, want one joined folder and two files", items)
	}

	if items[1].depth != 1 || items[2].depth != 1 {
		t.Errorf("files sit at depth %d and %d, want 1", items[1].depth, items[2].depth)
	}
}

func TestTreeItemsHostilePath(t *testing.T) {
	t.Parallel()

	files := paths("evil\nname\x1b[31m.go")

	items := treeItems(files)
	if len(items) != 1 || items[0].dir || items[0].file != 0 || items[0].name != files[0].Path {
		t.Errorf("items = %+v, want one file item with the raw name", items)
	}
}

func TestTreeWidthBounds(t *testing.T) {
	t.Parallel()

	short := paths("a.go")
	long := paths("some/dir/" + strings.Repeat("x", 80) + ".go")

	tests := []struct {
		name  string
		files []git.FileDiff
		inner int
		want  int
	}{
		{"short names get the floor", short, 120, 20},
		{"long names stop at 35 percent", long, 100, 35},
		{"the cap follows the width", long, 200, 70},
		{"the floor beats the cap", long, 40, 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := treeItems(tt.files)
			if got := treeWidth(items, tt.files, nil, tt.inner); got != tt.want {
				t.Errorf("treeWidth = %d, want %d", got, tt.want)
			}
		})
	}

	t.Run("a mid-length name fits exactly", func(t *testing.T) {
		files := paths(strings.Repeat("y", 26) + ".go")
		files[0].Added, files[0].Removed = 12, 3
		items := treeItems(files)
		// margins + "M " + name (29) + two spaces + "+12 -3" (6)
		if got, want := treeWidth(items, files, nil, 200), treePad+2+29+2+6; got != want {
			t.Errorf("treeWidth = %d, want %d", got, want)
		}
	})
}

func TestTreeWidthHiddenFlag(t *testing.T) {
	t.Parallel()

	files := paths(strings.Repeat("y", 26) + ".go")
	files[0].Added, files[0].Removed = 12, 3
	items := treeItems(files)

	plain := treeWidth(items, files, []bool{false}, 200)
	if got := treeWidth(items, files, []bool{true}, 200); got != plain+2 {
		t.Errorf("treeWidth with the flag = %d, want %d", got, plain+2)
	}
}
