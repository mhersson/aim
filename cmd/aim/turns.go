package main

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/harness"
	"github.com/mhersson/aim/internal/term"
)

const (
	// turnHold is how long the hook server holds a prompt hook's or an edit's
	// answer; the hook client gives up at 2 s.
	turnHold = 1500 * time.Millisecond
	// turnBudget bounds a turn snapshot, counted from when the hook server
	// reads the event (the gate's arrival time for an event without a deadline).
	turnBudget = 1400 * time.Millisecond
	// markerBudget bounds writing a "no snapshot" marker.
	markerBudget = 5 * time.Second
	// placeBudget bounds one place lookup, apart from the hold's deadline.
	placeBudget = 2 * time.Second
)

var sessionIDRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// turnWriter records turn snapshots; gitTurnWriter does it with git.
type turnWriter interface {
	// Start records the working tree of the worktree containing dir as the start
	// of turn. ends names an open turn of that worktree to end with the same
	// tree, 0 for none. unchanged says nothing changed since the session's last
	// successful snapshot.
	Start(ctx context.Context, dir, session string, turn, ends int, unchanged bool) error
	// Mark records that turn, or with end its end, has no snapshot.
	Mark(ctx context.Context, dir, session string, turn int, end bool) error
	// End records the working tree of the worktree rooted at root as the end of
	// its open turn. unchanged says nothing changed since that turn's start.
	End(ctx context.Context, root, session string, turn int, unchanged bool) error
	// OpenTurns lists the session's open turns at its highest number in the
	// repository containing dir.
	OpenTurns(ctx context.Context, dir, session string) ([]git.OpenTurn, error)
}

// gitTurnWriter records turns with git, its temporary indexes in tempDir and
// the refreshed index copies in indexCache.
type gitTurnWriter struct{ tempDir, indexCache string }

func (w gitTurnWriter) Start(ctx context.Context, dir, session string, turn, ends int, unchanged bool) error {
	_, err := git.Snapshot(ctx, dir, session, git.SnapshotOptions{
		TempDir: w.tempDir, IndexCache: w.indexCache, Unchanged: unchanged, Turn: turn, Ends: ends,
	})

	return err
}

func (w gitTurnWriter) End(ctx context.Context, root, session string, turn int, unchanged bool) error {
	_, err := git.EndTurn(ctx, root, session, turn, git.SnapshotOptions{
		TempDir: w.tempDir, IndexCache: w.indexCache, Unchanged: unchanged,
	})

	return err
}

func (gitTurnWriter) OpenTurns(ctx context.Context, dir, session string) ([]git.OpenTurn, error) {
	r, err := git.Open(ctx, dir)
	if err != nil {
		return nil, err
	}

	return git.OpenTurns(ctx, r, session)
}

func (gitTurnWriter) Mark(ctx context.Context, dir, session string, turn int, end bool) error {
	_, err := git.MarkMissing(ctx, dir, session, turn, end)

	return err
}

// nextTurnID numbers a turn by when its prompt arrived, in milliseconds, and
// never repeats or goes back: a clock set back gives one more than prev.
func nextTurnID(prev int, at time.Time) int {
	return max(int(at.UnixMilli()), prev+1)
}

// seqFunc resolves the worktree containing dir and reads its watcher's change
// counter; ok is false when that worktree is not watched.
type seqFunc func(ctx context.Context, dir string) (root string, n uint64, ok bool)

// seqKey names what the unchanged skip remembers a counter for: one pane's
// snapshots of one session in one worktree.
type seqKey struct {
	pane          core.PaneID
	session, root string
}

// seqMemory holds the counter read before the last successful snapshot of each
// seqKey. Snapshot goroutines of different panes share it.
type seqMemory struct {
	mu   sync.Mutex
	last map[seqKey]uint64
}

func (m *seqMemory) get(k seqKey) (uint64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	v, ok := m.last[k]

	return v, ok
}

