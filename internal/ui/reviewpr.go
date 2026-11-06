package ui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/github"
)

const (
	// prShowTimeout bounds finding a pull request's place and number.
	prShowTimeout = 15 * time.Second
	// prFetchTimeout bounds fetching its refs, which talks to the remote.
	prFetchTimeout = 60 * time.Second
)

// The PR mode's notices.
const (
	noticePRsOff        = "pull requests are off"
	noticePRNone        = "no pull request for %s"
	noticePRNotGitHub   = "%s's remote is not on GitHub"
	noticePRDetached    = "pull request: HEAD is detached"
	noticePRNoRemote    = "pull request: %s has no remote"
	noticePRNoBase      = "#%d: its base branch is unknown"
	noticePRFetching    = "fetching #%d…"
	noticePRNotOnRemote = "#%d is not on %s"
	noticePRLoaded      = "#%d loaded"
	noticePRsFailed     = "pull requests for %s: %s"
	noticePRGone        = "#%d is no longer open"
)

// prThen is what the drawer does once the shown pull request's diff has loaded.
type prThen uint8

// What a key asks for besides showing the pull request.
const (
	thenNothing prThen = iota
	thenConversation
	thenChecks
	thenBrowser
	thenApprove
	thenMerge
	thenReady // the place's pull request is enough: nothing is fetched or loaded
)

// prWant is a pull request already known, as the list knows it: its repository
// keyed "host/owner/name", its number and the worktree it is shown from.
type prWant struct {
	repo   string
	number int
	root   string
	base   string // the base branch when the caller knows it, else the state's

	// inCopy reads the pull request from the private copy at root, whose HEAD
	// means nothing; branch is its head branch, for the drawer's title.
	inCopy bool
	branch string
}

// prPlace is the worktree a pull request is read from: its root, branch, the
// remote the branch pushes to and the GitHub repository that remote is.
type prPlace struct {
	root, branch, remote string
	repo                 github.Repo
}

// The ways a place has no pull request; each has its own notice.
var (
	errPRNoRepo    = errors.New("pull request: not a git repository")
	errPRDetached  = errors.New("pull request: HEAD is detached")
	errPRNoRemote  = errors.New("pull request: the branch has no remote")
	errPRNotGitHub = errors.New("pull request: the remote is not on GitHub")
	errPRNone      = errors.New("pull request: the branch has none")
)

// prNext is a drawer being opened for a pull request: showPR runs once the
// drawer is open and has its source.
type prNext struct {
	tab  core.TabID
	pane core.PaneID
	want *prWant
	then prThen
	open bool // the drawer opened; the source is awaited
	// noRepo: the pane's repository answer failed or found none, so no source is coming and the ask goes at once.
	noRepo bool
}

// prAsk is what every answer of one request carries, so a request that was
// overtaken is told from the one in force.
type prAsk struct {
	tab   core.TabID
	pane  core.PaneID
	seq   int // drawerState.prSeq when asked
	epoch int // the view's epoch when asked
	then  prThen
	want  *prWant
	base  string
}

// prPlaceMsg answers finding the place, number and base of a pull request.
type prPlaceMsg struct {
	prAsk
	place  prPlace
	number int
	base   string // the pull request's base branch, when the lookup found it
	err    error
}

// prFetchedMsg answers the fetch of a pull request's refs.
type prFetchedMsg struct {
	prAsk
	place  prPlace
	number int
	// unpushed is the commits the worktree has that its remote lacks; 0 when
	// they could not be counted, which only leaves the header's line out.
	unpushed int
	err      error
}

// localPRPlace reads the root, branch and push remote of the worktree at dir.
// The repository is left empty.
func localPRPlace(ctx context.Context, g Git, dir string) (prPlace, error) {
	r, err := g.Open(ctx, dir)
	if err != nil {
		if errors.Is(err, git.ErrNotRepo) {
			return prPlace{}, errPRNoRepo
		}

		return prPlace{}, fmt.Errorf("pull request: %w", err)
	}

	h, err := g.Head(ctx, r)
	if err != nil {
		return prPlace{}, fmt.Errorf("pull request: %w", err)
	}

	place := prPlace{root: r.Root, branch: h.Branch}
	if h.Detached {
		return place, errPRDetached
	}

	place.remote, err = g.PushRemote(ctx, r, h.Branch)

	switch {
	case errors.Is(err, git.ErrNoRemote):
		return place, errPRNoRemote
	case err != nil:
		return place, fmt.Errorf("pull request: %w", err)
	}

	return place, nil
}

