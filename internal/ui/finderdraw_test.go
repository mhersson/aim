package ui

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/md"
)

// finderGoldenTree is the folder the finder scenes browse: ~/aim.
var finderGoldenTree = map[string]string{
	"aim/AGENTS.md":                        "# Agents\n",
	"aim/CHANGELOG.md":                     "# Changes\n",
	"aim/README.md":                        "# AIM\n",
	"aim/main.go":                          "package main\n",
	"aim/.github/PULL_REQUEST_TEMPLATE.md": "# PR\n",
	"aim/docs/architecture.md":             "# Architecture\n",
	"aim/docs/gotchas.md":                  "# Gotchas\n",
	"aim/docs/guide/reading.md":            "# Reading\n",
	"aim/internal/ui/README.md":            "# UI\n",
	"aim/notes/architecture-sketch.md":     "# Sketch\n",
	"aim/emptydir/x.txt":                   "text\n",
}

type finderScene struct {
	name  string
	w, h  int
	start string
	setup func(t *testing.T, r *finderRig)
}

// walking types a letter and delivers the walk's first batch but not its end.
func walking(t *testing.T, r *finderRig) {
	t.Helper()

	held := r.flow(r.step(keyRune('a')), holdWalk)
	if len(held) != 1 {
		t.Fatalf("typing held %d walk messages, want 1", len(held))
	}

	r.flow(r.step(held[0]), holdWalk)
}

// previewing puts the cursor on rel and delivers the preview it asks for.
func previewing(rel string) func(t *testing.T, r *finderRig) {
	return func(t *testing.T, r *finderRig) {
		t.Helper()

		r.finderSelect(t, rel)
		r.sync()
	}
}

func finderScenes() []finderScene {
	return []finderScene{
		{name: "browse", w: 120, h: 30, start: "aim"},
		{name: "narrow", w: 90, h: 24, start: "aim"},
		{name: "search", w: 120, h: 30, start: "aim", setup: func(_ *testing.T, r *finderRig) { r.typeKeys("arch") }},
		{name: "searching", w: 120, h: 30, start: "aim", setup: func(t *testing.T, r *finderRig) {
			t.Helper()
			r.flow(r.step(keyRune('a')), holdWalk)
		}},
		{name: "walking", w: 120, h: 30, start: "aim", setup: walking},
		{name: "limit", w: 120, h: 30, start: "aim", setup: func(t *testing.T, r *finderRig) {
			t.Helper()
			walking(t, r)
			r.Update(finderWalkMsg{view: r.finder, seq: r.finder.walk.seq, done: true, stop: md.WalkFileLimit})
		}},
		{name: "no-matches", w: 120, h: 30, start: "aim", setup: func(_ *testing.T, r *finderRig) { r.typeKeys("zzzz") }},
		{name: "hidden", w: 120, h: 30, start: "aim", setup: func(_ *testing.T, r *finderRig) { r.typeKeys(".") }},
		{name: "empty-folder", w: 120, h: 30, start: "aim/emptydir"},
		{name: "unreadable-folder", w: 120, h: 30, start: "aim/gone"},
		{name: "preview-file", w: 120, h: 30, start: "aim/docs", setup: previewing("architecture.md")},
		{name: "preview-readme", w: 120, h: 30, start: "aim/internal"},
		{name: "preview-contents", w: 120, h: 30, start: "aim"},
		{name: "preview-empty", w: 120, h: 30, start: "aim", setup: previewing("emptydir")},
		{name: "preview-error", w: 120, h: 30, start: "aim", setup: func(t *testing.T, r *finderRig) {
			t.Helper()

			if err := os.Remove(r.at("aim/CHANGELOG.md")); err != nil {
				t.Fatal(err)
			}

			previewing("CHANGELOG.md")(t, r)
		}},
	}
}

func finderSceneApp(t *testing.T, icons bool, sc finderScene) *App {
	t.Helper()

	r := newFinderRig(t, sc.start, finderGoldenTree)
	r.icons = icons

	sized(t, r.App, sc.w, sc.h)
	r.mdOpen()
	t.Cleanup(r.closeFinder)

	if sc.setup != nil {
		sc.setup(t, r)
	}

	return r.App
}

// TestFinderGolden draws three scenes in colour; it compares highlighted
// colours, so it runs serially.
func TestFinderGolden(t *testing.T) {
	for _, sc := range finderScenes() {
		if !slices.Contains([]string{"browse", "search", "unreadable-folder", "preview-file"}, sc.name) {
			continue
		}

		t.Run("catppuccin-mocha/icons=true/"+sc.name, func(t *testing.T) {
			golden.RequireEqual(t, []byte(finderSceneApp(t, true, sc).View().Content))
		})
	}
}

// TestFinderText is every scene without styling, so the layout reads.
func TestFinderText(t *testing.T) {
	t.Parallel()

	for _, icons := range []bool{true, false} {
		for _, sc := range finderScenes() {
			t.Run(fmt.Sprintf("icons=%v/%s", icons, sc.name), func(t *testing.T) {
				t.Parallel()
				golden.RequireEqual(t, []byte(ansi.Strip(finderSceneApp(t, icons, sc).View().Content)))
			})
		}
	}
}

func TestFinderMessage(t *testing.T) {
	t.Parallel()

	denied := &fs.PathError{Op: "open", Path: "/x", Err: fs.ErrPermission}
	up := []finderRow{{kind: finderUp, rel: ".."}}
	file := []finderRow{{rel: "a.md"}}

	tests := []struct {
		name    string
		f       finderView
		want    string
		wantErr bool
	}{
		{"rows to show", finderView{loaded: true, rows: append(up, file...)}, "", false},
		{"only .. in an empty folder", finderView{loaded: true, rows: up}, finderEmpty, false},
		{"nothing at the top of an empty folder", finderView{loaded: true}, finderEmpty, false},
		{"not read yet", finderView{rows: up}, "", false},
		{"unreadable", finderView{loaded: true, listErr: denied, rows: up}, finderUnreadable + fs.ErrPermission.Error(), true},
		{"no hidden entries", finderView{loaded: true, query: ".", rows: nil}, finderNoMatches, false},
		{"a search with nothing found", finderView{loaded: true, query: "x", walk: &finderWalk{}}, finderNoMatches, false},
		{"a search still walking", finderView{loaded: true, query: "x", walk: &finderWalk{running: true}}, finderSearching, false},
		{"a search with rows", finderView{loaded: true, query: "x", walk: &finderWalk{running: true}, rows: file}, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, isErr := finderMessage(&tt.f)
			if got != tt.want || isErr != tt.wantErr {
				t.Errorf("message = %q (error %v), want %q (error %v)", got, isErr, tt.want, tt.wantErr)
			}
		})
	}

	t.Run("the reason is the path error's, cleaned", func(t *testing.T) {
		t.Parallel()

		if got := pathErrReason(fmt.Errorf("wrapped: %w", &fs.PathError{Err: errors.New("bad\x1b[31m reason")})); strings.Contains(got, "\x1b") {
			t.Errorf("reason = %q, want no control characters", got)
		}
	})
}
