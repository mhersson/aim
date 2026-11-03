package ui

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	xkitty "github.com/charmbracelet/x/ansi/kitty"

	"github.com/mhersson/aim/internal/md"
	"github.com/mhersson/aim/internal/ui/kitty"
)

type recJobs struct{ jobs []pictureJob }

func (r *recJobs) add(j pictureJob) { r.jobs = append(r.jobs, j) }

func (r *recJobs) take() []pictureJob {
	out := r.jobs
	r.jobs = nil

	return out
}

type logBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.buf.Write(p)
}

func (l *logBuf) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return strings.Count(l.buf.String(), "picture not loaded")
}

func newTestPictures(env map[string]string) (*pictures, *recJobs, *logBuf) {
	jobs := &recJobs{}
	lb := &logBuf{}
	p := newPictures(jobs, func(k string) string { return env[k] }, slog.New(slog.NewTextHandler(lb, nil)))

	return p, jobs, lb
}

var (
	gfxOK    = uv.KittyGraphicsEvent{Options: xkitty.Options{ID: kitty.QueryID}, Payload: []byte("OK")}
	gfxNo    = uv.KittyGraphicsEvent{Options: xkitty.Options{ID: kitty.QueryID}, Payload: []byte("ENOTSUPPORTED:no")}
	cell10x2 = uv.CellSizeEvent{Width: 10, Height: 20}
	da1      = uv.PrimaryDeviceAttributesEvent{62, 4}
	truecol  = tea.ColorProfileMsg{Profile: colorprofile.TrueColor}
	color256 = tea.ColorProfileMsg{Profile: colorprofile.ANSI256}
)

// feed sends msgs through reply and returns each command.
func feed(p *pictures, msgs ...tea.Msg) []tea.Cmd {
	out := make([]tea.Cmd, len(msgs))
	for i, m := range msgs {
		out[i], _ = p.reply(m)
	}

	return out
}

// batched is the number of commands a batch command holds.
func batched(t *testing.T, cmd tea.Cmd) int {
	t.Helper()

	if cmd == nil {
		return 0
	}

	v := reflect.ValueOf(cmd())
	if v.Kind() != reflect.Slice {
		t.Fatalf("command gave %v, want a batch", v)
	}

	return v.Len()
}

func TestPictureDetect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		env   map[string]string
		start bool
		msgs  []tea.Msg
		on    bool
	}{
		{"all three", nil, true, []tea.Msg{truecol, gfxOK, cell10x2, da1}, true},
		{"truecolor after the reply", nil, true, []tea.Msg{gfxOK, cell10x2, da1, truecol}, true},
		{"da1 first", nil, true, []tea.Msg{truecol, da1, gfxOK, cell10x2}, false},
		{"no cell size", nil, true, []tea.Msg{truecol, gfxOK, da1}, false},
		{"not supported", nil, true, []tea.Msg{truecol, gfxNo, cell10x2, da1}, false},
		{"256 colours", nil, true, []tea.Msg{color256, gfxOK, cell10x2, da1}, false},
		{"256 colours then truecolor", nil, true, []tea.Msg{color256, gfxOK, cell10x2, da1, truecol}, true},
		{"tmux", map[string]string{"TMUX": "/tmp/x"}, false, []tea.Msg{truecol, gfxOK, cell10x2, da1}, false},
		{"tmux 256 colours", map[string]string{"TMUX": "/tmp/x"}, false, []tea.Msg{color256, gfxOK, cell10x2, da1}, false},
		{"nested pane", map[string]string{"AIM_PANE": "1"}, false, []tea.Msg{truecol, gfxOK, cell10x2, da1}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p, _, _ := newTestPictures(tt.env)

			if got := p.start() != nil; got != tt.start {
				t.Fatalf("start() sent a query = %v, want %v", got, tt.start)
			}

			for i, c := range feed(p, tt.msgs...) {
				if !tt.start && c != nil {
					t.Errorf("message %d returned a command with the query not sent", i)
				}
			}

			if got := p.on(); got != tt.on {
				t.Errorf("on() = %v, want %v", got, tt.on)
			}
		})
	}
}

