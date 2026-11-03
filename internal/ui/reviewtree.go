package ui

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/git"
)

const (
	treeMinWidth = 20
	treeMaxShare = 35 // percent of the inner width
)

// treeItem is one row of the file tree. A folder's name is its whole joined
// path without the trailing slash; names are raw, so drawing cleans them.
type treeItem struct {
	dir   bool
	name  string
	depth int
	file  int // index into the result's files; -1 for a folder
}

type treeNode struct {
	name  string
	dirs  map[string]*treeNode
	files map[string]int
}

// treeItems lists the files as a tree: folders before files, both by name,
// with single-folder chains joined into one item.
func treeItems(files []git.FileDiff) []treeItem {
	root := &treeNode{dirs: map[string]*treeNode{}, files: map[string]int{}}

	for i, f := range files {
		parts := slices.DeleteFunc(strings.Split(f.Path, "/"), func(p string) bool { return p == "" })
		if len(parts) == 0 {
			continue
		}

		n := root

		for _, p := range parts[:len(parts)-1] {
			c := n.dirs[p]
			if c == nil {
				c = &treeNode{name: p, dirs: map[string]*treeNode{}, files: map[string]int{}}
				n.dirs[p] = c
			}

			n = c
		}

		n.files[parts[len(parts)-1]] = i
	}

	return appendTree(nil, root, 0)
}

// inTreeOrder returns a new slice of the files in the order the tree lists
// them, so the diff and the tree cannot drift apart. A file the tree cannot
// place keeps its relative place after the rest.
func inTreeOrder(files []git.FileDiff) []git.FileDiff {
	out := make([]git.FileDiff, 0, len(files))
	placed := make([]bool, len(files))

	for _, it := range treeItems(files) {
		if !it.dir {
			out = append(out, files[it.file])
			placed[it.file] = true
		}
	}

	for i, f := range files {
		if !placed[i] {
			out = append(out, f)
		}
	}

	return out
}

func appendTree(out []treeItem, n *treeNode, depth int) []treeItem {
	for _, name := range slices.Sorted(maps.Keys(n.dirs)) {
		c := n.dirs[name]

		var joined strings.Builder
		joined.WriteString(c.name)

		for len(c.files) == 0 && len(c.dirs) == 1 {
			for _, only := range c.dirs {
				c = only
			}

			joined.WriteString("/" + c.name)
		}

		out = append(out, treeItem{dir: true, name: joined.String(), depth: depth, file: -1})
		out = appendTree(out, c, depth+1)
	}

	for _, name := range slices.Sorted(maps.Keys(n.files)) {
		out = append(out, treeItem{name: name, depth: depth, file: n.files[name]})
	}

	return out
}

// treePad is the blank cells a tree row keeps at each end: the left one holds
// the selection mark, the right one parts the counts from the rule.
const treePad = 2

// treeWidth is the width of the widest row, at least 20 columns and, when the
// inner width allows, at most 35 % of it. A row is a margin, the indent, the
// status and a space, the name (and a slash for a folder), two spaces, the
// hidden flag's two cells and the counts, then a margin. hidden says per file
// whether it has the flag; the caller caches it per result.
func treeWidth(items []treeItem, files []git.FileDiff, hidden []bool, inner int) int {
	widest := 0

	for _, it := range items {
		name, _ := cleanText(it.name)
		w := treePad + it.depth*2 + 2 + ansi.StringWidth(name)

		if it.dir {
			w++
		} else {
			f := files[it.file]
			w += 2 + len(fileCounts(f))

			if it.file < len(hidden) && hidden[it.file] {
				w += 2
			}
		}

		widest = max(widest, w)
	}

	return max(treeMinWidth, min(widest, inner*treeMaxShare/100))
}

// fileCounts is "+A -R", or "bin" for a binary file.
func fileCounts(f git.FileDiff) string {
	if f.Binary {
		return "bin"
	}

	return "+" + strconv.Itoa(f.Added) + " -" + strconv.Itoa(f.Removed)
}

// fileHidden reports whether the file's paths or diff text hold a character
// cleanText tags.
func fileHidden(f git.FileDiff) bool {
	for _, s := range []string{f.Path, f.OldPath} {
		if _, h := cleanText(s); h {
			return true
		}
	}

	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if _, hid := cleanLine(l.Text); hid {
				return true
			}
		}
	}

	return false
}
