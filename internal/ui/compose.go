package ui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/github"
)

const (
	// createTimeout bounds opening the pull request.
	createTimeout = 60 * time.Second
	// maxComposeWidth caps the compose box.
	maxComposeWidth = 96
	// maxComposeTitle and maxComposeBody are GitHub's limits.
	maxComposeTitle = 256
	maxComposeBody  = 65536

	noticeComposeNotRepo  = "open a PR: not a git repository"
	noticeComposeDetached = "open a PR: HEAD is detached"

	noticeEditorNoRunDir = "the editor needs the run directory, which is off with agent hooks"
	noticeEditorOpen     = "the PR body is open in an editor"
	statusEditorRead     = "could not read the edited body: "
	statusEditorNoPane   = "could not open the editor pane"

	// maxEditedBody is the most of the edited file that is read back.
	maxEditedBody = 64 << 10
	// editPending stands for the editor pane while its file is being written.
	editPending core.PaneID = "?"
)

// Status rows of the compose box.
const (
	statusComposeTitle    = "enter a title"
	statusComposeOpening  = "opening the pull request…"
	statusComposeChanged  = "%s changed since you opened this; nothing created"
	statusComposeURL      = "%s's push URL changed since you opened this; nothing created"
	statusComposeExists   = "%s already has an open pull request"
	statusComposePushBusy = "%s is already being pushed"
)

// composeField is the field of the compose box that has the keyboard.
type composeField uint8

const (
	composeTitle composeField = iota
	composeBase
	composeDraft
	composeBody
)

func (f composeField) String() string {
	return [...]string{"title", "base", "draft", "body"}[f]
}

// composeTarget is what the compose box opens a pull request for. It is fixed
// when the box opens, whatever the focus or the agent does next.
type composeTarget struct {
	root, branch, oid, remote string
	repo                      github.Repo
	unpushed                  int      // commits the remote lacks
	bases                     []string // the remote's branches, by name
	pane                      core.PaneID
	urls                      []string // the remote's push URLs as git has them, to tell a changed destination; never shown
}

// composeView is the open compose box; it owns every key while open.
type composeView struct {
	t         composeTarget
	title     textinput.Model
	base      int // index into t.bases
	draft     bool
	body      textArea
	source    string // where the body started: a template's path, or "the commits"
	commits   int    // commits of <remote>/<base>..HEAD
	logSeq    int    // the newest request for commits; an older answer is stale
	field     composeField
	status    string
	statusErr bool
	busy      bool
	editing   core.PaneID // the pane editing the body; the box is hidden meanwhile
	bodyRev   int         // bumped by every change of the body text
	preview   composePreview
}

// composePreviewDelay is how long the body rests before it is rendered again.
const composePreviewDelay = 150 * time.Millisecond

// composePreview is the body box showing the body as markdown instead of its
// text. doc is the newest parse; parsed is the body revision it came from and
// rev the newest revision a parse was started for.
type composePreview struct {
	on          bool
	doc         *mdDoc
	rev, parsed int
	top, rows   int
	anchor      int // source line to put at the top once there is a doc; 0 for none
}

// composeRenderDueMsg is the rest after body revision rev.
type composeRenderDueMsg struct {
	v   *composeView
	rev int
}

// composeRenderedMsg answers parsing body revision rev.
type composeRenderedMsg struct {
	v   *composeView
	rev int
	doc *mdDoc
}

// composePreviewW is the width of the body's text in a terminal width cells
// wide: the box less its frame, label column and padding.
func composePreviewW(width int) int { return max(min(maxComposeWidth, width-2)-10, 0) }

// bodyChanged notes a change of the body text and asks for it to be rendered
// once it rests.
func (a *App) bodyChanged(v *composeView) tea.Cmd {
	v.bodyRev++
	rev := v.bodyRev

	return a.tick(composePreviewDelay, func(time.Time) tea.Msg { return composeRenderDueMsg{v: v, rev: rev} })
}

// renderBody parses the body as it is now, off the update loop, unless that
// revision was already asked for.
func (a *App) renderBody(v *composeView) tea.Cmd {
	if v.preview.rev == v.bodyRev {
		return nil
	}

	v.preview.rev = v.bodyRev
	rev := v.bodyRev

	return parseCmd([]byte(v.body.Value()), "", a.drawer.hl.HighlightLang, func(d *mdDoc) tea.Msg {
		return composeRenderedMsg{v: v, rev: rev, doc: d}
	})
}

