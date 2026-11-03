package ui

import (
	"log/slog"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/mhersson/aim/internal/github"
	"github.com/mhersson/aim/internal/md"
	"github.com/mhersson/aim/internal/ui/kitty"
)

const (
	pictureMaxBytes = 5 << 20 // the most a fetch reads
	pictureMaxRows  = 20      // the tallest picture, in cells
	pictureMaxSent  = 64      // the most images sent at a time
	pictureMaxCols  = 100     // the widest picture, in cells
)

// pictureKey identifies a source. scope is the root for a local source, and for
// a URL the document's host when the URL is on that host, else "".
type pictureKey struct{ src, scope string }

// docPictures is what one view lends its documents: the registry, the root
// local images must lie in and the document's GitHub host.
type docPictures struct {
	reg        *pictures
	root, host string
	alias      map[string]string // a source to the URL that loads it, for signed images
}

// size answers md.LayoutOptions.Picture for a document of this view.
func (d docPictures) size(src string, maxCols int) md.PictureSize {
	return d.reg.size(d, src, maxCols)
}

// key reports the key of src when it may load for this view, and the URL that
// fetches a remote one. The key leaves out a signed token, whose value changes
// on every read; the URL keeps it.
func (d docPictures) key(src string) (key pictureKey, fetch string, ok bool) {
	if to, ok := d.alias[src]; ok {
		src = to
	}

	if strings.Contains(src, "://") {
		if !github.ImageHost(src, d.host) {
			return pictureKey{}, "", false
		}

		u, err := url.Parse(src)
		if err != nil {
			return pictureKey{}, "", false
		}

		scope := ""
		if d.host != "" && strings.EqualFold(u.Hostname(), d.host) {
			scope = d.host
		}

		return pictureKey{github.ImageKey(src), scope}, src, true
	}

	if d.root == "" || !filepath.IsAbs(src) || strings.HasSuffix(strings.ToLower(src), ".svg") {
		return pictureKey{}, "", false
	}

	rel, err := filepath.Rel(d.root, src)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return pictureKey{}, "", false
	}

	return pictureKey{src, d.root}, "", true
}

// pictureJob asks the loader for a source's pixel size (render false) or for
// the escape that sends it as image id at cols x rows cells of cell.
type pictureJob struct {
	key        pictureKey
	url        string // what to fetch for a remote key, when that differs from key.src
	fresh      bool   // a remote fetch skips the caches
	gen        uint64 // the entry generation that asked
	root, host string
	render     bool
	id         uint32
	cols, rows int
	cell       kitty.Cell
}

// pictureJobs is where the registry queues work; add must not block.
type pictureJobs interface{ add(pictureJob) }

// pictureDoneMsg is the loader's answer to a job: w and h for an info job, seq
// for a render job.
type pictureDoneMsg struct {
	job   pictureJob
	stamp pictureStamp // of a local file, set by an info job
	w, h  int
	seq   string
	err   error
}

// pictureStamp is a local file's size and mtime when it was read.
type pictureStamp struct{ size, mod int64 }

// pictureStaleMsg lists the local pictures whose files changed since they loaded.
type pictureStaleMsg struct{ refs []staleRef }

type staleRef struct {
	key   pictureKey
	root  string
	stamp pictureStamp
}

type pictureSize struct{ cols, rows int }

type sentPicture struct {
	id      uint32
	ready   bool   // the terminal holds the image
	pending bool   // a render job is out
	used    uint64 // when shown last; neither ready nor pending means unsent or evicted
}

type pictureEntry struct {
	known  bool // w and h are set
	failed bool
	w, h   int
	sent   map[pictureSize]*sentPicture
	root   string // of the doc that first sized it, for tombstone renders
	host   string
	fetch  string // the newest URL that loads a remote key
	fresh  bool   // the next fetch skips the caches
	gen    uint64 // told apart from an older entry of the same key
	stamp  pictureStamp
}

type sentRef struct {
	key  pictureKey
	size pictureSize
}

// pictures tracks which sources are known, failed or sent. It lives on the
// update loop and does no IO: the loader gets jobs and answers with messages.
type pictures struct {
	jobs   pictureJobs
	getenv func(string) string
	log    *slog.Logger

	queried  bool // start sent the query
	graphics bool // the graphics query answered OK before the DA1 reply
	da1      bool
	asked    bool // the RGB and Tc requests went out
	profile  colorprofile.Profile
	cell     kitty.Cell

	entries map[pictureKey]*pictureEntry
	egen    uint64                  // generation of the next entry
	refresh map[pictureKey]struct{} // forgotten by a reload: the next fetch skips the caches
	ids     map[uint32]sentRef
	nextID  uint32
	tick    uint64              // use counter for eviction order
	onTerm  map[uint32]struct{} // ids the terminal holds, apart from the entries so a failed source's stay
	frame   uint64              // tick at which the latest shown call began
	g       uint64

	fallbacks int // frames whose pictures the compositor placed because the splice declined; tests and benches read it
}