func TestPictureCapabilityRequest(t *testing.T) {
	t.Parallel()

	p, _, _ := newTestPictures(nil)
	p.start()
	cmds := feed(p, color256, gfxOK)

	if got := batched(t, cmds[1]); got != 2 {
		t.Fatalf("OK below truecolor gave %d requests, want RGB and Tc", got)
	}

	if again, _ := p.reply(gfxOK); again != nil {
		t.Error("the capability requests were sent twice")
	}

	q, _, _ := newTestPictures(nil)
	q.start()

	if cmd := feed(q, truecol, gfxOK)[1]; cmd != nil {
		t.Error("OK at truecolor asked for capabilities")
	}
}

func TestPictureNilJobs(t *testing.T) {
	t.Parallel()

	p := newPictures(nil, func(string) string { return "" }, nil)
	if p.start() != nil {
		t.Error("start with no jobs sent a query")
	}

	feed(p, truecol, gfxOK, cell10x2, da1)

	if p.on() {
		t.Error("pictures on with no loader")
	}
}

func onPictures(t *testing.T) (*pictures, *recJobs, *logBuf) {
	t.Helper()

	p, jobs, lb := newTestPictures(nil)
	p.start()
	feed(p, truecol, gfxOK, cell10x2, da1)

	if !p.on() {
		t.Fatal("pictures are off")
	}

	return p, jobs, lb
}

const ghURL = "https://github.com/user-attachments/assets/x"

func TestPictureSize(t *testing.T) {
	t.Parallel()

	d := func(p *pictures) docPictures { return docPictures{reg: p, root: "/repo", host: "github.com"} }

	t.Run("off", func(t *testing.T) {
		t.Parallel()

		p, jobs, _ := newTestPictures(nil)
		if got := p.size(d(p), ghURL, 96); got != (md.PictureSize{}) || len(jobs.jobs) != 0 {
			t.Errorf("off gave %+v with %d jobs", got, len(jobs.jobs))
		}
	})

	for _, src := range []string{
		"https://img.shields.io/b.png",
		"http://github.com/a.png",
		"https://github.com/a.svg",
		"/elsewhere/a.png",
		"/repo/../x/a.png",
		"/repo/a.SVG",
		"rel/a.png",
	} {
		t.Run("refused "+src, func(t *testing.T) {
			t.Parallel()

			p, jobs, _ := onPictures(t)
			if got := p.size(d(p), src, 96); got != (md.PictureSize{}) || len(jobs.jobs) != 0 {
				t.Errorf("gave %+v with %d jobs", got, len(jobs.jobs))
			}
		})
	}

	t.Run("local", func(t *testing.T) {
		t.Parallel()

		p, jobs, _ := onPictures(t)
		if got := p.size(d(p), "/repo/docs/a.png", 96); !got.Loading || len(jobs.jobs) != 1 || jobs.jobs[0].key.scope != "/repo" {
			t.Errorf("gave %+v with %+v", got, jobs.jobs)
		}
	})

	t.Run("lifecycle", func(t *testing.T) {
		t.Parallel()

		p, jobs, _ := onPictures(t)
		dp := d(p)

		if got := p.size(dp, ghURL, 96); got != (md.PictureSize{Loading: true}) {
			t.Fatalf("first = %+v", got)
		}

		if got := jobs.take(); len(got) != 1 || got[0].render || got[0].key.src != ghURL || got[0].key.scope != "github.com" || got[0].host != "github.com" {
			t.Fatalf("info job = %+v", got)
		}

		if p.size(dp, ghURL, 96); len(jobs.jobs) != 0 {
			t.Fatal("a second call added a job")
		}

		gen := p.gen()
		key := pictureKey{ghURL, "github.com"}

		if cmd := p.done(pictureDoneMsg{job: pictureJob{key: key}, w: 1847, h: 948}); cmd != nil {
			t.Error("an info answer returned a command")
		}

		if p.gen() == gen {
			t.Error("gen did not move")
		}

		got := p.size(dp, ghURL, 96)
		if got.Cols != 78 || got.Rows != 20 || got.ID == 0 || got.Loading || len(jobs.jobs) != 0 {
			t.Fatalf("after info = %+v with %d jobs", got, len(jobs.jobs))
		}

		if p.sent(got.ID) {
			t.Error("sent before shown")
		}

		p.shown([]uint32{got.ID})
		p.shown([]uint32{got.ID})

		jb := jobs.take()
		if len(jb) != 1 || !jb[0].render || jb[0].cols != 78 || jb[0].rows != 20 || jb[0].id == 0 || jb[0].cell != (kitty.Cell{W: 10, H: 20}) {
			t.Fatalf("render job = %+v", jb)
		}

		cmd := p.done(pictureDoneMsg{job: jb[0], seq: "SEQ"})
		if raw, ok := cmd().(tea.RawMsg); !ok || raw.Msg != "SEQ" {
			t.Fatalf("render command gave %+v", cmd())
		}

		if jb[0].id != got.ID || !p.sent(got.ID) {
			t.Errorf("job id %d, sent %v", jb[0].id, p.sent(got.ID))
		}

		if narrow := p.size(dp, ghURL, 40); narrow.ID == got.ID || narrow.Cols != 40 || len(jobs.take()) != 0 {
			t.Errorf("a narrower line gave %+v", narrow)
		}
	})

	t.Run("failure", func(t *testing.T) {
		t.Parallel()

		p, _, lb := onPictures(t)
		dp := d(p)
		key := pictureKey{ghURL, "github.com"}
		p.size(dp, ghURL, 96)
		p.done(pictureDoneMsg{job: pictureJob{key: key}, err: errors.New("boom")})

		if got := p.size(dp, ghURL, 96); got != (md.PictureSize{}) {
			t.Errorf("failed source gave %+v", got)
		}

		p.done(pictureDoneMsg{job: pictureJob{key: key}, err: errors.New("boom")})

		if n := lb.count(); n != 1 {
			t.Errorf("%d log records, want 1", n)
		}
	})
}