// onComposeRenderDue parses the body when no change came after revision rev.
func (a *App) onComposeRenderDue(msg composeRenderDueMsg) tea.Cmd {
	if a.compose != msg.v || msg.rev != msg.v.bodyRev {
		return nil
	}

	return a.renderBody(msg.v)
}

// onComposeRendered keeps the parse of the body's current revision.
func (a *App) onComposeRendered(msg composeRenderedMsg) {
	if a.compose != msg.v || msg.rev != msg.v.bodyRev {
		return
	}

	msg.v.preview.doc, msg.v.preview.parsed = msg.doc.withPictures(docPictures{reg: a.pictures, host: msg.v.t.repo.Host}), msg.rev
	a.composePreviewSettle(msg.v)
}

// composePreviewToggle is ctrl+p: it swaps Write and Preview on the body.
func (a *App) composePreviewToggle(v *composeView) tea.Cmd {
	v.focus(composeBody)

	if v.preview.on {
		v.preview.on = false

		return nil
	}

	v.preview.on, v.preview.top, v.preview.anchor = true, 0, v.body.row+1
	a.composePreviewSettle(v)

	return a.renderBody(v)
}

// composePreviewKey scrolls the preview; every other key is dropped.
func (a *App) composePreviewKey(v *composeView, key string) {
	p := &v.preview
	if p.doc == nil {
		return
	}

	a.composePreviewSettle(v)

	rows := p.rows

	switch key {
	case "up":
		p.top--
	case "down":
		p.top++
	case "pgup":
		p.top -= max(rows-1, 1)
	case "pgdown":
		p.top += max(rows-1, 1)
	}

	a.composePreviewSettle(v)
}

// composePreviewSettle fits the preview to the screen: it learns the body
// box's rows, puts the pending source line at the top once the doc for the
// current body is there, and keeps the box full. It runs in Update, never in
// draw.
func (a *App) composePreviewSettle(v *composeView) {
	p := &v.preview

	p.rows = composeBodyRowCount(v, a.height-2)

	if p.doc == nil {
		return
	}

	lay := p.doc.layout(composePreviewW(a.width), a.icons)

	if old, ok := p.doc.pictureMove(); ok {
		p.top += pictureShift(old, lay, p.top)
	}

	if p.anchor > 0 && p.parsed == v.bodyRev && len(lay.Source) > 0 {
		p.top, p.anchor = sourceLineIndex(lay.Source, p.anchor), 0
	}

	p.top = max(0, min(p.top, len(lay.Lines)-p.rows))
}

// bodyEdit is the temp file one editor pane works on, and the box it belongs to.
type bodyEdit struct {
	file string
	v    *composeView
}

// composeShown reports whether the compose box is open and not hidden behind
// its editor.
func (a *App) composeShown() bool { return a.compose != nil && a.compose.editing == "" }

// composeResolvedMsg answers reading what a pull request of one pane's branch
// would be.
type composeResolvedMsg struct {
	seq       int
	pane      core.PaneID
	t         composeTarget
	state     pushState
	notGitHub bool
	err       error // a failure that is none of the states
	gitBase   string
	tmpl      *github.Template
}

// composeLogMsg answers reading the commits a base would put in the pull
// request. open is the box being opened, nil for a change of base.
type composeLogMsg struct {
	seq     int
	logSeq  int
	open    *composeView
	box     *composeView // the box a change of base recounts
	tmpl    *github.Template
	commits []github.Commit
	err     error
}

// composePushedMsg answers the checks and the push before creating.
type composePushedMsg struct {
	v   *composeView
	pr  github.NewPR
	err error
}

// composeCreatedMsg answers creating the pull request.
type composeCreatedMsg struct {
	v   *composeView
	pr  github.PR
	err error
}

// composeCommand is pr.open: it reads, off the update loop, what a pull
// request of the branch push would use is made of, then opens the box.
func (a *App) composeCommand() tea.Cmd {
	a.endLeader()

	if a.compose != nil {
		if a.compose.editing != "" {
			a.notice = noticeEditorOpen
		}

		return nil
	}

	if a.actions == nil || a.prs.src == nil || a.prs.resolve == nil {
		a.notice = noticePRsOff

		return nil
	}

	st := a.store.State()
	id := st.Tab().Focus

	cwd := a.pushDir(st)
	if cwd == "" {
		a.notice = noticeComposeNotRepo

		return nil
	}

	a.composeSeq++

	var (
		seq     = a.composeSeq
		home    = a.getenv("HOME")
		appCtx  = a.ctx
		log     = a.log
		resolve = a.prs.resolve
		g       = a.git
	)

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(appCtx, pushResolveTimeout)
		defer cancel()

		msg := resolveCompose(ctx, g, log, resolve, id, cwd, home)
		msg.seq, msg.pane = seq, id

		return msg
	}
}

