package store

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mhersson/aim/internal/core"
)

var update = flag.Bool("update", false, "regenerate testdata/state.golden.json")

const (
	sessionA = "7c0b6f1e-3a52-4d8e-9b41-2f5e8a1d0c37"
	sessionB = "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
)

func fixedState() core.State {
	return core.State{
		Active:    1,
		NextID:    4,
		NextTabID: 2,
		Tabs: []core.Tab{
			{
				ID: "t1", Name: "api", Focus: "p2", Zoomed: true,
				Review: &core.Review{Pane: "p2", Mode: core.ReviewTurn},
				Mode:   core.ModeBSP, Panes: []core.PaneID{"p1", "p2", "p3"},
				Layout: split(core.SplitVertical, 0.333333,
					leaf("p1"),
					split(core.SplitHorizontal, 0.5, leaf("p2"), leaf("p3"))),
			},
			{ID: "t2", Focus: "p4", Mode: core.ModeBSP, Panes: []core.PaneID{"p4"}, Layout: leaf("p4")},
		},
		Panes: map[core.PaneID]core.Pane{
			"p1": {
				Kind: core.KindAgent, Cwd: "/home/me/code/api", Harness: "claude-code",
				Session: sessionA, Transcript: "/home/me/.claude/projects/api/" + sessionA + ".jsonl",
			},
			"p2": {Kind: core.KindShell, Cwd: "/home/me/code/api"},
			"p3": {Kind: core.KindShell},
			"p4": {Kind: core.KindAgent, Cwd: "/home/me", Harness: "claude-code"},
		},
	}
}

// roundTripped is fixedState as Decode returns it: the ratio on the 0.01 grid.
func roundTripped() core.State {
	st := fixedState()
	st.Tabs[0].Layout.Ratio = 0.33

	return st
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	data, err := Encode(fixedState())
	noErr(t, err)
	got, rep, err := decodeDefault(data)
	noErr(t, err)
	eq(t, rep, Report{})
	eq(t, got, roundTripped())
}

func TestEncodeGolden(t *testing.T) {
	data, err := Encode(fixedState())
	noErr(t, err)

	path := filepath.Join("testdata", "state.golden.json")
	if *update {
		noErr(t, os.WriteFile(path, data, 0o644))
	}

	want, err := os.ReadFile(path)
	noErr(t, err)

	if string(data) != string(want) {
		t.Fatalf("encoding differs from %s:\n%s", path, data)
	}
}

func TestEncodeNoTabs(t *testing.T) {
	data, err := Encode(core.State{})
	noErr(t, err)
	got, _, err := decodeDefault(data)
	noErr(t, err)
	eq(t, len(got.Tabs), 1)
	eq(t, got.Tabs[0].ID, core.TabID("t1"))

	if !got.Tabs[0].Empty() || len(got.Panes) != 0 {
		t.Fatalf("empty state decoded to %+v", got)
	}
}

func TestEncodeRejects(t *testing.T) {
	tests := []struct {
		name string
		mod  func(*core.State)
	}{
		{"active out of range", func(s *core.State) { s.Active = 5 }},
		{"leaf without pane metadata", func(s *core.State) { delete(s.Panes, "p3") }},
		{"unknown kind", func(s *core.State) { s.Panes["p3"] = core.Pane{Kind: "review"} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := fixedState()
			tt.mod(&st)
			_, err := Encode(st)
			wantErr(t, err)
		})
	}
}

func TestEncodeAgentWithoutHarnessIsShell(t *testing.T) {
	st := fixedState()
	st.Panes["p4"] = core.Pane{Kind: core.KindAgent, Cwd: "/home/me", Session: sessionA, Transcript: "/t.jsonl"}
	data, err := Encode(st)
	noErr(t, err)
	got, _, err := decodeDefault(data)
	noErr(t, err)
	eq(t, got.Panes["p4"], core.Pane{Kind: core.KindShell, Cwd: "/home/me"})
}

