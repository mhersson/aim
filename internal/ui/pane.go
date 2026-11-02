package ui

import (
	"image/color"
	"io"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
	"github.com/mhersson/aim/internal/term"
)

// readChunk is the PTY read size; large enough that a flooding child costs
// few lock round trips.
const readChunk = 32 << 10

// maxHeld bounds the hook events kept for a shell pane while a probe runs.
const maxHeld = 16

// proc is the child process behind a pane; *term.Process implements it.
type proc interface {
	io.ReadWriter
	Resize(cols, rows int) error
	Done() <-chan struct{}
	Cwd() (string, error)
	Foreground() (string, error)
	Close() error
}

// The messages below carry the pane they came from, so one from a pane since
// replaced under the same id is dropped.

// paneDirtyMsg tells the App a pane has output to render.
type paneDirtyMsg struct {
	id core.PaneID
	p  *pane
}

// paneExitedMsg tells the App a pane's output ended.
type paneExitedMsg struct {
	id core.PaneID
	p  *pane
}

// holdExpiredMsg ends a sync hold; the App renders the pane again.
type holdExpiredMsg struct {
	id core.PaneID
	p  *pane
}

// pane owns one process and its emulator, and keeps a per-row render cache.
// Its mutex serialises every VT call.
type pane struct {
	id    core.PaneID
	proc  proc
	clock clock
	tap   *tap // nil unless recording; closed after the read loop

	dirty  chan struct{} // cap 1: at most one outstanding notification
	exited chan struct{} // closed when the read loop ends
	wg     sync.WaitGroup

	// Update-goroutine state, outside mu.
	holdArmed bool            // a holdTick is outstanding
	cwdAt     time.Time       // start of the last cwd lookup
	cwdBusy   bool            // a lookup is running
	cwdWait   bool            // a deferred lookup is scheduled
	launched  time.Time       // when AIM started the agent; zero otherwise
	seen      bool            // the harness program was the foreground process
	resuming  bool            // launched with --resume and no SessionStart yet
	lastOut   time.Time       // when output last arrived
	workingAt time.Time       // when the agent last entered working
	held      []harness.Event // hooks that arrived while the pane was a shell, applied if a probe makes it an agent
	heldProbe bool            // a probe started by a held hook is out
	launchDue bool            // the next SessionStart names the directory Claude was typed in
	stale     bool            // output or a resize arrived while its tab was hidden; render when shown

	mu         sync.Mutex
	vt         term.VT
	rows       []string
	rowsBase   uint64 // stable row of rows[0] when they were last rendered
	gen        uint64 // bumps when rows change; part of the scrolled view's cache key
	alt        bool   // the alt screen was active at the last render
	altSwitch  bool   // the alt screen changed since takeAltSwitch
	clip       string // the guest's latest OSC 52 write, until takeClip
	scrolled   bool   // the view shows history; anchor is its first row
	anchor     uint64 // stable row of the first view row while scrolled
	viewCache  viewCache
	forced     bool
	closed     bool
	syncSerial uint64
	openedAt   time.Time
	closeOnce  sync.Once
	closeErr   error
}

// paneOption configures newPane.
type paneOption func(*pane)

// withTap records the pane's output and resizes.
func withTap(t *tap) paneOption { return func(p *pane) { p.tap = t } }

// withColors sets the colours the guest's OSC 10 and 11 queries are answered with.
func withColors(fg, bg color.Color) paneOption {
	return func(p *pane) { p.vt.SetDefaultColors(fg, bg) }
}

// setColors changes them on a running pane.
func (p *pane) setColors(fg, bg color.Color) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.closed {
		p.vt.SetDefaultColors(fg, bg)
	}
}

