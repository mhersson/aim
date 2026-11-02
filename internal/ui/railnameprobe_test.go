package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
)

// rewriteTranscript writes a synthetic transcript whose only custom-title entry
// names the session name, among other lines.
func rewriteTranscript(t *testing.T, path, name string) {
	t.Helper()

	title, err := json.Marshal(name)
	if err != nil {
		t.Fatal(err)
	}

	write(t, path, `{"type":"user","message":"hi"}`+"\n"+
		`{"type":"custom-title","customTitle":`+string(title)+`,"sessionId":"s"}`+"\n"+
		`{"type":"assistant","message":"hello"}`+"\n")
}

// transcriptNamed is a new transcript file for a session named name.
func transcriptNamed(t *testing.T, name string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "session.jsonl")
	rewriteTranscript(t, path, name)

	return path
}

// session gives an agent a session and its transcript, as a session start does.
func (r *railApp) session(t *testing.T, id core.PaneID, transcript string) {
	t.Helper()
	openIn(t, r.App, core.SetAgentSession{ID: id, Session: core.SessionID("s-" + string(id)), Transcript: transcript})
}

func (r *railApp) name(t *testing.T, id core.PaneID) string {
	t.Helper()

	c, ok := r.card(id)
	if !ok {
		t.Fatalf("no card for %s", id)
	}

	return c.name
}

func TestRailNameProbeOnOpen(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, false)
	dir := r.repo(t, "proj").Root()
	named := r.addAgent(t, false, dir)
	bare := r.addAgent(t, true, dir)
	r.session(t, named, transcriptNamed(t, "muxr-orchestrator"))

	// With the rail closed only the heads are probed.
	if msgs := r.deliver(r.settle()); countOf[railHeadMsg](msgs) != 2 || len(msgs) != 2 {
		t.Fatalf("settling with the rail closed ran %v, want two head probes", msgs)
	}

	msgs := r.deliver(r.railOpened())

	// Only the agent that has a transcript is asked.
	if n := countOf[railNameMsg](msgs); n != 1 {
		t.Fatalf("open ran %d name probes, want 1: %v", n, msgs)
	}

	if got := r.name(t, named); got != "muxr-orchestrator" {
		t.Errorf("name = %q, want muxr-orchestrator", got)
	}

	if got := r.name(t, bare); got != "" {
		t.Errorf("an agent with no session has the name %q", got)
	}
}

func TestRailNameProbeTriggers(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, false)
	id := r.addAgent(t, false, r.repo(t, "proj").Root())
	r.openRail()

	transcript := transcriptNamed(t, "first")

	// A session start reports the transcript; the name is read at once.
	r.clk.Advance(6 * time.Second)

	start := harness.Event{Pane: id, Name: "SessionStart", Start: true, Session: "s-1", Transcript: transcript, State: core.AgentIdle}
	cmd := hook(r.App, start)

	if got := r.name(t, id); got != "" {
		t.Errorf("the hook read the file on the update loop: name = %q", got)
	}

	if n := countOf[railNameMsg](r.deliver(cmd)); n != 1 {
		t.Fatalf("a session start ran %d name probes, want 1", n)
	}

	if got := r.name(t, id); got != "first" {
		t.Errorf("name after the session start = %q, want first", got)
	}

	// A rename happens between prompts: the next idle hook sees it.
	rewriteTranscript(t, transcript, "second")
	r.clk.Advance(6 * time.Second)

	if n := countOf[railNameMsg](r.deliver(hook(r.App, idleHook(id)))); n != 1 {
		t.Fatalf("an idle hook ran %d name probes, want 1", n)
	}

	if got := r.name(t, id); got != "second" {
		t.Errorf("name after the idle hook = %q, want second", got)
	}

	// A hook that keeps the agent working reads nothing.
	rewriteTranscript(t, transcript, "third")
	r.clk.Advance(6 * time.Second)

	working := harness.Event{Pane: id, Name: "PostToolUse", State: core.AgentWorking}
	if n := countOf[railNameMsg](r.deliver(hook(r.App, working))); n != 0 {
		t.Errorf("a working hook ran %d name probes", n)
	}

	if got := r.name(t, id); got != "second" {
		t.Errorf("name after a working hook = %q, want second", got)
	}
}