// resolvePRPlace finds the worktree at dir, its branch, push remote and the
// GitHub repository the remote is.
func resolvePRPlace(ctx context.Context, g Git, resolve func(ctx context.Context, cwd, branch string) (github.Repo, error), dir string) (prPlace, error) {
	place, err := localPRPlace(ctx, g, dir)
	if err != nil {
		return place, err
	}

	place.repo, err = resolve(ctx, place.root, place.branch)

	switch {
	case errors.Is(err, github.ErrNotGitHub):
		return place, errPRNotGitHub
	case errors.Is(err, git.ErrNoRemote):
		return place, errPRNoRemote
	case err != nil:
		return place, fmt.Errorf("pull request: %w", err)
	}

	return place, nil
}

// repoOfKey reads a repository keyed "host/owner/name".
func repoOfKey(key string) (github.Repo, bool) {
	parts := strings.Split(key, "/")
	if len(parts) != 3 || slices.Contains(parts, "") {
		return github.Repo{}, false
	}

	return github.Repo{Host: parts[0], Owner: parts[1], Name: parts[2]}, true
}

// prInfoByNumber finds a pull request the poll lists or a lookup found. A
// merged one a lookup holds outranks the poll's open entry, since a merge never
// reverts and the poll may still list it for a moment.
func prInfoByNumber(st core.State, repo string, number int) (core.PRInfo, bool) {
	r := st.PRs[repo]

	for _, p := range r.Branch {
		if p.Number == number && p.State == prMergedState {
			return p, true
		}
	}

	for _, p := range r.Open {
		if p.Number == number {
			return p, true
		}
	}

	for _, p := range r.Branch {
		if p.Number == number {
			return p, true
		}
	}

	return core.PRInfo{}, false
}

// reviewPR is `p`: it shows the pull request of the drawer's place.
func (a *App) reviewPR() tea.Cmd {
	tab, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	a.dropPages(v)

	return a.showPR(tab.ID, thenNothing)
}

// reviewReload is `r`: with a markdown page shown it reads the page's file
// again; in PR mode it fetches the pull request again and asks
// the poller for its repository; otherwise it loads the diff again. Without a
// page it also drops a pull request action still waiting on a load.
func (a *App) reviewReload() tea.Cmd {
	tab, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	if v.page() != nil {
		return a.reloadPage(tab.ID, v, true)
	}

	a.retryPictures(v)

	v.pollThen = thenNothing

	// A drawer still opening for a pull request shows it but no longer acts on it.
	if n := a.drawer.prNext; n != nil {
		n.then = thenNothing
	}

	if v.pr == (core.ReviewPR{}) {
		// r overtakes a pending pull request ask, as a newer ask does.
		a.drawer.prSeq++

		return a.refreshViews(tab.Review.Pane, nil)
	}

	if repo, ok := repoOfKey(v.pr.Repo); ok && a.prs.src != nil {
		a.prs.src.Refresh(repo)
	}

	return a.askPR(tab.ID, v.heldWant(v.pr.Root), v.prBase, thenNothing)
}

// retryPictures forgets the failed images of the rendered files v shows, so
// they load again.
func (a *App) retryPictures(v *reviewView) {
	for path := range v.mdOn {
		if f := v.mdFiles[path]; f != nil {
			a.pictures.retry(docPictures{reg: a.pictures, root: v.root}, f.doc.layout(max(v.mdW, 1), v.mdIcons))
		}
	}
}

// showPR shows the pull request of the active tab's drawer place in its drawer
// and runs then once its diff has loaded.
func (a *App) showPR(tab core.TabID, then prThen) tea.Cmd {
	return a.askPR(tab, nil, "", then)
}