func TestPictureRetry(t *testing.T) {
	t.Parallel()

	p, jobs, lb := onPictures(t)
	dp := docPictures{reg: p, root: "/repo", host: "github.com"}
	key := pictureKey{ghURL, "github.com"}
	lay := md.Layout{Images: []md.Image{{Src: ghURL}}}

	p.size(dp, ghURL, 96)
	jobs.take()
	p.done(pictureDoneMsg{job: pictureJob{key: key}, err: errors.New("boom")})

	if got := p.size(dp, ghURL, 96); got != (md.PictureSize{}) {
		t.Fatalf("failed = %+v", got)
	}

	gen := p.gen()
	p.retry(dp, md.Layout{Images: []md.Image{{Src: "https://github.com/other.png"}}})

	if p.gen() != gen {
		t.Error("retry of a layout with no failed source moved gen")
	}

	p.retry(dp, lay)

	if p.gen() == gen {
		t.Error("retry did not move gen")
	}

	got := p.size(dp, ghURL, 96)

	again := jobs.take()
	if !got.Loading || len(again) != 1 {
		t.Fatalf("after retry = %+v", got)
	}

	p.done(pictureDoneMsg{job: again[0], err: errors.New("boom again")})

	if n := lb.count(); n != 2 {
		t.Errorf("%d log records, want 2", n)
	}
}