// A session start replaces the session: the probe it raises reads the
// transcript the hook reports, not the earlier session's.
func TestRailNameProbeAfterASessionStartReadsTheNewTranscript(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, false)
	id := r.addAgent(t, false, r.repo(t, "proj").Root())
	r.session(t, id, transcriptNamed(t, "old"))
	r.openRail()

	if got := r.name(t, id); got != "old" {
		t.Fatalf("setup name = %q", got)
	}

	r.clk.Advance(6 * time.Second)

	next := transcriptNamed(t, "new")
	start := harness.Event{Pane: id, Name: "SessionStart", Start: true, Session: "s-2", Transcript: next, State: core.AgentIdle}

	var read []string

	for _, m := range flatten(hook(r.App, start)) {
		if nm, ok := m.(railNameMsg); ok {
			read = append(read, nm.transcript)
		}
	}

	if len(read) != 1 || read[0] != next {
		t.Errorf("the session start read %q, want only %q", read, next)
	}
}

func TestRailNameProbeForAPaneThatBecomesAnAgent(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, false)
	r.openRail()

	id := r.addAgent(t, false, r.repo(t, "proj").Root())
	r.session(t, id, transcriptNamed(t, "newcomer"))

	if n := countOf[railNameMsg](r.deliver(r.settle())); n != 1 {
		t.Fatalf("a new agent with a transcript ran %d name probes, want 1", n)
	}

	if got := r.name(t, id); got != "newcomer" {
		t.Errorf("name = %q, want newcomer", got)
	}

	// Nothing changed since: the next update asks nothing.
	if msgs := r.deliver(r.settle()); countOf[railNameMsg](msgs) != 0 {
		t.Errorf("an update with nothing new ran %v", msgs)
	}
}

func TestRailNameProbeInterval(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, false)
	id := r.addAgent(t, false, r.repo(t, "proj").Root())
	transcript := transcriptNamed(t, "first")
	r.session(t, id, transcript)
	r.openRail()

	r.clk.Advance(time.Second)
	rewriteTranscript(t, transcript, "second")

	// Inside the interval one delayed probe is scheduled, and nothing runs yet.
	r.ticks.delays = nil
	msgs := flatten(r.railFire(r.store.State(), id, probeName))

	if len(msgs) != 1 || countOf[railDueMsg](msgs) != 1 {
		t.Fatalf("a trigger inside the interval ran %v, want one delayed probe", msgs)
	}

	if len(r.ticks.delays) != 1 || r.ticks.delays[0] != 4*time.Second {
		t.Errorf("delays = %v, want [4s]", r.ticks.delays)
	}

	// More triggers while it waits add nothing.
	if cmd := r.railFire(r.store.State(), id, probeName); cmd != nil {
		t.Errorf("a second trigger scheduled %v", flatten(cmd))
	}

	if got := r.name(t, id); got != "first" {
		t.Errorf("name before the interval ended = %q, want first", got)
	}

	r.clk.Set(t0.Add(5 * time.Second))

	due, ok := msgs[0].(railDueMsg)
	if !ok || due.kind != probeName {
		t.Fatalf("delayed message = %+v, want a name due message", msgs[0])
	}

	if n := countOf[railNameMsg](r.deliver(r.step(due))); n != 1 {
		t.Fatalf("the due message ran %d name probes, want 1", n)
	}

	if got := r.name(t, id); got != "second" {
		t.Errorf("name after the interval = %q, want second", got)
	}

	// Nothing pending: a late due message does nothing.
	if msgs := flatten(r.step(due)); len(msgs) != 0 {
		t.Errorf("a due message with nothing pending ran %v", msgs)
	}
}

func TestRailNameTriggerDuringProbeRunsOneAfter(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, false)
	id := r.addAgent(t, false, r.repo(t, "proj").Root())
	r.session(t, id, transcriptNamed(t, "first"))

	cmd := r.railOpened()

	r.clk.Advance(6 * time.Second)

	// The first probe has not answered: a trigger waits for it.
	if cmd := r.railFire(r.store.State(), id, probeName); cmd != nil {
		t.Fatalf("a trigger during a probe ran %v", flatten(cmd))
	}

	var follow []tea.Msg

	for _, m := range flatten(cmd) {
		if _, ok := m.(railNameMsg); ok {
			follow = flatten(r.step(m))
		} else {
			r.step(m)
		}
	}

	if n := countOf[railNameMsg](follow); n != 1 {
		t.Errorf("follow-up name probes = %d, want 1", n)
	}
}