func (m *seqMemory) set(k seqKey, v uint64, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if ok {
		m.last[k] = v
	} else {
		delete(m.last, k)
	}
}

// startedRoot is a worktree a turn started. done closes once its start, or its
// marker, is written; marker is true for a worktree with no snapshot, whose
// end is not written either.
type startedRoot struct {
	done   chan struct{}
	marker bool
	// start is the context of the start being taken, and cancel ends it, which
	// turns it into a marker; both are nil for a worktree that gets no snapshot.
	start  context.Context //nolint:containedctx // the lifetime of this one start
	cancel context.CancelFunc
}

// paneTurn is what the gate remembers of a pane's last turn: the worktrees it
// started, and whether the next prompt has sealed it.
type paneTurn struct {
	session string
	turn    int
	roots   map[string]*startedRoot
	// sealed is set when the pane's next prompt takes over: a worktree not yet
	// in roots is no longer started.
	sealed bool
}

// split lists, in order, the worktrees the turn started with a snapshot that is
// written, which the pane's next prompt ends, and those whose start is still
// being taken, which it cannot end.
func (t paneTurn) split() (written []string, pending map[string]<-chan struct{}) {
	for root, e := range t.roots {
		switch {
		case e.marker:
		case closed(e.done):
			written = append(written, root)
		default:
			if pending == nil {
				pending = map[string]<-chan struct{}{}
			}

			pending[root] = e.done
		}
	}

	slices.Sort(written)

	return written, pending
}

func closed(c <-chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

func closedChan() chan struct{} {
	c := make(chan struct{})
	close(c)

	return c
}

// paneTurns holds each pane's last turn. Goroutines of different panes and of
// one pane's edits share it.
type paneTurns struct {
	mu   sync.Mutex
	last map[core.PaneID]paneTurn
}

// get returns a copy of the pane's turn.
func (p *paneTurns) get(pane core.PaneID) (paneTurn, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	v, ok := p.last[pane]
	if !ok {
		return v, false
	}

	roots := make(map[string]*startedRoot, len(v.roots))
	for r, e := range v.roots {
		roots[r] = &startedRoot{done: e.done, marker: e.marker, start: e.start, cancel: e.cancel}
	}

	v.roots = roots

	return v, true
}

func (p *paneTurns) set(pane core.PaneID, v paneTurn) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.last[pane] = v
}

// settled returns the pane's turn once every worktree it started has its start
// or marker written, or ctx ends.
func (p *paneTurns) settled(ctx context.Context, pane core.PaneID) (paneTurn, bool) {
	cur, ok := p.get(pane)

	for _, e := range cur.roots {
		select {
		case <-e.done:
		case <-ctx.Done():
		}
	}

	if !ok {
		return cur, false
	}

	return p.get(pane)
}

// seal stops the pane's turn from starting more worktrees and returns it once
// every worktree it started has its start or marker written, or ctx ends.
func (p *paneTurns) seal(ctx context.Context, pane core.PaneID) (paneTurn, bool) {
	p.mu.Lock()

	cur, ok := p.last[pane]
	if ok {
		cur.sealed = true
		p.last[pane] = cur
	}

	p.mu.Unlock()

	if !ok {
		return cur, false
	}

	return p.settled(ctx, pane)
}

// claim records root as started by the pane's turn, unless it already is. mine
// says the caller started it and must close done; marker says it gets no
// snapshot, because the turn is unsure or marker asks for it. ok is false when
// the pane has moved on to another turn or sealed this one without root.
func (p *paneTurns) claim(ctx context.Context, pane core.PaneID, active paneCurrent, root string, marker bool) (e *startedRoot, mine, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	cur, found := p.last[pane]
	if !found || cur.turn != active.turn {
		return nil, false, false
	}

	if e, has := cur.roots[root]; has {
		return e, false, true
	}

	if cur.sealed {
		return nil, false, false
	}

	e = &startedRoot{done: make(chan struct{}), marker: marker || active.uncertain.Load() || ctx.Err() != nil}
	if !e.marker {
		e.start, e.cancel = context.WithCancel(ctx)
	}

	if cur.roots == nil {
		cur.roots = map[string]*startedRoot{}
		p.last[pane] = cur
	}

	cur.roots[root] = e

	return e, true, true
}