// resolveCompose reads the branch checked out in cwd, where it would be pushed
// and the GitHub repository that is, the remote's branches and the first pull
// request template.
func resolveCompose(ctx context.Context, g Git, log *slog.Logger, resolve func(ctx context.Context, cwd, branch string) (github.Repo, error),
	pane core.PaneID, cwd, home string,
) composeResolvedMsg {
	pt, state, err := resolvePush(ctx, g, log, pane, cwd, home)
	msg := composeResolvedMsg{state: state, err: err}

	switch state {
	case pushFailed:
		return msg
	case pushNotRepo, pushDetached, pushUnborn, pushNoRemote:
		msg.t.branch = pt.branch

		return msg
	case pushAsk, pushUpToDate, pushNothing:
	}

	t := composeTarget{
		root: pt.root, branch: pt.branch, oid: pt.oid, remote: pt.remote,
		unpushed: pt.commits, pane: pane, urls: pt.rawURLs,
	}

	msg.t = t

	switch t.repo, err = resolve(ctx, t.root, t.branch); {
	case errors.Is(err, github.ErrNotGitHub):
		msg.notGitHub = true

		return msg
	case errors.Is(err, git.ErrNoRemote):
		msg.state = pushNoRemote

		return msg
	case err != nil:
		msg.err = fmt.Errorf("resolve repository: %w", err)

		return msg
	}

	r, err := g.Open(ctx, t.root)
	if err != nil {
		msg.err = err

		return msg
	}

	if t.bases, err = g.RemoteBranches(ctx, r, t.remote); err != nil {
		msg.err = err

		return msg
	}

	// A repository without a usable default is one the poll names or none.
	if def, err := g.DefaultBranch(ctx, r); err == nil {
		msg.gitBase = strings.TrimPrefix(def, t.remote+"/")
	}

	tmpls, err := github.Templates(r.Root)
	if err != nil && log != nil {
		log.Warn("read pull request templates", "err", err)
	}

	if len(tmpls) > 0 {
		msg.tmpl = &tmpls[0]
	}

	msg.t = t

	return msg
}

// composeBlocked reports whether a modal other than the compose box is open.
func (a *App) composeBlocked() bool { return a.modalOpen() || a.compose != nil }

// onComposeResolved shows why there is no box, or goes on to read the commits
// the box starts from. An answer for an earlier request, a pane that lost
// focus, or under a modal is dropped.
func (a *App) onComposeResolved(msg composeResolvedMsg) tea.Cmd {
	if msg.seq != a.composeSeq || a.store.State().Tab().Focus != msg.pane || a.composeBlocked() {
		return nil
	}

	t := msg.t
	branch := printable(t.branch)

	switch {
	case errors.Is(msg.err, context.Canceled):
		return nil
	case errors.Is(msg.err, context.DeadlineExceeded):
		a.toast(levelError, "open a PR: timed out reading the repository")

		return nil
	case msg.err != nil:
		a.toast(levelError, "open a PR: "+gitReason(msg.err))

		return nil
	case msg.state == pushNotRepo:
		a.notice = noticeComposeNotRepo

		return nil
	case msg.state == pushDetached:
		a.notice = noticeComposeDetached

		return nil
	case msg.state == pushUnborn:
		a.notice = fmt.Sprintf("open a PR: %s has no commits yet", branch)

		return nil
	case msg.state == pushNoRemote:
		a.notice = fmt.Sprintf("open a PR: %s has no remote to push to", branch)

		return nil
	case msg.notGitHub:
		a.notice = fmt.Sprintf(noticePRNotGitHub, branch)

		return nil
	}

	st := a.store.State()
	key := t.repo.String()

	if pr, ok := st.PRFor(key, t.branch); ok && pr.State == prOpenState {
		a.notice = fmt.Sprintf("%s already has #%d", branch, pr.Number)

		return nil
	}

	def := st.PRs[key].DefaultBranch
	if def == "" {
		def = msg.gitBase
	}

	bases := t.bases
	if def != "" && !slices.Contains(bases, def) {
		bases = append([]string{def}, bases...)
	}

	if len(bases) == 0 {
		a.notice = fmt.Sprintf("open a PR: %s has no branches to open against", printable(t.remote))

		return nil
	}

	t.bases = bases

	v := &composeView{t: t}
	if i := slices.Index(bases, def); i >= 0 {
		v.base = i
	}

	v.title = newComposeTitle()

	return a.composeLog(v, msg.seq, 0, true, msg.tmpl)
}