// askPR is showPR with the base branch given when the caller knows it.
func (a *App) askPR(tab core.TabID, want *prWant, base string, then prThen) tea.Cmd {
	st := a.store.State()

	t := st.Tab()
	if t.ID != tab || t.Review == nil {
		a.notice = noticeNoDrawer

		return nil
	}

	if a.prs.src == nil || a.prs.resolve == nil {
		a.notice = noticePRsOff

		return nil
	}

	dir := a.drawerDir(st, t, t.Review.Pane)

	// A copy's HEAD means nothing, so a pull request it shows is asked for as itself.
	if want == nil && t.Review.PR != nil && t.Review.PR.Copy {
		want = a.reviewViewOf(tab).heldWant(t.Review.PR.Root)
	}

	if want != nil && want.inCopy && a.reads == nil {
		a.notice = noticePRsOff

		return nil
	}

	switch {
	case want != nil:
		dir = want.root

		if base == "" {
			base = want.base
		}

		if base == "" {
			if info, ok := prInfoByNumber(st, want.repo, want.number); ok {
				base = info.BaseBranch
			}
		}

		if base == "" {
			a.notice = fmt.Sprintf(noticePRNoBase, want.number)

			return nil
		}
	case t.Review.PR != nil:
		dir = t.Review.PR.Root
	}

	if dir == "" {
		a.notice = noticeNotRepo

		return nil
	}

	v := a.reviewViewOf(tab)
	v.then, v.pollThen = thenNothing, thenNothing

	a.drawer.prSeq++
	ask := prAsk{tab: tab, pane: t.Review.Pane, seq: a.drawer.prSeq, epoch: v.epoch, then: then, want: want, base: base}

	var (
		resolve, lookup, appCtx = a.prs.resolve, a.prs.lookup, a.ctx
		g                       = a.git
		number                  int
	)

	if want != nil {
		number = want.number
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(appCtx, prShowTimeout)
		defer cancel()

		msg := prPlaceMsg{prAsk: ask, number: number}

		if want != nil && want.inCopy {
			if repo, ok := repoOfKey(want.repo); ok {
				msg.place = prPlace{root: want.root, remote: "origin", repo: repo}
			} else {
				msg.err = errPRNone
			}

			return msg
		}

		if want != nil {
			msg.place, msg.err = localPRPlace(ctx, g, dir)
			if repo, ok := repoOfKey(want.repo); ok && msg.err == nil {
				msg.place.repo = repo
			}

			return msg
		}

		if msg.place, msg.err = resolvePRPlace(ctx, g, resolve, dir); msg.err != nil {
			return msg
		}

		msg.number, msg.base, msg.err = prOfPlace(ctx, st, lookup, msg.place)

		return msg
	}
}

// prOfPlace is the number and base of the pull request of place's branch: the
// poll's, else one lookup's.
func prOfPlace(ctx context.Context, st core.State, lookup PRLookup, place prPlace) (int, string, error) {
	if info, ok := st.PRFor(place.repo.String(), place.branch); ok {
		return info.Number, info.BaseBranch, nil
	}

	if lookup == nil {
		return 0, "", errPRNone
	}

	pr, err := lookup(ctx, place.repo, place.branch)

	switch {
	case errors.Is(err, github.ErrNoPR):
		return 0, "", errPRNone
	case err != nil:
		return 0, "", fmt.Errorf("pull request: %w", err)
	}

	return pr.Number, pr.BaseBranch, nil
}

// prLive reports whether an answer still belongs to the request in force: the
// drawer it was asked for is still the active tab's, on the same source, and no
// newer request was made.
func (a *App) prLive(ask prAsk) bool {
	v := a.drawer.views[ask.tab]
	if v == nil || ask.seq != a.drawer.prSeq || ask.epoch != v.epoch {
		return false
	}

	t := a.store.State().Tab()

	return t.ID == ask.tab && t.Review != nil && t.Review.Pane == ask.pane
}