// newPane starts the emulator, renders every row once and starts the read loop.
func newPane(id core.PaneID, p proc, cols, rows int, c clock, opts ...paneOption) *pane {
	pn := &pane{
		id:     id,
		proc:   p,
		clock:  c,
		dirty:  make(chan struct{}, 1),
		exited: make(chan struct{}),
		vt:     term.NewVT(cols, rows, p),
		rows:   make([]string, rows),
	}

	for _, o := range opts {
		o(pn)
	}

	for y := range pn.rows {
		pn.rows[y] = renderRow(pn.vt, y, cols)
	}

	pn.rowsBase = pn.stableTopLocked()

	// Installed before the read loop: VT calls are not concurrent-safe.
	pn.vt.SetClipboardHandler(pn.setClip)

	pn.wg.Go(pn.readLoop)

	return pn
}

// setClip keeps the guest's latest clipboard write. It runs inside Write with
// mu held, so it must not lock.
func (p *pane) setClip(_, content string) {
	if content != "" {
		p.clip = content
	}
}

// takeClip returns the guest's latest clipboard write since the last call.
func (p *pane) takeClip() (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	text := p.clip
	p.clip = ""

	return text, text != ""
}

// takeAltSwitch reports whether the pane moved between the main and alt
// screens since the last call.
func (p *pane) takeAltSwitch() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	was := p.altSwitch
	p.altSwitch = false

	return was
}

func (p *pane) readLoop() {
	defer close(p.exited)

	buf := make([]byte, readChunk)

	for {
		n, err := p.proc.Read(buf)
		if n > 0 {
			p.tap.data(buf[:n])
			p.feed(buf[:n])
		}

		if err != nil {
			return
		}
	}
}

func (p *pane) feed(b []byte) {
	p.mu.Lock()

	if p.closed {
		p.mu.Unlock()

		return
	}

	_, _ = p.vt.Write(b)

	if open, serial := p.vt.SyncState(); open && serial != p.syncSerial {
		p.syncSerial = serial
		p.openedAt = p.clock.Now()
	}

	p.mu.Unlock()

	select {
	case p.dirty <- struct{}{}:
	default:
	}
}

// wait blocks until the pane has output or its process ended (output EOF or
// the process being reaped, whichever comes first). The App re-arms it after
// every dirty message. Pending output is reported before the exit.
func (p *pane) wait() tea.Cmd {
	return func() tea.Msg {
		select {
		case <-p.dirty:
			return paneDirtyMsg{id: p.id, p: p}
		default:
		}

		select {
		case <-p.dirty:
			return paneDirtyMsg{id: p.id, p: p}
		case <-p.exited:
			return paneExitedMsg{id: p.id, p: p}
		case <-p.proc.Done():
			return paneExitedMsg{id: p.id, p: p}
		}
	}
}

// holdTick fires holdExpiredMsg when a sync hold ends at until.
func (p *pane) holdTick(until time.Time) tea.Cmd {
	return tea.Tick(until.Sub(p.clock.Now()), func(time.Time) tea.Msg {
		return holdExpiredMsg{id: p.id, p: p}
	})
}

// render re-renders the rows and reports whether any cached row changed.
// While a sync update is held it renders nothing and returns the hold's end.
func (p *pane) render() (changed bool, heldUntil time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if alt := p.vt.AltScreen(); alt != p.alt {
		p.alt, p.scrolled, p.altSwitch = alt, false, true
	}

	if open, _ := p.vt.SyncState(); open {
		if end := p.openedAt.Add(syncMaxHold); p.clock.Now().Before(end) {
			return false, end
		}
	}

	w, _ := p.vt.Size()

	p.rowsBase = p.stableTopLocked()
	changed = p.forced
	p.forced = false

	for y := range p.rows {
		if s := renderRow(p.vt, y, w); s != p.rows[y] {
			p.rows[y] = s
			changed = true
		}
	}

	if changed {
		p.gen++
	}

	return changed, time.Time{}
}

// resize resizes the emulator and the process and blanks the cache; the
// caller renders afterwards. It reports false when the size is unchanged.
func (p *pane) resize(cols, rows int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if c, r := p.vt.Size(); c == cols && r == rows {
		return false
	}

	p.vt.Resize(cols, rows)
	p.tap.resize(cols, rows)
	// A dead child cannot take a resize; its exit is reported by wait.
	_ = p.proc.Resize(cols, rows)

	blank := strings.Repeat(" ", cols)
	p.rows = make([]string, rows)

	for y := range p.rows {
		p.rows[y] = blank
	}

	p.forced = true
	p.scrolled = false
	p.viewCache = viewCache{}
	p.rowsBase = p.stableTopLocked()

	return true
}

