package ui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/sahilm/fuzzy"

	"github.com/mhersson/aim/internal/md"
	"github.com/mhersson/aim/internal/ui/palette"
)

const (
	// finderFilterInline is the most walked names the filter runs over on the
	// update loop; past it the filter runs off the loop.
	finderFilterInline = 5000
	// finderPreviewMin is the narrowest terminal that keeps the preview column.
	finderPreviewMin = 100
	// finderListShare is the list's share of the box's inner width, in percent,
	// and finderListMin its least width in cells.
	finderListShare = 40
	finderListMin   = 18
	// finderMinH is the least box height with a list row: the borders, the
	// prompt, two dividers and the hint row take six.
	finderMinH = 7

	finderPlaceholder = "search below this folder"
	finderEmpty       = "no folders or markdown files here"
	finderNoMatches   = "No matches"
	finderSearching   = "searching…"
	finderUnreadable  = "can't read this folder: "
)

type finderRowKind int

const (
	finderEntry finderRowKind = iota // a folder or a markdown file
	finderUp                         // ".."
	finderJump                       // "~/"
)

// finderRow is one row of the list. rel is slash-separated from the folder the
// finder shows; hits are the bytes of rel the query matched.
type finderRow struct {
	kind finderRowKind
	rel  string
	dir  bool
	hits []int
}

// finderInput is what the rows are made of: the folder's listing and whether it
// has a parent, the query with its spaces removed, and what the walk found.
type finderInput struct {
	parent  bool
	listing []md.FolderEntry
	query   string
	entries []md.WalkEntry
}

// finderQuery is the typed text without its blanks.
func finderQuery(raw string) string { return strings.Join(strings.Fields(raw), "") }

// finderSearches reports whether q is searched for below the folder, as against
// the three queries that read the folder or jump.
func finderSearches(q string) bool { return q != "" && q != "." && q != "~" && q != ".." }

// finderHidden reports whether a search for q reaches hidden entries.
func finderHidden(q string) bool { return strings.HasPrefix(q, ".") }

// finderRows is the list for in. The folder's listing is in the order
// md.ReadFolder gives; a search is ranked by the palette's fuzzy matcher over
// the walk's paths, and ties keep the walk's order.
func finderRows(in finderInput) []finderRow {
	entry := func(e md.FolderEntry) finderRow { return finderRow{rel: e.Name, dir: e.Dir} }
	up := finderRow{kind: finderUp, rel: ".."}

	var rows []finderRow

	switch q := in.query; q {
	case "":
		if in.parent {
			rows = append(rows, up)
		}

		for _, e := range in.listing {
			if !strings.HasPrefix(e.Name, ".") {
				rows = append(rows, entry(e))
			}
		}
	case ".":
		for _, e := range in.listing {
			if strings.HasPrefix(e.Name, ".") {
				rows = append(rows, entry(e))
			}
		}
	case "~":
		rows = append(rows, finderRow{kind: finderJump, rel: "~/"})
	case "..":
		if in.parent {
			rows = append(rows, up)
		}
	default:
		rows = finderSearch(q, in.entries)
	}

	return rows
}

// walkNames is the walk's paths as the fuzzy matcher's source.
type walkNames []md.WalkEntry

func (w walkNames) String(i int) string { return w[i].Rel }
func (w walkNames) Len() int            { return len(w) }

func finderSearch(q string, entries []md.WalkEntry) []finderRow {
	ms := fuzzy.FindFrom(q, walkNames(entries))
	rows := make([]finderRow, len(ms))

	for i, m := range ms {
		e := entries[m.Index]
		rows[i] = finderRow{rel: e.Rel, dir: e.Dir, hits: m.MatchedIndexes}
	}

	return rows
}

// finderWalk is the walk of one folder: what it found so far and why it ended.
// Its goroutine is owned by the finder and ends when cancel runs or the walk is
// over.
type finderWalk struct {
	seq     int
	hidden  bool
	entries []md.WalkEntry
	running bool
	stop    md.WalkStop
	cancel  context.CancelFunc
	ch      <-chan finderWalkMsg
}

// The count's words for a walk that hit a limit.
const (
	finderStoppedFiles = "stopped at %s files"
	finderStoppedTime  = "stopped after %d s"
)