// onPRPlace starts the fetch once the pull request's place and number are
// known, or says why there is none.
func (a *App) onPRPlace(msg prPlaceMsg) tea.Cmd {
	if !a.prLive(msg.prAsk) {
		return nil
	}

	branch := printable(msg.place.branch)

	switch err := msg.err; {
	case err == nil:
	case errors.Is(err, context.Canceled):
		return nil
	case errors.Is(err, errPRNoRepo):
		a.notice = noticeNotRepo

		return nil
	case errors.Is(err, errPRDetached):
		a.notice = noticePRDetached

		return nil
	case errors.Is(err, errPRNoRemote):
		a.notice = fmt.Sprintf(noticePRNoRemote, branch)

		return nil
	case errors.Is(err, errPRNotGitHub):
		a.notice = fmt.Sprintf(noticePRNotGitHub, branch)

		return nil
	case errors.Is(err, errPRNone):
		a.notice = fmt.Sprintf(noticePRNone, branch)

		return nil
	case errors.Is(err, context.DeadlineExceeded):
		a.toast(levelError, "pull request: timed out")

		return nil
	default:
		a.toast(levelError, gitReason(err))

		return nil
	}

	if msg.then == thenReady {
		a.readyAtPlace(msg)

		return nil
	}

	base := msg.base
	if base == "" {
		base = msg.prAsk.base
	}

	if base == "" {
		a.notice = fmt.Sprintf(noticePRNoBase, msg.number)

		return nil
	}

	a.notice = fmt.Sprintf(noticePRFetching, msg.number)

	var (
		ask    = msg.prAsk
		place  = msg.place
		number = msg.number
		appCtx = a.ctx
		log    = a.log
	)

	ask.base = base

	if a.drawer.fetching == nil {
		a.drawer.fetching = map[int]int{}
	}

	a.drawer.fetching[number]++

	if ask.want != nil && ask.want.inCopy {
		return a.fetchCopyCmd(ask, place, number)
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(appCtx, prFetchTimeout)
		defer cancel()

		out := prFetchedMsg{prAsk: ask, place: place, number: number}

		r, err := a.git.Open(ctx, place.root)
		if err == nil {
			_, err = a.git.FetchPR(ctx, r, place.remote, number, base)
		}

		if err == nil {
			var uerr error

			out.unpushed, uerr = a.git.Unpushed(ctx, r, place.remote)
			if uerr != nil {
				log.Debug("unpushed count dropped", "err", uerr)
			}
		}

		out.err = err

		return out
	}
}

// onPRFetched puts the drawer in PR mode on the fetched pull request, or says
// why it cannot.
func (a *App) onPRFetched(msg prFetchedMsg) tea.Cmd {
	if n := a.drawer.fetching[msg.number]; n > 1 {
		a.drawer.fetching[msg.number] = n - 1
	} else {
		delete(a.drawer.fetching, msg.number)
	}

	if !a.prLive(msg.prAsk) {
		return nil
	}

	switch err := msg.err; {
	case err == nil:
	case errors.Is(err, context.Canceled):
		return nil
	case errors.Is(err, git.ErrNoPR):
		a.notice = fmt.Sprintf(noticePRNotOnRemote, msg.number, printable(msg.place.remote))

		return nil
	case errors.Is(err, context.DeadlineExceeded):
		a.toast(levelError, fmt.Sprintf("fetch #%d: timed out", msg.number))

		return nil
	default:
		a.toast(levelError, fmt.Sprintf("fetch #%d: %s", msg.number, gitReason(err)))

		return nil
	}

	if a.notice == fmt.Sprintf(noticePRFetching, msg.number) {
		a.notice = ""
	}

	v := a.drawer.views[msg.tab]
	epoch := v.epoch
	v.prBase, v.unpushed = msg.base, msg.unpushed

	inCopy := msg.want != nil && msg.want.inCopy

	v.prBranch = ""
	if inCopy {
		v.prBranch = msg.want.branch
	}

	show := core.ShowReviewPR{Repo: msg.place.repo.String(), Number: msg.number, Root: msg.place.root, Copy: inCopy}
	if err := a.dispatch(show); err != nil {
		return nil
	}

	// The sync loads a new source at once, so the view's `then` is set after it.
	// The same pull request shown again is not a new source: it is loaded here.
	cmd := a.reviewSync()

	nv := a.drawer.views[msg.tab]
	if nv == nil {
		return cmd
	}

	if nv == v && nv.epoch == epoch {
		cmd = tea.Batch(cmd, a.reviewRetarget(msg.tab, nv, 0))
	}

	nv.then = msg.then

	return cmd
}