// newPictures returns a registry; with no jobs sink every image stays a line.
func newPictures(jobs pictureJobs, getenv func(string) string, log *slog.Logger) *pictures {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &pictures{
		jobs: jobs, getenv: getenv, log: log,
		entries: map[pictureKey]*pictureEntry{}, refresh: map[pictureKey]struct{}{}, ids: map[uint32]sentRef{},
		onTerm: map[uint32]struct{}{},
	}
}

// on reports whether pictures show: graphics support, a cell size and truecolor.
func (p *pictures) on() bool {
	return p.jobs != nil && p.queried && p.graphics && p.cell.W > 0 && p.cell.H > 0 && p.profile == colorprofile.TrueColor
}

// gen moves whenever an answer or a state change may alter a layout.
func (p *pictures) gen() uint64 { return p.g }

// start sends the support query, except where a terminal in between cannot show
// the pictures: tmux, or an AIM pane (whose terminal answers OK but cannot).
func (p *pictures) start() tea.Cmd {
	if p.jobs == nil || p.getenv("TMUX") != "" || p.getenv("AIM_PANE") != "" {
		return nil
	}

	p.queried = true

	return tea.Raw(kitty.Query())
}

// reply handles the replies to start's query and the color profile; handled is
// false for any other message.
func (p *pictures) reply(msg tea.Msg) (cmd tea.Cmd, handled bool) {
	was := p.on()

	switch m := msg.(type) {
	case uv.KittyGraphicsEvent:
		if m.Options.ID != kitty.QueryID {
			return nil, false
		}

		if p.queried && !p.da1 && string(m.Payload) == "OK" {
			p.graphics = true
			cmd = p.askTruecolor()
		}
	case uv.PrimaryDeviceAttributesEvent:
		p.da1 = true
	case uv.CellSizeEvent:
		cmd = p.setCell(kitty.Cell{W: m.Width, H: m.Height})
	case tea.ColorProfileMsg:
		p.profile = m.Profile
	default:
		return nil, false
	}

	if !was && p.on() {
		p.g++
	}

	return cmd, true
}

// askTruecolor asks for RGB and Tc once when the profile is lower; either
// answer makes Bubble Tea send a truecolor profile.
func (p *pictures) askTruecolor() tea.Cmd {
	if p.profile == colorprofile.TrueColor || p.asked {
		return nil
	}

	p.asked = true

	return tea.Batch(tea.RequestCapability("RGB"), tea.RequestCapability("Tc"))
}

func (p *pictures) setCell(c kitty.Cell) tea.Cmd {
	if c.W <= 0 || c.H <= 0 || c == p.cell {
		return nil
	}

	old := p.cell
	p.cell = c

	if old.W == 0 {
		return nil
	}

	del := p.deletes()

	for _, e := range p.entries {
		e.sent = nil
	}

	clear(p.ids)

	p.g++

	if del == "" {
		return nil
	}

	return tea.Raw(del)
}

// deletes returns the escapes that free every image the terminal holds, by id:
// a delete-all never touches virtual placements.
func (p *pictures) deletes() string {
	ids := slices.Sorted(maps.Keys(p.onTerm))

	clear(p.onTerm)

	var b strings.Builder
	for _, id := range ids {
		b.WriteString(kitty.Delete(id))
	}

	return b.String()
}

// resized asks for the cell size again after a window size change.
func (p *pictures) resized() tea.Cmd {
	if !p.on() {
		return nil
	}

	return tea.Raw(kitty.CellSizeQuery)
}

// size answers the picture for src at most maxCols cells wide, queueing the
// work it still needs.
func (p *pictures) size(d docPictures, src string, maxCols int) md.PictureSize {
	if !p.on() {
		return md.PictureSize{}
	}

	key, fetch, ok := d.key(src)
	if !ok {
		return md.PictureSize{}
	}

	e := p.entries[key]
	if e == nil {
		_, fresh := p.refresh[key]
		delete(p.refresh, key)

		e = &pictureEntry{root: d.root, host: d.host, fetch: fetch, fresh: fresh, gen: p.egen}
		p.egen++
		p.entries[key] = e
		p.jobs.add(pictureJob{key: key, url: fetch, fresh: fresh, gen: e.gen, root: d.root, host: d.host})

		return md.PictureSize{Loading: true}
	}

	if fetch != "" {
		e.fetch = fetch // the newest signed URL; an older one expires
	}

	if e.failed {
		return md.PictureSize{}
	}

	if !e.known {
		return md.PictureSize{Loading: true}
	}

	cols, rows := kitty.Fit(e.w, e.h, p.cell, min(maxCols, pictureMaxCols), pictureMaxRows)
	sz := pictureSize{cols, rows}

	if s := e.sent[sz]; s != nil {
		return md.PictureSize{Cols: cols, Rows: rows, ID: s.id}
	}

	id := p.allocID()

	if e.sent == nil {
		e.sent = map[pictureSize]*sentPicture{}
	}

	p.tick++
	e.sent[sz] = &sentPicture{id: id, used: p.tick}
	p.ids[id] = sentRef{key, sz}

	return md.PictureSize{Cols: cols, Rows: rows, ID: id}
}