// newComposeTitle returns the title field. The box places the terminal's cursor.
func newComposeTitle() textinput.Model {
	in := textinput.New()
	in.SetVirtualCursor(false)
	in.KeyMap.Paste.SetEnabled(false)
	in.CharLimit = maxComposeTitle
	in.Focus()

	return in
}

// composeLog reads, off the update loop, the commits the base of v puts in
// the pull request. open is v while it is being opened, else nil.
func (a *App) composeLog(v *composeView, seq, logSeq int, opening bool, tmpl *github.Template) tea.Cmd {
	var (
		appCtx = a.ctx
		t      = v.t
		base   = t.remote + "/" + t.bases[v.base]
		open   *composeView
	)

	if opening {
		open = v
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(appCtx, pushResolveTimeout)
		defer cancel()

		msg := composeLogMsg{seq: seq, logSeq: logSeq, open: open, box: v, tmpl: tmpl}

		r, err := a.git.Open(ctx, t.root)
		if err != nil {
			msg.err = err

			return msg
		}

		// A base the remote-tracking ref of which is gone has no commits to list.
		cs, err := a.git.Log(ctx, r, base)
		if err != nil {
			if !errors.Is(err, errNoBaseRef) {
				msg.err = err
			}

			return msg
		}

		for _, c := range cs {
			msg.commits = append(msg.commits, github.Commit{Subject: c.Subject, Body: c.Body})
		}

		return msg
	}
}

// onComposeLog opens the box with the commits it starts from, or recounts a
// box already open. An answer for an earlier request is dropped.
func (a *App) onComposeLog(msg composeLogMsg) {
	if errors.Is(msg.err, context.Canceled) {
		return
	}

	if msg.open == nil {
		if v := a.compose; v != nil && v == msg.box && msg.logSeq == v.logSeq {
			if msg.err != nil {
				v.status, v.statusErr = "commits: "+gitReason(msg.err), true
			} else {
				v.commits = len(msg.commits)
			}
		}

		return
	}

	v := msg.open
	if msg.seq != a.composeSeq || a.store.State().Tab().Focus != v.t.pane || a.composeBlocked() {
		return
	}

	template := ""
	v.source = "the commits"

	if msg.tmpl != nil && strings.TrimSpace(msg.tmpl.Body) != "" {
		template, v.source = msg.tmpl.Body, msg.tmpl.Path
	}

	d := github.Fill(v.t.branch, msg.commits, template)

	v.title.SetValue(d.Title)
	v.title.CursorEnd()
	v.body = newTextArea(d.Body, maxComposeBody)
	v.body.Start()
	v.bodyRev = 1
	v.commits = len(msg.commits)

	if msg.err != nil {
		v.status, v.statusErr = "commits: "+gitReason(msg.err), true
	}

	a.endLeader()
	a.compose = v
}

// closeCompose closes the box.
func (a *App) closeCompose() { a.compose = nil }

// composeKeys are the default keys of the compose box. [keys.compose] is laid over them.
var composeKeys = []reviewKey{
	{"tab", "compose.field.next"},
	{"shift+tab", "compose.field.prev"},
	{"ctrl+s", "compose.create"},
	{"ctrl+e", "compose.editor"},
	{"ctrl+p", "compose.preview"},
	{"esc", "compose.cancel"},
}