// tail is what the count says of the walk, after the numbers; nothing for a
// walk that ran to its end.
func (w *finderWalk) tail() string {
	switch {
	case w == nil:
		return ""
	case w.running:
		return " · " + finderSearching
	case w.stop == md.WalkFileLimit:
		return " · " + fmt.Sprintf(finderStoppedFiles, thousands(md.WalkMaxFiles))
	case w.stop == md.WalkTimeLimit:
		return " · " + fmt.Sprintf(finderStoppedTime, int(md.WalkMaxTime/time.Second))
	}

	return ""
}

// thousands writes n with a comma between each three digits.
func thousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}

	return s
}

// finderCountText is the count in the bottom border: the selected row, 1-based,
// of all of them.
func finderCountText(sel, n int, tail string) string {
	shown := 0
	if n > 0 {
		shown = sel + 1
	}

	return strconv.Itoa(shown) + " of " + strconv.Itoa(n) + tail
}

// finderHints are the hint row's pairs that fit width cells, the margin and the
// gaps counted as Box.Hints draws them. enter is the label of the enter hint;
// backspace goes first when the row is narrow.
func finderHints(enter string, emptyPrompt bool, width int) [][2]string {
	pairs := [][2]string{{"up/down", "move"}, {"enter", enter}}
	if emptyPrompt {
		pairs = append(pairs, [2]string{"backspace", "up"})
	}

	pairs = append(pairs, [2]string{"ctrl+d/u", "preview"}, [2]string{"esc", "close"})

	total := 1 + 3*(len(pairs)-1)
	for _, p := range pairs {
		total += len(p[0]) + 1 + len(p[1])
	}

	if emptyPrompt && total > width {
		pairs = append(pairs[:2], pairs[3:]...)
	}

	return pairs
}

// finderFilter is the off-loop filter's state: at most one runs, and a change
// that lands while it does marks it dirty so its answer starts the next.
type finderFilter struct{ running, dirty bool }

// finderView is the open finder. It is UI state and is never saved; it stays
// set while the reading box shows over it.
type finderView struct {
	dir   string
	input textinput.Model
	query string // the input without its blanks

	listing []md.FolderEntry
	listErr error
	loaded  bool
	want    string // the folder to select when the listing lands

	walk    *finderWalk
	walkSeq int

	rows     []finderRow
	sel, top int
	// moved says the operator moved the cursor since the query last changed, so
	// a refilter keeps it on the same row; fresh says the rows are for a query
	// whose answer has not landed, so the cursor starts at the first row.
	moved, fresh bool
	filter       finderFilter

	// gen is bumped at each folder change and at close; an answer for another
	// generation is stale.
	gen int

	// The preview column: what is on show, the path last asked for, the serial
	// of the ask that may still land and the cancel of its read.
	preview  *finderPreview
	pvAsked  string
	pvSeq    int
	pvCancel context.CancelFunc
}

// finderListMsg answers the read of a folder.
type finderListMsg struct {
	view    *finderView
	gen     int
	entries []md.FolderEntry
	err     error
}

// finderWalkMsg carries a batch of the walk, or its end.
type finderWalkMsg struct {
	view  *finderView
	seq   int
	batch []md.WalkEntry
	done  bool
	stop  md.WalkStop
}

// finderFilterMsg answers an off-loop filter of the walk's names by query.
type finderFilterMsg struct {
	view  *finderView
	gen   int
	query string
	rows  []finderRow
}

func (f *finderView) in() finderInput {
	in := finderInput{parent: filepath.Dir(f.dir) != f.dir, listing: f.listing, query: f.query}
	if f.walk != nil {
		in.entries = f.walk.entries
	}

	return in
}

// selected is the row under the cursor.
func (f *finderView) selected() (finderRow, bool) {
	if f.sel < 0 || f.sel >= len(f.rows) {
		return finderRow{}, false
	}

	return f.rows[f.sel], true
}

// keep is the rel a refilter keeps the cursor on, or "" when the cursor goes to
// the default row.
func (f *finderView) keep() string {
	if r, ok := f.selected(); ok && f.moved && !f.fresh {
		return r.rel
	}

	return ""
}

// setRows replaces the rows and puts the cursor on keep, else on the first row
// under "..".
func (f *finderView) setRows(rows []finderRow, keep string) {
	f.rows, f.fresh = rows, false
	f.sel = 0

	if keep != "" {
		for i, r := range rows {
			if r.rel == keep {
				f.sel = i

				return
			}
		}

		return
	}

	if len(rows) > 1 && rows[0].kind == finderUp {
		f.sel = 1
	}
}

