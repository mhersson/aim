package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhersson/aim/internal/core"
)

func modesState() core.State {
	return core.State{
		Active:    2,
		NextID:    8,
		NextTabID: 3,
		Tabs: []core.Tab{
			{
				ID: "t1", Focus: "p2", Mode: core.ModeBSP, Panes: []core.PaneID{"p1", "p2", "p3"},
				Layout: split(core.SplitVertical, 0.25, leaf("p1"),
					split(core.SplitHorizontal, 0.5, leaf("p2"), leaf("p3"))),
			},
			{ID: "t2", Focus: "p5", Mode: core.ModeMaster, Panes: []core.PaneID{"p4", "p5"}, Master: 0.65},
			{
				ID: "t3", Focus: "p8", Mode: core.ModeScrolling, Panes: []core.PaneID{"p6", "p7", "p8"},
				Columns: []core.Column{
					{Panes: []core.PaneID{"p6"}, Width: 0.5},
					{Panes: []core.PaneID{"p7", "p8"}, Width: 1},
				},
				First: 1,
			},
		},
		Panes: map[core.PaneID]core.Pane{
			"p1": {Kind: core.KindShell}, "p2": {Kind: core.KindShell}, "p3": {Kind: core.KindShell},
			"p4": {Kind: core.KindShell}, "p5": {Kind: core.KindShell}, "p6": {Kind: core.KindShell},
			"p7": {Kind: core.KindShell}, "p8": {Kind: core.KindShell},
		},
	}
}

func TestModesRoundTripAndGolden(t *testing.T) {
	data, err := Encode(modesState())
	noErr(t, err)

	path := filepath.Join("testdata", "state.modes.golden.json")
	if *update {
		noErr(t, os.WriteFile(path, data, 0o644))
	}

	want, err := os.ReadFile(path)
	noErr(t, err)

	if string(data) != string(want) {
		t.Fatalf("encoding differs from %s:\n%s", path, data)
	}

	got, rep, err := Decode(data, core.LayoutConfig{})
	noErr(t, err)
	eq(t, rep, Report{})
	eq(t, got, modesState())
}

func TestEncodeFieldsPerMode(t *testing.T) {
	st := modesState()
	st.Tabs[2].First = 0
	data, err := Encode(st)
	noErr(t, err)

	if strings.Contains(string(data), `"first"`) {
		t.Errorf("first written at 0:\n%s", data)
	}
}

func TestEncodeEmptyTab(t *testing.T) {
	for _, mode := range []core.LayoutMode{"", core.ModeBSP} {
		data, err := Encode(core.State{Tabs: []core.Tab{{ID: "t1", Mode: mode}}})
		noErr(t, err)

		s := string(data)
		if !strings.Contains(s, `"layout": null`) || strings.Contains(s, "master_ratio") || strings.Contains(s, "columns") || strings.Contains(s, `"first"`) {
			t.Errorf("mode %q: empty tab encoded as\n%s", mode, s)
		}
	}

	data, err := Encode(core.State{Tabs: []core.Tab{{ID: "t1", Mode: core.ModeScrolling}}})
	noErr(t, err)

	if strings.Contains(string(data), "columns") || strings.Contains(string(data), "layout") {
		t.Errorf("empty scrolling tab encoded as\n%s", data)
	}

	got, _, err := Decode(data, core.LayoutConfig{})
	noErr(t, err)
	eq(t, got.Tabs[0].Mode, core.ModeScrolling)
}

func TestDecodeVersion1KeepsPanesWithConfigDefault(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "state.v1.json"))
	noErr(t, err)

	tests := []struct {
		name  string
		lc    core.LayoutConfig
		check func(*testing.T, core.Tab)
	}{
		{"bsp", core.LayoutConfig{}, func(t *testing.T, tab core.Tab) {
			eq(t, tab.Mode, core.ModeBSP)
			eq(t, tab.Layout, defaultLayout(tab.Panes))
		}},
		{"scrolling", core.LayoutConfig{DefaultMode: "scrolling", ColumnWidth: 40}, func(t *testing.T, tab core.Tab) {
			eq(t, tab.Mode, core.ModeScrolling)
			eq(t, len(tab.Columns), len(tab.Panes))
			eq(t, tab.Columns[0].Width, 0.4)
			eq(t, tab.First, 0)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, rep, err := Decode(data, tt.lc)
			noErr(t, err)
			eq(t, rep, Report{Version: "1"})
			eq(t, st.Active, 1)
			eq(t, st.Panes["p1"].Session, core.SessionID(sessionA))
			eq(t, st.Panes["p1"].Cwd, "/home/me/code/api")
			eq(t, st.Panes["p1"].Transcript, "/home/me/.claude/projects/api/"+sessionA+".jsonl")
			eq(t, st.Tabs[0].Panes, []core.PaneID{"p1", "p2", "p3"})
			eq(t, st.Tabs[0].Focus, core.PaneID("p2"))
			eq(t, st.Tabs[0].Zoomed, true)

			for _, tab := range st.Tabs {
				tt.check(t, tab)
			}
		})
	}
}

