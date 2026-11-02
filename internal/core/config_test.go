package core

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mhersson/aim/internal/core/tiling"
)

func TestIconsOn(t *testing.T) {
	if !DefaultConfig().IconsOn() || !(Config{Icons: new(true)}).IconsOn() || (Config{Icons: new(false)}).IconsOn() {
		t.Error("IconsOn: want true for nil and true, false for false")
	}
}

func TestClipboardDefaultsOn(t *testing.T) {
	d := DefaultConfig()
	if !d.Clipboard.OSC52On() || !d.Clipboard.NativeOn() {
		t.Error("clipboard: want OSC 52 and native on by default")
	}

	if d.Mouse.FocusFollowsMouse || d.Mouse.CopyOnSelect {
		t.Error("mouse: want both options off by default")
	}

	off := Clipboard{OSC52: new(false), Native: new(false)}
	if off.OSC52On() || off.NativeOn() {
		t.Error("clipboard: want false to turn both off")
	}
}

func TestShellIntegrationDefaultsOn(t *testing.T) {
	t.Parallel()

	if !DefaultConfig().Shell.IntegrationOn() || (ShellConfig{Integration: new(false)}).IntegrationOn() {
		t.Error("shell integration: want on by default and off for false")
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name       string
		content    string
		missing    bool
		unreadable bool
		want       Config
		wantErr    bool
		errHas     []string // offending keys besides the path
	}{
		{name: "missing file", missing: true, want: DefaultConfig()},
		{name: "empty file", content: "", want: DefaultConfig()},
		{name: "theme set", content: "theme = \"catppuccin-latte\"\n", want: Config{Theme: "catppuccin-latte"}},
		{name: "theme name not validated", content: "theme = \"no-such-theme\"\n", want: Config{Theme: "no-such-theme"}},
		{name: "icons off", content: "icons = false\n", want: Config{Theme: DefaultTheme, Icons: new(false)}},
		{name: "icons wrong type", content: "icons = \"no\"\n", wantErr: true, errHas: []string{"icons"}},
		{
			name: "mouse options", content: "[mouse]\nfocus_follows_mouse = true\ncopy_on_select = true\n",
			want: Config{Theme: DefaultTheme, Mouse: Mouse{FocusFollowsMouse: true, CopyOnSelect: true}},
		},
		{
			name: "clipboard off", content: "[clipboard]\nosc52 = false\nnative = false\n",
			want: Config{Theme: DefaultTheme, Clipboard: Clipboard{OSC52: new(false), Native: new(false)}},
		},
		{
			name: "shell integration off", content: "[shell]\nintegration = false\n",
			want: Config{Theme: DefaultTheme, Shell: ShellConfig{Integration: new(false)}},
		},
		{
			name: "layout section", content: "[layout]\ndefault_mode = \"scrolling\"\nmaster_count = 3\n",
			want: func() Config {
				c := DefaultConfig()
				c.Layout.DefaultMode, c.Layout.MasterCount = "scrolling", 3

				return c
			}(),
		},
		{
			name: "diff section", content: "[diff]\nlayout = \"split\"\nmode = \"turn\"\nmax_files = 10\n",
			want: func() Config {
				c := DefaultConfig()
				c.Diff.Layout, c.Diff.Mode, c.Diff.MaxFiles = "split", "turn", 10

				return c
			}(),
		},
		{
			name: "worktrees section", content: "[worktrees]\nroot = \"sibling\"\n",
			want: func() Config {
				c := DefaultConfig()
				c.Worktrees.Root = "sibling"

				return c
			}(),
		},
		{
			name: "pull_requests section", content: "[pull_requests]\nreview_requested_days = 30\n",
			want: func() Config {
				c := DefaultConfig()
				c.PullRequests.ReviewRequestedDays = 30

				return c
			}(),
		},
		{name: "mouse wrong type", content: "[mouse]\ncopy_on_select = \"yes\"\n", wantErr: true, errHas: []string{"copy_on_select"}},
		{name: "unknown keys context", content: "[keys.drawr]\no = \"x\"\n", wantErr: true, errHas: []string{"keys.drawr"}},
		{name: "invalid toml", content: "theme = \"x", wantErr: true},
		{name: "unknown key", content: "them = \"x\"\n", wantErr: true, errHas: []string{"them"}},
		{name: "unreadable", content: "theme = \"catppuccin-latte\"\n", unreadable: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.unreadable && os.Geteuid() == 0 {
				t.Skip("root can read a mode 0o000 file")
			}

			path := filepath.Join(dir, strings.ReplaceAll(tt.name, " ", "_")+".toml")
			if !tt.missing {
				if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			if tt.unreadable {
				if err := os.Chmod(path, 0o000); err != nil {
					t.Fatal(err)
				}
			}

			got, err := LoadConfig(path)

			// A want that leaves Layout empty means the layout defaults.
			if tt.want.Layout == (LayoutConfig{}) {
				tt.want.Layout = DefaultConfig().Layout
			}

			// Likewise for Diff.
			if tt.want.Diff == (DiffConfig{}) {
				tt.want.Diff = DefaultConfig().Diff
			}

			// And for Worktrees.
			if tt.want.Worktrees == (WorktreesConfig{}) {
				tt.want.Worktrees = DefaultConfig().Worktrees
			}

			// And for Plans.
			if tt.want.Plans.Paths == nil {
				tt.want.Plans = DefaultConfig().Plans
			}

			// And for PullRequests; TestPullRequestsConfig loads an explicit 0.
			if tt.want.PullRequests == (PullRequestsConfig{}) {
				tt.want.PullRequests = DefaultConfig().PullRequests
			}

			if !tt.wantErr {
				if err != nil {
					t.Fatalf("LoadConfig() error = %v", err)
				}

				if !reflect.DeepEqual(got, tt.want) {
					t.Errorf("LoadConfig() = %+v, want %+v", got, tt.want)
				}

				return
			}

			if err == nil {
				t.Fatalf("LoadConfig() = %+v, want an error", got)
			}

			for _, s := range append([]string{path}, tt.errHas...) {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("error %q does not contain %q", err, s)
				}
			}
		})
	}
}