func TestPictureEvictSkipsShown(t *testing.T) {
	t.Parallel()

	const visible = pictureMaxSent + 6

	p, jobs, _ := onPictures(t)
	dp := docPictures{reg: p, root: "/repo"}

	ids := make([]uint32, 0, visible+1)
	for i := range visible + 1 {
		ids = append(ids, known(t, p, jobs, dp, "/repo/"+strings.Repeat("a", i+1)+".png", 96).ID)
	}

	// frame is one View: it shows ids, then answers every render it queued.
	frame := func(shown []uint32) (renders, deletes int) {
		for range 200 {
			p.shown(shown)

			jb := jobs.take()
			if len(jb) == 0 {
				return renders, deletes
			}

			for _, j := range jb {
				renders++
				deletes += strings.Count(rawOf(t, p.done(pictureDoneMsg{job: j, seq: "S"})), "a=d")
			}
		}

		t.Fatal("renders never settled")

		return 0, 0
	}

	if renders, deletes := frame(ids[:visible]); renders != visible || deletes != 0 {
		t.Fatalf("%d pictures shown: %d renders and %d deletes, want %d and none", visible, renders, deletes, visible)
	}

	if renders, deletes := frame(ids[:visible]); renders != 0 || deletes != 0 {
		t.Fatalf("a settled frame gave %d renders and %d deletes", renders, deletes)
	}

	// A frame showing 10 of them, plus one more: the cap holds again.
	if _, deletes := frame(append(slices.Clone(ids[:10]), ids[visible])); deletes != visible+1-pictureMaxSent {
		t.Errorf("deletes = %d, want %d", deletes, visible+1-pictureMaxSent)
	}

	if n := p.readyCount(); n != pictureMaxSent {
		t.Errorf("ready = %d, want %d", n, pictureMaxSent)
	}

	for _, id := range ids[:10] {
		if !p.sent(id) {
			t.Errorf("image %d shown by the frame was deleted", id)
		}
	}
}

// known sizes src and answers its info, returning the size it then has.
func known(t *testing.T, p *pictures, jobs *recJobs, dp docPictures, src string, maxCols int) md.PictureSize {
	t.Helper()

	p.size(dp, src, maxCols)

	info := jobs.take()
	if len(info) != 1 || info[0].render {
		t.Fatalf("info jobs = %+v", info)
	}

	p.done(pictureDoneMsg{job: info[0], w: 100, h: 100})

	got := p.size(dp, src, maxCols)
	if got.ID == 0 || len(jobs.jobs) != 0 {
		t.Fatalf("size after info = %+v with %d jobs", got, len(jobs.jobs))
	}

	return got
}

// rendered shows src and answers its render, answers its info and render, and returns the id.
func rendered(t *testing.T, p *pictures, jobs *recJobs, dp docPictures, src string) (uint32, tea.Cmd) {
	t.Helper()

	id := known(t, p, jobs, dp, src, 96).ID

	p.shown([]uint32{id})

	r := jobs.take()
	if len(r) != 1 || !r[0].render {
		t.Fatalf("render jobs = %+v", r)
	}

	return r[0].id, p.done(pictureDoneMsg{job: r[0], seq: "S" + src})
}

func TestPictureLate(t *testing.T) {
	t.Parallel()

	dp := func(p *pictures) docPictures { return docPictures{reg: p, root: "/repo"} }

	t.Run("after a cell change", func(t *testing.T) {
		t.Parallel()

		p, jobs, _ := onPictures(t)
		p.shown([]uint32{known(t, p, jobs, dp(p), "/repo/a.png", 96).ID})

		job := jobs.take()[0]

		p.reply(uv.CellSizeEvent{Width: 8, Height: 16})

		gen := p.gen()
		if cmd := p.done(pictureDoneMsg{job: job, seq: "S"}); cmd != nil || p.gen() != gen {
			t.Error("a stale render changed state")
		}
	})

	t.Run("evicted id", func(t *testing.T) {
		t.Parallel()

		p, jobs, _ := onPictures(t)
		p.shown([]uint32{known(t, p, jobs, dp(p), "/repo/a.png", 96).ID})

		job := jobs.take()[0]
		job.id += 1000

		gen := p.gen()
		if cmd := p.done(pictureDoneMsg{job: job, seq: "S"}); cmd != nil || p.gen() != gen {
			t.Error("an unknown id changed state")
		}
	})

	t.Run("failed source", func(t *testing.T) {
		t.Parallel()

		p, jobs, lb := onPictures(t)
		p.size(dp(p), "/repo/a.png", 96)

		job := jobs.take()[0]
		p.done(pictureDoneMsg{job: job, err: errors.New("x")})

		gen := p.gen()
		if cmd := p.done(pictureDoneMsg{job: job, w: 5, h: 5}); cmd != nil || p.gen() != gen {
			t.Error("an info answer for a failed source changed state")
		}

		if got := p.size(dp(p), "/repo/a.png", 96); got != (md.PictureSize{}) || lb.count() != 1 {
			t.Errorf("got %+v, %d logs", got, lb.count())
		}
	})
}

