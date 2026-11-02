package ui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/sahilm/fuzzy"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/ui/palette"
)

const (
	pickerOpenTimeout   = 5 * time.Second // the repository check and the worktree list
	pickerProbeTimeout  = 5 * time.Second // the new-worktree page's probes
	pickerCreateTimeout = 2 * time.Minute // git checks a whole tree out
	maxPickerChars      = 200
)

// noticePrunable is shown for enter on a worktree whose directory is gone.
const noticePrunable = "that worktree is gone; git worktree prune removes it"

const (
	formLabelW  = len("branch")
	formValueAt = 1 + 2 + formLabelW + 2 // a cell, the marker, the label and its gap: where a field's text starts inside the box
)

type pickerPage int

const (
	pageWorktrees pickerPage = iota
	pageNewWorktree
)

// pickerState is the worktree picker's share of the App.
type pickerState struct {
	cfg  core.WorktreesConfig
	seq  int     // counts requests; an answer for an earlier one is dropped
	open *picker // nil unless the picker shows
}

// pickerTree is one listed worktree with the texts its row draws.
type pickerTree struct {
	wt           git.Worktree
	label, short string // the branch, or 7 characters of a detached HEAD; the shortened path
}

// pickerMatch is a row the query keeps and the byte offsets of its label it matched.
type pickerMatch struct {
	tree int
	hit  map[int]bool
}

// picker is the open worktree picker. It owns every key until it closes.
type picker struct {
	seq   int
	tab   core.TabID
	repo  git.Repo
	trees []pickerTree
	main  string // the main checkout's path; "" when the list has none
	home  string

	page    pickerPage
	query   textinput.Model
	matches []pickerMatch
	sel     int // an index into matches, or len(matches) for the new worktree row
	top     int
	form    *worktreeForm // nil until the new-worktree page was first shown
}

// worktreeForm is the new-worktree page.
type worktreeForm struct {
	branch, base textinput.Model
	onBase       bool // the base field has the keyboard
	tools        []git.Tool
	err          string
	busy         bool // a creation is running
}

// pickerOpenMsg answers the repository check of a new agent request.
type pickerOpenMsg struct {
	seq   int
	tab   core.TabID
	pane  core.PaneID // the focused pane when it was asked
	cwd   string
	repo  git.Repo
	trees []git.Worktree
	err   error
}

// pickerPageMsg answers the new-worktree page's probes.
type pickerPageMsg struct {
	seq     int
	base    string // the default base branch, "" when there is none
	baseErr error
	tools   []git.Tool
}

// pickerCreatedMsg answers a worktree creation.
type pickerCreatedMsg struct {
	seq     int
	created git.Created
	err     error
}

// pickerSource lets fuzzy search a worktree's label and path together.
type pickerSource []pickerTree

func (s pickerSource) String(i int) string { return s[i].label + " " + s[i].short }
func (s pickerSource) Len() int            { return len(s) }

// newAgent implements agent.new. Whether the focused pane's working directory
// is in a repository is found off the update loop: in one the picker opens on its
// worktrees, elsewhere the agent opens at once.
func (a *App) newAgent() tea.Cmd {
	if !a.started {
		return nil
	}

	st := a.store.State()
	if a.panesFull(st) {
		return nil
	}

	cwd, tab, pane := a.focusedCwd(st), st.Tab().ID, st.Tab().Focus

	a.picker.seq++
	seq := a.picker.seq

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(a.ctx, pickerOpenTimeout)
		defer cancel()

		msg := pickerOpenMsg{seq: seq, tab: tab, pane: pane, cwd: cwd}
		if msg.repo, msg.err = a.git.Open(ctx, cwd); msg.err == nil {
			msg.trees, msg.err = a.git.ListWorktrees(ctx, msg.repo)
		}

		return msg
	}
}

// onPickerOpen opens the picker, or the agent when there is nothing to pick. An
// answer for an earlier request, another tab or pane, or under another modal is
// dropped.
func (a *App) onPickerOpen(msg pickerOpenMsg) tea.Cmd {
	st := a.store.State()
	if msg.seq != a.picker.seq || st.Tab().ID != msg.tab || st.Tab().Focus != msg.pane ||
		a.modalOpen() {
		return nil
	}

	if errors.Is(msg.err, context.Canceled) {
		return nil
	}

	home := a.getenv("HOME")

	trees, main := pickerTrees(msg.trees, home)
	if msg.err == nil && len(trees) > 0 {
		a.endLeader()

		p := &picker{seq: msg.seq, tab: msg.tab, repo: msg.repo, trees: trees, main: main, home: home, query: newPickerInput()}
		p.refilter()
		a.picker.open = p

		return nil
	}

	failed := msg.err != nil && !errors.Is(msg.err, git.ErrNotRepo)
	if failed {
		a.log.Warn("new agent: list worktrees", "cwd", msg.cwd, "err", msg.err)
	}

	cmd := a.openPaneIn(core.KindAgent, core.SplitVertical, msg.cwd)

	// The pane's own notice (it did not open, or fell back to the launch dir) says more.
	if failed && a.notice == "" {
		a.notice = "worktrees unavailable: " + gitReason(msg.err)
	}

	return cmd
}