func TestRailNameProbeDropsStaleResults(t *testing.T) {
	t.Parallel()

	t.Run("pane closed", func(t *testing.T) {
		r := newRailApp(t, false)
		id := r.addAgent(t, false, r.repo(t, "proj").Root())
		r.session(t, id, transcriptNamed(t, "gone"))
		msgs := flatten(r.railOpened())

		openIn(t, r.App, core.ClosePane{ID: id})
		r.settle()

		for _, m := range msgs {
			r.step(m)
		}

		if _, ok := r.rail.panes[id]; ok {
			t.Error("a late answer revived the closed pane's entry")
		}
	})

	t.Run("session changed", func(t *testing.T) {
		r := newRailApp(t, false)
		id := r.addAgent(t, false, r.repo(t, "proj").Root())
		r.session(t, id, transcriptNamed(t, "old"))
		msgs := flatten(r.railOpened())

		next := transcriptNamed(t, "new")
		r.session(t, id, next)

		var follow []tea.Msg

		for _, m := range msgs {
			if _, ok := m.(railNameMsg); ok {
				follow = flatten(r.step(m))
			} else {
				r.step(m)
			}
		}

		if got := r.name(t, id); got != "" {
			t.Errorf("the answer for the old transcript was applied: name = %q", got)
		}

		// The new transcript gets its own probe once the interval ends.
		r.clk.Advance(6 * time.Second)

		for _, m := range follow {
			r.deliver(r.step(m))
		}

		if got := r.name(t, id); got != "new" {
			t.Errorf("name = %q, want new", got)
		}
	})

	t.Run("rail closed", func(t *testing.T) {
		r := newRailApp(t, false)
		id := r.addAgent(t, false, r.repo(t, "proj").Root())
		r.session(t, id, transcriptNamed(t, "late"))
		cmd := r.railOpened()
		r.railClosed()

		for _, m := range flatten(cmd) {
			if nm, ok := m.(railNameMsg); ok && !errors.Is(nm.err, context.Canceled) {
				t.Errorf("a probe of a closed rail ended with %v, want a cancel", nm.err)
			}

			r.step(m)
		}

		if rp := r.rail.panes[id]; rp == nil || rp.name.text != "" {
			t.Error("an answer after the rail closed was applied")
		}
	})

	t.Run("a hook with the rail closed", func(t *testing.T) {
		r := newRailApp(t, false)
		id := r.addAgent(t, false, r.repo(t, "proj").Root())
		r.session(t, id, transcriptNamed(t, "x"))

		if msgs := flatten(hook(r.App, idleHook(id))); countOf[railNameMsg](msgs) != 0 {
			t.Errorf("a hook read a transcript with the rail closed: %v", msgs)
		}
	})
}