func TestEncodeSkipsOrphanPanes(t *testing.T) {
	st := fixedState()
	st.Panes["p9"] = core.Pane{Kind: core.KindShell}
	data, err := Encode(st)
	noErr(t, err)

	if _, _, err := decodeDefault(data); err != nil {
		t.Fatalf("a pane in no tab broke the encoding: %v", err)
	}
}

// doc assembles a state file from raw JSON pieces.
func doc(version, active, tabs, panes string) string {
	return fmt.Sprintf(`{"version":%s,"active":%s,"tabs":[%s],"panes":{%s}}`, version, active, tabs, panes)
}

func tabDoc(id, focused, ids, layout string) string {
	return fmt.Sprintf(`{"id":%q,"mode":"bsp","focused":%q,"pane_ids":[%s],"layout":%s}`, id, focused, ids, layout)
}

const (
	twoPanes  = `"p1":{"kind":"shell","cwd":"/a"},"p2":{"kind":"shell"}`
	twoLayout = `{"split":"vertical","ratio":0.5,"children":[{"pane":"p1"},{"pane":"p2"}]}`
)

func TestDecodeVersions(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    string
		layout  bool // the file's own layout survives
	}{
		{"one", `1`, "1", false},
		{"two", `2`, "", true},
		{"long", `"abcdefghijklmnopqrstuvwxyz"`, `"abcdefghijklmno`, false},
		{"object with whitespace", "{\"a\":\t1}", `{"a":1}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The layout ratio 0.3 is off the default, so a kept layout shows.
			layout := `{"split":"vertical","ratio":0.3,"children":[{"pane":"p1"},{"pane":"p2"}]}`
			data := doc(tt.version, "0", tabDoc("t1", "p2", `"p1","p2"`, layout), twoPanes)
			st, rep, err := decodeDefault([]byte(data))
			noErr(t, err)

			if strings.ContainsFunc(rep.Version, func(r rune) bool { return r < ' ' || r == 0x7f }) {
				t.Fatalf("version %q has control characters", rep.Version)
			}

			if len([]rune(rep.Version)) > 16 {
				t.Fatalf("version %q longer than 16 runes", rep.Version)
			}

			eq(t, rep.Version, tt.want)
			eq(t, rep.Defaulted, 0)
			eq(t, len(st.Panes), 2)
			eq(t, st.Panes["p1"].Cwd, "/a")

			wantLayout := defaultLayout([]core.PaneID{"p1", "p2"})
			if tt.layout {
				wantLayout = split(core.SplitVertical, 0.3, leaf("p1"), leaf("p2"))
			}

			eq(t, st.Tabs[0].Layout, wantLayout)
			eq(t, st.Tabs[0].Focus, core.PaneID("p2"))
		})
	}
}

func TestDecodeMissingVersion(t *testing.T) {
	data := `{"active":0,"tabs":[` + tabDoc("t1", "p1", `"p1","p2"`, twoLayout) + `],"panes":{` + twoPanes + `}}`
	st, rep, err := decodeDefault([]byte(data))
	noErr(t, err)
	eq(t, rep, Report{Version: "unknown"})
	eq(t, st.Tabs[0].Layout, defaultLayout([]core.PaneID{"p1", "p2"}))
}

func TestDecodeBrokenLayoutDefaultsThatTab(t *testing.T) {
	panes := `"p1":{"kind":"shell"},"p2":{"kind":"shell"},"p3":{"kind":"shell"},"p4":{"kind":"shell"}`
	good := `{"split":"horizontal","ratio":0.3,"children":[{"pane":"p3"},{"pane":"p4"}]}`

	tests := []struct {
		name   string
		layout string
	}{
		{"ratio five on a non-split", `{"pane":"p1","ratio":5}`},
		{"garbage", `"nonsense"`},
		{"null", `null`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			layout := tt.layout
			t1 := fmt.Sprintf(`{"id":"t1","mode":"bsp","focused":"p2","pane_ids":["p1","p2"]%s}`, optLayout(layout))
			t2 := tabDoc("t2", "p4", `"p3","p4"`, good)
			st, rep, err := decodeDefault([]byte(doc("2", "1", t1+","+t2, panes)))
			noErr(t, err)
			eq(t, rep, Report{Defaulted: 1})
			eq(t, st.Tabs[0].Layout, defaultLayout([]core.PaneID{"p1", "p2"}))
			eq(t, st.Tabs[1].Layout, split(core.SplitHorizontal, 0.3, leaf("p3"), leaf("p4")))
			eq(t, st.Tabs[0].Focus, core.PaneID("p2"))
			eq(t, st.Active, 1)
		})
	}
}

func optLayout(l string) string {
	if l == "" {
		return ""
	}

	return `,"layout":` + l
}

func TestDecodeValidation(t *testing.T) {
	one := tabDoc("t1", "p1", `"p1","p2"`, twoLayout)

	tests := []struct {
		name string
		data string
	}{
		{"truncated", doc("2", "0", one, twoPanes)[:40]},
		{"empty input", ``},
		{"null", `null`},
		{"array", `[]`},
		{"nan ratio token", `{"version":2,"active":0,"tabs":[` + tabDoc("t1", "p1", `"p1","p2"`, `{"split":"vertical","ratio":NaN,"children":[{"pane":"p1"},{"pane":"p2"}]}`) + `],"panes":{` + twoPanes + `}}`},
		{"trailing data", doc("2", "0", one, twoPanes) + `{}`},
		{"no tabs", `{"version":2,"active":0,"tabs":[],"panes":{}}`},
		{"active negative", doc("2", "-1", one, twoPanes)},
		{"active past the end", doc("2", "3", one+","+tabDoc("t2", "p3", `"p3"`, `{"pane":"p3"}`), twoPanes+`,"p3":{"kind":"shell"}`)},
		{"duplicate tab id", doc("2", "0", one+","+tabDoc("t1", "p3", `"p3"`, `{"pane":"p3"}`), twoPanes+`,"p3":{"kind":"shell"}`)},
		{"pane in two tabs", doc("2", "0", one+","+tabDoc("t2", "p2", `"p2"`, `{"pane":"p2"}`), twoPanes)},
		{"pane twice in a tab", doc("2", "0", tabDoc("t1", "p1", `"p1","p1","p2"`, twoLayout), twoPanes)},
		{"pane_ids names a missing pane", doc("2", "0", tabDoc("t1", "p1", `"p1","p2","p7"`, twoLayout), twoPanes)},
		{"pane in no tab", doc("2", "0", tabDoc("t1", "p1", `"p1"`, `{"pane":"p1"}`), twoPanes)},
		{"kind review", doc("2", "0", one, `"p1":{"kind":"review"},"p2":{"kind":"shell"}`)},
		{"harness codex", doc("2", "0", one, `"p1":{"kind":"agent","harness":"codex"},"p2":{"kind":"shell"}`)},
		{"agent without harness", doc("2", "0", one, `"p1":{"kind":"agent"},"p2":{"kind":"shell"}`)},
		{"empty tab beside another", doc("2", "0", one+","+tabDoc("t2", "", ``, `null`), twoPanes)},
		{"tab id shape", doc("2", "0", tabDoc("x1", "p1", `"p1","p2"`, twoLayout), twoPanes)},
		{"tab id zero", doc("2", "0", tabDoc("t0", "p1", `"p1","p2"`, twoLayout), twoPanes)},
		{"tab id overflow", doc("2", "0", tabDoc("t99999999999999999999", "p1", `"p1","p2"`, twoLayout), twoPanes)},
		{"pane id shape", doc("2", "0", tabDoc("t1", "q1", `"q1"`, `{"pane":"q1"}`), `"q1":{"kind":"shell"}`)},
		{"pane wrong type", `{"version":2,"active":0,"tabs":[{"id":"t1","pane_ids":["p1"]}],"panes":{"p1":"shell"}}`},
		{"null tab", `{"version":2,"active":0,"tabs":[null],"panes":{}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := decodeDefault([]byte(tt.data))
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

// manyPanes is a one-tab document with n shell panes in a row.
func manyPanes(n int) string {
	var ids, panes []string
	for i := 1; i <= n; i++ {
		ids = append(ids, fmt.Sprintf("%q", fmt.Sprintf("p%d", i)))
		panes = append(panes, fmt.Sprintf(`"p%d":{"kind":"shell"}`, i))
	}

	return doc("2", "0", tabDoc("t1", "p1", strings.Join(ids, ","), `null`), strings.Join(panes, ","))
}

func TestDecodePaneCap(t *testing.T) {
	st, _, err := decodeDefault([]byte(manyPanes(64)))
	noErr(t, err)
	eq(t, len(st.Panes), 64)

	_, _, err = decodeDefault([]byte(manyPanes(65)))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("65 panes: err = %v, want ErrInvalid", err)
	}
}

func TestDecodeFallbacks(t *testing.T) {
	other := tabDoc("t2", "p3", `"p3"`, `{"pane":"p3"}`)

	tests := []struct {
		name  string
		tabs  string
		panes string
		check func(*testing.T, core.State)
	}{
		{
			"focused pane from another tab",
			tabDoc("t1", "p3", `"p1","p2"`, twoLayout) + "," + other,
			twoPanes + `,"p3":{"kind":"shell"}`,
			func(t *testing.T, st core.State) { eq(t, st.Tabs[0].Focus, core.PaneID("p1")) },
		},
		{
			"focused pane missing",
			`{"id":"t1","mode":"bsp","pane_ids":["p1","p2"],"layout":` + twoLayout + `}`,
			twoPanes,
			func(t *testing.T, st core.State) { eq(t, st.Tabs[0].Focus, core.PaneID("p1")) },
		},
		{
			"focused pane missing from a reordered layout",
			tabDoc("t1", "p9", `"p1","p2"`, `{"split":"vertical","ratio":0.5,"children":[{"pane":"p2"},{"pane":"p1"}]}`),
			twoPanes,
			func(t *testing.T, st core.State) { eq(t, st.Tabs[0].Focus, core.PaneID("p2")) },
		},
		{
			"shell metacharacters in a session id",
			tabDoc("t1", "p1", `"p1"`, `{"pane":"p1"}`),
			`"p1":{"kind":"agent","harness":"claude-code","session_id":"$(rm -rf ~)","transcript":"/t.jsonl"}`,
			func(t *testing.T, st core.State) {
				p := st.Panes["p1"]
				eq(t, p.Session, core.SessionID(""))
				eq(t, p.Transcript, "")
				eq(t, p.Kind, core.KindAgent)
			},
		},
		{
			"valid session kept",
			tabDoc("t1", "p1", `"p1"`, `{"pane":"p1"}`),
			`"p1":{"kind":"agent","harness":"claude-code","session_id":"` + sessionB + `","transcript":"/t.jsonl"}`,
			func(t *testing.T, st core.State) {
				eq(t, st.Panes["p1"].Session, core.SessionID(sessionB))
				eq(t, st.Panes["p1"].Transcript, "/t.jsonl")
			},
		},
		{
			"transcript without a session",
			tabDoc("t1", "p1", `"p1"`, `{"pane":"p1"}`),
			`"p1":{"kind":"agent","harness":"claude-code","transcript":"/t.jsonl"}`,
			func(t *testing.T, st core.State) { eq(t, st.Panes["p1"].Transcript, "") },
		},
		{
			"agent fields on a shell",
			tabDoc("t1", "p1", `"p1"`, `{"pane":"p1"}`),
			`"p1":{"kind":"shell","harness":"claude-code","session_id":"` + sessionA + `","transcript":"/t.jsonl"}`,
			func(t *testing.T, st core.State) { eq(t, st.Panes["p1"], core.Pane{Kind: core.KindShell}) },
		},
		{
			"relative cwd",
			tabDoc("t1", "p1", `"p1"`, `{"pane":"p1"}`),
			`"p1":{"kind":"shell","cwd":"work/api"}`,
			func(t *testing.T, st core.State) { eq(t, st.Panes["p1"].Cwd, "") },
		},
		{
			"cwd with spaces and quotes",
			tabDoc("t1", "p1", `"p1"`, `{"pane":"p1"}`),
			`"p1":{"kind":"shell","cwd":"/tmp/a b's \"c\"; $(x)"}`,
			func(t *testing.T, st core.State) { eq(t, st.Panes["p1"].Cwd, `/tmp/a b's "c"; $(x)`) },
		},
		{
			"long name with a tab",
			`{"id":"t1","mode":"bsp","name":"` + strings.Repeat("a", 5) + `\t` + strings.Repeat("b", 34) + `","focused":"p1","pane_ids":["p1"],"layout":{"pane":"p1"}}`,
			`"p1":{"kind":"shell"}`,
			func(t *testing.T, st core.State) {
				n := st.Tabs[0].Name
				eq(t, len([]rune(n)), 32)

				if strings.ContainsFunc(n, func(r rune) bool { return r < ' ' }) {
					t.Fatalf("name %q has control characters", n)
				}

				eq(t, n, strings.Repeat("a", 5)+strings.Repeat("b", 27))
			},
		},
		{
			"name with an escape sequence",
			`{"id":"t1","mode":"bsp","name":"x\u001b[2Jy\u0000z\u007f","focused":"p1","pane_ids":["p1"],"layout":{"pane":"p1"}}`,
			`"p1":{"kind":"shell"}`,
			func(t *testing.T, st core.State) { eq(t, st.Tabs[0].Name, "x[2Jyz") },
		},
		{
			"next ids from the highest numbers",
			tabDoc("t3", "p2", `"p2"`, `{"pane":"p2"}`) + "," + tabDoc("t9", "p7", `"p7"`, `{"pane":"p7"}`),
			`"p2":{"kind":"shell"},"p7":{"kind":"shell"}`,
			func(t *testing.T, st core.State) {
				eq(t, st.NextID, 7)
				eq(t, st.NextTabID, 9)
			},
		},
		{
			"empty only tab",
			tabDoc("t4", "", ``, `null`),
			``,
			func(t *testing.T, st core.State) {
				eq(t, len(st.Tabs), 1)

				if !st.Tabs[0].Empty() || st.Tabs[0].Focus != "" {
					t.Fatalf("empty tab decoded to %+v", st.Tabs[0])
				}

				eq(t, st.NextTabID, 4)
				eq(t, st.NextID, 0)
			},
		},
		{
			"states are unknown and theme empty",
			tabDoc("t1", "p1", `"p1"`, `{"pane":"p1"}`),
			`"p1":{"kind":"agent","harness":"claude-code"}`,
			func(t *testing.T, st core.State) {
				eq(t, st.Panes["p1"].State, core.AgentUnknown)
				eq(t, st.Theme, "")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, rep, err := decodeDefault([]byte(doc("2", "0", tt.tabs, tt.panes)))
			noErr(t, err)
			eq(t, rep, Report{})
			tt.check(t, st)
		})
	}
}

func TestDecodeNeverPanics(t *testing.T) {
	for _, in := range []string{
		`{"version":[[[[[[]]]]]],"tabs":{},"panes":[]}`,
		strings.Repeat("[", 10001),
		strings.Repeat(`{"a":`, 10001),
	} {
		_, _, _ = decodeDefault([]byte(in))
	}
}

func TestDecodeRejectsHugeInput(t *testing.T) {
	_, _, err := decodeDefault([]byte(strings.Repeat(" ", 17<<20)))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()

	_, _, err := Load(filepath.Join(dir, "state.json"), core.LayoutConfig{})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want os.ErrNotExist", err)
	}

	data, err := Encode(fixedState())
	noErr(t, err)

	path := filepath.Join(dir, "state.json")
	noErr(t, os.WriteFile(path, data, 0o600))
	st, _, err := Load(path, core.LayoutConfig{})
	noErr(t, err)
	eq(t, st, roundTripped())

	noErr(t, os.WriteFile(path, []byte(`{"version":2`), 0o600))

	_, _, err = Load(path, core.LayoutConfig{})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func decodeDefault(data []byte) (core.State, Report, error) {
	return Decode(data, core.LayoutConfig{})
}

// TestVolatileFieldsNotSaved pins "only what cannot come back is saved": state
// the app re-derives never reaches the file.
func TestVolatileFieldsNotSaved(t *testing.T) {
	t.Parallel()

	withPane := func(f func(*core.Pane)) func(*core.State) {
		return func(st *core.State) {
			p := st.Panes["p1"]
			f(&p)
			st.Panes["p1"] = p
		}
	}

	tests := []struct {
		name string
		mod  func(*core.State)
	}{
		{"agent cwd", withPane(func(p *core.Pane) { p.AgentCwd = "/home/me/code/api/sub" })},
		{"agent status", withPane(func(p *core.Pane) {
			p.Model, p.Context, p.ContextKnown, p.StatusSession = "Opus 4.8", 73, true, "status-session"
		})},
		{"edits", withPane(func(p *core.Pane) {
			p.Edits = []core.Edit{{Root: "/home/me/code/other", Common: "/home/me/code/other/.git", Repo: "other", Seq: 3}}
		})},
		{"pin", func(st *core.State) { st.Tabs[0].Pin = &core.Pin{Pane: "p2", Root: "/home/me/code/other"} }},
		{"review pr mode", func(st *core.State) {
			st.Tabs[0].Review.PR = &core.ReviewPR{Repo: "github.com/o/r", Number: 3, Root: "/w"}
		}},
	}

	want, err := Encode(fixedState())
	noErr(t, err)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			st := fixedState()
			tt.mod(&st)

			got, err := Encode(st)
			noErr(t, err)
			eq(t, string(got), string(want))
		})
	}
}