func TestPictureEvict(t *testing.T) {
	t.Parallel()

	p, jobs, _ := onPictures(t)
	dp := docPictures{reg: p, root: "/repo"}

	ids := make([]uint32, 0, pictureMaxSent+1)
	srcs := make([]string, 0, pictureMaxSent+1)

	var last tea.Cmd

	for i := range pictureMaxSent + 1 {
		src := "/repo/" + strings.Repeat("a", i+1) + ".png"
		srcs = append(srcs, src)

		var id uint32

		id, last = rendered(t, p, jobs, dp, src)
		ids = append(ids, id)

		if i < pictureMaxSent && strings.Contains(rawOf(t, last), "a=d") {
			t.Fatalf("picture %d evicted early", i)
		}
	}

	raw := rawOf(t, last)
	if !strings.Contains(raw, kitty.Delete(ids[0])) {
		t.Errorf("command %q lacks the delete of the oldest", raw)
	}

	if strings.Count(raw, "a=d") != 1 {
		t.Errorf("command %q deletes more than one image", raw)
	}

	want := md.PictureSize{Cols: 10, Rows: 5, ID: ids[0]}
	if got := p.size(dp, srcs[0], 96); got != want || len(jobs.take()) != 0 || p.sent(ids[0]) {
		t.Errorf("evicted picture gave %+v, want %+v, no job and unsent", got, want)
	}

	p.shown([]uint32{ids[0]})
	p.shown([]uint32{ids[0]})

	jb := jobs.take()
	if len(jb) != 1 || !jb[0].render || jb[0].id != ids[0] || jb[0].key.src != srcs[0] {
		t.Fatalf("shown queued %+v", jb)
	}

	if raw := rawOf(t, p.done(pictureDoneMsg{job: jb[0], seq: "AGAIN"})); !strings.HasPrefix(raw, "AGAIN") {
		t.Errorf("done gave %q", raw)
	}

	if got := p.size(dp, srcs[1], 96); got.ID != ids[1] {
		t.Errorf("a kept picture gave %+v", got)
	}
}

func rawOf(t *testing.T, cmd tea.Cmd) string {
	t.Helper()

	if cmd == nil {
		return ""
	}

	raw, ok := cmd().(tea.RawMsg)
	if !ok {
		t.Fatalf("command did not give a RawMsg")
	}

	s, _ := raw.Msg.(string)

	return s
}

func TestPictureCellChange(t *testing.T) {
	t.Parallel()

	p, jobs, _ := onPictures(t)
	dp := docPictures{reg: p, root: "/repo"}
	id, _ := rendered(t, p, jobs, dp, "/repo/a.png")

	gen := p.gen()

	cmd, _ := p.reply(uv.CellSizeEvent{Width: 8, Height: 16})
	if got := rawOf(t, cmd); got != kitty.Delete(id) {
		t.Errorf("command = %q, want the delete of image %d", got, id)
	}

	if p.gen() == gen {
		t.Error("gen did not move")
	}

	got := p.size(dp, "/repo/a.png", 96)
	if got.ID == 0 || got.Cols != 13 || len(jobs.jobs) != 0 {
		t.Fatalf("size = %+v with %d jobs", got, len(jobs.jobs))
	}

	p.shown([]uint32{got.ID})

	jb := jobs.take()
	if len(jb) != 1 || jb[0].id != got.ID || !jb[0].render || jb[0].cell != (kitty.Cell{W: 8, H: 16}) {
		t.Errorf("jobs = %+v", jb)
	}

	if cmd, _ := p.reply(uv.CellSizeEvent{Width: 8, Height: 16}); cmd != nil {
		t.Error("an unchanged cell size returned a command")
	}
}

func TestPictureResized(t *testing.T) {
	t.Parallel()

	p, _, _ := onPictures(t)
	if rawOf(t, p.resized()) != kitty.CellSizeQuery {
		t.Error("resized did not ask for the cell size")
	}

	q, _, _ := newTestPictures(nil)
	if q.resized() != nil {
		t.Error("resized asked while off")
	}
}