func TestRailNameProbeFailures(t *testing.T) {
	t.Parallel()

	setup := func(t *testing.T) (*railApp, core.PaneID, string) {
		t.Helper()

		r := newRailApp(t, false)
		id := r.addAgent(t, false, r.repo(t, "proj").Root())
		transcript := transcriptNamed(t, "kept")
		r.session(t, id, transcript)
		r.openRail()

		if got := r.name(t, id); got != "kept" {
			t.Fatalf("setup name = %q", got)
		}

		r.clk.Advance(6 * time.Second)

		return r, id, transcript
	}

	t.Run("a read that fails clears the name", func(t *testing.T) {
		r, id, transcript := setup(t)

		// A directory where the file was cannot be read.
		if err := os.Remove(transcript); err != nil {
			t.Fatal(err)
		}

		if err := os.Mkdir(transcript, 0o755); err != nil {
			t.Fatal(err)
		}

		r.deliver(hook(r.App, idleHook(id)))

		if got := r.name(t, id); got != "" {
			t.Errorf("name after a failed read = %q, want none", got)
		}
	})

	t.Run("a file that is gone means no name", func(t *testing.T) {
		r, id, transcript := setup(t)

		if err := os.Remove(transcript); err != nil {
			t.Fatal(err)
		}

		r.deliver(hook(r.App, idleHook(id)))

		if got := r.name(t, id); got != "" {
			t.Errorf("name after the file went = %q, want none", got)
		}
	})

	t.Run("a read that finds no entry clears the name", func(t *testing.T) {
		r, id, transcript := setup(t)
		write(t, transcript, `{"type":"user","message":"hi"}`+"\n"+`{"type":"assistant","message":"hello"}`+"\n")

		r.deliver(hook(r.App, idleHook(id)))

		if got := r.name(t, id); got != "" {
			t.Errorf("name after a read with no entry = %q, want none", got)
		}
	})

	// A probe with no answer says nothing about the name, so the card keeps it.
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"deadline exceeded inside the reader's error", fmt.Errorf("harness: read transcript: %w", context.DeadlineExceeded)},
		{"cancelled", context.Canceled},
	} {
		t.Run("a probe that gets no answer keeps the name: "+tc.name, func(t *testing.T) {
			r, id, transcript := setup(t)

			r.step(railNameMsg{pane: id, transcript: transcript, gen: r.rail.gen, err: tc.err})

			if got := r.name(t, id); got != "kept" {
				t.Errorf("name after the probe gave no answer = %q, want kept", got)
			}

			// The next probe that completes still replaces it.
			rewriteTranscript(t, transcript, "renamed")
			r.deliver(hook(r.App, idleHook(id)))

			if got := r.name(t, id); got != "renamed" {
				t.Errorf("name after the next read = %q, want renamed", got)
			}
		})
	}

	t.Run("a probe with no answer for another session does not show this name", func(t *testing.T) {
		r, id, _ := setup(t)

		other := transcriptNamed(t, "other")
		r.session(t, id, other)
		r.step(railNameMsg{pane: id, transcript: other, gen: r.rail.gen, err: context.DeadlineExceeded})

		if got := r.name(t, id); got != "" {
			t.Errorf("name = %q, want none: it belongs to the earlier session", got)
		}
	})

	t.Run("a failed read of another session does not keep this name", func(t *testing.T) {
		r, id, _ := setup(t)
		r.session(t, id, t.TempDir()) // the new transcript is unreadable

		r.deliver(r.settle())
		r.deliver(hook(r.App, idleHook(id)))

		if got := r.name(t, id); got != "" {
			t.Errorf("name = %q, want none: it belongs to the earlier session", got)
		}
	})
}

func TestRailNameProbeSharesTheProbeSlots(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, false)
	id := r.addAgent(t, false, r.repo(t, "proj").Root())
	r.openRail()

	transcript := transcriptNamed(t, "early")
	r.session(t, id, transcript)

	cmd := r.railFire(r.store.State(), id, probeName)
	if cmd == nil {
		t.Fatal("no name probe started")
	}

	for range railProbeMax {
		r.probeSem <- struct{}{}
	}

	done := make(chan tea.Msg, 1)

	go func() { done <- cmd() }()

	// The transcript is read only once a slot is free, so it holds what it says now.
	rewriteTranscript(t, transcript, "waited")
	<-r.probeSem

	select {
	case msg := <-done:
		if nm, ok := msg.(railNameMsg); !ok || nm.name != "waited" {
			t.Errorf("answer = %+v, want the name written while every slot was taken", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the name probe did not run once a slot was free")
	}

	for range railProbeMax - 1 {
		<-r.probeSem
	}
}

func TestRailNameProbeCleansWhatItReads(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, false)
	id := r.addAgent(t, false, r.repo(t, "proj").Root())
	r.session(t, id, transcriptNamed(t, "ok\x1b]0;pwn\x07\u202eed\u009b2J"))
	r.openRail()

	if got := r.name(t, id); got != "ok]0;pwned2J" {
		t.Errorf("name = %q, want the hostile characters dropped", got)
	}

	view := r.View().Content
	for _, bad := range []string{"\x1b]", "\u202e", "\u009b", "\x07"} {
		if strings.Contains(view, bad) {
			t.Errorf("the frame holds %q", bad)
		}
	}
}
