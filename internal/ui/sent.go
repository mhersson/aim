package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
	"github.com/mhersson/aim/internal/ui/kitty"
)

const sentCopyTimeout = 10 * time.Second

var (
	errSentNotFile = errors.New("not a regular file")
	errSentNoDir   = errors.New("no cache folder for copies")
)

// sentImage is one image an agent sent.
type sentImage struct {
	path   string // as sent, absolute
	copy   string // AIM's copy under the sent dir; "" when not copied
	w, h   int    // from the header; 0 when not decoded
	reason string // why it shows as a name; "" when decoded
}

func (im sentImage) decoded() bool { return im.copy != "" && im.reason == "" }

// sentBatch is the images of one send.
type sentBatch struct {
	seq     int // arrival order over all panes, from 1
	pane    core.PaneID
	name    string // a.cardName at arrival
	tab     int    // 0-based tab index at arrival
	caption string
	at      time.Time
	images  []sentImage
}

type sentPane struct {
	batches []sentBatch     // by seq, oldest first
	turn    map[string]bool // agent panes: paths sent or named since the last prompt; only a Named event is filtered by it
}

type sentState struct {
	dir   string // Options.SentDir; "" means no copies
	seq   int
	panes map[core.PaneID]*sentPane
	view  *sentView // the open viewer
}

// sentCopiedMsg answers onSent's command with the batch once its images are copied.
type sentCopiedMsg struct {
	p     *pane
	batch sentBatch
}

// onSent records an event's images as a batch and returns the command that copies
// them. It does no IO.
func (a *App) onSent(ev harness.Event) tea.Cmd {
	p, ok := a.panes[ev.Pane]
	if !ok {
		return nil
	}

	st := a.store.State()
	paths := ev.Images

	if st.Panes[ev.Pane].Kind == core.KindAgent {
		sp := a.sentPane(ev.Pane)
		if sp.turn == nil {
			sp.turn = map[string]bool{}
		}

		if ev.Named {
			paths = slices.DeleteFunc(slices.Clone(paths), func(s string) bool { return sp.turn[s] })
		}

		for _, s := range paths {
			sp.turn[s] = true
		}
	}

	if len(paths) == 0 {
		return nil
	}

	a.sent.seq++

	tab, _ := st.TabOf(ev.Pane)
	batch := sentBatch{
		seq: a.sent.seq, pane: ev.Pane, name: a.cardName(st, ev.Pane), tab: tab,
		caption: ev.Caption, at: a.clock.Now(),
	}
	dir, ctx, named := a.sent.dir, a.ctx, ev.Named

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, sentCopyTimeout)
		defer cancel()

		batch.images = copySent(ctx, dir, batch.seq, paths, named)

		return sentCopiedMsg{p: p, batch: batch}
	}
}

func (a *App) sentPane(id core.PaneID) *sentPane {
	if a.sent.panes == nil {
		a.sent.panes = map[core.PaneID]*sentPane{}
	}

	sp := a.sent.panes[id]
	if sp == nil {
		sp = &sentPane{}
		a.sent.panes[id] = sp
	}

	return sp
}

// onSentCopied files a copied batch, unless its pane closed meanwhile.
func (a *App) onSentCopied(msg sentCopiedMsg) {
	if a.panes[msg.batch.pane] != msg.p || len(msg.batch.images) == 0 {
		return
	}

	sp := a.sentPane(msg.batch.pane)
	i, _ := slices.BinarySearchFunc(sp.batches, msg.batch.seq, func(b sentBatch, seq int) int { return b.seq - seq })
	sp.batches = slices.Insert(sp.batches, i, msg.batch)
}

// sentBatches returns a pane's batches, oldest first.
func (a *App) sentBatches(id core.PaneID) []sentBatch {
	if sp := a.sent.panes[id]; sp != nil {
		return sp.batches
	}

	return nil
}

// sentAfterUpdate forgets the panes the state no longer holds, and closes the
// viewer of one that went.
func (a *App) sentAfterUpdate() tea.Cmd { //nolint:unparam // shaped like the other after-update hooks, whose commands join a.later
	panes := a.store.State().Panes
	for id := range a.sent.panes {
		if _, ok := panes[id]; !ok {
			delete(a.sent.panes, id)
		}
	}

	if v := a.sent.view; v != nil && len(a.sentBatches(v.pane)) == 0 {
		a.sent.view = nil
	}

	return nil
}

// copySent copies each path into dir as <seq>-<i><ext>, so a later send of the
// same name keeps its own bytes. An entry that cannot be copied keeps its path
// and the reason; with named, one that is not a regular file is left out.
func copySent(ctx context.Context, dir string, seq int, paths []string, named bool) []sentImage {
	var out []sentImage

	if dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			dir = ""
		}
	}

	for i, path := range paths {
		if ctx.Err() != nil {
			break
		}

		im := sentImage{path: path}

		fi, err := os.Stat(path)

		switch {
		case err != nil && named:
			continue
		case err != nil:
			im.reason = reasonOf(err)
		case !fi.Mode().IsRegular() && named:
			continue
		case !fi.Mode().IsRegular():
			im.reason = errSentNotFile.Error()
		case dir == "":
			im.reason = errSentNoDir.Error()
		default:
			im.copy, im.w, im.h, err = copyOne(dir, seq, i, path)
			if err != nil {
				im.reason = reasonOf(err)
			}
		}

		out = append(out, im)
	}

	return out
}

func reasonOf(err error) string {
	if pe, ok := errors.AsType[*fs.PathError](err); ok {
		return pe.Err.Error()
	}

	return err.Error()
}

// copyOne reads path, checks it is an image and writes it into dir; the copy is
// named only when the whole of it is there.
func copyOne(dir string, seq, i int, path string) (string, int, int, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", 0, 0, fmt.Errorf("open: %w", err)
	}

	defer func() { _ = f.Close() }()

	fi, err := f.Stat()
	if err != nil {
		return "", 0, 0, fmt.Errorf("stat: %w", err)
	}

	if !fi.Mode().IsRegular() {
		return "", 0, 0, errSentNotFile
	}

	data, err := io.ReadAll(io.LimitReader(f, pictureMaxBytes+1))
	if err != nil {
		return "", 0, 0, fmt.Errorf("read: %w", err)
	}

	if len(data) > pictureMaxBytes {
		return "", 0, 0, errPictureTooBig
	}

	w, h, err := kitty.Size(data)
	if err != nil {
		return "", 0, 0, err
	}

	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return "", 0, 0, fmt.Errorf("create copy: %w", err)
	}

	_, werr := tmp.Write(data)
	cerr := tmp.Close()

	dst := filepath.Join(dir, strconv.Itoa(seq)+"-"+strconv.Itoa(i)+strings.ToLower(filepath.Ext(path)))

	if err := errors.Join(werr, cerr, os.Chmod(tmp.Name(), 0o600), os.Rename(tmp.Name(), dst)); err != nil {
		_ = os.Remove(tmp.Name())

		return "", 0, 0, fmt.Errorf("write copy: %w", err)
	}

	return dst, w, h, nil
}