func TestRailPersistence(t *testing.T) {
	open := fixedState()
	open.Rail = true

	data, err := Encode(open)
	noErr(t, err)

	if !strings.Contains(string(data), "\"rail\": true") {
		t.Errorf("open rail is not written:\n%s", data)
	}

	got, rep, err := decodeDefault(data)
	noErr(t, err)
	eq(t, rep, Report{})
	eq(t, got.Rail, true)

	data, err = Encode(fixedState())
	noErr(t, err)

	if strings.Contains(string(data), "rail") {
		t.Errorf("closed rail is written:\n%s", data)
	}
}

func TestDecodeRailByVersion(t *testing.T) {
	tests := []struct {
		name, version, rail string
		want                bool
	}{
		{"v2 without the field", "2", "", false},
		{"v2 open", "2", `"rail":true,`, true},
		{"v1 ignores the field", "1", `"rail":true,`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := doc(tt.version, "0", tabDoc("t1", "p1", `"p1","p2"`, twoLayout), twoPanes)
			data := strings.Replace(body, `"active"`, tt.rail+`"active"`, 1)
			st, _, err := decodeDefault([]byte(data))
			noErr(t, err)
			eq(t, st.Rail, tt.want)
		})
	}
}

// Each tab's drawer saves and loads its own tree state.
func TestReviewTreePersistence(t *testing.T) {
	tests := []struct {
		name         string
		tree1, tree2 bool
		saved        int
	}{
		{"first closed, second open", false, true, 1},
		{"both open", true, true, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := fixedState()
			st.Tabs[0].Review = &core.Review{Pane: "p2", Mode: core.ReviewBranch, Tree: tt.tree1}
			st.Tabs[1].Review = &core.Review{Pane: "p4", Mode: core.ReviewTurn, Tree: tt.tree2}

			data, err := Encode(st)
			noErr(t, err)

			if n := strings.Count(string(data), `"tree": true`); n != tt.saved {
				t.Errorf("%d tree entries written, want %d:\n%s", n, tt.saved, data)
			}

			if strings.Contains(string(data), `"tree": false`) {
				t.Errorf("a closed tree is written:\n%s", data)
			}

			got, rep, err := decodeDefault(data)
			noErr(t, err)
			eq(t, rep, Report{})
			eq(t, got.Tabs[0].Review, st.Tabs[0].Review)
			eq(t, got.Tabs[1].Review, st.Tabs[1].Review)
		})
	}
}