func TestPictureQuit(t *testing.T) {
	t.Parallel()

	next := func() tea.Msg { return "next" }

	p, jobs, _ := onPictures(t)
	if got := p.quit(next)(); got != "next" {
		t.Errorf("with nothing sent, quit gave %v", got)
	}

	id, _ := rendered(t, p, jobs, docPictures{reg: p, root: "/repo"}, "/repo/a.png")

	v := reflect.ValueOf(p.quit(next)())
	if v.Kind() != reflect.Slice || v.Len() != 2 {
		t.Fatalf("quit gave %v", v)
	}

	first, _ := reflect.TypeAssert[tea.Cmd](v.Index(0))
	if got := rawOf(t, first); got != kitty.Delete(id) {
		t.Errorf("first = %q, want the delete of image %d", got, id)
	}

	second, _ := reflect.TypeAssert[tea.Cmd](v.Index(1))
	if second() != "next" {
		t.Error("second is not next, a frame tick later")
	}
}

func TestDocPictureMove(t *testing.T) {
	t.Parallel()

	src := []byte("hello\n\n![a](/repo/a.png)\n")

	t.Run("gen bump", func(t *testing.T) {
		t.Parallel()

		p, _, _ := onPictures(t)
		d := parseMarkdown(src, "/repo", nil).withPictures(docPictures{reg: p, root: "/repo"})
		first := d.layout(80, false)

		if _, ok := d.pictureMove(); ok {
			t.Fatal("move before any change")
		}

		p.g++

		d.layout(80, false)

		if d.layouts != 2 {
			t.Fatalf("layouts = %d, want 2", d.layouts)
		}

		old, ok := d.pictureMove()
		if !ok || !reflect.DeepEqual(old, first) {
			t.Errorf("move = %v, old layout match = %v", ok, reflect.DeepEqual(old, first))
		}

		if _, ok := d.pictureMove(); ok {
			t.Error("move offered twice")
		}
	})

	t.Run("width change", func(t *testing.T) {
		t.Parallel()

		p, _, _ := onPictures(t)
		d := parseMarkdown(src, "/repo", nil).withPictures(docPictures{reg: p, root: "/repo"})
		d.layout(80, false)

		p.g++

		d.layout(70, false)

		if _, ok := d.pictureMove(); ok {
			t.Error("a width change offered a move")
		}
	})

	t.Run("no images", func(t *testing.T) {
		t.Parallel()

		p, _, _ := onPictures(t)
		d := parseMarkdown([]byte("just text\n"), "/repo", nil).withPictures(docPictures{reg: p, root: "/repo"})
		d.layout(80, false)

		p.g++

		d.layout(80, false)

		if d.layouts != 1 {
			t.Errorf("layouts = %d, want 1", d.layouts)
		}
	})
}

func TestPictureUnsolicitedCell(t *testing.T) {
	t.Parallel()

	p, _, _ := newTestPictures(nil)
	feed(p, truecol, cell10x2)

	if cmd, _ := p.reply(uv.CellSizeEvent{Width: 8, Height: 16}); cmd != nil {
		t.Error("a cell size change with graphics off returned a command")
	}
}

func TestPictureSettles(t *testing.T) {
	t.Parallel()

	p, jobs, _ := onPictures(t)
	dp := docPictures{reg: p, root: "/repo"}

	srcs := make([]string, 70)
	for i := range srcs {
		srcs[i] = "/repo/" + strings.Repeat("b", i+1) + ".png"
		known(t, p, jobs, dp, srcs[i], 96)
	}

	gen := p.gen()

	for range 3 {
		for _, src := range srcs {
			if got := p.size(dp, src, 96); got.Rows == 0 {
				t.Fatalf("%s gave %+v", src, got)
			}
		}
	}

	if len(jobs.jobs) != 0 || p.gen() != gen {
		t.Fatalf("did not settle: %d jobs, gen moved %v", len(jobs.jobs), p.gen() != gen)
	}

	var ids []uint32
	for _, src := range srcs[:5] {
		ids = append(ids, p.size(dp, src, 96).ID)
	}

	p.shown(ids)

	if n := len(jobs.take()); n != 5 {
		t.Errorf("shown of 5 queued %d renders", n)
	}
}

