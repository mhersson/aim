package git

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// listRig takes a worktree's turns step by step and remembers each turn's
// number, so a row can name turns by the order they were taken in.
type listRig struct {
	t   *testing.T
	dir string
	ns  []int
}

func (r *listRig) snap() {
	r.t.Helper()
	r.ns = append(r.ns, snap(r.t, r.dir, sessA, SnapshotOptions{}).N)
}

func (r *listRig) markStart() {
	r.t.Helper()

	turn, err := MarkMissing(context.Background(), r.dir, sessA, 0, false)
	must(r.t, err)

	r.ns = append(r.ns, turn.N)
}

func (r *listRig) markEnd() {
	r.t.Helper()

	_, err := MarkMissing(context.Background(), r.dir, sessA, r.ns[len(r.ns)-1], true)
	must(r.t, err)
}

func (r *listRig) write(content string) {
	r.t.Helper()
	writeFile(r.t, r.dir, "a.txt", content)
}

func TestTurnListKeepsTurnsThatChangedTheWorktree(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		do   func(r *listRig)
		want []int // the turns kept, by the order they were taken in
		live bool
	}{
		{"a closed turn that changed nothing is left out", func(r *listRig) {
			r.snap()
			r.snap()
		}, nil, false},
		{"a closed turn that changed a file is kept", func(r *listRig) {
			r.snap()
			r.write("one\n")
			r.snap()
		}, []int{1}, false},
		{"a start marker is kept", func(r *listRig) {
			r.markStart()
		}, []int{1}, false},
		{"an end marker is kept", func(r *listRig) {
			r.snap()
			r.markEnd()
		}, []int{1}, false},
		{"an open current turn is left out while the working tree equals its start", func(r *listRig) {
			r.write("one\n")
			r.snap()
		}, nil, false},
		{"an open current turn is live once a file changes", func(r *listRig) {
			r.snap()
			r.write("one\n")
		}, []int{1}, true},
		{"an open turn behind a later one is kept, not live", func(r *listRig) {
			r.snap()
			r.markStart()
			r.write("one\n")
		}, []int{1, 2}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := &listRig{t: t, dir: seedRepo(t)}
			tt.do(r)

			got, err := testEngine(t).TurnList(context.Background(), r.dir, sessA)
			if err != nil {
				t.Fatal(err)
			}

			var want []int
			for _, i := range tt.want {
				want = append(want, r.ns[i-1])
			}

			var have []int
			for _, turn := range got.Turns {
				have = append(have, turn.N)
			}

			if !slices.Equal(have, want) || got.Live != tt.live {
				t.Errorf("turns = %v live = %v, want %v live = %v", have, got.Live, want, tt.live)
			}
		})
	}
}

func TestTurnListAtComparesWithTheTreeItIsGiven(t *testing.T) {
	t.Parallel()

	dir := seedRepo(t)
	start := snap(t, dir, sessA, SnapshotOptions{})

	r, err := Open(context.Background(), dir)
	must(t, err)

	wt, err := WorkingTree(context.Background(), r, TreeOptions{})
	must(t, err)

	tests := []struct {
		name     string
		tree     string
		wantLive bool
	}{
		{"the working tree's id counts like TurnList", wt.Tree, false},
		{"another id makes the open turn live, whatever the files hold", "0000000000000000000000000000000000000000", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, logPath := wrapGit(t, "")

			got, err := testEngine(t).TurnListAt(ctx, dir, sessA, tt.tree)
			must(t, err)

			if calls := wrappedCalls(t, logPath); strings.Contains(calls, "write-tree") {
				t.Errorf("TurnListAt snapshotted the working tree; git calls:\n%s", calls)
			}

			if got.Live != tt.wantLive || len(got.Turns) != map[bool]int{false: 0, true: 1}[tt.wantLive] {
				t.Errorf("turns = %d live = %v, want live = %v (start tree %s)", len(got.Turns), got.Live, tt.wantLive, start.Tree)
			}
		})
	}
}

func TestDiffTurnCountsOnlyTurnsThatChanged(t *testing.T) {
	t.Parallel()

	dir := seedRepo(t)
	writeFile(t, dir, "a.txt", "before\n")
	snap(t, dir, sessA, SnapshotOptions{})
	writeFile(t, dir, "a.txt", "one\n")
	snap(t, dir, sessA, SnapshotOptions{})
	snap(t, dir, sessA, SnapshotOptions{})
	writeFile(t, dir, "b.txt", "three\n")

	e := testEngine(t)
	ctx := context.Background()
	src := func(pos int) Source { return TurnSource{Worktree: dir, Session: sessA, Turn: pos} }

	first := diffOK(t, e, src(1))
	if first.Turn != 1 || first.Turns != 2 || first.Live || pathsOf(first)[0] != "a.txt" {
		t.Errorf("position 1: Turn=%d Turns=%d Live=%v files=%v", first.Turn, first.Turns, first.Live, pathsOf(first))
	}

	second := diffOK(t, e, src(2))
	if second.Turn != 2 || second.Turns != 2 || !second.Live || len(second.Files) != 1 || second.Files[0].Path != "b.txt" {
		t.Errorf("position 2: Turn=%d Turns=%d Live=%v files=%v", second.Turn, second.Turns, second.Live, pathsOf(second))
	}

	if _, err := e.Diff(ctx, src(3)); !errors.Is(err, ErrNoSnapshot) {
		t.Errorf("position 3: err = %v, want ErrNoSnapshot", err)
	}

	if latest := diffOK(t, e, src(0)); latest.Turn != 2 || latest.Turns != 2 {
		t.Errorf("latest: Turn=%d Turns=%d", latest.Turn, latest.Turns)
	}
}

// Nothing that changes after a turn's end enters it.
func TestFinishedTurnIgnoresLaterChanges(t *testing.T) {
	t.Parallel()

	dir := seedRepo(t)
	snap(t, dir, sessA, SnapshotOptions{})
	writeFile(t, dir, "a.txt", "during the turn\n")

	end, err := EndTurn(context.Background(), dir, sessA, 1, SnapshotOptions{TempDir: t.TempDir()})
	must(t, err)

	writeFile(t, dir, "b.txt", "after the end\n")
	writeFile(t, dir, "new.txt", "new after the end\n")

	res := diffOK(t, testEngine(t), TurnSource{Worktree: dir, Session: sessA})

	if res.Live || res.Turn != 1 || res.Turns != 1 || res.New != end.EndTree {
		t.Errorf("Live=%v Turn=%d Turns=%d New=%s, want turn 1 of 1 closed at %s", res.Live, res.Turn, res.Turns, res.New, end.EndTree)
	}

	if got := pathsOf(res); len(got) != 1 || got[0] != "a.txt" {
		t.Errorf("files = %v, want a.txt alone", got)
	}
}