// move puts the cursor n rows down, wrapping.
func (f *finderView) move(n int) {
	if len(f.rows) == 0 {
		return
	}

	f.sel = ((f.sel+n)%len(f.rows) + len(f.rows)) % len(f.rows)
	f.moved = true
}

// target is the path a row stands for.
func (f *finderView) target(r finderRow, home string) string {
	switch r.kind {
	case finderUp:
		return filepath.Dir(f.dir)
	case finderJump:
		return home
	}

	return filepath.Join(f.dir, filepath.FromSlash(r.rel))
}

// mdOpenCommand is md.open: the finder, in the folder the operator is looking
// at, with an empty query. It leaves any drawer as it is.
func (a *App) mdOpenCommand() tea.Cmd {
	st := a.store.State()
	dir := cmp.Or(a.mdOpenDir(st), st.Panes[st.Tab().Focus].Cwd, a.getenv("HOME"), "/")

	a.closeFinder()
	a.endLeader()

	in := palette.NewInput(a.theme)
	in.Placeholder = finderPlaceholder

	f := &finderView{input: in}
	a.finder = f

	return a.finderGo(f, filepath.Clean(dir), "")
}

// closeFinder closes the finder, ends its walk and drops every answer still on
// its way, a file read included.
func (a *App) closeFinder() {
	f := a.finder
	if f == nil {
		return
	}

	f.gen++
	f.cancelWalk()
	f.drop()

	a.finder = nil
	a.planSeq++
}

func (f *finderView) cancelWalk() {
	if f.walk != nil {
		f.walk.cancel()
		f.walk = nil
	}
}

// finderGo shows dir with an empty query and reads it off the update loop. want
// is the folder to put the cursor on once the listing lands.
func (a *App) finderGo(f *finderView, dir, want string) tea.Cmd {
	f.gen++
	f.cancelWalk()
	f.drop()
	f.dir, f.want = dir, want
	f.listing, f.listErr, f.loaded = nil, nil, false
	f.filter = finderFilter{}
	f.input.SetValue("")
	f.query, f.moved, f.fresh = "", false, false
	f.setRows(finderRows(f.in()), "")

	gen, ctx := f.gen, a.ctx

	return func() tea.Msg {
		es, err := md.ReadFolder(ctx, dir)

		return finderListMsg{view: f, gen: gen, entries: es, err: err}
	}
}

// onFinderList shows the folder once it was read. An answer for a closed finder
// or another folder is dropped.
func (a *App) onFinderList(msg finderListMsg) tea.Cmd {
	f := a.finder
	if f == nil || msg.view != f || msg.gen != f.gen {
		return nil
	}

	f.listing, f.listErr, f.loaded = msg.entries, msg.err, true

	if finderSearches(f.query) {
		return nil
	}

	f.fresh = true
	cmd := a.finderRefilter(f)

	if f.want != "" && f.query == "" {
		for i, r := range f.rows {
			if r.dir && r.rel == f.want {
				f.sel = i

				break
			}
		}
	}

	return cmd
}

// finderRefilter makes the rows for the query and the walk so far: at once when
// it is cheap, else off the update loop.
func (a *App) finderRefilter(f *finderView) tea.Cmd {
	if !finderSearches(f.query) || len(f.walk.entriesOrNil()) <= finderFilterInline {
		f.setRows(finderRows(f.in()), f.keep())

		return nil
	}

	if f.filter.running {
		f.filter.dirty = true

		return nil
	}

	return a.finderFilter(f)
}

func (w *finderWalk) entriesOrNil() []md.WalkEntry {
	if w == nil {
		return nil
	}

	return w.entries
}

// finderFilter starts the off-loop filter of the names found so far. A filter
// reads the slice up to its length: append on the loop only writes past it.
func (a *App) finderFilter(f *finderView) tea.Cmd {
	n := len(f.walk.entries)
	entries, q, gen := f.walk.entries[:n:n], f.query, f.gen
	f.filter = finderFilter{running: true}

	return func() tea.Msg {
		return finderFilterMsg{view: f, gen: gen, query: q, rows: finderSearch(q, entries)}
	}
}