func TestLayoutDefaults(t *testing.T) {
	got := DefaultConfig().Layout

	want := LayoutConfig{
		DefaultMode: "bsp", AutoScheme: "spiral", MasterRatio: 50, MasterPosition: "left",
		MasterCount: 1, MasterGrid: new(true), ColumnWidth: 50,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("default layout = %+v, want %+v", got, want)
	}

	if err := got.Validate(); err != nil {
		t.Errorf("default layout: Validate() = %v", err)
	}
}

func TestLayoutZeroValueMeansDefaults(t *testing.T) {
	var z LayoutConfig
	if z.Mode() != ModeBSP || z.Scheme() != tiling.SchemeSpiral || z.ratio() != 0.5 || z.count() != 1 ||
		z.position() != "left" || !z.grid() || z.columnWidth() != 0.5 {
		t.Errorf("zero layout accessors: %v %v %v %v %v %v %v",
			z.Mode(), z.Scheme(), z.ratio(), z.count(), z.position(), z.grid(), z.columnWidth())
	}

	set := LayoutConfig{
		DefaultMode: "master-stack", AutoScheme: "alternate", MasterRatio: 70, MasterCount: 2,
		MasterPosition: "top", MasterGrid: new(false), ColumnWidth: 25,
	}
	if set.Mode() != ModeMaster || set.Scheme() != tiling.SchemeAlternate || set.ratio() != 0.7 || set.count() != 2 ||
		set.position() != "top" || set.grid() || set.columnWidth() != 0.25 {
		t.Errorf("set layout accessors: %v %v %v %v %v %v %v",
			set.Mode(), set.Scheme(), set.ratio(), set.count(), set.position(), set.grid(), set.columnWidth())
	}
}

