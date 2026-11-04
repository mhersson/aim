// Package store reads, validates and writes the state file, and saves the
// theme picker's theme to the config file. The state file is untrusted input:
// Decode never panics and nothing it returns needs escaping beyond what core
// already quotes.
package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mhersson/aim/internal/core"
)

// Version is the file format Encode writes and Decode reads without fallback.
const Version = 2

// ErrInvalid reports a state file whose tabs or panes cannot be used.
var ErrInvalid = errors.New("invalid state file")

const (
	maxFileSize   = 16 << 20
	maxNameRunes  = 32
	maxVersionLen = 16
	maxNumber     = 999_999_999 // keeps NextID + 1 far from overflow
	harnessClaude = "claude-code"
)

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type fileJSON struct {
	Version json.RawMessage     `json:"version"`
	Active  int                 `json:"active"`
	Rail    bool                `json:"rail,omitempty"`
	Tabs    []tabJSON           `json:"tabs"`
	Panes   map[string]paneJSON `json:"panes"`
}

type tabJSON struct {
	ID      string          `json:"id"`
	Name    string          `json:"name,omitempty"`
	Focused string          `json:"focused"`
	Zoomed  bool            `json:"zoomed,omitempty"`
	Mode    json.RawMessage `json:"mode,omitempty"` // raw so a non-string reads as unknown
	PaneIDs []string        `json:"pane_ids"`
	Layout  json.RawMessage `json:"layout,omitempty"` // bsp only; null for an empty tab

	// Per-mode fields stay raw so a bad one defaults only its tab.
	MasterRatio json.RawMessage `json:"master_ratio,omitempty"`
	Columns     json.RawMessage `json:"columns,omitempty"`
	First       json.RawMessage `json:"first,omitempty"`

	Review json.RawMessage `json:"review,omitempty"` // raw so a bad drawer drops alone
}

type reviewJSON struct {
	Pane string `json:"pane"`
	Mode string `json:"mode"`
	Tree bool   `json:"tree,omitempty"`
}

