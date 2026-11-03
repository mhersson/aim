package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

const renderedPicDoc = "# Readme\n\nhello there\n\n![logo](docs/logo.png)\n\nafter one\n\nafter two\n\nafter three\n"

// renderedPicture is a drawer showing a README that adds one picture, rendered.
type renderedPicture struct {
	*pageRig
	jobs *recJobs
}

func tinyPNG(t *testing.T) string {
	t.Helper()

	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}

	return b.String()
}

func newRenderedPicture(t *testing.T, w, h int, readme string) *renderedPicture {
	t.Helper()

	m := newPageRig(t, map[string]string{"README.md": readme, "docs/logo.png": tinyPNG(t)})
	jobs := picturesOn(t, m.App)

	sized(t, m.App, w, h)

	for i, row := range m.v.rows {
		if row.kind == rowFile && m.v.res.Files[row.file].Path == "README.md" {
			m.v.cursor = i
		}
	}

	m.press(keyRune('m'))

	if m.v.mdFiles["README.md"] == nil {
		t.Fatal("README.md did not render")
	}

	return &renderedPicture{pageRig: m, jobs: jobs}
}

// image is the README's first picture as the shown doc lays it out.
func (m *renderedPicture) image(t *testing.T) (id uint32, rows int) {
	t.Helper()

	f := m.v.mdFiles["README.md"]
	if f == nil {
		t.Fatal("README.md is not rendered")
	}

	im := f.doc.layout(max(m.v.mdW, 1), m.v.mdIcons).Images[0]

	return im.ID, im.Rows
}

// show answers the picture's info as w x h pixels and its render, and returns its id.
func (m *renderedPicture) show(t *testing.T, w, h int) uint32 {
	t.Helper()

	if n := answerInfo(m.App, m.jobs, w, h, nil); n != 1 {
		t.Fatalf("info jobs = %d, want 1", n)
	}

	m.View()

	id, _ := m.image(t)

	if got := answerRender(t, m.App, m.jobs); !slices.Equal(got, []string{"SEQ"}) {
		t.Fatalf("render answers = %q, want the transmission", got)
	}

	return id
}

// textX is the column the README's text starts at in the frame.
func textX(t *testing.T, content, text string) int {
	t.Helper()

	for l := range strings.SplitSeq(ansi.Strip(content), "\n") {
		if before, _, ok := strings.Cut(l, text); ok {
			return ansi.StringWidth(before)
		}
	}

	t.Fatalf("no line holds %q", text)

	return 0
}

// frameRow is the frame row that holds text.
func frameRow(t *testing.T, content, text string) int {
	t.Helper()

	for y, l := range strings.Split(ansi.Strip(content), "\n") {
		if strings.Contains(l, text) {
			return y
		}
	}

	t.Fatalf("no line holds %q", text)

	return 0
}

func TestRenderedPicture(t *testing.T) {
	t.Parallel()

	m := newRenderedPicture(t, 200, 50, renderedPicDoc)

	if got := ansi.Strip(m.View().Content); !strings.Contains(got, "loading…") {
		t.Fatalf("before the answers the file shows:\n%s", got)
	}

	id := m.show(t, 5, 3)

	content := m.View().Content
	rows := placeholders(content, id)

	if len(rows) != 3 {
		t.Fatalf("placeholder rows = %+v, want 3", rows)
	}

	x := textX(t, content, "hello there")

	buf := uv.NewScreenBuffer(200, 50)
	uv.NewStyledString(content).Draw(buf, buf.Bounds())

	success := string(m.current().theme.UI.Success)

	for k, r := range rows {
		if r.x != x || r.n != 5 || r.row != k {
			t.Errorf("row %d = %+v, want x %d, 5 cells, row %d", k, r, x, k)
		}

		c := buf.CellAt(x-2, r.y)
		if c == nil || c.Content != "▎" || c.Style.Fg == nil || hexOf(c.Style.Fg) != success {
			t.Errorf("row %d cell 0 = %+v, want the added mark in %s", k, c, success)
		}
	}
}

func TestRenderedPictureKeepsRow(t *testing.T) {
	t.Parallel()

	var b strings.Builder

	b.WriteString("intro\n\n![brand](docs/logo.png)\n\n")

	for i := range 40 {
		fmt.Fprintf(&b, "para %d\n\n", i)
	}

	m := newRenderedPicture(t, 100, 20, b.String())
	v := m.v

	at := rowWith(t, v, "para 1")
	v.top, v.cursor = rowWith(t, v, "brand")-1, at

	before := frameRow(t, m.View().Content, "para 1")

	// Sized 5 x 8 cells: the picture grows by seven rows.
	if n := answerInfo(m.App, m.jobs, 5, 8, nil); n != 1 {
		t.Fatalf("info jobs = %d, want 1", n)
	}

	if got := v.rows[v.cursor].text; got != "para 1" {
		t.Errorf("cursor on %q, want para 1", got)
	}

	if got := frameRow(t, m.View().Content, "para 1"); got != before {
		t.Errorf("cursor text at frame row %d, want %d", got, before)
	}
}