func TestDecodeNewerVersionDefaultsEveryTab(t *testing.T) {
	cols := `"columns":[{"panes":["p1","p2"],"width":0.5}]`
	tab := fmt.Sprintf(`{"id":"t1","mode":"scrolling","focused":"p1","pane_ids":["p1","p2"],%s}`, cols)
	st, rep, err := Decode([]byte(doc("3", "0", tab, twoPanes)), core.LayoutConfig{DefaultMode: "master-stack"})
	noErr(t, err)
	eq(t, rep, Report{Version: "3"})
	eq(t, st.Tabs[0].Mode, core.ModeMaster)
	eq(t, st.Tabs[0].Master, 0.5)
	eq(t, st.Panes["p1"].Cwd, "/a")
}

func TestDecodeMasterWithoutRatioUsesConfig(t *testing.T) {
	tab := `{"id":"t1","mode":"master-stack","focused":"p1","pane_ids":["p1","p2"]}`
	st, rep, err := Decode([]byte(doc("2", "0", tab, twoPanes)), core.LayoutConfig{MasterRatio: 65})
	noErr(t, err)
	eq(t, rep, Report{})
	eq(t, st.Tabs[0].Mode, core.ModeMaster)
	eq(t, st.Tabs[0].Master, 0.65)
}

// TestDecodeHostileVersion2 checks each bad per-tab value against its rule:
// default layout plus a count, or a silent repair. The second tab is valid and
// must survive untouched.
func TestDecodeHostileVersion2(t *testing.T) {
	lc := core.LayoutConfig{DefaultMode: "scrolling", ColumnWidth: 40}
	good := `{"id":"t2","mode":"master-stack","master_ratio":0.4,"focused":"p3","pane_ids":["p3"]}`
	panes := twoPanes + `,"p3":{"kind":"shell"}`
	wantDefault := func(t *testing.T, tab core.Tab) {
		t.Helper()
		eq(t, tab.Mode, core.ModeScrolling)
		eq(t, len(tab.Columns), 2)
		eq(t, tab.Columns[0].Width, 0.4)
		eq(t, tab.Panes, []core.PaneID{"p1", "p2"})
	}
	scroll := func(first, width string) string {
		return `"mode":"scrolling","columns":[{"panes":["p1"],"width":` + width + `},{"panes":["p2"],"width":0.5}]` + first
	}

	tests := []struct {
		name     string
		fields   string
		defaults bool
		check    func(*testing.T, core.Tab)
	}{
		{"unknown mode", `"mode":"floating"`, true, wantDefault},
		{"mode a number", `"mode":5`, true, wantDefault},
		{"missing mode", `"layout":` + twoLayout, true, wantDefault},
		{"columns not matching pane_ids", `"mode":"scrolling","columns":[{"panes":["p2"],"width":0.5},{"panes":["p1"],"width":0.5}]`, true, wantDefault},
		{"columns missing a pane", `"mode":"scrolling","columns":[{"panes":["p1"],"width":0.5}]`, true, wantDefault},
		{"columns absent", `"mode":"scrolling"`, true, wantDefault},
		{"empty column", `"mode":"scrolling","columns":[{"panes":["p1","p2"],"width":0.5},{"panes":[],"width":0.5}]`, true, wantDefault},
		{"width as numeric string", scroll(``, `"7"`), true, wantDefault},
		{"width null", scroll(``, `null`), true, wantDefault},
		{"width out of float range", scroll(``, `1e999`), true, wantDefault},
		{"columns wrong type", `"mode":"scrolling","columns":"x"`, true, wantDefault},
		{"master_ratio NaN string", `"mode":"master-stack","master_ratio":"NaN"`, true, wantDefault},
		{"bsp without layout", `"mode":"bsp"`, true, wantDefault},
		{"width 7 is clamped", scroll(``, `7`), false, func(t *testing.T, tab core.Tab) {
			eq(t, tab.Mode, core.ModeScrolling)
			eq(t, tab.Columns[0].Width, 1.0)
			eq(t, tab.Columns[1].Width, 0.5)
		}},
		{"width 0.004 is clamped", scroll(``, `0.004`), false, func(t *testing.T, tab core.Tab) {
			eq(t, tab.Columns[0].Width, 0.1)
		}},
		{"width 0.456 is rounded", scroll(``, `0.456`), false, func(t *testing.T, tab core.Tab) {
			eq(t, tab.Columns[0].Width, 0.46)
		}},
		{"first -3 becomes 0", scroll(`,"first":-3`, `0.5`), false, func(t *testing.T, tab core.Tab) {
			eq(t, tab.Mode, core.ModeScrolling)
			eq(t, tab.First, 0)
		}},
		{"first past the end becomes 0", scroll(`,"first":9`, `0.5`), false, func(t *testing.T, tab core.Tab) { eq(t, tab.First, 0) }},
		{"first 1 is kept", scroll(`,"first":1`, `0.5`), false, func(t *testing.T, tab core.Tab) { eq(t, tab.First, 1) }},
		{"master_ratio 50 is clamped", `"mode":"master-stack","master_ratio":50`, false, func(t *testing.T, tab core.Tab) {
			eq(t, tab.Mode, core.ModeMaster)
			eq(t, tab.Master, 0.9)
		}},
		{"columns on a master-stack tab are ignored", `"mode":"master-stack","master_ratio":0.3,"columns":"junk"`, false, func(t *testing.T, tab core.Tab) {
			eq(t, tab.Master, 0.3)
			eq(t, tab.Columns == nil, true)
		}},
		{"layout on a scrolling tab is ignored", scroll(``, `0.5`) + `,"layout":"junk"`, false, func(t *testing.T, tab core.Tab) {
			eq(t, tab.Mode, core.ModeScrolling)
			eq(t, tab.Layout == nil, true)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bad := `{"id":"t1","focused":"p2","pane_ids":["p1","p2"],` + tt.fields + `}`
			st, rep, err := Decode([]byte(doc("2", "0", bad+","+good, panes)), lc)
			noErr(t, err)

			want := Report{}
			if tt.defaults {
				want.Defaulted = 1
			}

			eq(t, rep, want)
			tt.check(t, st.Tabs[0])
			eq(t, st.Tabs[0].Focus, core.PaneID("p2"))
			eq(t, st.Tabs[1].Master, 0.4)
			eq(t, st.Tabs[1].Mode, core.ModeMaster)

			// The result encodes and decodes again: it is a valid state.
			data, err := Encode(st)
			noErr(t, err)
			again, rep2, err := Decode(data, lc)
			noErr(t, err)
			eq(t, rep2, Report{})
			eq(t, again, st)
		})
	}
}

func TestDecodeEmptyTabWithUnknownMode(t *testing.T) {
	lc := core.LayoutConfig{DefaultMode: "master-stack"}
	tab := `{"id":"t1","mode":"floating","pane_ids":[]}`
	st, rep, err := Decode([]byte(doc("2", "0", tab, ``)), lc)
	noErr(t, err)
	eq(t, rep, Report{})
	eq(t, st.Tabs[0].Mode, core.ModeMaster)
	eq(t, st.Tabs[0].Empty(), true)
}

func TestDecodeDefaultedScrollingTabShowsFocus(t *testing.T) {
	const v1 = `{"version":1,"active":0,"tabs":[{"id":"t1","focused":"p4","pane_ids":["p1","p2","p3","p4"]}],` +
		`"panes":{"p1":{"kind":"shell"},"p2":{"kind":"shell"},"p3":{"kind":"shell"},"p4":{"kind":"shell"}}}`

	st, _, err := Decode([]byte(v1), core.LayoutConfig{DefaultMode: "scrolling"})
	noErr(t, err)

	tab := st.Tabs[0]
	eq(t, tab.Focus, core.PaneID("p4"))
	eq(t, tab.First, 2)
}
