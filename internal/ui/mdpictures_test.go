package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	xkitty "github.com/charmbracelet/x/ansi/kitty"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/md"
)

var cell1x1 = uv.CellSizeEvent{Width: 1, Height: 1}

// picturesOn turns pictures on in a test app: a fake job sink, a graphics
// answer, a 1x1 cell and a truecolor profile.
func picturesOn(t *testing.T, a *App) *recJobs {
	t.Helper()

	jobs := &recJobs{}
	a.pictures.jobs = jobs
	a.pictures.start()

	for _, m := range []tea.Msg{truecol, gfxOK, cell1x1, da1} {
		a.Update(m)
	}

	if !a.pictures.on() {
		t.Fatal("pictures are off")
	}

	return jobs
}

// answerInfo answers the queued info jobs with w x h pixels, or with err, and
// returns how many there were; render jobs stay queued.
func answerInfo(a *App, jobs *recJobs, w, h int, err error) int {
	var keep []pictureJob

	n := 0

	for _, j := range jobs.take() {
		if j.render {
			keep = append(keep, j)

			continue
		}

		n++

		a.Update(pictureDoneMsg{job: j, w: w, h: h, err: err})
	}

	jobs.jobs = append(jobs.jobs, keep...)

	return n
}

// answerRender answers the queued render jobs and returns the escapes written.
func answerRender(t *testing.T, a *App, jobs *recJobs) []string {
	t.Helper()

	var out []string

	for _, j := range jobs.take() {
		if !j.render {
			jobs.jobs = append(jobs.jobs, j)

			continue
		}

		out = append(out, rawOf(t, a.pictures.done(pictureDoneMsg{job: j, seq: "SEQ"})))
	}

	return out
}

// phRow is a run of placeholder cells in a frame: column x, row y, n cells,
// showing picture row row.
type phRow struct{ x, y, n, row int }

func placeholders(content string, id uint32) []phRow {
	mark := fmt.Sprintf("38;2;0;0;%d", id)

	var out []phRow

	for y, line := range strings.Split(content, "\n") {
		if !strings.Contains(line, mark) {
			continue
		}

		s := ansi.Strip(line)
		rs := []rune(s)

		at := slices.Index(rs, xkitty.Placeholder)
		if at < 0 {
			continue
		}

		n := 0

		for _, r := range rs {
			if r == xkitty.Placeholder {
				n++
			}
		}

		row := -1

		for k := range 32 {
			if rs[at+1] == xkitty.Diacritic(k) {
				row = k

				break
			}
		}

		out = append(out, phRow{x: ansi.StringWidth(string(rs[:at])), y: y, n: n, row: row})
	}

	return out
}

// picDoc is a doc of src whose images are sized in a registry with 10x20 cells.
func picDoc(t *testing.T, src string) (*mdDoc, *pictures, *recJobs, string) {
	t.Helper()

	p, jobs, _ := onPictures(t)
	root := realTemp(t)
	d := parseMarkdown([]byte(src), root, nil).withPictures(docPictures{reg: p, root: root})

	return d, p, jobs, root
}

// answerDoc answers every queued info job of p with w x h pixels.
func answerDoc(t *testing.T, p *pictures, jobs *recJobs, w, h int) {
	t.Helper()

	for _, j := range jobs.take() {
		if j.render {
			t.Fatalf("unexpected render job %+v", j)
		}

		p.done(pictureDoneMsg{job: j, w: w, h: h})
	}
}