// pickerTrees prepares the rows for list, skipping a bare repository's entry,
// and finds the main checkout their paths are shortened against. Branches and
// paths are repository content, so the rows keep them without control or
// invisible characters; the fuzzy matches index the cleaned text.
func pickerTrees(list []git.Worktree, home string) (trees []pickerTree, main string) {
	for _, w := range list {
		if w.Main && !w.Bare {
			main = w.Path
		}
	}

	for _, w := range list {
		if !w.Bare {
			trees = append(trees, pickerTree{wt: w, label: printable(worktreeLabel(w)), short: shortPath(w.Path, main, home)})
		}
	}

	return trees, main
}

func worktreeLabel(w git.Worktree) string {
	switch {
	case w.Branch != "":
		return w.Branch
	case w.Head != "":
		return w.Head[:min(7, len(w.Head))]
	}

	return "(unknown)"
}

// shortPath is path relative to the main checkout when it lies inside it, else
// with ~ for the home directory, without control or invisible characters.
func shortPath(path, main, home string) string {
	if rel, ok := within(main, path); ok {
		return printable(rel)
	}

	if rel, ok := within(home, path); ok {
		return "~/" + printable(rel)
	}

	return printable(path)
}

// within is path relative to dir when it lies strictly inside it.
func within(dir, path string) (string, bool) {
	if dir == "" {
		return "", false
	}

	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}

	return rel, true
}

func newPickerInput() textinput.Model {
	in := textinput.New()
	// The picker draws the text itself and places the terminal's cursor.
	in.SetVirtualCursor(false)
	in.KeyMap.Paste.SetEnabled(false)
	in.CharLimit = maxPickerChars
	in.Focus()

	return in
}

// refilter rebuilds the rows from the query and selects the first.
func (p *picker) refilter() {
	p.matches = p.matches[:0:0]

	if q := p.query.Value(); q == "" {
		for i := range p.trees {
			p.matches = append(p.matches, pickerMatch{tree: i})
		}
	} else {
		for _, r := range fuzzy.FindFrom(q, pickerSource(p.trees)) {
			hit := make(map[int]bool, len(r.MatchedIndexes))
			for _, ix := range r.MatchedIndexes {
				hit[ix] = true
			}

			p.matches = append(p.matches, pickerMatch{tree: r.Index, hit: hit})
		}
	}

	p.sel, p.top = 0, 0
}

// move selects the row delta away, wrapping; the new worktree row is the last.
func (p *picker) move(delta int) {
	n := len(p.matches) + 1
	p.sel = ((p.sel+delta)%n + n) % n

	if p.sel < p.top {
		p.top = p.sel
	}

	if p.sel >= p.top+palette.MaxRows {
		p.top = p.sel - palette.MaxRows + 1
	}
}

// edit gives msg to the input that has the keyboard and keeps what depends on
// its text in step.
func (p *picker) edit(msg tea.Msg) {
	if p.page == pageWorktrees {
		before := p.query.Value()

		p.query, _ = p.query.Update(msg)
		if p.query.Value() != before {
			p.refilter()
		}

		return
	}

	in := &p.form.branch
	if p.form.onBase {
		in = &p.form.base
	}

	before := in.Value()

	*in, _ = in.Update(msg)
	if in.Value() != before {
		p.form.err = ""
	}
}

// handlePickerKey edits the open picker. routeKey clears the notice only after
// this slot, so the picker clears it itself.
func (a *App) handlePickerKey(msg tea.KeyPressMsg) tea.Cmd {
	a.notice = ""

	p := a.picker.open
	if p.page == pageNewWorktree {
		return a.pickerFormKey(p, msg)
	}

	return a.pickerListKey(p, msg)
}

func (a *App) pickerListKey(p *picker, msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		a.picker.open = nil
	case "down", "ctrl+n", "tab":
		p.move(1)
	case "up", "ctrl+p", "shift+tab":
		p.move(-1)
	case "enter":
		return a.pickerChoose(p)
	default:
		p.edit(msg)
	}

	return nil
}

// pickerChoose opens an agent in the selected worktree, or the new-worktree page.
func (a *App) pickerChoose(p *picker) tea.Cmd {
	if p.sel >= len(p.matches) {
		return a.pickerShowForm(p)
	}

	t := p.trees[p.matches[p.sel].tree]
	if t.wt.Prunable {
		a.notice = noticePrunable

		return nil
	}

	a.picker.open = nil

	return a.openPaneIn(core.KindAgent, core.SplitVertical, t.wt.Path)
}