func TestReviewDecodeDropsBadFields(t *testing.T) {
	tests := []struct {
		name, review string
		want         *core.Review
	}{
		{"tree is a string", `{"pane":"p2","mode":"turn","tree":"yes"}`, nil},
		{"unknown field", `{"pane":"p2","mode":"turn","later":1}`, &core.Review{Pane: "p2", Mode: core.ReviewTurn}},
		{"pane of another tab", `{"pane":"p3","mode":"turn"}`, nil},
		{"unknown mode", `{"pane":"p2","mode":"x"}`, nil},
		{"null", `null`, nil},
		{"wrong type", `"p2"`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t1 := strings.Replace(tabDoc("t1", "p1", `"p1","p2"`, twoLayout), `"mode":"bsp",`, `"mode":"bsp","review":`+tt.review+`,`, 1)
			t2 := tabDoc("t2", "p3", `"p3"`, `{"pane":"p3"}`)
			panes := twoPanes + `,"p3":{"kind":"shell"}`

			st, rep, err := decodeDefault([]byte(doc("2", "0", t1+","+t2, panes)))
			noErr(t, err)
			eq(t, rep, Report{})
			eq(t, st.Tabs[0].Review, tt.want)
			eq(t, len(st.Tabs[0].Panes), 2)
		})
	}
}