// cancelPending ends the starts of the pane's turn still being taken, in root or,
// for an empty root, in every worktree, so each becomes a marker, and returns
// their done channels.
func (p *paneTurns) cancelPending(pane core.PaneID, turn int, root string) []<-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()

	cur, ok := p.last[pane]
	if !ok || cur.turn != turn {
		return nil
	}

	var dones []<-chan struct{}

	for r, e := range cur.roots {
		if (root == "" || r == root) && e.cancel != nil && !closed(e.done) {
			e.cancel()

			dones = append(dones, e.done)
		}
	}

	return dones
}

// failStart turns a claimed start into a marker after its snapshot failed.
func (p *paneTurns) failStart(pane core.PaneID, turn int, root string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if cur, ok := p.last[pane]; ok && cur.turn == turn {
		if e, has := cur.roots[root]; has {
			e.marker = true
		}
	}
}

// placeFunc finds the worktree an edited file belongs to.
type placeFunc func(ctx context.Context, path string) (git.Place, error)

// paneCurrent is the turn the gate's own goroutine last started for a pane.
type paneCurrent struct {
	session   string
	turn      int
	uncertain *atomic.Bool    // an incomplete batch may have edited an unknown root
	tail      <-chan struct{} // closes when that turn's start is done
}

type codexLaunch struct {
	tty      string
	pid      int
	session  core.SessionID
	sessions map[core.SessionID]*codexPrompts
	turn     string
}

// A completed submit keeps only its ID. Duplicate submits arriving during
// its snapshot share the original acknowledgement.
type codexPrompts struct {
	mu   sync.Mutex
	seen map[string]chan struct{}
}

func (p *codexPrompts) submit(ev *harness.Event) (bool, <-chan struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()

	id := ev.TurnID
	if ack, exists := p.seen[id]; exists {
		return false, ack
	}

	ack, done := make(chan struct{}), ev.Done
	p.seen[id] = ack
	ev.Done = sync.OnceFunc(func() {
		if done != nil {
			done()
		}

		p.mu.Lock()
		p.seen[id] = nil

		close(ack)
		p.mu.Unlock()
	})

	return true, nil
}

// admit binds the first trusted parent prompt when hook review skipped SessionStart.
// A duplicate submit shares the first submit's acknowledgement, including its deadline.
func (l *codexLaunch) admit(ev *harness.Event) (allow bool, wait <-chan struct{}) {
	if ev.OwnerPID != l.pid || ev.Subagent || !sessionIDRE.MatchString(string(ev.Session)) {
		return false, nil
	}

	prompts := l.sessions[ev.Session]
	if ev.Session != l.session {
		if !ev.Start && (l.session != "" || !ev.TurnStart) {
			return false, nil
		}

		if prompts == nil {
			prompts = &codexPrompts{seen: make(map[string]chan struct{})}
			l.sessions[ev.Session] = prompts
		}

		l.session, l.turn = ev.Session, ""
		ev.Start = true
	}

	if ev.TurnStart {
		allow, wait := prompts.submit(ev)
		if allow {
			l.turn = ev.TurnID
		}

		return allow, wait
	}

	if ev.Start {
		return true, nil
	}

	if ev.TurnID == "" || ev.TurnID != l.turn {
		return false, nil
	}

	if ev.Name == "Stop" || ev.Name == "Interrupt" {
		l.turn = ""
	}

	return true, nil
}

// gateOptions holds the gate's probe of a registered Codex leader; a nil
// probe uses the real one.
type gateOptions struct {
	ownerLeads func(tty string, pid int) bool
}