// reviewThen runs what was asked of the pull request when its diff has landed.
// A load that failed runs nothing.
func (a *App) reviewThen(v *reviewView, msg reviewLoadedMsg) tea.Cmd {
	then := v.then
	if then == thenNothing || v.pr == (core.ReviewPR{}) || msg.live {
		return nil
	}

	v.then = thenNothing

	if msg.err != nil {
		return nil
	}

	// A pull request loaded under a tab that is not shown is not acted on.
	if a.drawer.views[a.store.State().Tab().ID] != v {
		return nil
	}

	// A late answer never takes the keyboard from where it went since the key.
	if a.modalOpen() || !a.drawerHasKeys() {
		a.notice = a.prLoadedNotice(v.pr.Number, then)

		return nil
	}

	return a.prThenRun(then)
}

// prLoadedNotice is the notice of a dropped answer: it names the drawer key
// that asks for then as bound, and only the number when the command has no key.
func (a *App) prLoadedNotice(number int, then prThen) string {
	id, what := "", ""

	switch then {
	case thenConversation:
		id, what = "review.conversation", "for the conversation"
	case thenChecks:
		id, what = "review.checks", "for checks"
	case thenBrowser:
		id, what = "review.browser", "to open it"
	case thenApprove:
		id, what = "review.approve", "to approve"
	case thenMerge:
		id, what = "review.merge", "to merge"
	case thenReady:
		id, what = "review.ready", "to mark it ready"
	case thenNothing:
	}

	key, ok := a.keys.drawer.first(id)
	if !ok {
		return fmt.Sprintf(noticePRLoaded, number)
	}

	return fmt.Sprintf(noticePRLoaded, number) + "; press " + key + " " + what
}

// prThenRun is what a key asked of the pull request once its diff has loaded.
func (a *App) prThenRun(then prThen) tea.Cmd {
	_, v, ok := a.reviewActive()
	if !ok || v.pr == (core.ReviewPR{}) {
		return nil
	}

	if a.prPollHolds(v, then) {
		return nil
	}

	switch then {
	case thenConversation:
		info, _ := prInfoByNumber(a.store.State(), v.pr.Repo, v.pr.Number)

		return a.openConversation(v.pr.Repo, v.pr.Number, info)
	case thenChecks:
		return a.openChecks(v.pr.Repo, v.pr.Number)
	case thenBrowser:
		return a.openURL(a.prLink(v.pr))
	case thenApprove:
		a.openApprove(v.pr.Repo, v.pr.Number, v.res.Head)
	case thenMerge:
		a.openMerge(v.pr.Repo, v.pr.Number, v.res.Head, v.pr.Root)
	case thenReady, thenNothing:
	}

	return nil
}

// prPollHolds holds an approve or merge of a copy's pull request until its
// repository's poll knows it: the box freezes what it knows when it opens, and a
// copy's repository is watched only once the drawer shows it. It reports whether
// then was held or refused, so the caller runs nothing.
func (a *App) prPollHolds(v *reviewView, then prThen) bool {
	if !v.pr.Copy || (then != thenApprove && then != thenMerge) || a.prs.src == nil {
		return false
	}

	repo, ok := repoOfKey(v.pr.Repo)
	if !ok {
		return false
	}

	st := a.store.State()

	if _, known := prInfoByNumber(st, v.pr.Repo, v.pr.Number); known && a.prs.polled[repo] {
		return false
	}

	if reason := st.PRs[v.pr.Repo].Err; reason != "" {
		a.notice = fmt.Sprintf(noticePRsFailed, v.pr.Repo, reason)

		return true
	}

	v.pollThen = then
	a.notice = fmt.Sprintf(noticePRFetching, v.pr.Number)
	a.prs.src.Refresh(repo)

	return true
}

// prPollAnswered ends the holds of s's repository that the poll answered: a
// failed poll says why, an answer for another pull request leaves the hold, and
// one that knows the pull request runs what was held.
func (a *App) prPollAnswered(s github.Snapshot) tea.Cmd {
	var (
		cmds   []tea.Cmd
		repo   = s.Repo.String()
		active = a.drawer.views[a.store.State().Tab().ID]
	)

	for _, v := range a.drawer.views {
		then := v.pollThen
		if then == thenNothing || v.pr.Repo != repo {
			continue
		}

		switch {
		case s.Err != nil:
			v.pollThen = thenNothing

			if v == active {
				a.notice = fmt.Sprintf(noticePRsFailed, repo, prReason(s.Err))
			}
		case s.Asked != v.pr.Number:
		case v != active:
			v.pollThen = thenNothing
		default:
			v.pollThen = thenNothing

			if a.notice == fmt.Sprintf(noticePRFetching, v.pr.Number) {
				a.notice = ""
			}

			cmds = append(cmds, a.prPollRun(v, s, then))
		}
	}

	return tea.Batch(cmds...)
}