// onFinderFilter shows a filter's rows when they are for the query typed since,
// and starts the next filter when a change landed while it ran.
func (a *App) onFinderFilter(msg finderFilterMsg) tea.Cmd {
	f := a.finder
	if f == nil || msg.view != f || msg.gen != f.gen {
		return nil
	}

	dirty := f.filter.dirty
	f.filter = finderFilter{}

	if msg.query == f.query {
		f.setRows(msg.rows, f.keep())
	}

	if dirty && finderSearches(f.query) {
		return a.finderFilter(f)
	}

	return nil
}

// finderQueryChanged follows an edit of the prompt: the cursor starts over, the
// walk starts when the query needs one and the rows are made.
func (a *App) finderQueryChanged(f *finderView) tea.Cmd {
	f.query = finderQuery(f.input.Value())
	f.moved, f.fresh = false, true

	return tea.Batch(a.finderWalkFor(f), a.finderRefilter(f))
}

// finderWalkFor starts the walk the query needs unless the folder's walk for the
// same reach of hidden entries is running or done.
func (a *App) finderWalkFor(f *finderView) tea.Cmd {
	if !finderSearches(f.query) {
		return nil
	}

	hidden := finderHidden(f.query)
	if f.walk != nil && f.walk.hidden == hidden {
		return nil
	}

	f.cancelWalk()

	f.walkSeq++
	live, cancel := context.WithCancel(a.ctx)
	ch := make(chan finderWalkMsg)
	f.walk = &finderWalk{seq: f.walkSeq, hidden: hidden, running: true, cancel: cancel, ch: ch}

	opts := md.WalkOptions{Root: f.dir, Hidden: hidden, SkipAbs: md.WalkSkips(runtime.GOOS, a.getenv("HOME"))}
	go runFinderWalk(live, opts, f, f.walk.seq, ch)

	return waitFinderWalk(ch)
}

// runFinderWalk walks under live and the time limit, sending batches and the
// end on ch; it ends when the walk does or live is cancelled. The sends select
// on live alone, since md.Walk still flushes after the time limit and the
// reason it stopped must land.
func runFinderWalk(live context.Context, opts md.WalkOptions, f *finderView, seq int, ch chan<- finderWalkMsg) {
	defer close(ch)

	send := func(m finderWalkMsg) {
		select {
		case ch <- m:
		case <-live.Done():
		}
	}

	timed, cancel := context.WithTimeout(live, md.WalkMaxTime)
	defer cancel()

	stop := md.Walk(timed, opts, func(batch []md.WalkEntry) {
		send(finderWalkMsg{view: f, seq: seq, batch: batch})
	})

	send(finderWalkMsg{view: f, seq: seq, done: true, stop: stop})
}

// waitFinderWalk reads the walk's next message.
func waitFinderWalk(ch <-chan finderWalkMsg) tea.Cmd {
	return func() tea.Msg {
		if m, ok := <-ch; ok {
			return m
		}

		return nil
	}
}

// onFinderWalk takes a batch of the walk, or its end. A message of a closed
// finder or a walk since replaced is dropped, and the walk is not waited on
// again.
func (a *App) onFinderWalk(msg finderWalkMsg) tea.Cmd {
	f := a.finder
	if f == nil || msg.view != f || f.walk == nil || msg.seq != f.walk.seq {
		return nil
	}

	w := f.walk
	w.entries = append(w.entries, msg.batch...)

	var wait tea.Cmd

	if msg.done {
		w.running, w.stop = false, msg.stop
	} else {
		wait = waitFinderWalk(w.ch)
	}

	if len(msg.batch) == 0 || !finderSearches(f.query) {
		return wait
	}

	return tea.Batch(wait, a.finderRefilter(f))
}

// handleFinderKey answers the open finder. A box too small to draw answers esc
// alone.
func (a *App) handleFinderKey(msg tea.KeyPressMsg) tea.Cmd {
	a.notice = ""
	f := a.finder
	key := msg.String()

	if key == "esc" {
		a.closeFinder()

		return nil
	}

	if !a.finderShows() {
		return nil
	}

	home := a.getenv("HOME")

	switch key {
	case "down", "ctrl+n", "tab":
		f.move(1)

		return nil
	case "up", "ctrl+p", "shift+tab":
		f.move(-1)

		return nil
	case "ctrl+d", "pgdown":
		a.finderPreviewScroll(f, 1)

		return nil
	case "ctrl+u", "pgup":
		a.finderPreviewScroll(f, -1)

		return nil
	case "enter":
		return a.finderEnter(f, home)
	case "backspace":
		if f.input.Value() == "" {
			return a.finderUp(f)
		}
	case "/":
		if cmd, ok := a.finderJump(f, home); ok {
			return cmd
		}
	}

	return a.finderInput(msg)
}