func TestPictureShift(t *testing.T) {
	t.Parallel()

	img := func(line, rows int) md.Image { return md.Image{Line: line, Rows: rows} }
	lay := func(ims ...md.Image) md.Layout { return md.Layout{Images: ims} }

	tests := []struct {
		name     string
		old, cur md.Layout
		i        int
		want     int
	}{
		{"a growing picture above moves i", lay(img(2, 0)), lay(img(2, 8)), 5, 7},
		{"the image line maps to the first picture row", lay(img(2, 0)), lay(img(2, 8)), 2, 0},
		{"a line above is unchanged", lay(img(2, 0)), lay(img(2, 8)), 1, 0},
		{"a shrinking picture maps to its last new row", lay(img(2, 8)), lay(img(2, 3)), 8, -4},
		{"a row inside the old rows keeps its place when it fits", lay(img(2, 8)), lay(img(2, 3)), 3, 0},
		{"two pictures above add up", lay(img(1, 0), img(4, 0)), lay(img(1, 3), img(6, 5)), 7, 6},
		{"the second picture's line", lay(img(1, 0), img(4, 0)), lay(img(1, 3), img(6, 5)), 4, 2},
		{"no images", lay(), lay(), 3, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := pictureShift(tt.old, tt.cur, tt.i); got != tt.want {
				t.Errorf("shift = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestPictureAtAndCol(t *testing.T) {
	t.Parallel()

	lay := md.Layout{
		Lines: make([]md.Line, 6),
		Images: []md.Image{
			{Line: 1, Rows: 0, Src: "line"},
			{Line: 2, Rows: 3, Cols: 5, ID: 7},
		},
	}

	for i, want := range []struct {
		ok  bool
		row int
		id  uint32
	}{{false, 0, 0}, {false, 0, 0}, {true, 0, 7}, {true, 1, 7}, {true, 2, 7}, {false, 0, 0}} {
		im, row, ok := pictureAt(lay, i)
		if ok != want.ok || row != want.row || im.ID != want.id {
			t.Errorf("pictureAt(%d) = %+v, %d, %v, want ok %v row %d id %d", i, im, row, ok, want.ok, want.row, want.id)
		}
	}

	line := md.Line{{Text: "▎ ", Role: md.RoleQuoteBar}, {Text: "   ", Role: md.RolePicture}}
	if got := pictureCol(line); got != 2 {
		t.Errorf("pictureCol = %d, want 2", got)
	}
}

func TestMDViewPictureKeepsRow(t *testing.T) {
	t.Parallel()

	var b strings.Builder

	b.WriteString("intro\n\n![a](a.png)\n\n")

	for i := range 30 {
		fmt.Fprintf(&b, "para %d\n\n", i)
	}

	d, p, jobs, _ := picDoc(t, b.String())
	v := &mdView{doc: d}
	v.size(40, 10, false)

	if len(jobs.jobs) != 1 {
		t.Fatalf("jobs = %+v, want one info job", jobs.jobs)
	}

	at := slices.IndexFunc(d.text(), func(s string) bool { return s == "para 1" })

	v.cursor = at
	v.top = at - 6
	v.clamp()

	if v.cursor-v.top != 6 {
		t.Fatalf("cursor row = %d, want 6", v.cursor-v.top)
	}

	answerDoc(t, p, jobs, 50, 160) // 5 x 8 cells

	got := v.layout()
	if got.Images[0].Rows != 8 {
		t.Fatalf("picture rows = %d, want 8", got.Images[0].Rows)
	}

	if text := d.text()[v.cursor]; text != "para 1" {
		t.Errorf("cursor on %q, want para 1", text)
	}

	if row := v.cursor - v.top; row != 6 {
		t.Errorf("cursor at screen row %d, want 6", row)
	}
}

func TestMDViewPictureRows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		x    int
	}{
		{"plain", "para\n\n![a](a.png)\n\nafter\n", 2},
		{"in a quote", "> ![a](a.png)\n", 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d, p, jobs, _ := picDoc(t, tt.src)
			v := &mdView{doc: d}
			v.size(40, 10, false)
			answerDoc(t, p, jobs, 50, 60) // 5 x 3 cells

			mp := newMDPainter(mochaTheme(t), mochaTheme(t).UI.Bg)
			rows, pics := v.drawPictures(mp, false)

			if len(rows) != 10 {
				t.Fatalf("rows = %d, want 10", len(rows))
			}

			im := v.layout().Images[0]
			if im.Rows != 3 || im.Cols != 5 {
				t.Fatalf("picture = %+v, want 5 x 3", im)
			}

			if len(pics) != 3 {
				t.Fatalf("pics = %+v, want 3 rows", pics)
			}

			for k, r := range pics {
				if r.X != tt.x || r.Y != im.Line+k || r.Row != k || r.Cols != 5 || r.ID != im.ID || r.Ground != mp.ground {
					t.Errorf("row %d = %+v, want X %d Y %d Row %d Cols 5 ID %d", k, r, tt.x, im.Line+k, k, im.ID)
				}
			}

			// Scrolled to the picture's second row.
			v.top = im.Line + 1
			v.cursor = v.top

			_, pics = v.drawPictures(mp, true)
			if len(pics) != 2 || pics[0].Row != 1 || pics[0].Y != 0 {
				t.Errorf("scrolled pics = %+v, want rows 1 and 2 from Y 0", pics)
			}

			// The cursor on a picture row shows the bar beside it.
			v.cursor = im.Line + 1
			rows, _ = v.drawPictures(mp, true)

			if got := ansi.Strip(rows[v.cursor-v.top]); !strings.HasPrefix(got, "▎") {
				t.Errorf("cursor row = %q, want a bar in cell 0", got)
			}

			// draw is drawPictures' rows.
			if got := v.draw(mp, true); !slices.Equal(got, rows) {
				t.Errorf("draw differs from drawPictures' rows")
			}
		})
	}
}

func TestLineTargetsPicture(t *testing.T) {
	t.Parallel()

	d, p, jobs, _ := picDoc(t, "[![alt](a.png)](https://example.com/x)\n")
	v := &mdView{doc: d}
	v.size(40, 10, false)
	answerDoc(t, p, jobs, 50, 60)

	lay := v.layout()
	im := lay.Images[0]

	if im.Rows != 3 {
		t.Fatalf("picture rows = %d, want 3", im.Rows)
	}

	for i := im.Line; i < im.Line+im.Rows; i++ {
		got := lineTargets(lay, i)
		if len(got) != 1 || got[0].URL != "https://example.com/x" || got[0].Text != "alt" {
			t.Errorf("row %d targets = %+v, want the image's link", i, got)
		}
	}

	if got := lineTargets(lay, im.Line+im.Rows); len(got) != 0 {
		t.Errorf("the line after targets %+v", got)
	}
}

// picturePage is a drawer showing docs/p.md, which holds one picture.
type picturePage struct {
	*pageRig
	jobs *recJobs
}

const picturePageDoc = "# P\n\n![a](a.png)\n\ntail\n"

func newPicturePage(t *testing.T, w, h int, doc string) *picturePage {
	t.Helper()

	m := newPageRig(t, map[string]string{"docs/p.md": doc})
	jobs := picturesOn(t, m.App)

	sized(t, m.App, w, h)
	m.openPage(t, "docs/p.md")

	return &picturePage{pageRig: m, jobs: jobs}
}

// show answers the picture's info as w x h pixels and its render, and returns
// its id.
func (m *picturePage) show(t *testing.T, w, h int) uint32 {
	t.Helper()

	if n := answerInfo(m.App, m.jobs, w, h, nil); n != 1 {
		t.Fatalf("info jobs = %d, want 1", n)
	}

	m.View()

	id := m.v.page().view.layout().Images[0].ID
	if id == 0 {
		t.Fatal("the picture has no id")
	}

	if got := answerRender(t, m.App, m.jobs); !slices.Equal(got, []string{"SEQ"}) {
		t.Fatalf("render answers = %q, want the transmission", got)
	}

	return id
}

func (m *picturePage) textX() int {
	r, _ := m.drawerRect(m.store.State())

	return r.X + 1 + 2
}

func TestPagePicture(t *testing.T) {
	t.Parallel()

	m := newPicturePage(t, 200, 50, picturePageDoc)

	if got := ansi.Strip(m.View().Content); !strings.Contains(got, "loading…") {
		t.Fatalf("before the answers the page shows:\n%s", got)
	}

	if n := answerInfo(m.App, m.jobs, 5, 3, nil); n != 1 {
		t.Fatalf("info jobs = %d, want 1", n)
	}

	// Sized, not yet sent: the rows stay blank and the render is queued.
	id := m.v.page().view.layout().Images[0].ID

	if got := placeholders(m.View().Content, id); len(got) != 0 {
		t.Fatalf("placeholders before the transmission: %+v", got)
	}

	renders := m.jobs.take()
	if len(renders) != 1 || !renders[0].render || renders[0].id != id {
		t.Fatalf("jobs after the first frame = %+v, want one render of id %d", renders, id)
	}

	raw := rawOf(t, m.pictures.done(pictureDoneMsg{job: renders[0], seq: "SEQ"}))
	if raw != "SEQ" {
		t.Fatalf("transmission = %q", raw)
	}

	rows := placeholders(m.View().Content, id)
	if len(rows) != 3 {
		t.Fatalf("placeholder rows = %+v, want 3", rows)
	}

	for k, r := range rows {
		if r.x != m.textX() || r.n != 5 || r.row != k {
			t.Errorf("row %d = %+v, want x %d, 5 cells, row %d", k, r, m.textX(), k)
		}
	}

	if m.pictures.fallbacks != 0 {
		t.Errorf("the compositor placed the picture in %d frames, want the splice", m.pictures.fallbacks)
	}
}

func TestPagePictureCovered(t *testing.T) {
	t.Parallel()

	// 100x12 leaves the drawer no hint row, so its last body row is the toast's top row.
	tests := []struct {
		name string
		hide func(m *picturePage)
	}{
		{"a notice", func(m *picturePage) { m.notice = strings.Repeat("x", 200) }},
		{"the palette", func(m *picturePage) { m.leader(':') }},
		{"the link chooser", func(m *picturePage) {
			m.drawer.links = &linkChooser{targets: []mdTarget{{Text: "one", URL: "https://example.com/1"}, {Text: "two", URL: "https://example.com/2"}}, root: m.root}
			m.drawer.linksOf = linksOwner{tab: m.tabID(), pane: "p1", view: m.v}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newPicturePage(t, 100, 12, picturePageDoc)
			id := m.show(t, 5, 20)

			shown := placeholders(m.View().Content, id)
			if len(shown) < 5 {
				t.Fatalf("placeholder rows before = %+v", shown)
			}

			tt.hide(m)

			if got := placeholders(m.View().Content, id); len(got) != 0 {
				t.Fatalf("placeholders under %s: %+v", tt.name, got)
			}

			if tt.name == "a notice" {
				m.press(keyRune('j'))
			} else {
				m.press(keyEsc)
			}

			if got := placeholders(m.View().Content, id); len(got) == 0 {
				t.Fatalf("placeholders did not come back (notice %q, overlay %v, links %v)", m.notice, m.overlay != nil, m.drawer.links != nil)
			}
		})
	}
}

func TestPagePictureSizes(t *testing.T) {
	t.Parallel()

	for _, sz := range [][2]int{{80, 24}, {40, 12}} {
		t.Run(fmt.Sprintf("%dx%d", sz[0], sz[1]), func(t *testing.T) {
			t.Parallel()

			m := newPicturePage(t, sz[0], sz[1], picturePageDoc)
			id := m.show(t, 5, 20)

			first := placeholders(assertScreen(t, m.App, sz[0], sz[1]), id)
			if len(first) == 0 || first[0].row != 0 {
				t.Fatalf("before scrolling: %+v", first)
			}

			m.press(keyRune('G'))

			after := placeholders(assertScreen(t, m.App, sz[0], sz[1]), id)
			if len(after) == 0 || after[0].row == 0 {
				t.Fatalf("scrolled to the end: %+v, want the top rows cropped", after)
			}

			m.press(keyRune('g'))

			if again := placeholders(assertScreen(t, m.App, sz[0], sz[1]), id); len(again) != len(first) {
				t.Errorf("back at the top: %d rows, want %d", len(again), len(first))
			}
		})
	}
}

func TestPagePictureSlide(t *testing.T) {
	t.Parallel()

	m := newPicturePage(t, 200, 50, picturePageDoc)
	m.show(t, 5, 3)

	st := m.store.State()
	r, _ := m.drawerRect(st)

	_, pics, _ := m.drawerDrawn(st, r)
	if len(pics) != 3 || pics[0].X != 3 || pics[0].Y != 4 {
		t.Fatalf("pics = %+v, want 3 rows from X 3, Y 4 in the drawer's cells", pics)
	}

	m.drawer.slideFrom = m.clk.Now()

	if _, pics, _ = m.drawerDrawn(st, r); len(pics) != 0 {
		t.Errorf("pics while sliding = %+v, want none", pics)
	}
}

func TestPagePictureLate(t *testing.T) {
	t.Parallel()

	m := newPicturePage(t, 200, 50, picturePageDoc)

	m.press(keyEsc)

	if m.v.page() != nil {
		t.Fatal("esc did not pop the page")
	}

	answerInfo(m.App, m.jobs, 5, 3, nil)

	content := m.View().Content
	if strings.Contains(content, string(xkitty.Placeholder)) {
		t.Error("a picture was placed after the page was popped")
	}

	if len(m.jobs.jobs) != 0 {
		t.Errorf("jobs after the pop = %+v, want none", m.jobs.jobs)
	}
}

// planPicture opens the plan overlay on a plan file that holds one picture.
func planPicture(t *testing.T, text string) (*planRig, *recJobs) {
	t.Helper()

	r := newPlanRig(t, 120, 40)
	jobs := picturesOn(t, r.a)
	r.a.plans = core.DefaultConfig().Plans
	r.filePlan(t, "docs/superpowers/plans/p.md", text)
	chord(r.a, 'a')
	r.open(t)

	if r.a.plan == nil {
		t.Fatal("the overlay did not open")
	}

	return r, jobs
}

func TestPlanPicture(t *testing.T) {
	t.Parallel()

	r, jobs := planPicture(t, picturePageDoc)

	if !strings.Contains(r.shown(t), "loading…") {
		t.Fatalf("the plan shows:\n%s", r.shown(t))
	}

	if n := answerInfo(r.a, jobs, 5, 3, nil); n != 1 {
		t.Fatalf("info jobs = %d, want 1", n)
	}

	id := r.a.plan.view.layout().Images[0].ID

	r.a.View()

	if got := answerRender(t, r.a, jobs); !slices.Equal(got, []string{"SEQ"}) {
		t.Fatalf("render answers = %q", got)
	}

	content := r.a.View().Content

	rows := placeholders(content, id)
	if len(rows) != 3 || rows[0].n != 5 {
		t.Fatalf("placeholder rows = %+v, want 3 of 5 cells", rows)
	}

	// The picture sits in the first column of the view, where the text starts.
	if x := textX(t, content, "tail"); rows[0].x != x {
		t.Fatalf("placeholders start at column %d, the text at %d", rows[0].x, x)
	}

	r.a.plan.chooser = &linkChooser{targets: []mdTarget{{Text: "one", URL: "https://example.com/1"}, {Text: "two", URL: "https://example.com/2"}}, root: r.wt}

	if got := placeholders(r.a.View().Content, id); len(got) != 0 {
		t.Fatalf("placeholders under the chooser: %+v", got)
	}

	r.a.plan.chooser = nil

	if got := placeholders(r.a.View().Content, id); len(got) != 3 {
		t.Fatalf("placeholders after the chooser closed: %+v", got)
	}
}

func TestPictureReloadRetry(t *testing.T) {
	t.Parallel()

	type rig struct {
		a      *App
		jobs   *recJobs
		view   func() string
		reload func()
		id     func() uint32
	}

	tests := []struct {
		name  string
		build func(t *testing.T) rig
	}{
		{"r on a page", func(t *testing.T) rig {
			m := newPicturePage(t, 200, 50, picturePageDoc)

			return rig{
				a: m.App, jobs: m.jobs,
				view:   func() string { return m.View().Content },
				reload: func() { m.press(keyRune('r')) },
				id:     func() uint32 { return m.v.page().view.layout().Images[0].ID },
			}
		}},
		{"a rewritten plan", func(t *testing.T) rig {
			r, jobs := planPicture(t, picturePageDoc)
			path := r.a.plan.plan.Path

			return rig{
				a: r.a, jobs: jobs,
				view: func() string { return r.a.View().Content },
				reload: func() {
					for _, m := range r.rewrite(t, "p2", path, picturePageDoc+"\nmore\n") {
						r.a.Update(m)
					}
				},
				id: func() uint32 { return r.a.plan.view.layout().Images[0].ID },
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := tt.build(t)

			if n := answerInfo(r.a, r.jobs, 0, 0, fmt.Errorf("boom")); n != 1 {
				t.Fatalf("first info jobs = %d, want 1", n)
			}

			if got := ansi.Strip(r.view()); strings.Contains(got, "loading…") {
				t.Fatalf("after the failure the doc shows:\n%s", got)
			}

			r.reload()

			if got := ansi.Strip(r.view()); !strings.Contains(got, "loading…") {
				t.Fatalf("after the reload the doc shows:\n%s", got)
			}

			if n := answerInfo(r.a, r.jobs, 5, 3, nil); n != 1 {
				t.Fatalf("info jobs after the reload = %d, want 1", n)
			}

			id := r.id()
			r.view()

			if got := answerRender(t, r.a, r.jobs); !slices.Equal(got, []string{"SEQ"}) {
				t.Fatalf("render answers = %q", got)
			}

			if rows := placeholders(r.view(), id); len(rows) != 3 {
				t.Fatalf("placeholder rows = %+v, want 3", rows)
			}
		})
	}
}