// prPollRun runs the held then once the poll answered for v's pull request, or
// says why it cannot.
func (a *App) prPollRun(v *reviewView, s github.Snapshot, then prThen) tea.Cmd {
	if _, known := prInfoByNumber(a.store.State(), v.pr.Repo, v.pr.Number); !known {
		if f := s.Focused; f != nil && f.Number == v.pr.Number && f.Err != nil {
			a.notice = fmt.Sprintf(noticePRsFailed, v.pr.Repo, prReason(f.Err))
		} else {
			a.notice = fmt.Sprintf(noticePRGone, v.pr.Number)
		}

		return nil
	}

	if a.modalOpen() || !a.drawerHasKeys() {
		a.notice = a.prLoadedNotice(v.pr.Number, then)

		return nil
	}

	return a.prThenRun(then)
}

// prLink is the page of a pull request: the poll's, else the one its repository
// and number make.
func (a *App) prLink(pr core.ReviewPR) string {
	if info, ok := prInfoByNumber(a.store.State(), pr.Repo, pr.Number); ok && info.URL != "" {
		return info.URL
	}

	return "https://" + pr.Repo + "/pull/" + strconv.Itoa(pr.Number)
}

// prAct runs then on the active drawer's pull request: at once when the drawer
// shows it loaded, else after showing the drawer's place's pull request first.
func (a *App) prAct(then prThen) tea.Cmd {
	tab, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	a.drawerTake()

	if v.pr != (core.ReviewPR{}) && v.loaded && !v.loading && v.err == nil {
		return a.prThenRun(then)
	}

	// The held pull request is asked for again as itself: resolving the worktree's
	// branch could pick another.
	if v.pr != (core.ReviewPR{}) && v.root != "" {
		return a.askPR(tab.ID, v.heldWant(v.root), v.prBase, then)
	}

	return a.showPR(tab.ID, then)
}

// openWithPR opens or retargets the active tab's drawer on pane, as leader d
// does, and shows a pull request in it once it is open and has its source.
func (a *App) openWithPR(pane core.PaneID, want *prWant, then prThen) tea.Cmd {
	tab := a.store.State().Tab()

	if tab.Review != nil && tab.Review.Pane == pane {
		a.drawerTake()

		return a.askPR(tab.ID, want, "", then)
	}

	cmd := a.openSliding(pane, want != nil && want.inCopy)
	if cmd == nil {
		return nil
	}

	a.drawer.prNext = &prNext{tab: tab.ID, pane: pane, want: want, then: then}

	return cmd
}

// prNextAfterSync shows the pull request a drawer was opened for, once the
// drawer has its source: the epoch of its first load is the one answers carry.
func (a *App) prNextAfterSync() tea.Cmd {
	n := a.drawer.prNext
	if n == nil || !n.open {
		return nil
	}

	t := a.store.State().Tab()
	if t.ID != n.tab || t.Review == nil || t.Review.Pane != n.pane {
		a.drawer.prNext = nil

		return nil
	}

	if n.noRepo {
		a.drawer.prNext = nil

		return a.askPR(n.tab, n.want, "", n.then)
	}

	v := a.drawer.views[t.ID]
	if v == nil {
		return nil
	}

	if v.src == nil {
		// A view that finished loading without a source never gets one for this open.
		if v.loaded {
			a.drawer.prNext = nil
		}

		return nil
	}

	a.drawer.prNext = nil

	return a.askPR(n.tab, n.want, "", n.then)
}

// prNumberText is the pull request's number as the tag and the mode row write it.
func prNumberText(n int) string { return "#" + strconv.Itoa(n) }

// prBaseText is the branch the shown pull request is compared with.
func prBaseText(v *reviewView) string {
	if v.base != "" {
		return v.base
	}

	return "the base branch"
}