// failOtherSize renders /repo/a.png at one size and fails it at another, and
// returns the rendered size's id.
func failOtherSize(t *testing.T, p *pictures, jobs *recJobs) uint32 {
	t.Helper()

	dp := docPictures{reg: p, root: "/repo"}
	id, _ := rendered(t, p, jobs, dp, "/repo/a.png")

	p.shown([]uint32{p.size(dp, "/repo/a.png", 5).ID})

	job := jobs.take()[0]
	p.done(pictureDoneMsg{job: job, err: errors.New("x")})

	return id
}

func TestPictureQuitAfterFailedSize(t *testing.T) {
	t.Parallel()

	p, jobs, _ := onPictures(t)
	id := failOtherSize(t, p, jobs)

	v := reflect.ValueOf(p.quit(func() tea.Msg { return "next" })())
	if v.Kind() != reflect.Slice || v.Len() != 2 {
		t.Fatalf("quit gave %v, want the delete then next", v)
	}

	first, _ := reflect.TypeAssert[tea.Cmd](v.Index(0))
	if got := rawOf(t, first); got != kitty.Delete(id) {
		t.Errorf("first = %q, want the delete of image %d", got, id)
	}
}

func TestPictureCellChangeAfterFailedSize(t *testing.T) {
	t.Parallel()

	p, jobs, _ := onPictures(t)
	id := failOtherSize(t, p, jobs)

	cmd, _ := p.reply(uv.CellSizeEvent{Width: 8, Height: 16})
	if got := rawOf(t, cmd); got != kitty.Delete(id) {
		t.Errorf("command = %q, want the delete of image %d", got, id)
	}

	if q := p.quit(func() tea.Msg { return "next" }); q().(string) != "next" {
		t.Error("quit after the deletes did not give next")
	}
}

func TestPictureLogHasNoQuery(t *testing.T) {
	t.Parallel()

	p, jobs, lb := onPictures(t)
	dp := docPictures{reg: p, root: "/repo"}
	src := "https://private-user-images.githubusercontent.com/1/a.png?jwt=SECRET"

	p.size(dp, src, 96)
	p.done(pictureDoneMsg{job: jobs.take()[0], err: errors.New("Get " + src + ": boom")})

	if lb.count() != 1 || strings.Contains(lb.buf.String(), "SECRET") {
		t.Errorf("log = %q", lb.buf.String())
	}
}

func TestPictureKeptRender(t *testing.T) {
	t.Parallel()

	p, jobs, _ := onPictures(t)
	dp := docPictures{reg: p, root: "/repo"}

	p.shown([]uint32{known(t, p, jobs, dp, "/repo/a.png", 96).ID})
	job := jobs.take()[0]

	if rawOf(t, p.done(pictureDoneMsg{job: job, seq: "S"})) != "S" {
		t.Fatal("render not kept")
	}

	if !p.sent(job.id) {
		t.Error("kept render not marked sent")
	}

	if got := p.size(dp, "/repo/a.png", 96); got.ID != job.id || len(jobs.take()) != 0 {
		t.Errorf("later size gave %+v", got)
	}
}

func TestPictureSignedKeepsAcrossReads(t *testing.T) {
	t.Parallel()

	const (
		src    = "https://github.com/user-attachments/assets/aaaa"
		bare   = "https://private-user-images.githubusercontent.com/1/9-aaaa.png"
		first  = bare + "?jwt=one"
		second = bare + "?jwt=two"
	)

	p, jobs, _ := onPictures(t)
	dp := func(signed string) docPictures {
		return docPictures{reg: p, host: "github.com", alias: map[string]string{src: signed}}
	}

	p.size(dp(first), src, 96)

	j := jobs.take()
	if len(j) != 1 || j[0].key.src != bare || j[0].url != first {
		t.Fatalf("first job = %+v, want key %q fetching %q", j, bare, first)
	}

	p.done(pictureDoneMsg{job: j[0], w: 100, h: 100})

	if got := p.size(dp(second), src, 96); got.ID == 0 || len(jobs.jobs) != 0 {
		t.Fatalf("a re-read with a new token = %+v with %d jobs, want the kept picture", got, len(jobs.jobs))
	}

	p.shown([]uint32{p.size(dp(second), src, 96).ID})

	r := jobs.take()
	if len(r) != 1 || !r[0].render || r[0].url != second {
		t.Errorf("render jobs = %+v, want one fetching the newest URL", r)
	}
}