func TestDiffDefaults(t *testing.T) {
	got := DefaultConfig().Diff

	want := DiffConfig{Layout: "unified", Mode: "branch", MaxFiles: 400, MaxBytes: 2097152, MaxLines: 5000}
	if got != want {
		t.Errorf("default diff = %+v, want %+v", got, want)
	}

	if !got.BranchFirst() {
		t.Error("default diff: BranchFirst() = false, want a new drawer to open in branch mode")
	}

	if err := got.Validate(); err != nil {
		t.Errorf("default diff: Validate() = %v", err)
	}
}

func TestZeroDiffConfigReadsAsTheDefaults(t *testing.T) {
	var zero DiffConfig

	if !zero.BranchFirst() {
		t.Error("a zero DiffConfig: BranchFirst() = false, want the branch default")
	}

	if zero.Split() {
		t.Error("a zero DiffConfig: Split() = true, want the unified default")
	}
}

func TestDiffBranchFirstFollowsTheFile(t *testing.T) {
	t.Parallel()

	if (DiffConfig{Mode: "turn"}).BranchFirst() {
		t.Error("BranchFirst() = true with mode turn, want false")
	}
}

func TestWorktreesConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		c    WorktreesConfig
		want bool
	}{
		{"inside", WorktreesConfig{Root: "inside"}, false},
		{"sibling", WorktreesConfig{Root: "sibling"}, true},
		{"zero value reads as inside", WorktreesConfig{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.c.Sibling(); got != tt.want {
				t.Errorf("Sibling() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPullRequestsConfig(t *testing.T) {
	t.Parallel()

	def := DefaultConfig().PullRequests
	if def.ReviewRequestedDays != 7 {
		t.Errorf("default review_requested_days = %d, want 7", def.ReviewRequestedDays)
	}

	now := time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		days      int
		wantSince time.Time
	}{
		{days: 0},
		{days: 1, wantSince: now.Add(-24 * time.Hour)},
		{days: 7, wantSince: now.Add(-7 * 24 * time.Hour)},
	}
	for _, tt := range tests {
		t.Run(strconv.Itoa(tt.days), func(t *testing.T) {
			t.Parallel()

			c := PullRequestsConfig{ReviewRequestedDays: tt.days}
			if got := c.RequestedSince(now); !got.Equal(tt.wantSince) {
				t.Errorf("RequestedSince() = %v, want %v", got, tt.wantSince)
			}
		})
	}

	if got := def.RequestedSince(now); !got.Equal(now.Add(-7 * 24 * time.Hour)) {
		t.Errorf("default RequestedSince() = %v, want 7 days back", got)
	}
}

// An explicit 0 in the file is no limit, not the default 7.
func TestLoadConfigReviewRequestedDaysZero(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[pull_requests]\nreview_requested_days = 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	if d := cfg.PullRequests.ReviewRequestedDays; d != 0 {
		t.Errorf("review_requested_days = %d, want 0", d)
	}
}

func TestAllowedValuesMatchValidate(t *testing.T) {
	t.Parallel()

	var modes, reviews []string
	for _, m := range LayoutModes() {
		modes = append(modes, string(m))
	}

	for _, m := range ReviewModes() {
		reviews = append(reviews, string(m))
		if !m.Valid() {
			t.Errorf("ReviewMode %q is listed but not Valid", m)
		}
	}

	nameCases := []struct {
		key    string
		values []string
		set    func(c *Config, v string)
		check  func(c Config) error
	}{
		{"layout.default_mode", modes, func(c *Config, v string) { c.Layout.DefaultMode = v }, func(c Config) error { return c.Layout.Validate() }},
		{"layout.auto_scheme", AutoSchemes(), func(c *Config, v string) { c.Layout.AutoScheme = v }, func(c Config) error { return c.Layout.Validate() }},
		{"diff.layout", DiffLayouts(), func(c *Config, v string) { c.Diff.Layout = v }, func(c Config) error { return c.Diff.Validate() }},
		{"diff.mode", reviews, func(c *Config, v string) { c.Diff.Mode = v }, func(c Config) error { return c.Diff.Validate() }},
		{"layout.master_position", tiling.MasterPositions(), func(c *Config, v string) { c.Layout.MasterPosition = v }, func(c Config) error { return c.Layout.Validate() }},
		{"worktrees.root", WorktreeRoots(), func(c *Config, v string) { c.Worktrees.Root = v }, func(c Config) error { return c.Worktrees.Validate() }},
	}

	for _, tc := range nameCases {
		if len(tc.values) == 0 {
			t.Errorf("%s: no allowed values", tc.key)
		}

		for _, v := range tc.values {
			c := DefaultConfig()
			tc.set(&c, v)

			if err := tc.check(c); err != nil {
				t.Errorf("%s = %q: %v", tc.key, v, err)
			}

			c = DefaultConfig()
			tc.set(&c, v+"x")

			if err := tc.check(c); err == nil || !strings.HasPrefix(err.Error(), tc.key+":") {
				t.Errorf("%s = %q: error %v, want prefix %q", tc.key, v+"x", err, tc.key+":")
			}
		}
	}

	rangeCases := []struct {
		key    string
		lo, hi int
		set    func(c *Config, n int)
		check  func(c Config) error
	}{
		{"layout.master_ratio", MinMasterRatio, MaxMasterRatio, func(c *Config, n int) { c.Layout.MasterRatio = n }, func(c Config) error { return c.Layout.Validate() }},
		{"layout.master_count", MinMasterCount, MaxMasterCount, func(c *Config, n int) { c.Layout.MasterCount = n }, func(c Config) error { return c.Layout.Validate() }},
		{"layout.column_width", MinColumnWidth, MaxColumnWidth, func(c *Config, n int) { c.Layout.ColumnWidth = n }, func(c Config) error { return c.Layout.Validate() }},
		{"pull_requests.review_requested_days", MinReviewRequestedDays, MaxReviewRequestedDays, func(c *Config, n int) { c.PullRequests.ReviewRequestedDays = n }, func(c Config) error { return c.PullRequests.Validate() }},
		{"keys.which_key_delay", MinWhichKeyDelay, MaxWhichKeyDelay, func(c *Config, n int) { c.Keys.WhichKeyDelay = &n }, func(c Config) error { return c.Keys.Validate() }},
	}

	for _, tc := range rangeCases {
		for _, n := range []int{tc.lo, tc.hi} {
			c := DefaultConfig()
			tc.set(&c, n)

			if err := tc.check(c); err != nil {
				t.Errorf("%s = %d: %v", tc.key, n, err)
			}
		}

		for _, n := range []int{tc.lo - 1, tc.hi + 1} {
			c := DefaultConfig()
			tc.set(&c, n)

			if err := tc.check(c); err == nil || !strings.HasPrefix(err.Error(), tc.key+":") {
				t.Errorf("%s = %d: error %v, want prefix %q", tc.key, n, err, tc.key+":")
			}
		}
	}

	limits := []struct {
		key string
		set func(c *Config, n int)
	}{
		{"diff.max_files", func(c *Config, n int) { c.Diff.MaxFiles = n }},
		{"diff.max_bytes", func(c *Config, n int) { c.Diff.MaxBytes = n }},
		{"diff.max_lines", func(c *Config, n int) { c.Diff.MaxLines = n }},
	}

	for _, tc := range limits {
		c := DefaultConfig()
		tc.set(&c, MinDiffLimit)

		if err := c.Diff.Validate(); err != nil {
			t.Errorf("%s = %d: %v", tc.key, MinDiffLimit, err)
		}

		c = DefaultConfig()
		tc.set(&c, MinDiffLimit-1)

		if err := c.Diff.Validate(); err == nil || !strings.HasPrefix(err.Error(), tc.key+":") {
			t.Errorf("%s = %d: error %v, want prefix %q", tc.key, MinDiffLimit-1, err, tc.key+":")
		}
	}

	want := []LayoutMode{ModeBSP, ModeMaster, ModeScrolling}
	if got := LayoutModes(); !reflect.DeepEqual(got, want) {
		t.Errorf("LayoutModes() = %v, want %v", got, want)
	}

	LayoutModes()[0] = "x"

	if got := LayoutModes(); !reflect.DeepEqual(got, want) {
		t.Errorf("LayoutModes() after a write = %v, want %v", got, want)
	}
}
