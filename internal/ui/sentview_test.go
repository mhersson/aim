package ui

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
)

// pageLabels names pages as "<seq>:<image indices>".
func pageLabels(pages []sentPage) []string {
	out := make([]string, len(pages))

	for i, p := range pages {
		idx := make([]string, len(p.images))
		for k, n := range p.images {
			idx[k] = fmt.Sprint(n)
		}

		out[i] = fmt.Sprintf("%d:%s", p.batch.seq, strings.Join(idx, ","))
	}

	return out
}

func testBatches() []sentBatch {
	mk := func(seq, n int) sentBatch {
		return sentBatch{seq: seq, images: make([]sentImage, n)}
	}

	return []sentBatch{mk(1, 1), mk(2, 2), mk(3, 3)}
}

func TestSentPages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		wide bool
		want string
	}{
		{"wide", true, "3:0 3:1 3:2 2:0,1 1:0"},
		{"narrow", false, "3:0 3:1 3:2 2:0 2:1 1:0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := strings.Join(pageLabels(sentPages(testBatches(), tt.wide)), " "); got != tt.want {
				t.Fatalf("pages = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestSentIndicator(t *testing.T) {
	t.Parallel()

	pages := sentPages(testBatches(), true)

	for at, want := range map[int]string{0: " 1 of 6 ", 3: " 4-5 of 6 ", 4: " 6 of 6 "} {
		if got := sentIndicator(pages, at); got != want {
			t.Errorf("sentIndicator(%d) = %q, want %q", at, got, want)
		}
	}
}

// sentSend sends one batch of n fresh PNGs from pane id with a caption.
func sentSend(t *testing.T, a *App, id core.PaneID, n int, caption string) {
	t.Helper()

	dir := t.TempDir()

	var paths []string

	for i := range n {
		p := filepath.Join(dir, fmt.Sprintf("shot%d.png", i))
		writePNG(t, p, 40, 20)

		paths = append(paths, p)
	}

	sentHook(a, harness.Event{Pane: id, Name: "show", Show: true, Images: paths, Caption: caption})
}

func keyOf(s string) tea.KeyPressMsg {
	switch s {
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "esc":
		return keyEsc
	}

	return keyRune([]rune(s)[0])
}

func shownAt(a *App) (seq, part int) {
	if a.sent.view == nil {
		return 0, 0
	}

	return a.sent.view.batch, a.sent.view.part
}

func TestSentViewPicksTheAgent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		send func(t *testing.T, a *App)
		// focus is the pane to focus before opening.
		focus core.PaneID
		want  core.PaneID // "" for a viewer left shut
	}{
		{
			"the focused pane with batches",
			func(t *testing.T, a *App) {
				t.Helper()
				sentSend(t, a, "p2", 1, "")
				sentSend(t, a, "p1", 1, "")
			},
			"p2", "p2",
		},
		{
			"a focused pane without batches gives the newest batch's pane",
			func(t *testing.T, a *App) {
				t.Helper()
				sentSend(t, a, "p2", 1, "")
				sentSend(t, a, "p1", 1, "")
			},
			"p3", "p1",
		},
		{"no batches anywhere", func(*testing.T, *App) {}, "p2", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, _ := sentApp(t)
			chord(a, 's')
			tt.send(t, a)

			if err := a.dispatch(core.FocusPane{ID: tt.focus}); err != nil {
				t.Fatal(err)
			}

			run(a, a.openSentView())

			switch {
			case tt.want == "":
				if a.sent.view != nil || a.notice != noticeNoImages {
					t.Fatalf("view = %+v, notice = %q, want a shut viewer and the notice", a.sent.view, a.notice)
				}
			case a.sent.view == nil || a.sent.view.pane != tt.want:
				t.Fatalf("view = %+v, want pane %s", a.sent.view, tt.want)
			}
		})
	}
}

func TestSentViewSteps(t *testing.T) {
	t.Parallel()

	a, _ := sentApp(t)
	sized(t, a, 120, 40)

	for range 3 {
		sentSend(t, a, "p2", 1, "")
	}

	run(a, a.openSentView())

	if seq, _ := shownAt(a); seq != 3 {
		t.Fatalf("opened on batch %d, want the newest, 3", seq)
	}

	steps := []struct {
		key  string
		want int
	}{
		{"h", 3}, {"left", 3}, {"l", 2}, {"right", 1}, {"l", 1}, {"h", 2}, {"left", 3},
	}

	for _, s := range steps {
		pressRun(a, keyOf(s.key))

		if seq, _ := shownAt(a); seq != s.want {
			t.Fatalf("after %s: batch %d, want %d", s.key, seq, s.want)
		}
	}

	pressRun(a, keyEsc)

	if a.sent.view != nil {
		t.Fatal("esc left the viewer open")
	}
}