// runTurnGate admits events in arrival order. Each pane's snapshots start
// before UI forwarding and run one at a time independently of other panes.
func runTurnGate(ctx context.Context, in <-chan harness.Event, out chan<- harness.Event, w turnWriter, places placeFunc, seq seqFunc, log *slog.Logger, opts gateOptions) {
	if opts.ownerLeads == nil {
		opts.ownerLeads = term.ForegroundOwner
	}

	var wg sync.WaitGroup
	defer wg.Wait()

	// The last snapshot started per pane; touched only by this goroutine.
	// Each snapshot waits for its predecessor, so a pane's turn starts run one
	// at a time in arrival order.
	tails := make(map[core.PaneID]chan struct{})
	g := &gate{
		w: w, places: places, seq: seq, log: log, wg: &wg,
		memory: &seqMemory{last: map[seqKey]uint64{}},
		panes:  &paneTurns{last: map[core.PaneID]paneTurn{}},
	}

	// The last number given per pane; touched only by this goroutine.
	numbers := make(map[core.PaneID]int)
	current := make(map[core.PaneID]paneCurrent)
	owners := make(map[core.PaneID]*codexLaunch)

	for {
		var ev harness.Event

		select {
		case <-ctx.Done():
			return
		case e, ok := <-in:
			if !ok {
				return
			}

			ev = e
		}

		owner := owners[ev.Pane]
		if ev.Harness == "codex" {
			switch ev.Name {
			case harness.CodexOwnerStart:
				if ev.OwnerPID > 0 && (owner == nil || owner.pid != ev.OwnerPID) {
					owners[ev.Pane] = &codexLaunch{pid: ev.OwnerPID, tty: ev.OwnerTTY, sessions: make(map[core.SessionID]*codexPrompts)}
				}

				release(ev)

				continue
			case harness.CodexOwnerEnd:
				if owner != nil && owner.pid == ev.OwnerPID {
					delete(owners, ev.Pane)
				}

				release(ev)

				continue
			}

			if owner == nil || !opts.ownerLeads(owner.tty, owner.pid) || (ev.Status && ev.OwnerPID != owner.pid) {
				release(ev)

				continue
			}

			// A status binds no session and starts no snapshot, so it skips the prompt admission.
			if ev.Status {
				select {
				case out <- ev:
				case <-ctx.Done():
					release(ev)

					return
				}

				continue
			}

			allow, wait := owner.admit(&ev)
			if !allow {
				if wait == nil {
					release(ev)
				} else {
					wg.Go(func() {
						select {
						case <-wait:
						case <-ctx.Done():
						}

						release(ev)
					})
				}

				continue
			}
		} else if owner != nil && !ev.Show && opts.ownerLeads(owner.tty, owner.pid) {
			release(ev)

			continue
		}

		arrived := time.Now()

		deadline := ev.Deadline
		if deadline.IsZero() {
			deadline = arrived.Add(turnBudget)
		}

		if cur, ok := current[ev.Pane]; ok && (ev.Before || len(ev.Changed) > 0) && (ev.Subagent || strings.EqualFold(string(ev.Session), cur.session)) {
			if ev.Before {
				wg.Go(func() { g.edit(ctx, deadline, cur, ev) })
			} else {
				wg.Go(func() { g.changed(ctx, cur, ev) })
			}
		} else if ev.Before {
			release(ev)
		}

		if ev.TurnStart {
			if ev.Cwd == "" || !sessionIDRE.MatchString(string(ev.Session)) {
				release(ev)
			} else {
				turn := nextTurnID(numbers[ev.Pane], arrived)
				numbers[ev.Pane] = turn
				prev, done := tails[ev.Pane], make(chan struct{})
				tails[ev.Pane] = done
				ev.Snapped = done
				current[ev.Pane] = paneCurrent{session: string(ev.Session), turn: turn, tail: done, uncertain: new(atomic.Bool)}

				wg.Go(func() {
					defer close(done)

					g.startTurn(ctx, deadline, prev, ev, turn)
				})
			}
		}

		// A full app channel blocks here; the snapshot and the server's hold
		// run on their own.
		select {
		case out <- ev:
		case <-ctx.Done():
			release(ev)

			return
		}
	}
}