var planAt = time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)

func planPane(plan core.Plan) core.State {
	st := fixedState()
	p := st.Panes["p1"]
	p.Plan = plan
	st.Panes["p1"] = p

	return st
}

func TestEncodePlan(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		plan core.Plan
		want string
	}{
		{
			"file plan",
			core.Plan{Path: "/r/docs/superpowers/plans/a.md", Root: "/r", Kind: core.PlanFile, At: planAt, Unread: true},
			`"plan": {"path": "/r/docs/superpowers/plans/a.md", "root": "/r", "kind": "file", "at": "2026-10-30T09:00:00Z", "unread": true}`,
		},
		{
			"mode plan, read",
			core.Plan{Path: "/h/.claude/plans/x.md", Kind: core.PlanMode, At: planAt},
			`"plan": {"path": "/h/.claude/plans/x.md", "kind": "mode", "at": "2026-10-30T09:00:00Z"}`,
		},
		{"no plan", core.Plan{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := Encode(planPane(tt.plan))
			noErr(t, err)

			var m struct {
				Panes map[string]map[string]json.RawMessage `json:"panes"`
			}
			noErr(t, json.Unmarshal(data, &m))

			for id, pane := range m.Panes {
				raw, has := pane["plan"]
				if id != "p1" || tt.want == "" {
					if has {
						t.Errorf("pane %s writes a plan: %s", id, raw)
					}

					continue
				}

				var got, want map[string]any

				noErr(t, json.Unmarshal(raw, &got))
				noErr(t, json.Unmarshal([]byte("{"+tt.want+"}"), &want))
				eq(t, map[string]any{"plan": got}, want)
			}
		})
	}

	t.Run("a shell writes none", func(t *testing.T) {
		t.Parallel()

		st := fixedState()
		p := st.Panes["p2"]
		p.Plan = core.Plan{Path: "/a/b.md", Kind: core.PlanMode, At: planAt}
		st.Panes["p2"] = p

		data, err := Encode(st)
		noErr(t, err)

		if strings.Contains(string(data), `"plan"`) {
			t.Errorf("a shell's plan is written:\n%s", data)
		}
	})

	t.Run("an invalid plan is not written", func(t *testing.T) {
		t.Parallel()

		data, err := Encode(planPane(core.Plan{Path: "rel.md", Kind: core.PlanMode, At: planAt}))
		noErr(t, err)

		if strings.Contains(string(data), `"plan"`) {
			t.Errorf("an invalid plan is written:\n%s", data)
		}
	})
}