func TestSentViewKeepsItsPageWhenABatchArrives(t *testing.T) {
	t.Parallel()

	a, _ := sentApp(t)
	sized(t, a, 70, 40)

	sentSend(t, a, "p2", 2, "")
	sentSend(t, a, "p2", 1, "")

	run(a, a.openSentView())
	pressRun(a, keyRune('l'), keyRune('l'))

	seq, part := shownAt(a)
	if seq != 1 || part != 1 {
		t.Fatalf("shown = batch %d part %d, want batch 1 part 1", seq, part)
	}

	sentSend(t, a, "p2", 1, "")

	if s, p := shownAt(a); s != seq || p != part {
		t.Fatalf("after a new batch: batch %d part %d, want batch %d part %d", s, p, seq, part)
	}
}

func TestSentViewClosesWhenItsPaneGoes(t *testing.T) {
	t.Parallel()

	a, _ := sentApp(t)
	sentSend(t, a, "p2", 1, "")
	run(a, a.openSentView())

	if a.sent.view == nil {
		t.Fatal("viewer did not open")
	}

	if err := a.dispatch(core.ClosePane{ID: "p2"}); err != nil {
		t.Fatal(err)
	}

	sized(t, a, 100, 30)

	if a.sent.view != nil {
		t.Fatal("viewer stayed open after its pane closed")
	}
}

func TestSentViewTakesKeysBeforeTheLeader(t *testing.T) {
	t.Parallel()

	a, st, _ := kindApp(t)
	chord(a, 'n')

	a.sent.dir = t.TempDir()

	sentSend(t, a, "p2", 1, "")
	run(a, a.openSentView())

	panes, started := len(a.store.State().Panes), st.count()

	pressRun(a, keyLeader, keyRune('x'))
	a.Update(tea.PasteMsg{Content: "pasted"})

	if a.sent.view == nil || a.pending || len(a.store.State().Panes) != panes || st.count() != started {
		t.Fatalf("viewer %v, pending %v, panes %d, want it open, no leader and no pane change", a.sent.view, a.pending, len(a.store.State().Panes))
	}

	for i := range started {
		if got := st.proc(i).sent(); got != "" {
			t.Fatalf("pane %d received %q", i, got)
		}
	}
}

func TestSentViewTooSmall(t *testing.T) {
	t.Parallel()

	a, _ := sentApp(t)
	sentSend(t, a, "p2", 1, "")
	sentSend(t, a, "p2", 1, "")
	run(a, a.openSentView())

	sized(t, a, 20, 30)

	if _, _, _, _, ok := planBox(a.width, a.height); ok {
		t.Fatal("planBox is ok at 20 columns")
	}

	before, part := shownAt(a)

	pressRun(a, keyRune('l'))

	if s, p := shownAt(a); s != before || p != part {
		t.Fatalf("l moved to batch %d part %d in a box that is not drawn", s, p)
	}

	pressRun(a, keyEsc)

	if a.sent.view != nil {
		t.Fatal("esc did not close the unseen viewer")
	}
}

func TestPicturesSizeIn(t *testing.T) {
	t.Parallel()

	a, _ := sentApp(t)
	jobs := picturesOn(t, a)
	a.pictures.cell.W, a.pictures.cell.H = 10, 20

	src := filepath.Join(a.sent.dir, "big.png")
	writePNG(t, src, 4, 2)

	d := docPictures{reg: a.pictures, root: a.sent.dir}

	if sz := a.pictures.sizeIn(d, src, 150, 40); !sz.Loading {
		t.Fatalf("first answer = %+v, want loading", sz)
	}

	if n := answerInfo(a, jobs, 2000, 1000, nil); n != 1 {
		t.Fatalf("info jobs = %d, want 1", n)
	}

	if sz := a.pictures.sizeIn(d, src, 150, 40); sz.Cols != 150 || sz.Rows != 38 {
		t.Fatalf("sizeIn = %dx%d, want 150x38", sz.Cols, sz.Rows)
	}

	// The 2:1 image at 20 rows is 80 columns: size's caps are 100x20.
	if sz := d.size(src, 150); sz.Cols != 80 || sz.Rows != pictureMaxRows {
		t.Fatalf("size = %dx%d, want 80x%d", sz.Cols, sz.Rows, pictureMaxRows)
	}
}