func release(ev harness.Event) {
	if ev.Done != nil {
		ev.Done()
	}
}

// gate is what the goroutines of a turn start and of a turn's edits work with.
type gate struct {
	w      turnWriter
	places placeFunc
	seq    seqFunc
	log    *slog.Logger
	memory *seqMemory
	panes  *paneTurns
	wg     *sync.WaitGroup // owned by runTurnGate; a late end marker runs on it
}

// pendingEnd is an open turn of an earlier prompt that a turn start ends.
type pendingEnd struct {
	root, session string
	turn          int
	failed        bool // the end was not written; it gets a marker
	// wait is set for a start still being taken: its end is a marker, written
	// once the start is.
	wait <-chan struct{}
}

// startTurn ends the pane's previous turn and snapshots the new one after prev
// closes and before deadline, and releases the held answer when it returns or
// the budget ends, whichever is first. A turn that gets no snapshot gets a
// marker, so its number stays taken, and an end that was not written gets an
// end marker.
func (g *gate) startTurn(parent context.Context, deadline time.Time, prev <-chan struct{}, ev harness.Event, turn int) {
	snapCtx, cancel := context.WithDeadline(parent, deadline)
	defer cancel()

	// Releases the answer at the budget even while waiting for the previous snapshot.
	stop := context.AfterFunc(snapCtx, func() { release(ev) })
	defer stop()

	session := string(ev.Session)

	// A pane's first turn start has no predecessor.
	if prev != nil {
		<-prev
	}

	// From here on a late edit of the previous turn can start nothing.
	last, known := g.panes.seal(snapCtx, ev.Pane)

	var (
		pending []pendingEnd
		err     error
	)

	// A snapshot taken after the budget could already hold the agent's edits.
	if err = snapCtx.Err(); err == nil {
		pending, err = g.endAndStart(snapCtx, ev, session, turn, last, known)
	} else {
		release(ev)

		pending = g.forget(parent, ev, session, turn, last)
	}

	release(ev)

	g.mark(parent, ev, session, turn, pending, err)
}

// forget replaces the pane's turn with one whose own worktree has no snapshot
// and returns the previous turn's worktrees, all still open.
func (g *gate) forget(ctx context.Context, ev harness.Event, session string, turn int, last paneTurn) []pendingEnd {
	next := paneTurn{session: session, turn: turn, roots: map[string]*startedRoot{}}

	lookup, cancel := context.WithTimeout(ctx, placeBudget)
	defer cancel()

	if root, _, _ := g.seq(lookup, ev.Cwd); root != "" {
		next.roots[root] = &startedRoot{done: closedChan(), marker: true}

		// No snapshot was taken, so no counter says what a later start may skip.
		g.memory.set(seqKey{pane: ev.Pane, session: strings.ToLower(session), root: root}, 0, false)
	}

	g.panes.set(ev.Pane, next)

	roots, late := last.split()
	pending := make([]pendingEnd, 0, len(roots)+len(late))

	for _, root := range roots {
		pending = append(pending, pendingEnd{root: root, session: last.session, turn: last.turn, failed: true})

		g.memory.set(seqKey{pane: ev.Pane, session: strings.ToLower(last.session), root: root}, 0, false)
	}

	return append(pending, latePending(last, late)...)
}

// latePending is the ends owed for starts still being taken: each waits for its
// start and then gets an end marker, whatever the start turns out to be.
func latePending(last paneTurn, late map[string]<-chan struct{}) []pendingEnd {
	out := make([]pendingEnd, 0, len(late))

	for _, root := range slices.Sorted(maps.Keys(late)) {
		out = append(out, pendingEnd{root: root, session: last.session, turn: last.turn, wait: late[root]})
	}

	return out
}