func TestRenderedPictureAboveCursorFile(t *testing.T) {
	t.Parallel()

	m := newRenderedPicture(t, 100, 20, renderedPicDoc)
	v := m.v

	at := rowWith(t, v, "edited 30")
	if at < rowWith(t, v, "after three") {
		t.Fatal("the cursor's file is not below the rendered one")
	}

	v.top, v.cursor = at-5, at

	before := frameRow(t, m.View().Content, "edited 30")

	// Sized 5 x 3 cells: the picture above the cursor grows by two rows.
	if n := answerInfo(m.App, m.jobs, 5, 3, nil); n != 1 {
		t.Fatalf("info jobs = %d, want 1", n)
	}

	if v.cursor != at+2 {
		t.Errorf("cursor at row %d, want %d", v.cursor, at+2)
	}

	if got := frameRow(t, m.View().Content, "edited 30"); got != before {
		t.Errorf("cursor text at frame row %d, want %d", got, before)
	}
}

func TestRenderedPictureCovered(t *testing.T) {
	t.Parallel()

	// 100x12 leaves the drawer no hint row, so its last body row is the toast's top row.
	tests := []struct {
		name string
		hide func(m *renderedPicture)
		back func(m *renderedPicture)
	}{
		{"a notice", func(m *renderedPicture) { m.notice = strings.Repeat("x", 200) }, func(m *renderedPicture) { m.press(keyRune('j')) }},
		{"the worktree list", func(m *renderedPicture) { m.press(keyRune('w')) }, func(m *renderedPicture) { m.press(keyEsc) }},
		{"the link chooser", func(m *renderedPicture) {
			m.drawer.links = &linkChooser{targets: []mdTarget{{Text: "one", URL: "https://example.com/1"}, {Text: "two", URL: "https://example.com/2"}}, root: m.root}
			m.drawer.linksOf = linksOwner{tab: m.tabID(), pane: "p1", view: m.v}
		}, func(m *renderedPicture) { m.press(keyEsc) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newRenderedPicture(t, 100, 12, "![logo](docs/logo.png)\n\nafter one\n")
			id := m.show(t, 5, 20)

			if shown := placeholders(m.View().Content, id); len(shown) < 5 {
				t.Fatalf("placeholder rows before = %+v", shown)
			}

			tt.hide(m)

			if got := placeholders(m.View().Content, id); len(got) != 0 {
				t.Fatalf("placeholders under %s: %+v", tt.name, got)
			}

			tt.back(m)

			if got := placeholders(m.View().Content, id); len(got) == 0 {
				t.Fatalf("placeholders did not come back (notice %q, links %v)", m.notice, m.drawer.links != nil)
			}
		})
	}
}

func TestRenderedPictureSlide(t *testing.T) {
	t.Parallel()

	m := newRenderedPicture(t, 200, 50, renderedPicDoc)
	m.show(t, 5, 3)

	st := m.store.State()
	r, _ := m.drawerRect(st)

	_, pics, _ := m.drawerDrawn(st, r)
	if len(pics) != 3 {
		t.Fatalf("pics = %+v, want 3 rows", pics)
	}

	m.drawer.slideFrom = m.clk.Now()

	if _, pics, _ = m.drawerDrawn(st, r); len(pics) != 0 {
		t.Errorf("pics while sliding = %+v, want none", pics)
	}

	m.drawer.slideFrom = time.Time{}

	if _, pics, _ = m.drawerDrawn(st, r); len(pics) != 3 {
		t.Errorf("pics after the slide = %+v, want 3 rows", pics)
	}
}

func TestRenderedPictureReload(t *testing.T) {
	t.Parallel()

	m := newRenderedPicture(t, 200, 50, renderedPicDoc)
	id := m.show(t, 5, 3)

	if got := placeholders(m.View().Content, id); len(got) != 3 {
		t.Fatalf("placeholder rows = %+v, want 3", got)
	}

	write(t, filepath.Join(m.dir, "README.md"), renderedPicDoc+"\nafter four\n")
	m.press(keyRune('r'))

	if !strings.Contains(ansi.Strip(m.View().Content), "after four") {
		t.Fatal("the reload did not show the new text")
	}

	// r reloads every picture, so the source is asked for again.
	m.View()

	if jobs := m.jobs.take(); len(jobs) != 1 || jobs[0].render {
		t.Errorf("jobs after the reload = %+v, want one info job", jobs)
	}
}

func TestRenderedPictureRetry(t *testing.T) {
	t.Parallel()

	m := newRenderedPicture(t, 200, 50, renderedPicDoc)

	if n := answerInfo(m.App, m.jobs, 0, 0, fmt.Errorf("boom")); n != 1 {
		t.Fatalf("first info jobs = %d, want 1", n)
	}

	if got := ansi.Strip(m.View().Content); strings.Contains(got, "loading…") {
		t.Fatalf("after the failure the file shows:\n%s", got)
	}

	m.press(keyRune('r'))

	if got := ansi.Strip(m.View().Content); !strings.Contains(got, "loading…") {
		t.Fatalf("after r the file shows:\n%s", got)
	}

	if n := answerInfo(m.App, m.jobs, 5, 3, nil); n != 1 {
		t.Fatalf("info jobs after r = %d, want 1", n)
	}
}