// sentViewScene opens the viewer on a two-image batch at width x height.
func sentViewScene(t *testing.T, width, height int, pictures bool) *App {
	t.Helper()

	a, _ := sentApp(t)
	sized(t, a, width, height)

	var jobs *recJobs
	if pictures {
		jobs = picturesOn(t, a)
	}

	sentSend(t, a, "p2", 2, "before and after the fix")
	run(a, a.openSentView())

	if a.sent.view == nil {
		t.Fatal("viewer did not open")
	}

	if pictures {
		a.View()
		answerInfo(a, jobs, 400, 200, nil)
		a.View()
		answerRender(t, a, jobs)
	}

	return a
}

// plain strips styling and shows each picture cell as a block, so the golden
// reads and holds where the pictures sit.
func plain(s string) string {
	var b strings.Builder

	for _, r := range ansi.Strip(s) {
		switch {
		case r == 0x10EEEE:
			b.WriteRune('▒')
		case unicode.Is(unicode.Mn, r):
		default:
			b.WriteRune(r)
		}
	}

	return b.String()
}

func TestSentViewGolden(t *testing.T) {
	t.Parallel()

	for _, sc := range []struct {
		name string
		w, h int
	}{{"side-by-side", 120, 40}, {"one-per-page", 70, 40}} {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()

			a := sentViewScene(t, sc.w, sc.h, true)
			golden.RequireEqual(t, []byte(plain(a.View().Content)))
		})
	}
}

func TestSentViewNoPicturesGolden(t *testing.T) {
	t.Parallel()

	a := sentViewScene(t, 100, 30, false)
	golden.RequireEqual(t, []byte(plain(a.View().Content)))
}

// sentOpenScene opens the viewer on one batch of n images at 120x40, with a
// fake opener.
func sentOpenScene(t *testing.T, n int) (*App, *fakeOpen) {
	t.Helper()

	a, _ := sentApp(t)
	sized(t, a, 120, 40)

	f := &fakeOpen{}
	a.openLink = f.open

	sentSend(t, a, "p2", n, "")
	run(a, a.openSentView())

	return a, f
}

func shownImages(a *App) []sentImage {
	pages, at := a.sentPos()

	var out []sentImage
	for _, i := range pages[at].images {
		out = append(out, pages[at].batch.images[i])
	}

	return out
}

func TestSentOpenRunsTheOpener(t *testing.T) {
	t.Parallel()

	for _, n := range []int{1, 2} {
		t.Run(fmt.Sprint(n, " images"), func(t *testing.T) {
			t.Parallel()

			a, f := sentOpenScene(t, n)

			var want []string
			for _, im := range shownImages(a) {
				want = append(want, im.copy)
			}

			pressRun(a, keyRune('o'))

			if got := f.urls(); !slices.Equal(got, want) || len(want) != n {
				t.Fatalf("opened %v, want the copies %v", got, want)
			}
		})
	}
}

func TestSentOpenRefusesWhatAIMCouldNotRead(t *testing.T) {
	t.Parallel()

	a, f := sentOpenScene(t, 2)
	a.sent.panes["p2"].batches[0].images[1].reason = "not an image"

	pressRun(a, keyRune('o'))

	bad := a.sent.panes["p2"].batches[0].images[1]
	if want := noticeNotOpening(filepath.Base(bad.path), bad.reason); a.notice != want {
		t.Fatalf("notice = %q, want %q", a.notice, want)
	}

	if got := f.urls(); len(got) != 0 {
		t.Fatalf("opened %v, want nothing", got)
	}
}

func TestSentOpenSaysWhyTheViewerFailed(t *testing.T) {
	t.Parallel()

	a, f := sentOpenScene(t, 1)
	f.fail = errors.New("no xdg-open")

	pressRun(a, keyRune('o'))

	if a.noticeLevel() != levelError || !strings.Contains(a.notice, f.fail.Error()) {
		t.Fatalf("notice = %q at level %d, want an error with %q", a.notice, a.noticeLevel(), f.fail)
	}
}