func composeCommands() []command {
	cmd := func(id, title string, run func(a *App, v *composeView) tea.Cmd) command {
		return command{id: id, title: "Compose: " + title, run: func(a *App) tea.Cmd {
			if !a.composeShown() || a.compose.busy {
				return nil
			}

			return run(a, a.compose)
		}}
	}

	return scoped("compose", []command{
		cmd("compose.field.next", "next field", func(_ *App, v *composeView) tea.Cmd {
			v.focus((v.field + 1) % (composeBody + 1))

			return nil
		}),
		cmd("compose.field.prev", "previous field", func(_ *App, v *composeView) tea.Cmd {
			v.focus((v.field + composeBody) % (composeBody + 1))

			return nil
		}),
		cmd("compose.create", "create the pull request", func(a *App, v *composeView) tea.Cmd { return a.composeSubmit(v) }),
		cmd("compose.editor", "body in the editor", func(a *App, _ *composeView) tea.Cmd { return a.composeEditor() }),
		cmd("compose.preview", "preview or write", func(a *App, v *composeView) tea.Cmd { return a.composePreviewToggle(v) }),
		cmd("compose.cancel", "cancel", func(a *App, _ *composeView) tea.Cmd {
			a.closeCompose()

			return nil
		}),
	})
}

// handleComposeKey answers the open compose box. While a run is in flight every
// key but the leader is dropped.
func (a *App) handleComposeKey(msg tea.KeyPressMsg) tea.Cmd {
	v := a.compose

	if v.busy {
		cmd, _ := a.runLeader(msg)

		return cmd
	}

	v.status, v.statusErr = "", false

	if key := bindingKey(msg); a.keys.compose.has(key) {
		return a.runKey(a.keys.compose, key)
	}

	switch v.field {
	case composeTitle:
		var cmd tea.Cmd

		v.title, cmd = v.title.Update(msg)

		return cmd
	case composeBase:
		switch msg.String() {
		case "left":
			return a.composeStep(v, -1)
		case "right":
			return a.composeStep(v, 1)
		}
	case composeDraft:
		if msg.String() == "space" {
			v.draft = !v.draft
		}
	case composeBody:
		if v.preview.on {
			a.composePreviewKey(v, msg.String())

			return nil
		}

		n := v.body.size()
		v.body.Key(msg)

		if v.body.size() != n {
			return a.bodyChanged(v)
		}
	}

	return nil
}

// focus gives f the keyboard; only the title shows the input's cursor.
func (v *composeView) focus(f composeField) {
	v.field = f

	if f == composeTitle {
		v.title.Focus()
	} else {
		v.title.Blur()
	}
}

// composeStep selects the base delta away, wrapping, and recounts its commits.
func (a *App) composeStep(v *composeView, delta int) tea.Cmd {
	n := len(v.t.bases)
	v.base = ((v.base+delta)%n + n) % n
	v.logSeq++

	return a.composeLog(v, a.composeSeq, v.logSeq, false, nil)
}

// handleComposePaste puts pasted text in the field that has the keyboard.
func (a *App) handleComposePaste(msg tea.PasteMsg) tea.Cmd {
	v := a.compose

	switch {
	case v.busy:
	case v.field == composeTitle:
		var cmd tea.Cmd

		v.title, cmd = v.title.Update(msg)

		return cmd
	case v.field == composeBody && !v.preview.on:
		n := v.body.size()
		v.body.Paste(msg.Content)

		if v.body.size() != n {
			return a.bodyChanged(v)
		}
	}

	return nil
}

// composeSubmit freezes the box and checks, pushes and creates off the update
// loop. The worktree must still be on the branch and head the box opened on.
func (a *App) composeSubmit(v *composeView) tea.Cmd {
	title := strings.TrimSpace(v.title.Value())
	if title == "" {
		v.status, v.statusErr = statusComposeTitle, true

		return nil
	}

	t := v.t
	if busy, ok := a.pushing[t.root]; ok && t.unpushed > 0 {
		v.status, v.statusErr = fmt.Sprintf(statusComposePushBusy, printable(busy)), true

		return nil
	}

	pr := github.NewPR{Title: title, Body: v.body.Value(), Base: t.bases[v.base], Head: t.branch, Draft: v.draft}
	v.busy = true

	v.status = statusComposeOpening
	if t.unpushed > 0 {
		v.status = fmt.Sprintf("pushing %s to %s…", printable(t.branch), printable(t.remote))

		if a.pushing == nil {
			a.pushing = map[string]string{}
		}

		a.pushing[t.root] = t.branch
	}

	appCtx, g := a.ctx, a.git

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(appCtx, pushTimeout)
		defer cancel()

		return composePushedMsg{v: v, pr: pr, err: composePush(ctx, g, t)}
	}
}