func (p *pictures) allocID() uint32 {
	for {
		p.nextID++
		if p.nextID > kitty.MaxID {
			p.nextID = 1
		}

		_, taken := p.ids[p.nextID]
		if _, held := p.onTerm[p.nextID]; !taken && !held {
			return p.nextID
		}
	}
}

// retry forgets the failed sources of lay's images under d, so they load again,
// and checks its local images against their files. The command answers with
// pictureStaleMsg for those that changed.
func (p *pictures) retry(d docPictures, lay md.Layout) tea.Cmd {
	forgot := false

	for _, im := range lay.Images {
		key, _, ok := d.key(im.Src)
		if !ok {
			continue
		}

		if e := p.entries[key]; e != nil && e.failed {
			delete(p.entries, key)

			forgot = true
		}
	}

	if forgot {
		p.g++
	}

	return p.stale(d, lay)
}

// stale returns the command that stats the loaded local images of lay under d
// off the update loop.
func (p *pictures) stale(d docPictures, lay md.Layout) tea.Cmd {
	var refs []staleRef

	for _, im := range lay.Images {
		key, _, ok := d.key(im.Src)
		if !ok || strings.Contains(key.src, "://") {
			continue
		}

		if e := p.entries[key]; e != nil && e.known && e.stamp != (pictureStamp{}) && !slices.ContainsFunc(refs, func(r staleRef) bool { return r.key == key }) {
			refs = append(refs, staleRef{key: key, root: key.scope, stamp: e.stamp})
		}
	}

	if len(refs) == 0 {
		return nil
	}

	return func() tea.Msg {
		var changed []staleRef

		for _, r := range refs {
			if st, ok := statPicture(r); !ok || st != r.stamp {
				changed = append(changed, r)
			}
		}

		if len(changed) == 0 {
			return nil
		}

		return pictureStaleMsg{refs: changed}
	}
}

// statPicture stats a local picture inside its root; ok is false when it cannot.
func statPicture(r staleRef) (pictureStamp, bool) {
	rel, err := filepath.Rel(r.root, r.key.src)
	if err != nil {
		return pictureStamp{}, false
	}

	root, err := os.OpenRoot(r.root)
	if err != nil {
		return pictureStamp{}, false
	}

	defer func() { _ = root.Close() }()

	fi, err := root.Stat(rel)
	if err != nil {
		return pictureStamp{}, false
	}

	return pictureStamp{fi.Size(), fi.ModTime().UnixNano()}, true
}

// reloadAll forgets every picture of lay's images under d, so all load again
// and a remote one skips the caches. The command deletes what the terminal held.
func (p *pictures) reloadAll(d docPictures, lay md.Layout) tea.Cmd {
	var keys []pictureKey

	for _, im := range lay.Images {
		if key, _, ok := d.key(im.Src); ok && p.entries[key] != nil {
			keys = append(keys, key)
		}
	}

	return p.forget(keys)
}

// onStale reloads the local pictures whose files changed since they loaded; an
// answer for a picture that loaded again meanwhile is dropped.
func (p *pictures) onStale(msg pictureStaleMsg) tea.Cmd {
	var keys []pictureKey

	for _, r := range msg.refs {
		if e := p.entries[r.key]; e != nil && e.stamp == r.stamp {
			keys = append(keys, r.key)
		}
	}

	return p.forget(keys)
}

// forget drops the entries of keys and the images they sent, and returns the
// command that deletes those from the terminal.
func (p *pictures) forget(keys []pictureKey) tea.Cmd {
	var del strings.Builder

	for _, key := range keys {
		e := p.entries[key]
		if e == nil {
			continue
		}

		for _, s := range e.sent {
			delete(p.ids, s.id)

			if _, held := p.onTerm[s.id]; held {
				delete(p.onTerm, s.id)
				del.WriteString(kitty.Delete(s.id))
			}
		}

		delete(p.entries, key)

		p.refresh[key] = struct{}{}
	}

	if len(keys) == 0 {
		return nil
	}

	p.g++

	if del.Len() == 0 {
		return nil
	}

	return tea.Raw(del.String())
}

