package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
	"github.com/mhersson/aim/internal/store"
)

const (
	noticeSecondInstance = "another AIM is running; this one does not save its layout"
	noticeBadFile        = "the saved layout could not be read; it was kept as state.json.bad"
	noticeBadFileKept    = "the saved layout could not be read"
)

// loadState reads the saved state for a start. A missing file, or fresh, gives
// the state of a new session; a file that cannot be used is set aside as
// state.json.bad. Each notice is one thing the user should know about the start.
func loadState(statePath string, fresh bool, cfg core.Config) (core.State, []string) {
	if fresh {
		return core.InitialState(cfg), nil
	}

	st, rep, err := store.Load(statePath, cfg.Layout)
	if errors.Is(err, os.ErrNotExist) {
		return core.InitialState(cfg), nil
	}

	if err != nil {
		notice := noticeBadFile
		if rerr := os.Rename(statePath, statePath+".bad"); rerr != nil {
			notice = noticeBadFileKept
		}

		return core.InitialState(cfg), []string{notice}
	}

	// The config decides the theme and layout settings, not the file.
	st.Theme, st.Layout = cfg.Theme, cfg.Layout

	var notices []string

	if rep.Version != "" {
		notices = append(notices, fmt.Sprintf("saved layout format %s is not supported; restored the tabs with default layouts", rep.Version))
	}

	switch rep.Defaulted {
	case 0:
	case 1:
		notices = append(notices, "a saved tab layout could not be read; restored it with a default layout")
	default:
		notices = append(notices, fmt.Sprintf("%d saved tab layouts could not be read; restored them with default layouts", rep.Defaulted))
	}

	if n := skippedNotice(rep.Skipped); n != "" {
		notices = append(notices, n)
	}

	return st, notices
}

// skippedNotice names the harnesses of saved agents restored as shells, each
// once; %q escapes control bytes. It is empty for none.
func skippedNotice(ids []string) string {
	if len(ids) == 0 {
		return ""
	}

	var names []string

	for _, id := range slices.Compact(slices.Clone(ids)) {
		names = append(names, strconv.Quote(id))
	}

	return fmt.Sprintf("saved agents of unknown kind restored as shells: %s", strings.Join(names, ", "))
}

// checkResume clears the session of every agent that cannot resume: one with
// no session, or a Claude session whose transcript file does not exist.
func checkResume(st core.State, exists func(path string) bool) (core.State, []string) {
	panes := make(map[core.PaneID]core.Pane, len(st.Panes))

	var fresh []core.Pane

	for id, p := range st.Panes {
		if p.Kind == core.KindAgent && (p.Session == "" || (p.Harness == (harness.Claude{}).ID() && !exists(p.Transcript))) {
			fresh = append(fresh, p)
			p.Session, p.Transcript = "", ""
		}

		panes[id] = p
	}

	st.Panes = panes

	switch len(fresh) {
	case 0:
		return st, nil
	case 1:
		return st, []string{fmt.Sprintf("started a fresh %s in %s: its session could not be resumed", programOf(fresh[0]), cwdName(fresh[0].Cwd))}
	}

	return st, []string{fmt.Sprintf("started %d fresh agents: their sessions could not be resumed", len(fresh))}
}

func programOf(p core.Pane) string {
	if h, ok := harness.ByID(p.Harness); ok {
		return h.Program()
	}

	return "agent"
}

// cwdName is the last element of cwd; an empty cwd means the launch dir.
func cwdName(cwd string) string {
	if cwd == "" {
		return "the launch directory"
	}

	return filepath.Base(cwd)
}

// transcriptExists reports whether the transcript file Claude reported is there.
func transcriptExists(path string) bool {
	if path == "" {
		return false
	}

	_, err := os.Stat(path)

	return err == nil
}