// composePush checks that the worktree is still on the branch and head t names
// and pushes the branch when the remote lacks commits. It never forces.
func composePush(ctx context.Context, g Git, t composeTarget) error {
	if t.unpushed > 0 {
		_, err := runPush(ctx, g, pushTarget{root: t.root, branch: t.branch, oid: t.oid, remote: t.remote, rawURLs: t.urls})

		return err
	}

	r, err := g.Open(ctx, t.root)
	if err != nil {
		return err
	}

	h, err := g.Head(ctx, r)
	if err != nil {
		return err
	}

	if h.Detached || h.Branch != t.branch || h.OID != t.oid {
		return errPushChanged
	}

	return nil
}

// fail shows why a run stopped and gives the keys back.
func (v *composeView) fail(err error) {
	branch := printable(v.t.branch)
	v.busy, v.statusErr = false, true

	switch {
	case errors.Is(err, errPushChanged):
		v.status = fmt.Sprintf(statusComposeChanged, branch)
	case errors.Is(err, errPushURLChanged):
		v.status = fmt.Sprintf(statusComposeURL, branch)
	case errors.Is(err, github.ErrPRExists):
		v.status = fmt.Sprintf(statusComposeExists, branch)
	case errors.Is(err, context.DeadlineExceeded):
		v.status = "timed out"
	default:
		v.status = gitReason(err)
	}
}

// onComposePushed goes on to create the pull request once the branch is on the
// remote. A run cut short by the app closing says nothing.
func (a *App) onComposePushed(msg composePushedMsg) tea.Cmd {
	v := msg.v

	if v.t.unpushed > 0 {
		delete(a.pushing, v.t.root)
	}

	switch {
	case errors.Is(msg.err, context.Canceled):
		return nil
	case a.compose != v:
		return nil
	case msg.err != nil:
		v.fail(msg.err)

		return nil
	}

	var probe tea.Cmd

	if v.t.unpushed > 0 {
		v.t.unpushed = 0

		if _, ok := a.store.State().Panes[v.t.pane]; ok {
			a.railEntry(v.t.pane).head.force = true
			probe = a.railFire(a.store.State(), v.t.pane, probeHead)
		}
	}

	v.status = statusComposeOpening

	var (
		appCtx  = a.ctx
		actions = a.actions
		repo    = v.t.repo
		pr      = msg.pr
	)

	create := func() tea.Msg {
		ctx, cancel := context.WithTimeout(appCtx, createTimeout)
		defer cancel()

		res, err := actions.CreatePR(ctx, repo, pr)

		return composeCreatedMsg{v: v, pr: res, err: err}
	}

	return tea.Batch(probe, create)
}

// onComposeCreated closes the box on success and asks the poller for the
// repository; a failure stays in the box.
func (a *App) onComposeCreated(msg composeCreatedMsg) {
	v := msg.v

	switch {
	case errors.Is(msg.err, context.Canceled), a.compose != v:
		return
	case msg.err != nil:
		v.fail(msg.err)

		return
	}

	a.closeCompose()
	a.toast(levelSuccess, fmt.Sprintf("opened #%d", msg.pr.Number))

	if a.prs.src != nil {
		a.prs.src.Refresh(v.t.repo)
	}
}

// bodyWrittenMsg answers writing the body to its temp file.
type bodyWrittenMsg struct {
	v    *composeView
	file string
	err  error
}

// bodyReadMsg answers reading the edited body back.
type bodyReadMsg struct {
	v    *composeView
	text string
	err  error
}

// composeEditor is ctrl+e: it writes the body to a temp file in the run dir,
// off the update loop, then opens a pane running only the editor on it.
func (a *App) composeEditor() tea.Cmd {
	v := a.compose

	if a.runDir == "" {
		a.notice = noticeEditorNoRunDir

		return nil
	}

	v.editing = editPending

	var (
		dir    = a.runDir
		body   = v.body.Value()
		appCtx = a.ctx
		files  = &a.bodyFiles
	)

	return func() tea.Msg {
		file, err := writeBodyFile(dir, body)

		// An app that closed meanwhile never sees the file.
		if err == nil && (appCtx.Err() != nil || !files.add(file)) {
			_ = os.Remove(file)
		}

		return bodyWrittenMsg{v: v, file: file, err: err}
	}
}

