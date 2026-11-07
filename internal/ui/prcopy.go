package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/git"
)

// heldWant is the pull request the view shows, asked for again as itself: a
// copy's HEAD means nothing, so its branch is never looked up.
func (v *reviewView) heldWant(root string) *prWant {
	w := &prWant{repo: v.pr.Repo, number: v.pr.Number, root: root, base: v.prBase}
	if v.pr.Copy {
		w.inCopy, w.branch = true, v.prBranch
	}

	return w
}

// prCopyRoot is the directory of the private copy of the repository keyed
// "host/owner/name". It is a path join, safe on the update loop.
func (a *App) prCopyRoot(repoKey string) (string, bool) {
	r, ok := repoOfKey(repoKey)
	if !ok {
		return "", false
	}

	return git.PRCopyDir(a.prCopies, r.Host, r.Owner, r.Name)
}

// prCopyGate lets one fetch at a time work in a copy. Its zero value is ready.
type prCopyGate struct {
	mu    sync.Mutex
	slots map[string]chan struct{}
}

// acquire waits for root's turn, or for ctx to end. The release it returns
// hands the turn on.
func (g *prCopyGate) acquire(ctx context.Context, root string) (release func(), err error) {
	root = resolvedRoot(root)

	g.mu.Lock()

	if g.slots == nil {
		g.slots = map[string]chan struct{}{}
	}

	slot := g.slots[root]
	if slot == nil {
		slot = make(chan struct{}, 1)
		g.slots[root] = slot
	}

	g.mu.Unlock()

	select {
	case slot <- struct{}{}:
		return func() { <-slot }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("wait for the copy: %w", ctx.Err())
	}
}

// resolvedRoot is root with the symlinks of its deepest existing part resolved,
// so the spellings of one copy, before and after it exists, share a slot. It
// reads the disk, so it runs off the update loop.
func resolvedRoot(root string) string {
	var rest []string

	for p := root; ; {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(append([]string{r}, rest...)...)
		}

		parent := filepath.Dir(p)
		if parent == p {
			return root
		}

		rest = append([]string{filepath.Base(p)}, rest...)
		p = parent
	}
}

// fetchPRCopy brings pull request number into the copy at place.root: its
// head, then the merge base of the head it fetched, then the files' contents.
// It returns the copy's root.
func fetchPRCopy(ctx context.Context, g Git, reads prReads, gate *prCopyGate, place prPlace, number int, base string) (string, error) {
	token, err := reads.GitToken(ctx, place.repo.Host)
	if err != nil {
		return "", fmt.Errorf("token for %s: %w", place.repo.Host, err)
	}

	release, err := gate.acquire(ctx, place.root)
	if err != nil {
		return "", err
	}
	defer release()

	origin := "https://" + place.repo.Host + "/" + place.repo.Owner + "/" + place.repo.Name + ".git"

	c, err := g.OpenPRCopy(ctx, place.root, origin)
	if err != nil {
		return "", err
	}

	head, err := g.FetchPRCopyHead(ctx, c, number, token)
	if err != nil {
		return "", err
	}

	mergeBase, err := reads.MergeBase(ctx, place.repo, base, head)
	if err != nil {
		return "", fmt.Errorf("merge base: %w", err)
	}

	if err := g.FetchPRCopyBase(ctx, c, number, mergeBase, token); err != nil {
		return "", err
	}

	return c.Repo.Root, nil
}

// fetchCopyCmd fetches the pull request into its copy off the update loop, under
// the same limit as a fetch into a worktree.
func (a *App) fetchCopyCmd(ask prAsk, place prPlace, number int) tea.Cmd {
	var (
		appCtx, g, reads = a.ctx, a.git, a.reads
		gate             = &a.prs.copyGate
	)

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(appCtx, prFetchTimeout)
		defer cancel()

		out := prFetchedMsg{prAsk: ask, place: place, number: number}

		root, err := fetchPRCopy(ctx, g, reads, gate, place, number, ask.base)
		if err != nil {
			out.err = err

			return out
		}

		out.place.root = root

		return out
	}
}