// finderInput gives the prompt a message and follows an edit of its text. A
// paste never jumps.
func (a *App) finderInput(msg tea.Msg) tea.Cmd {
	f := a.finder
	prev := f.input.Value()

	var cmd tea.Cmd

	f.input, cmd = f.input.Update(msg)

	if v := f.input.Value(); printable(v) != v {
		f.input.SetValue(printable(v))
	}

	if f.input.Value() == prev {
		return cmd
	}

	return tea.Batch(cmd, a.finderQueryChanged(f))
}

// finderPaste puts pasted text in the prompt.
func (a *App) finderPaste(text string) tea.Cmd {
	if !a.finderShows() {
		return nil
	}

	return a.finderInput(tea.PasteMsg{Content: printable(text)})
}

// finderPrompt gives the prompt a message of its own, such as the answer to the
// clipboard read ctrl+v starts, unless a modal is over the finder.
func (a *App) finderPrompt(msg tea.Msg) tea.Cmd {
	if a.finder == nil || a.modalOverFinder() || !a.finderShows() {
		return nil
	}

	return a.finderInput(msg)
}

// finderShows reports whether the box is big enough to draw with a list row.
func (a *App) finderShows() bool {
	_, _, _, h, ok := planBox(a.width, a.height)

	return ok && h >= finderMinH
}

// finderUp goes up a level and leaves the cursor on the folder it came from;
// at the top it does nothing.
func (a *App) finderUp(f *finderView) tea.Cmd {
	parent := filepath.Dir(f.dir)
	if parent == f.dir {
		return nil
	}

	return a.finderGo(f, parent, filepath.Base(f.dir))
}

// finderJump takes a typed "/" as a jump when the prompt holds nothing, "~" or
// "..": to "/", home or the parent. Anywhere else, or with no home to go to, it
// is an ordinary character and ok is false.
func (a *App) finderJump(f *finderView, home string) (cmd tea.Cmd, ok bool) {
	switch f.input.Value() {
	case "":
		return a.finderGo(f, "/", ""), true
	case "~":
		if home == "" {
			return nil, false
		}

		return a.finderGo(f, filepath.Clean(home), ""), true
	case "..":
		if cmd := a.finderUp(f); cmd != nil {
			return cmd, true
		}

		f.input.SetValue("")

		return a.finderQueryChanged(f), true
	}

	return nil, false
}

// finderEnter opens the row under the cursor: a file in the reading box, a
// folder by going into it, ".." by going up and "~/" by going home.
func (a *App) finderEnter(f *finderView, home string) tea.Cmd {
	r, ok := f.selected()
	if !ok {
		return nil
	}

	switch {
	case r.kind == finderUp:
		return a.finderUp(f)
	case r.kind == finderJump || r.dir:
		return a.finderGo(f, f.target(r, home), "")
	}

	f.moved, f.fresh = true, false

	return a.openFinderFile(f.target(r, home))
}

// pathErrReason is the reason in a failed read, as markdownNotice words it.
func pathErrReason(err error) string {
	if pe, ok := errors.AsType[*fs.PathError](err); ok {
		return printable(pe.Err.Error())
	}

	return printable(err.Error())
}

// finderMessage is the line the list shows when it has no row to show, or
// under ".." when that is the only one, and whether it is an error. The row it
// stands on is len(f.rows).
func finderMessage(f *finderView) (text string, isErr bool) {
	switch q := f.query; {
	case finderSearches(q):
		if len(f.rows) > 0 {
			return "", false
		}

		if f.walk != nil && f.walk.running {
			return finderSearching, false
		}

		return finderNoMatches, false
	case q == "" || q == ".":
		if !f.loaded {
			return "", false
		}

		if f.listErr != nil {
			return finderUnreadable + pathErrReason(f.listErr), true
		}

		if n := len(f.rows); n > 1 || n == 1 && f.rows[0].kind != finderUp {
			return "", false
		}

		if q == "" {
			return finderEmpty, false
		}

		return finderNoMatches, false
	}

	if len(f.rows) == 0 {
		return finderNoMatches, false
	}

	return "", false
}

// finderTitle is the folder in the title: ~ for home itself, ~/ inside it.
func finderTitle(dir, home string) string {
	if home != "" && dir == home {
		return "~"
	}

	return shortPath(dir, "", home)
}