// endAndStart ends what the pane's previous turn started, then takes the new
// turn's start. It returns the ends that failed and the start's error.
func (g *gate) endAndStart(ctx context.Context, ev harness.Event, session string, turn int, last paneTurn, known bool) ([]pendingEnd, error) {
	root, n, watched := g.seq(ctx, ev.Cwd)

	if !known {
		last = g.openTurns(ctx, ev.Cwd, session)
	}

	// The own worktree ends through the new start when it is the same session.
	ends := 0
	lastRoots, late := last.split()
	pending := make([]pendingEnd, 0, len(lastRoots))

	for _, r := range lastRoots {
		p := pendingEnd{root: r, session: last.session, turn: last.turn}

		if root != "" && r == root && last.session == session {
			ends = last.turn
		} else {
			pending = append(pending, p)
		}
	}

	g.endAll(ctx, ev.Pane, pending)

	key := seqKey{pane: ev.Pane, session: strings.ToLower(session), root: root}
	prevN, had := g.memory.get(key)

	err := g.w.Start(ctx, ev.Cwd, session, turn, ends, watched && had && prevN == n)
	g.memory.set(key, n, err == nil && watched)

	next := paneTurn{session: session, turn: turn, roots: map[string]*startedRoot{}}
	if root != "" {
		next.roots[root] = &startedRoot{done: closedChan(), marker: err != nil}
	}

	g.panes.set(ev.Pane, next)

	if err != nil && ends != 0 {
		pending = append(pending, pendingEnd{root: root, session: session, turn: ends, failed: true})
	}

	pending = slices.DeleteFunc(pending, func(p pendingEnd) bool { return !p.failed })

	return append(pending, latePending(last, late)...), err
}

// openTurns finds the session's open turns in dir's repository for a pane with
// no remembered turn, as after a restart.
func (g *gate) openTurns(ctx context.Context, dir, session string) paneTurn {
	open, err := g.w.OpenTurns(ctx, dir, session)
	if err != nil {
		g.log.Debug("open turns not found", "session", session, "err", err)
	}

	// The refs name one number for all of them.
	last := paneTurn{session: session, roots: map[string]*startedRoot{}}

	for _, o := range open {
		last.turn = o.N
		last.roots[o.Root] = &startedRoot{done: closedChan()}
	}

	return last
}

// endAll ends every pending turn concurrently and marks the ones that failed.
func (g *gate) endAll(ctx context.Context, pane core.PaneID, pending []pendingEnd) {
	var wg sync.WaitGroup

	for i := range pending {
		wg.Go(func() {
			p := &pending[i]
			_, n, watched := g.seq(ctx, p.root)
			key := seqKey{pane: pane, session: strings.ToLower(p.session), root: p.root}
			prevN, had := g.memory.get(key)

			err := g.w.End(ctx, p.root, p.session, p.turn, watched && had && prevN == n)
			g.memory.set(key, n, err == nil && watched)

			switch {
			case err == nil:
			case errors.Is(err, git.ErrTurnNotOpen):
				g.log.Debug("turn not ended", "session", p.session, "root", p.root, "err", err)
			default:
				g.log.Warn("turn end failed", "session", p.session, "root", p.root, "err", err)

				p.failed = true
			}
		})
	}

	wg.Wait()
}