type paneJSON struct {
	Kind       string `json:"kind"`
	Cwd        string `json:"cwd,omitempty"`
	Harness    string `json:"harness,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	Transcript string `json:"transcript,omitempty"`

	Plan json.RawMessage `json:"plan,omitempty"` // raw so a bad plan drops alone
}

type planJSON struct {
	Path   string    `json:"path"`
	Root   string    `json:"root,omitempty"`
	Kind   string    `json:"kind"`
	At     time.Time `json:"at"`
	Unread bool      `json:"unread,omitempty"`
}

var planKinds = map[core.PlanKind]string{core.PlanMode: "mode", core.PlanFile: "file"}

// Report says which fallbacks Decode used, for the start-up notices.
type Report struct {
	Version   string // "" for the current version; else the version as written, or "unknown" when missing
	Defaulted int    // tabs whose layout could not be read and got the default
}

// Encode returns the state file for st: indented, deterministic, ratios
// rounded to 0.01. Only panes that sit in a tab are written.
func Encode(st core.State) ([]byte, error) {
	tabs := st.Tabs
	if len(tabs) == 0 {
		tabs = []core.Tab{{ID: "t1"}}
	}

	if st.Active < 0 || st.Active >= len(tabs) {
		return nil, fmt.Errorf("active tab %d is out of range for %d tabs", st.Active, len(tabs))
	}

	f := fileJSON{
		Version: json.RawMessage(strconv.Itoa(Version)),
		Active:  st.Active,
		Rail:    st.Rail,
		Panes:   map[string]paneJSON{},
	}
	for _, t := range tabs {
		ids := t.Panes

		jt := tabJSON{
			ID: string(t.ID), Name: t.Name, Focused: string(t.Focus), Zoomed: t.Zoomed,
			PaneIDs: make([]string, 0, len(ids)),
		}
		if t.Review != nil {
			raw, err := json.Marshal(reviewJSON{Pane: string(t.Review.Pane), Mode: string(t.Review.Mode), Tree: t.Review.Tree})
			if err != nil {
				return nil, fmt.Errorf("tab %s: review: %w", t.ID, err)
			}

			jt.Review = raw
		}

		if err := encodeMode(&jt, t); err != nil {
			return nil, fmt.Errorf("tab %s: %w", t.ID, err)
		}

		for _, id := range ids {
			p, ok := st.Panes[id]
			if !ok {
				return nil, fmt.Errorf("tab %s: pane %s has no metadata", t.ID, id)
			}

			jp, err := encodePane(p)
			if err != nil {
				return nil, fmt.Errorf("pane %s: %w", id, err)
			}

			jt.PaneIDs = append(jt.PaneIDs, string(id))
			f.Panes[string(id)] = jp
		}

		f.Tabs = append(f.Tabs, jt)
	}

	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding state: %w", err)
	}

	return append(data, '\n'), nil
}

// encodeMode writes the tab's mode and the fields that belong to it. A tab
// that has panes but no mode is BSP when it has a tree.
func encodeMode(jt *tabJSON, t core.Tab) error {
	mode := t.Mode
	if mode == "" && t.Layout != nil {
		mode = core.ModeBSP
	}

	if mode != "" {
		if err := setRaw(&jt.Mode, string(mode)); err != nil {
			return err
		}
	}

	switch mode {
	case core.ModeBSP:
		layout, err := encodeLayout(t.Layout)
		jt.Layout = layout

		return err
	case core.ModeMaster:
		if t.Master == 0 {
			return nil
		}

		if !finite(t.Master) {
			return errors.New("master ratio is not a finite number")
		}

		return setRaw(&jt.MasterRatio, normRatio(t.Master))
	case core.ModeScrolling:
		cols, err := encodeColumns(t.Columns)
		if err != nil {
			return err
		}

		if len(cols) > 0 {
			if err := setRaw(&jt.Columns, cols); err != nil {
				return err
			}
		}

		if t.First != 0 {
			return setRaw(&jt.First, t.First)
		}

		return nil
	case "":
		if t.Empty() {
			jt.Layout = json.RawMessage("null")

			return nil
		}
	}

	return fmt.Errorf("unknown layout mode %q", mode)
}

func setRaw(dst *json.RawMessage, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encoding layout: %w", err)
	}

	*dst = raw

	return nil
}

func encodePane(p core.Pane) (paneJSON, error) {
	switch p.Kind {
	case core.KindShell:
		return paneJSON{Kind: string(p.Kind), Cwd: p.Cwd}, nil
	case core.KindAgent:
		if p.Harness == "" {
			// Decode requires a harness; a file it would reject must never be written.
			return paneJSON{Kind: string(core.KindShell), Cwd: p.Cwd}, nil
		}

		jp := paneJSON{
			Kind: string(p.Kind), Cwd: p.Cwd, Harness: p.Harness,
			SessionID: string(p.Session), Transcript: p.Transcript,
		}

		if p.Plan.Valid() {
			raw, err := json.Marshal(planJSON{
				Path: p.Plan.Path, Root: p.Plan.Root, Kind: planKinds[p.Plan.Kind], At: p.Plan.At.UTC(), Unread: p.Plan.Unread,
			})
			if err != nil {
				return paneJSON{}, fmt.Errorf("encoding plan: %w", err)
			}

			jp.Plan = raw
		}

		return jp, nil
	}

	return paneJSON{}, fmt.Errorf("unknown kind %q", p.Kind)
}

// Load reads and decodes the state file at path. A missing file wraps
// os.ErrNotExist.
func Load(path string, lc core.LayoutConfig) (core.State, Report, error) {
	f, err := os.Open(path)
	if err != nil {
		return core.State{}, Report{}, fmt.Errorf("opening state file: %w", err)
	}
	defer func() { _ = f.Close() }()

	data, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return core.State{}, Report{}, fmt.Errorf("reading state file: %w", err)
	}

	return Decode(data, lc)
}

// Decode parses and validates a state file. Tab and pane failures wrap
// ErrInvalid; a version other than the current one, or a tab whose mode or
// layout cannot be read, falls back to the default layout from lc and is
// reported. Theme is left empty and every pane's State is AgentUnknown.
func Decode(data []byte, lc core.LayoutConfig) (core.State, Report, error) {
	invalid := func(format string, args ...any) (core.State, Report, error) {
		return core.State{}, Report{}, fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
	}
	if len(data) > maxFileSize {
		return invalid("file is larger than %d bytes", maxFileSize)
	}

	var f fileJSON
	if err := json.Unmarshal(data, &f); err != nil {
		return invalid("%v", err)
	}

	if len(f.Panes) > core.MaxPanes {
		return invalid("%d panes, at most %d", len(f.Panes), core.MaxPanes)
	}

	var rep Report

	current := isCurrentVersion(f.Version)
	if !current {
		rep.Version = versionText(f.Version)
	}

	st := core.State{Panes: make(map[core.PaneID]core.Pane, len(f.Panes))}
	for key, jp := range f.Panes {
		n, err := parseNumbered(key, "p")
		if err != nil {
			return invalid("pane id %q: %v", key, err)
		}

		p, err := decodePane(jp)
		if err != nil {
			return invalid("pane %s: %v", key, err)
		}

		st.Panes[core.PaneID(key)] = p
		st.NextID = max(st.NextID, n)
	}

	if len(f.Tabs) == 0 {
		return invalid("no tabs")
	}

	if f.Active < 0 || f.Active >= len(f.Tabs) {
		return invalid("active tab %d is out of range for %d tabs", f.Active, len(f.Tabs))
	}

	st.Active = f.Active
	st.Rail = current && f.Rail

	owner := map[core.PaneID]string{}
	tabIDs := map[string]bool{}

	for _, jt := range f.Tabs {
		n, err := parseNumbered(jt.ID, "t")
		if err != nil {
			return invalid("tab id %q: %v", jt.ID, err)
		}

		if tabIDs[jt.ID] {
			return invalid("tab %s appears twice", jt.ID)
		}

		tabIDs[jt.ID] = true
		st.NextTabID = max(st.NextTabID, n)

		ids := make([]core.PaneID, 0, len(jt.PaneIDs))
		for _, s := range jt.PaneIDs {
			id := core.PaneID(s)
			if _, ok := st.Panes[id]; !ok {
				return invalid("tab %s names unknown pane %q", jt.ID, s)
			}

			if o, dup := owner[id]; dup {
				return invalid("pane %s is in tab %s and tab %s", s, o, jt.ID)
			}

			owner[id] = jt.ID
			ids = append(ids, id)
		}

		if len(ids) == 0 && len(f.Tabs) > 1 {
			return invalid("tab %s has no panes", jt.ID)
		}

		tab := core.Tab{ID: core.TabID(jt.ID), Name: cleanName(jt.Name), Panes: ids}

		if current {
			var ok bool
			if tab, ok = decodeTab(jt, tab, lc); !ok {
				rep.Defaulted++
			}
		} else {
			tab = core.DefaultLayout(tab, lc)
		}

		focus := core.PaneID(jt.Focused)
		if len(tab.Panes) == 0 {
			focus = ""
		} else if owner[focus] != jt.ID {
			focus = tab.Panes[0]
		}

		tab.Focus, tab.Zoomed = focus, jt.Zoomed && focus != ""
		if current {
			tab.Review = decodeReview(jt.Review, owner, jt.ID)
		}

		tab.KeepVisible()
		st.Tabs = append(st.Tabs, tab)
	}

	for id := range st.Panes {
		if _, ok := owner[id]; !ok {
			return invalid("pane %s is in no tab", id)
		}
	}

	return st, rep, nil
}

// decodeReview reads a tab's drawer; anything unreadable, or naming a pane
// outside that tab or an unknown mode, is dropped.
func decodeReview(raw json.RawMessage, owner map[core.PaneID]string, tabID string) *core.Review {
	var jr reviewJSON
	if len(raw) == 0 || json.Unmarshal(raw, &jr) != nil {
		return nil
	}

	pane, mode := core.PaneID(jr.Pane), core.ReviewMode(jr.Mode)
	if owner[pane] != tabID || !mode.Valid() {
		return nil
	}

	return &core.Review{Pane: pane, Mode: mode, Tree: jr.Tree}
}

// tabMode reads the mode; anything but a string is "", which no mode matches.
func tabMode(raw json.RawMessage) core.LayoutMode {
	var m string
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}

	return core.LayoutMode(m)
}

// decodeTab builds the tab's layout from its mode and fields; base carries the
// identity and the panes. ok is false when the layout was unusable and the tab
// got the default. A valid saved first column is kept and an invalid one
// becomes 0; Decode then scrolls the focused column into view.
func decodeTab(jt tabJSON, base core.Tab, lc core.LayoutConfig) (tab core.Tab, ok bool) {
	ids := base.Panes
	mode := tabMode(jt.Mode)

	if len(ids) == 0 {
		tab = core.DefaultLayout(base, lc)
		if mode == core.ModeBSP || mode == core.ModeMaster || mode == core.ModeScrolling {
			tab.Mode = mode
		}

		return tab, true
	}

	tab = base

	switch mode {
	case core.ModeBSP:
		root, err := decodeLayout(jt.Layout, ids)
		if err != nil {
			return core.DefaultLayout(base, lc), false
		}

		tab.Mode, tab.Layout, tab.Panes = core.ModeBSP, root, leafOrder(root)
	case core.ModeMaster:
		tab.Mode = core.ModeMaster
		if isNull(jt.MasterRatio) {
			lc.DefaultMode = string(core.ModeMaster)
			tab.Master = core.DefaultLayout(base, lc).Master
		} else {
			r, good := finiteNumber(jt.MasterRatio)
			if !good {
				return core.DefaultLayout(base, lc), false
			}

			tab.Master = normRatio(r)
		}
	case core.ModeScrolling:
		cols, err := decodeColumns(jt.Columns, ids)
		if err != nil {
			return core.DefaultLayout(base, lc), false
		}

		tab.Mode, tab.Columns = core.ModeScrolling, cols
		if f, good := finiteNumber(jt.First); good && f == math.Trunc(f) && f >= 0 && f < float64(len(cols)) {
			tab.First = int(f)
		}
	default:
		return core.DefaultLayout(base, lc), false
	}

	return tab, true
}

func decodePane(jp paneJSON) (core.Pane, error) {
	p := core.Pane{Kind: core.PaneKind(jp.Kind)}
	if filepath.IsAbs(jp.Cwd) {
		p.Cwd = jp.Cwd
	}

	switch p.Kind {
	case core.KindShell:
	case core.KindAgent:
		if jp.Harness != harnessClaude {
			return core.Pane{}, fmt.Errorf("unknown harness %q", jp.Harness)
		}

		p.Harness = jp.Harness
		if uuidRE.MatchString(jp.SessionID) {
			p.Session = core.SessionID(jp.SessionID)
			p.Transcript = jp.Transcript
		}

		p.Plan = decodePlan(jp.Plan)
	default:
		return core.Pane{}, fmt.Errorf("unknown kind %q", jp.Kind)
	}

	return p, nil
}

// decodePlan reads an agent's plan; anything unreadable or invalid is dropped.
func decodePlan(raw json.RawMessage) core.Plan {
	var jp planJSON
	if len(raw) == 0 || json.Unmarshal(raw, &jp) != nil {
		return core.Plan{}
	}

	plan := core.Plan{Path: jp.Path, Root: jp.Root, At: jp.At.UTC(), Unread: jp.Unread}

	for kind, name := range planKinds {
		if name == jp.Kind {
			plan.Kind = kind
		}
	}

	if !plan.Valid() {
		return core.Plan{}
	}

	return plan
}

// parseNumbered reads ids such as "p12": the prefix, then a positive number
// without leading zeros.
func parseNumbered(s, prefix string) (int, error) {
	digits, ok := strings.CutPrefix(s, prefix)
	if !ok || digits == "" || digits[0] == '0' || len(digits) > 9 {
		return 0, fmt.Errorf("want %s followed by a positive number", prefix)
	}

	for _, r := range digits {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("want %s followed by a positive number", prefix)
		}
	}

	n, err := strconv.Atoi(digits)
	if err != nil || n > maxNumber {
		return 0, errors.New("number is too large")
	}

	return n, nil
}

func isCurrentVersion(raw json.RawMessage) bool {
	var v float64

	return json.Unmarshal(raw, &v) == nil && v == Version
}

func isNull(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)

	return len(t) == 0 || bytes.Equal(t, []byte("null"))
}

// versionText is the version as written, cut and cleaned because it reaches
// the toast.
func versionText(raw json.RawMessage) string {
	if isNull(raw) {
		return "unknown"
	}

	return cleanText(string(raw), maxVersionLen)
}

func cleanName(s string) string { return cleanText(s, maxNameRunes) }

// cleanText drops control characters and invalid UTF-8, then cuts to max runes.
func cleanText(s string, limit int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}

		return r
	}, strings.ToValidUTF8(s, ""))
	if r := []rune(s); len(r) > limit {
		return string(r[:limit])
	}

	return s
}