// writeBodyFile writes body to a new 0600 file in dir.
func writeBodyFile(dir, body string) (string, error) {
	f, err := os.CreateTemp(dir, "pr-body-*.md")
	if err != nil {
		return "", fmt.Errorf("create the body file: %w", err)
	}

	_, werr := f.WriteString(body)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}

	if werr != nil {
		_ = os.Remove(f.Name())

		return "", fmt.Errorf("write the body file: %w", werr)
	}

	return f.Name(), nil
}

// onBodyWritten opens the editor pane beside the focused pane, or brings the
// box back when it cannot.
func (a *App) onBodyWritten(msg bodyWrittenMsg) tea.Cmd {
	v := msg.v

	a.bodyFiles.remove(msg.file)

	fail := func(reason string) {
		if msg.file != "" {
			a.closing.Go(func() { _ = os.Remove(msg.file) })
		}

		if a.compose == v {
			v.editing = ""
			v.status, v.statusErr = reason, true
		}
	}

	switch {
	case msg.err != nil:
		fail(gitReason(msg.err))

		return nil
	case a.compose != v || v.editing != editPending:
		fail("")

		return nil
	}

	st := a.store.State()
	anchor := st.Tab().Focus
	id := st.NewPaneID()
	run := &paneRun{program: reviewEditor(a.getenv), args: []string{msg.file}, once: true}

	cmd, opened := a.openPaneAt(core.KindShell, core.SplitVertical, anchor, v.t.root, run)
	if !opened {
		fail(cmp.Or(a.notice, statusEditorNoPane))

		return nil
	}

	v.editing = id

	if a.editing == nil {
		a.editing = map[core.PaneID]*bodyEdit{}
	}

	a.editing[id] = &bodyEdit{file: msg.file, v: v}

	return cmd
}

// composeEdited hands the file of editor pane id back: off the update loop it
// reads at most 64 KiB and removes the file. It is nil for any other pane.
func (a *App) composeEdited(id core.PaneID) tea.Cmd {
	e, ok := a.editing[id]
	if !ok {
		return nil
	}

	delete(a.editing, id)

	return func() tea.Msg {
		text, err := readBodyFile(e.file)

		return bodyReadMsg{v: e.v, text: text, err: err}
	}
}

// readBodyFile reads at most maxEditedBody bytes of file and removes it.
func readBodyFile(file string) (string, error) {
	defer func() { _ = os.Remove(file) }()

	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()

	b, err := io.ReadAll(io.LimitReader(f, maxEditedBody))
	if err != nil {
		return "", err
	}

	if len(b) == maxEditedBody {
		b = dropPartialRune(b)
	}

	return string(b), nil
}

// onBodyRead puts the edited text in the body and shows the box again on it. A
// box closed meanwhile is left alone.
func (a *App) onBodyRead(msg bodyReadMsg) tea.Cmd {
	v := msg.v
	if a.compose != v {
		return nil
	}

	v.editing = ""
	v.focus(composeBody)

	if msg.err != nil {
		v.status, v.statusErr = statusEditorRead+gitReason(msg.err), true

		return nil
	}

	v.body = newTextArea(msg.text, maxComposeBody)
	v.body.Start()
	v.preview.top, v.preview.anchor = 0, 1

	cmd := a.bodyChanged(v)
	a.composePreviewSettle(v)

	if v.preview.on {
		return tea.Batch(cmd, a.renderBody(v))
	}

	return cmd
}

// bodyFileSet is the editor files written by a command and not yet handed to
// the update loop, so Close can remove one that is still on its way.
type bodyFileSet struct {
	mu     sync.Mutex
	closed bool
	files  map[string]struct{}
}

// add records file, or reports false when the set already closed.
func (s *bodyFileSet) add(file string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return false
	}

	if s.files == nil {
		s.files = map[string]struct{}{}
	}

	s.files[file] = struct{}{}

	return true
}

func (s *bodyFileSet) remove(file string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.files, file)
}

// closeAll deletes the recorded files and refuses later ones.
func (s *bodyFileSet) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.closed = true

	for f := range s.files {
		_ = os.Remove(f)
	}

	clear(s.files)
}

// removeEditFiles deletes the temp files of editors still open.
func (a *App) removeEditFiles() {
	for id, e := range a.editing {
		_ = os.Remove(e.file)

		delete(a.editing, id)
	}
}

// dropPartialRune cuts a rune the size limit split off the end of b.
func dropPartialRune(b []byte) []byte {
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if !utf8.FullRune(b[i:]) {
				return b[:i]
			}

			break
		}
	}

	return b
}