// mark writes the markers a turn start owes after its answer was released: its
// own start marker and the end markers of ends that failed.
func (g *gate) mark(parent context.Context, ev harness.Event, session string, turn int, failed []pendingEnd, err error) {
	switch {
	case parent.Err() != nil:
		return
	case errors.Is(err, git.ErrNotRepo), errors.Is(err, git.ErrBadSession):
		g.log.Debug("turn outside a repository", "session", session, "err", err)

		err = nil
	}

	markCtx, cancel := context.WithTimeout(parent, markerBudget)
	defer cancel()

	for _, p := range failed {
		if p.wait != nil {
			g.wg.Go(func() { g.markLate(parent, p) })

			continue
		}

		if err := g.w.Mark(markCtx, p.root, p.session, p.turn, true); err != nil {
			g.log.Debug("end marker not written", "session", p.session, "root", p.root, "err", err)
		}
	}

	if err == nil {
		return
	}

	g.log.Warn("turn snapshot failed", "session", session, "err", err)

	if err := g.w.Mark(markCtx, ev.Cwd, session, turn, false); err != nil {
		g.log.Warn("turn marker failed", "session", session, "err", err)
	}
}

// markLate writes the end marker of a turn whose start landed after the pane's
// next prompt took over, so that turn never stays open.
func (g *gate) markLate(parent context.Context, p pendingEnd) {
	ctx, cancel := context.WithTimeout(parent, markerBudget)
	defer cancel()

	select {
	case <-p.wait:
	case <-ctx.Done():
		return
	}

	if err := g.w.Mark(ctx, p.root, p.session, p.turn, true); err != nil {
		g.log.Debug("late end marker not written", "session", p.session, "root", p.root, "err", err)
	}
}

// lookup resolves what places says of each path, in order. A lookup that ends
// with the gate stops the rest.
func (g *gate) lookup(parent context.Context, paths []string) []placeAnswer {
	out := make([]placeAnswer, 0, len(paths))

	for _, path := range paths {
		ctx, cancel := context.WithTimeout(parent, placeBudget)
		place, err := g.places(ctx, path)

		cancel()

		switch {
		case err == nil:
			out = append(out, placeAnswer{place: place})
		case errors.Is(err, git.ErrNotCounted):
			out = append(out, placeAnswer{skip: true})
		case parent.Err() != nil:
			return out
		default:
			g.log.Warn("edit place not found", "path", path, "err", err)

			out = append(out, placeAnswer{unsure: true})
		}
	}

	return out
}

// placeAnswer is one path's lookup: a counted place, a path that does not
// count, or a failure.
type placeAnswer struct {
	place        git.Place
	skip, unsure bool
}

// edit answers an edit tool's before-event once the worktree of its file has a
// start for the turn, taking it when this is the first edit there. The answer
// goes out at the deadline, but never while a start in the edit's worktree is
// still being taken: that start is cancelled first and becomes a marker. A
// lookup or snapshot that runs later still records, as a marker.
func (g *gate) edit(parent context.Context, deadline time.Time, cur paneCurrent, ev harness.Event) {
	ctx, cancel := context.WithDeadline(parent, deadline)
	defer cancel()

	// Until every root is claimed, a timeout must cancel even starts claimed
	// by another edit and prevent any late before-picture of the unknown files.
	var resolved atomic.Pointer[[]string]

	finished := make(chan struct{})

	g.wg.Go(func() {
		select {
		case <-finished:
			return
		case <-ctx.Done():
		}

		if roots := resolved.Load(); roots != nil {
			for _, root := range *roots {
				g.cancelAndWait(parent, ev.Pane, cur.turn, root)
			}
		} else {
			cur.uncertain.Store(true)
			g.cancelAndWait(parent, ev.Pane, cur.turn, "")
		}

		release(ev)
	})

	defer close(finished)
	defer release(ev)

	paths := ev.BeforePaths
	if ev.Edit != "" {
		paths = []string{ev.Edit}
	}

	answers := g.lookup(parent, paths)

	var roots []string

	seen := make(map[string]bool)

	unsure := len(answers) != len(paths)
	for _, a := range answers {
		if a.unsure {
			unsure = true
		}

		if !a.skip && !a.unsure && !seen[a.place.Root] {
			seen[a.place.Root] = true
			roots = append(roots, a.place.Root)
		}
	}

	if !unsure && len(roots) == 0 {
		resolved.Store(&roots)

		return
	}

	if unsure {
		cur.uncertain.Store(true)
		g.cancelAndWait(parent, ev.Pane, cur.turn, "")
	}

	select {
	case <-cur.tail:
	case <-parent.Done():
		return
	}

	var batch sync.WaitGroup

	for _, root := range roots {
		e, mine, ok := g.panes.claim(ctx, ev.Pane, cur, root, false)

		batch.Go(func() { g.editRoot(parent, ctx, cur, ev.Pane, root, e, mine, ok) })
	}

	if !unsure {
		resolved.Store(&roots)
	}

	batch.Wait()
}