// done records the loader's answer. A render answer returns the escape to
// write, with the deletes of the images it pushes out.
func (p *pictures) done(msg pictureDoneMsg) tea.Cmd {
	j := msg.job
	e := p.entries[j.key]

	if e == nil || e.failed || e.gen != j.gen {
		return nil
	}

	if !j.render {
		return p.doneInfo(e, msg)
	}

	ref, ok := p.ids[j.id]
	if !ok || ref.key != j.key || ref.size != (pictureSize{j.cols, j.rows}) || j.cell != p.cell {
		return nil
	}

	s := e.sent[ref.size]
	if s == nil || !s.pending {
		return nil
	}

	if msg.err != nil {
		p.fail(j.key, e, msg.err)

		return nil
	}

	s.ready, s.pending = true, false
	p.onTerm[j.id] = struct{}{}

	var out strings.Builder
	out.WriteString(msg.seq)

	for _, id := range p.evict(s) {
		out.WriteString(kitty.Delete(id))
	}

	return tea.Raw(out.String())
}

func (p *pictures) doneInfo(e *pictureEntry, msg pictureDoneMsg) tea.Cmd {
	if e.known {
		return nil
	}

	if msg.err != nil {
		p.fail(msg.job.key, e, msg.err)

		return nil
	}

	e.known, e.w, e.h, e.stamp = true, msg.w, msg.h, msg.stamp
	p.g++

	return nil
}

func (p *pictures) fail(key pictureKey, e *pictureEntry, err error) {
	for _, s := range e.sent {
		delete(p.ids, s.id)
	}

	e.sent, e.failed = nil, true
	p.g++

	src := key.src
	msg := err.Error()

	for _, raw := range []string{e.fetch, key.src} {
		if u, perr := url.Parse(raw); perr == nil && u.Scheme != "" {
			u.RawQuery, u.Fragment = "", ""
			clean := u.String()
			msg = strings.ReplaceAll(msg, raw, clean)

			if raw == key.src {
				src = clean
			}
		}
	}

	p.log.Info("picture not loaded", "src", src, "err", msg)
}

// evict drops the least recently used sent images past pictureMaxSent, never
// keep or one the latest frame shows, and returns their ids. A frame showing
// more than pictureMaxSent images so raises the cap while it shows them.
func (p *pictures) evict(keep *sentPicture) []uint32 {
	var out []uint32

	for p.readyCount() > pictureMaxSent {
		var oldest *sentPicture

		for _, ref := range p.ids {
			s := p.entries[ref.key].sent[ref.size]
			if s == keep || !s.ready || s.used >= p.frame || (oldest != nil && s.used >= oldest.used) {
				continue
			}

			oldest = s
		}

		if oldest == nil {
			break
		}

		oldest.ready = false

		delete(p.onTerm, oldest.id)

		out = append(out, oldest.id)
	}

	return out
}

func (p *pictures) readyCount() int {
	n := 0

	for _, ref := range p.ids {
		if p.entries[ref.key].sent[ref.size].ready {
			n++
		}
	}

	return n
}

// quit deletes every sent image before next. Bubble Tea flushes raw output on
// its 60 Hz tick while the alt screen is up, but at shutdown only after leaving
// it, so next runs two frames later.
func (p *pictures) quit(next tea.Cmd) tea.Cmd {
	del := p.deletes()
	if del == "" {
		return next
	}

	return tea.Sequence(tea.Raw(del), tea.Tick(2*time.Second/60, func(time.Time) tea.Msg { return next() }))
}

// shown marks the pictures a frame shows as used, and queues the render of any
// the terminal does not hold (never sent, or evicted).
func (p *pictures) shown(ids []uint32) {
	p.frame = p.tick + 1

	for _, id := range ids {
		ref, ok := p.ids[id]
		if !ok {
			continue
		}

		e := p.entries[ref.key]
		s := e.sent[ref.size]
		p.tick++
		s.used = p.tick

		if !s.ready && !s.pending {
			s.pending = true

			p.jobs.add(pictureJob{key: ref.key, url: e.fetch, gen: e.gen, root: e.root, host: e.host, render: true, id: id, cols: ref.size.cols, rows: ref.size.rows, cell: p.cell})
		}
	}
}

// sent reports whether the terminal holds image id.
func (p *pictures) sent(id uint32) bool {
	ref, ok := p.ids[id]

	return ok && p.entries[ref.key].sent[ref.size].ready
}