func TestPictureReloadAll(t *testing.T) {
	t.Parallel()

	p, jobs, _ := onPictures(t)
	dp := docPictures{reg: p, root: "/repo", host: "github.com"}
	lay := md.Layout{Images: []md.Image{{Src: ghURL}}}

	id, cmd := rendered(t, p, jobs, dp, ghURL)
	if cmd == nil {
		t.Fatal("render gave no escape")
	}

	p.size(dp, ghURL, 96)
	stale := pictureJob{key: pictureKey{ghURL, "github.com"}}

	del := p.reloadAll(dp, lay)
	if del == nil || !strings.Contains(rawOf(t, del), kitty.Delete(id)) {
		t.Errorf("reloadAll did not delete image %d", id)
	}

	if got := p.size(dp, ghURL, 96); !got.Loading {
		t.Fatalf("after reloadAll = %+v, want loading", got)
	}

	j := jobs.take()
	if len(j) != 1 || j[0].render || !j[0].fresh {
		t.Fatalf("jobs after reloadAll = %+v, want one fresh info job", j)
	}

	p.done(pictureDoneMsg{job: stale, w: 5, h: 5})

	if got := p.size(dp, ghURL, 96); !got.Loading {
		t.Errorf("an answer of the forgotten entry was taken: %+v", got)
	}

	p.done(pictureDoneMsg{job: j[0], w: 100, h: 100})

	if got := p.size(dp, ghURL, 96); got.ID == 0 {
		t.Errorf("the reloaded entry never settled: %+v", got)
	}
}

func TestPictureStaleLocal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(t *testing.T, path string)
		stale  bool
	}{
		{"unchanged", func(*testing.T, string) {}, false},
		{"rewritten larger", func(t *testing.T, path string) {
			t.Helper()

			if err := os.WriteFile(path, []byte("longer than before"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"same size, new mtime", func(t *testing.T, path string) {
			t.Helper()

			if err := os.Chtimes(path, time.Unix(1, 0), time.Unix(1, 0)); err != nil {
				t.Fatal(err)
			}
		}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			path := filepath.Join(root, "a.png")

			if err := os.WriteFile(path, []byte("12345678"), 0o600); err != nil {
				t.Fatal(err)
			}

			fi, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}

			p, jobs, _ := onPictures(t)
			dp := docPictures{reg: p, root: root}
			lay := md.Layout{Images: []md.Image{{Src: path}}}

			p.size(dp, path, 96)

			j := jobs.take()
			if len(j) != 1 {
				t.Fatalf("jobs = %+v", j)
			}

			p.done(pictureDoneMsg{job: j[0], w: 100, h: 100, stamp: pictureStamp{fi.Size(), fi.ModTime().UnixNano()}})
			tt.change(t, path)

			cmd := p.retry(dp, lay)

			var msg tea.Msg
			if cmd != nil {
				msg = cmd()
			}

			sm, ok := msg.(pictureStaleMsg)
			if ok != tt.stale {
				t.Fatalf("stale answer = %#v, want stale %v", msg, tt.stale)
			}

			if !tt.stale {
				if got := p.size(dp, path, 96); got.ID == 0 {
					t.Errorf("an unchanged file's picture was forgotten: %+v", got)
				}

				return
			}

			p.onStale(sm)

			if got := p.size(dp, path, 96); !got.Loading || len(jobs.take()) != 1 {
				t.Errorf("a changed file did not load again: %+v", got)
			}
		})
	}
}