func (g *gate) editRoot(parent, ctx context.Context, cur paneCurrent, pane core.PaneID, root string, e *startedRoot, mine, ok bool) {
	if !ok {
		// The next prompt took over: the turn still lists the worktree.
		g.wg.Go(func() { g.markStart(parent, root, cur) })

		return
	}

	if !mine {
		select {
		case <-e.done:
		case <-ctx.Done():
			g.cancelAndWait(parent, pane, cur.turn, root)
		}

		return
	}

	if e.cancel == nil {
		close(e.done)
		g.wg.Go(func() { g.markStart(parent, root, cur) })

		return
	}

	defer e.cancel()

	err := g.startRoot(e.start, pane, root, cur)
	if err != nil {
		g.panes.failStart(pane, cur.turn, root)
	}

	close(e.done)

	if err != nil {
		g.log.Warn("before-picture failed", "session", cur.session, "root", root, "err", err)
		g.wg.Go(func() { g.markStart(parent, root, cur) })
	}
}

// cancelAndWait ends the starts of the pane's turn that an edit answered now
// would race, and waits for them to finish.
func (g *gate) cancelAndWait(parent context.Context, pane core.PaneID, turn int, root string) {
	for _, done := range g.panes.cancelPending(pane, turn, root) {
		select {
		case <-done:
		case <-parent.Done():
		}
	}
}

// startRoot takes the start of cur's turn in root, passing unchanged as
// startTurn does.
func (g *gate) startRoot(ctx context.Context, pane core.PaneID, root string, cur paneCurrent) error {
	_, n, watched := g.seq(ctx, root)
	key := seqKey{pane: pane, session: strings.ToLower(cur.session), root: root}
	prevN, had := g.memory.get(key)

	err := g.w.Start(ctx, root, cur.session, cur.turn, 0, watched && had && prevN == n)
	g.memory.set(key, n, err == nil && watched)

	return err
}

// changed marks the worktrees a finished Bash command changed that the turn has
// not started: Claude Code gives no before-picture of a command's changes, so
// they get a marker. Nothing is held.
func (g *gate) changed(parent context.Context, cur paneCurrent, ev harness.Event) {
	answers := g.lookup(parent, ev.Changed)

	select {
	case <-cur.tail:
	case <-parent.Done():
		return
	}

	for _, a := range answers {
		if a.skip {
			continue
		}

		if a.unsure {
			cur.uncertain.Store(true)

			continue
		}

		e, mine, ok := g.panes.claim(parent, ev.Pane, cur, a.place.Root, true)
		if !ok {
			g.markStart(parent, a.place.Root, cur)

			continue
		}

		if mine {
			close(e.done)
			g.markStart(parent, a.place.Root, cur)
		}
	}
}

// markStart writes the start marker of cur's turn in root.
func (g *gate) markStart(parent context.Context, root string, cur paneCurrent) {
	if parent.Err() != nil {
		return
	}

	ctx, cancel := context.WithTimeout(parent, markerBudget)
	defer cancel()

	if err := g.w.Mark(ctx, root, cur.session, cur.turn, false); err != nil {
		g.log.Warn("start marker failed", "session", cur.session, "root", root, "err", err)
	}
}