func TestDecodePlan(t *testing.T) {
	t.Parallel()

	file := core.Plan{Path: "/r/docs/superpowers/plans/a.md", Root: "/r", Kind: core.PlanFile, At: planAt, Unread: true}
	mode := core.Plan{Path: "/h/.claude/plans/x.md", Kind: core.PlanMode, At: planAt}

	tests := []struct {
		name string
		pane string // the pane's "plan" member, or a whole pane when it starts with "{"
		want core.Plan
	}{
		{"uncleaned path", `{"path":"/a/../b.md","kind":"mode","at":"2026-10-30T09:00:00Z"}`, core.Plan{}},
		{"unknown kind", `{"path":"/a/b.md","kind":"x","at":"2026-10-30T09:00:00Z"}`, core.Plan{}},
		{"root does not hold the path", `{"path":"/a/b.md","root":"/c","kind":"file","at":"2026-10-30T09:00:00Z"}`, core.Plan{}},
		{"file plan without root", `{"path":"/a/b.md","kind":"file","at":"2026-10-30T09:00:00Z"}`, core.Plan{}},
		{"mode plan with a root", `{"path":"/a/b.md","root":"/a","kind":"mode","at":"2026-10-30T09:00:00Z"}`, core.Plan{}},
		{"bad time", `{"path":"/a/b.md","kind":"mode","at":"yesterday"}`, core.Plan{}},
		{"plan is null", `null`, core.Plan{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			panes := `"p1":{"kind":"agent","harness":"claude-code","cwd":"/home/me","plan":` + tt.pane + `},` +
				`"p2":{"kind":"shell","cwd":"/home/me"}`
			data := doc("2", "0", tabDoc("t1", "p1", `"p1","p2"`, `{"split":"vertical","ratio":0.5,"children":[{"pane":"p1"},{"pane":"p2"}]}`), panes)

			st, rep, err := decodeDefault([]byte(data))
			noErr(t, err)
			eq(t, rep, Report{})
			eq(t, st.Panes["p1"].Plan, tt.want)
			eq(t, st.Panes["p1"].Cwd, "/home/me")
			eq(t, st.Panes["p1"].Harness, "claude-code")
		})
	}

	t.Run("a shell's plan is dropped", func(t *testing.T) {
		t.Parallel()

		panes := `"p1":{"kind":"shell","cwd":"/home/me","plan":{"path":"/a/b.md","kind":"mode","at":"2026-10-30T09:00:00Z"}}`
		data := doc("2", "0", tabDoc("t1", "p1", `"p1"`, `{"pane":"p1"}`), panes)

		st, rep, err := decodeDefault([]byte(data))
		noErr(t, err)
		eq(t, rep, Report{})
		eq(t, st.Panes["p1"], core.Pane{Kind: core.KindShell, Cwd: "/home/me"})
	})

	t.Run("round trip", func(t *testing.T) {
		t.Parallel()

		for _, plan := range []core.Plan{file, mode} {
			data, err := Encode(planPane(plan))
			noErr(t, err)

			got, _, err := decodeDefault(data)
			noErr(t, err)
			eq(t, got, planRoundTripped(plan))
		}
	})
}

func planRoundTripped(plan core.Plan) core.State {
	st := roundTripped()
	p := st.Panes["p1"]
	p.Plan = plan
	st.Panes["p1"] = p

	return st
}

func TestEncodeWritesNoMarks(t *testing.T) {
	t.Parallel()

	st := core.InitialState(core.DefaultConfig())

	plain, err := Encode(st)
	if err != nil {
		t.Fatal(err)
	}

	stm := core.NewStore(st)
	if err := stm.Dispatch(core.SetMarks{Repo: "/r/.git", Days: map[string]string{"abc": "2026-10-30"}}); err != nil {
		t.Fatal(err)
	}

	marked, err := Encode(stm.State())
	if err != nil {
		t.Fatal(err)
	}

	if string(plain) != string(marked) {
		t.Errorf("marks reached state.json:\n%s", marked)
	}
}