// stableTopLocked is the stable row of the first live screen row: the history
// length plus the lines trimmed so far.
func (p *pane) stableTopLocked() uint64 {
	return uint64(p.histLenLocked()) + p.vt.HistoryTrimmed()
}

// lines returns the rows to draw, each exactly the pane width: the live screen,
// or the scrolled view. It is only valid until the next render or resize.
func (p *pane) lines() []string {
	lines, _ := p.linesAt()

	return lines
}

// linesAt is lines plus the stable row of the first one, so a caller can place
// text-anchored state on the rows as drawn rather than as the emulator is now.
func (p *pane) linesAt() (lines []string, base uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if top, hist := p.viewTopLocked(); p.scrolled {
		return p.viewLocked(top, hist), p.anchor
	}

	return p.rows, p.rowsBase
}

func (p *pane) size() (cols, rows int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.vt.Size()
}

// cursor is the guest's cursor; hidden while the view is scrolled.
func (p *pane) cursor() (uv.Position, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	pos, visible := p.vt.Cursor()
	p.viewTopLocked()

	return pos, visible && !p.scrolled
}

func (p *pane) title() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.vt.Title()
}

// workingDir prefers the guest's OSC 7 report, then the process's own cwd. A
// closed pane has none.
func (p *pane) workingDir() string {
	p.mu.Lock()
	d := p.vt.WorkingDir()
	closed := p.closed
	p.mu.Unlock()

	if closed {
		return ""
	}

	if d != "" {
		return d
	}

	// Outside mu: the lookup is file IO.
	if d, err := p.proc.Cwd(); err == nil {
		return d
	}

	return ""
}

// foreground is the name of the terminal's foreground process, or "" when it
// cannot be read, which the kind rule treats as unknown.
func (p *pane) foreground() string {
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()

	if closed {
		return ""
	}

	// Outside mu: the lookup is file IO.
	if name, err := p.proc.Foreground(); err == nil {
		return name
	}

	return ""
}

func (p *pane) sendKey(k uv.KeyPressEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.closed {
		p.scrolled = false
		p.vt.SendKey(k)
	}
}

func (p *pane) sendPaste(text string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.closed {
		p.scrolled = false
		p.vt.SendBytes(term.EncodePaste(text, p.vt.BracketedPaste()))
	}
}

// inputModes reports whether the guest tracks the mouse and whether it is on
// the alt screen.
func (p *pane) inputModes() (tracking, alt bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.vt.MouseTracking(), p.vt.AltScreen()
}

// sendMouse forwards a mouse event in pane cell coordinates; the emulator
// encodes it per the guest's modes.
func (p *pane) sendMouse(ev uv.MouseEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.closed {
		p.vt.SendMouse(ev)
	}
}

// sendFocus reports focus in or out, only when the guest asked for it.
func (p *pane) sendFocus(in bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed || !p.vt.FocusReporting() {
		return
	}

	if in {
		p.vt.SendBytes([]byte("\x1b[I"))
	} else {
		p.vt.SendBytes([]byte("\x1b[O"))
	}
}

// close stops the process, waits for the read loop, then closes the emulator.
// It is idempotent.
func (p *pane) close() error {
	p.closeOnce.Do(func() {
		// Close the process before taking mu: VT.Close waits on a reply drain that may be blocked writing to the PTY.
		err := p.proc.Close()

		p.mu.Lock()
		p.closed = true
		p.mu.Unlock()

		p.wg.Wait()

		if terr := p.tap.close(); err == nil {
			err = terr
		}

		p.mu.Lock()
		defer p.mu.Unlock()

		if verr := p.vt.Close(); err == nil {
			err = verr
		}

		p.closeErr = err
	})

	return p.closeErr
}