// pickerShowForm shows the new-worktree page. The first time it also asks for
// the default base and the tools that need an ignore entry.
func (a *App) pickerShowForm(p *picker) tea.Cmd {
	p.page = pageNewWorktree
	if p.form != nil {
		return nil
	}

	p.form = &worktreeForm{branch: newPickerInput(), base: newPickerInput()}
	repo, seq, sibling := p.repo, p.seq, a.picker.cfg.Sibling()

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(a.ctx, pickerProbeTimeout)
		defer cancel()

		msg := pickerPageMsg{seq: seq}
		msg.base, msg.baseErr = a.git.DefaultBranch(ctx, repo)

		// Only the inside root puts a worktree where these tools look.
		if main, err := repo.MainRoot(); err == nil && !sibling {
			msg.tools, _ = git.DetectTools(main) // a check that failed is skipped; the others still count
		}

		return msg
	}
}

// onPickerPage fills the page from the probes' answer, keeping what was typed.
func (a *App) onPickerPage(msg pickerPageMsg) {
	p := a.picker.open
	if p == nil || p.seq != msg.seq || p.form == nil {
		return
	}

	if msg.baseErr != nil && !errors.Is(msg.baseErr, git.ErrNoBase) && !errors.Is(msg.baseErr, context.Canceled) {
		a.log.Debug("new worktree: default branch", "err", msg.baseErr)
	}

	if p.form.base.Value() == "" {
		p.form.base.SetValue(msg.base)
	}

	p.form.tools = msg.tools
}

func (a *App) pickerFormKey(p *picker, msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		p.page = pageWorktrees
	case "tab", "shift+tab":
		p.form.onBase = !p.form.onBase
	case "enter":
		return a.pickerCreate(p)
	default:
		p.edit(msg)
	}

	return nil
}

// rootStyle maps [worktrees] root to the layout git creates.
func (a *App) rootStyle() git.RootStyle {
	if a.picker.cfg.Sibling() {
		return git.RootSibling
	}

	return git.RootInside
}

// pickerCreate starts creating the worktree the page describes.
func (a *App) pickerCreate(p *picker) tea.Cmd {
	f := p.form
	if f.busy {
		return nil
	}

	branch := strings.TrimSpace(f.branch.Value())
	if branch == "" {
		f.err = "enter a branch name"

		return nil
	}

	if _, err := git.WorktreeDir(branch); err != nil {
		f.err = worktreeReason(err)

		return nil
	}

	nw := git.NewWorktree{Branch: branch, Base: strings.TrimSpace(f.base.Value()), Root: a.rootStyle()}
	repo, seq := p.repo, p.seq
	f.busy, f.err = true, ""

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(a.ctx, pickerCreateTimeout)
		defer cancel()

		created, err := a.git.CreateWorktree(ctx, repo, nw)

		return pickerCreatedMsg{seq: seq, created: created, err: err}
	}
}

// onPickerCreated opens an agent in the new worktree, or shows why there is
// none and stays on the page. An answer for a picker that closed is dropped;
// the worktree it made stays.
func (a *App) onPickerCreated(msg pickerCreatedMsg) tea.Cmd {
	p := a.picker.open
	if p == nil || p.seq != msg.seq || p.form == nil {
		a.log.Info("new worktree: answer after the picker closed", "path", msg.created.Worktree.Path, "err", msg.err)

		return nil
	}

	p.form.busy = false

	path := msg.created.Worktree.Path
	if path == "" {
		p.form.err = worktreeReason(msg.err)

		return nil
	}

	// A path with an error means the worktree exists but git could not list it.
	if msg.err != nil {
		a.log.Warn("new worktree", "path", path, "err", msg.err)
	}

	a.picker.open = nil

	ver := a.storeVer
	cmd := a.openPaneIn(core.KindAgent, core.SplitVertical, path)

	notice := "created " + shortPath(path, p.main, p.home)
	if a.storeVer == ver && a.notice != "" {
		notice += "; " + a.notice
	}

	a.notice = notice

	return cmd
}

// worktreeReason is err in a few words for the page.
func worktreeReason(err error) string {
	switch {
	case err == nil:
		return "the worktree was not created"
	case errors.Is(err, git.ErrExists):
		return "that path already exists"
	case errors.Is(err, git.ErrBadBranch):
		return "not a valid branch name"
	case errors.Is(err, git.ErrNoBase):
		return "no usable base branch; name one in the base field"
	case errors.Is(err, git.ErrBare):
		return `no main checkout; set root = "sibling" in [worktrees]`
	}

	return gitReason(err)
}

// gitReason is why git failed, on one line: what git printed after its exit
// status, else the whole message, without control or invisible characters.
func gitReason(err error) string {
	msg, _, _ := strings.Cut(err.Error(), "\n")

	if i := strings.Index(msg, "exit status "); i >= 0 {
		if _, reason, ok := strings.Cut(msg[i:], ": "); ok {
			msg = reason
		}
	}

	return printable(msg)
}

// agentCounts counts the agent panes by the repository root they work in. It
// copies the state, so a draw takes it once.
func (a *App) agentCounts() map[string]int {
	counts := map[string]int{}

	for _, id := range a.store.State().AgentPanes() {
		if root, ok := a.paneRoot(id); ok {
			counts[filepath.Clean(root)]++
		}
	}

	return counts
}
