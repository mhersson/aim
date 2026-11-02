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
	tests := []struct {
		name       string
		content    string
		missing    bool
		unreadable bool
		want       Config
		wantErr    bool
		errHas     []string // substrings besides the path
	}{
		{name: "missing file", missing: true, want: DefaultConfig()},
		{name: "empty file", content: "", want: DefaultConfig()},
		{name: "comments only", content: "# theme = \"x\"\n\n# more\n", want: DefaultConfig()},
		{name: "theme set", content: "theme = \"catppuccin-latte\"\n", want: Config{Theme: "catppuccin-latte"}},
		{name: "theme name not validated", content: "theme = \"no-such-theme\"\n", want: Config{Theme: "no-such-theme"}},
		{name: "icons off", content: "icons = false\n", want: Config{Theme: DefaultTheme, Icons: new(false)}},
		{name: "icons on", content: "icons = true\n", want: Config{Theme: DefaultTheme, Icons: new(true)}},
		{name: "icons wrong type", content: "icons = \"no\"\n", wantErr: true, errHas: []string{"line 1", "icons"}},
		{
			name: "mouse options", content: "[mouse]\nfocus_follows_mouse = true\ncopy_on_select = true\n",
			want: Config{Theme: DefaultTheme, Mouse: Mouse{FocusFollowsMouse: true, CopyOnSelect: true}},
		},
		{
			name: "mouse partly set", content: "[mouse]\ncopy_on_select = true\n",
			want: Config{Theme: DefaultTheme, Mouse: Mouse{CopyOnSelect: true}},
		},
		{
			name: "clipboard off", content: "[clipboard]\nosc52 = false\nnative = false\n",
			want: Config{Theme: DefaultTheme, Clipboard: Clipboard{OSC52: new(false), Native: new(false)}},
		},
		{
			name: "clipboard native only off", content: "[clipboard]\nnative = false\n",
			want: Config{Theme: DefaultTheme, Clipboard: Clipboard{Native: new(false)}},
		},
		{name: "unknown mouse key", content: "[mouse]\nfocus = true\n", wantErr: true, errHas: []string{"unknown key mouse.focus"}},
		{
			name: "shell integration off", content: "[shell]\nintegration = false\n",
			want: Config{Theme: DefaultTheme, Shell: ShellConfig{Integration: new(false)}},
		},
		{name: "unknown clipboard key", content: "[clipboard]\nosc = true\n", wantErr: true, errHas: []string{"unknown key clipboard.osc"}},
		{name: "mouse wrong type", content: "[mouse]\ncopy_on_select = \"yes\"\n", wantErr: true, errHas: []string{"copy_on_select"}},
		{
			name: "layout section", content: "[layout]\ndefault_mode = \"scrolling\"\nmaster_count = 3\n",
			want: func() Config {
				c := DefaultConfig()
				c.Layout.DefaultMode, c.Layout.MasterCount = "scrolling", 3

				return c
			}(),
		},
		{name: "unknown layout key", content: "[layout]\nmaster = 3\n", wantErr: true, errHas: []string{"unknown key layout.master"}},
		{
			name: "diff section", content: "[diff]\nlayout = \"split\"\nmode = \"turn\"\nmax_files = 10\n",
			want: func() Config {
				c := DefaultConfig()
				c.Diff.Layout, c.Diff.Mode, c.Diff.MaxFiles = "split", "turn", 10

				return c
			}(),
		},
		{name: "unknown diff key", content: "[diff]\nsplit = true\n", wantErr: true, errHas: []string{"unknown key diff.split"}},
		{
			name: "worktrees section", content: "[worktrees]\nroot = \"sibling\"\n",
			want: func() Config {
				c := DefaultConfig()
				c.Worktrees.Root = "sibling"

				return c
			}(),
		},
		{name: "unknown worktrees key", content: "[worktrees]\nroots = \"inside\"\n", wantErr: true, errHas: []string{"unknown key worktrees.roots"}},
		{name: "worktrees wrong type", content: "[worktrees]\nroot = 3\n", wantErr: true, errHas: []string{"worktrees.root"}},
		{
			name: "pull_requests section", content: "[pull_requests]\nreview_requested_days = 30\n",
			want: func() Config {
				c := DefaultConfig()
				c.PullRequests.ReviewRequestedDays = 30

				return c
			}(),
		},
		{name: "unknown pull_requests key", content: "[pull_requests]\nrequested_days = 3\n", wantErr: true, errHas: []string{"unknown key pull_requests.requested_days"}},
		{name: "pull_requests wrong type", content: "[pull_requests]\nreview_requested_days = \"week\"\n", wantErr: true, errHas: []string{"pull_requests.review_requested_days"}},
		{name: "unknown keys key", content: "[keys]\nleaderkey = \"ctrl+a\"\n", wantErr: true, errHas: []string{"unknown key keys.leaderkey"}},
		{name: "unknown keys context", content: "[keys.drawr]\no = \"x\"\n", wantErr: true, errHas: []string{"unknown key keys.drawr"}},
		{name: "diff wrong type", content: "[diff]\nmax_lines = \"many\"\n", wantErr: true, errHas: []string{"max_lines"}},
		{name: "layout wrong type", content: "[layout]\nmaster_ratio = \"half\"\n", wantErr: true, errHas: []string{"master_ratio"}},
		{name: "invalid toml", content: "theme = \"x", wantErr: true, errHas: []string{"line 1"}},
		{name: "unknown key", content: "them = \"x\"\n", wantErr: true, errHas: []string{"unknown key them"}},
		{name: "wrong type", content: "theme = 3\n", wantErr: true, errHas: []string{"line 1", "theme"}},
		{name: "unreadable", content: "theme = \"catppuccin-latte\"\n", unreadable: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.unreadable && os.Geteuid() == 0 {
				t.Skip("root can read a mode 0o000 file")
			}

			path := filepath.Join(t.TempDir(), "config.toml")
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

func TestLayoutValidate(t *testing.T) {
	with := func(f func(*LayoutConfig)) LayoutConfig {
		c := DefaultConfig().Layout
		f(&c)

		return c
	}

	good := []struct {
		name string
		c    LayoutConfig
	}{
		{"ratio low", with(func(c *LayoutConfig) { c.MasterRatio = 10 })},
		{"ratio high", with(func(c *LayoutConfig) { c.MasterRatio = 90 })},
		{"count low", with(func(c *LayoutConfig) { c.MasterCount = 1 })},
		{"count high", with(func(c *LayoutConfig) { c.MasterCount = 9 })},
		{"width low", with(func(c *LayoutConfig) { c.ColumnWidth = 10 })},
		{"width high", with(func(c *LayoutConfig) { c.ColumnWidth = 100 })},
		{"master-stack", with(func(c *LayoutConfig) { c.DefaultMode = "master-stack" })},
		{"scrolling", with(func(c *LayoutConfig) { c.DefaultMode = "scrolling" })},
		{"longest_side", with(func(c *LayoutConfig) { c.AutoScheme = "longest_side" })},
		{"alternate", with(func(c *LayoutConfig) { c.AutoScheme = "alternate" })},
		{"smart_split", with(func(c *LayoutConfig) { c.AutoScheme = "smart_split" })},
		{"right", with(func(c *LayoutConfig) { c.MasterPosition = "right" })},
		{"top", with(func(c *LayoutConfig) { c.MasterPosition = "top" })},
		{"bottom", with(func(c *LayoutConfig) { c.MasterPosition = "bottom" })},
		{"center", with(func(c *LayoutConfig) { c.MasterPosition = "center" })},
		{"grid off", with(func(c *LayoutConfig) { c.MasterGrid = new(false) })},
	}
	for _, tt := range good {
		if err := tt.c.Validate(); err != nil {
			t.Errorf("%s: Validate() = %v", tt.name, err)
		}
	}

	bad := []struct {
		name string
		c    LayoutConfig
		want string
	}{
		{"ratio high", with(func(c *LayoutConfig) { c.MasterRatio = 95 }), "layout.master_ratio: 95 is out of range 10-90"},
		{"ratio low", with(func(c *LayoutConfig) { c.MasterRatio = 9 }), "layout.master_ratio"},
		{"count zero", with(func(c *LayoutConfig) { c.MasterCount = 0 }), "layout.master_count: 0 is out of range 1-9"},
		{"count ten", with(func(c *LayoutConfig) { c.MasterCount = 10 }), "layout.master_count"},
		{"width small", with(func(c *LayoutConfig) { c.ColumnWidth = 5 }), "layout.column_width: 5 is out of range 10-100"},
		{"width big", with(func(c *LayoutConfig) { c.ColumnWidth = 101 }), "layout.column_width"},
		{"mode", with(func(c *LayoutConfig) { c.DefaultMode = "tiled" }), "layout.default_mode"},
		{"scheme", with(func(c *LayoutConfig) { c.AutoScheme = "x" }), "layout.auto_scheme"},
		{"position", with(func(c *LayoutConfig) { c.MasterPosition = "middle" }), "layout.master_position"},
	}
	for _, tt := range bad {
		err := tt.c.Validate()
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: Validate() = %v, want it to contain %q", tt.name, err, tt.want)
		}
	}
}

func TestLoadConfigLayoutOutOfRangeFailsValidate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[layout]\nmaster_count = 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := cfg.Layout.Validate(); err == nil || !strings.Contains(err.Error(), "layout.master_count") {
		t.Errorf("Validate() = %v, want an error naming layout.master_count", err)
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
	tests := []struct {
		name, content string
		want          bool
	}{
		{"no diff section", "", true},
		{"mode branch", "[diff]\nmode = \"branch\"\n", true},
		{"mode turn", "[diff]\nmode = \"turn\"\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}

			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}

			if got := cfg.Diff.BranchFirst(); got != tt.want {
				t.Errorf("BranchFirst() = %v with mode %q, want %v", got, cfg.Diff.Mode, tt.want)
			}
		})
	}
}

func TestDiffConfigValidate(t *testing.T) {
	with := func(f func(*DiffConfig)) DiffConfig {
		c := DefaultConfig().Diff
		f(&c)

		return c
	}

	good := []struct {
		name string
		c    DiffConfig
	}{
		{"split", with(func(c *DiffConfig) { c.Layout = "split" })},
		{"turn", with(func(c *DiffConfig) { c.Mode = "turn" })},
		{"smallest limits", with(func(c *DiffConfig) { c.MaxFiles, c.MaxBytes, c.MaxLines = 1, 1, 1 })},
	}
	for _, tt := range good {
		if err := tt.c.Validate(); err != nil {
			t.Errorf("%s: Validate() = %v", tt.name, err)
		}
	}

	bad := []struct {
		name string
		c    DiffConfig
		want string
	}{
		{"layout", with(func(c *DiffConfig) { c.Layout = "tiled" }), `diff.layout: "tiled" is not unified or split`},
		{"empty layout", with(func(c *DiffConfig) { c.Layout = "" }), "diff.layout"},
		{"mode", with(func(c *DiffConfig) { c.Mode = "last" }), `diff.mode: "last" is not turn or branch`},
		{"files zero", with(func(c *DiffConfig) { c.MaxFiles = 0 }), "diff.max_files: 0 is not a positive number"},
		{"files negative", with(func(c *DiffConfig) { c.MaxFiles = -5 }), "diff.max_files: -5"},
		{"bytes zero", with(func(c *DiffConfig) { c.MaxBytes = 0 }), "diff.max_bytes"},
		{"lines negative", with(func(c *DiffConfig) { c.MaxLines = -1 }), "diff.max_lines"},
	}
	for _, tt := range bad {
		err := tt.c.Validate()
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: Validate() = %v, want it to contain %q", tt.name, err, tt.want)
		}
	}
}

func TestWorktreesConfig(t *testing.T) {
	def := DefaultConfig().Worktrees
	if def.Root != "inside" || def.Sibling() {
		t.Errorf("default worktrees = %+v (sibling %v), want root inside", def, def.Sibling())
	}

	if err := def.Validate(); err != nil {
		t.Errorf("default worktrees: Validate() = %v", err)
	}

	tests := []struct {
		root        string
		wantSibling bool
		wantErr     string
	}{
		{root: "inside"},
		{root: "sibling", wantSibling: true},
		{root: "", wantErr: `worktrees.root: "" is not inside or sibling`},
		{root: "beside", wantErr: `worktrees.root: "beside" is not inside or sibling`},
		{root: "Inside", wantErr: "worktrees.root"},
	}
	for _, tt := range tests {
		t.Run(tt.root, func(t *testing.T) {
			c := WorktreesConfig{Root: tt.root}

			err := c.Validate()
			if tt.wantErr == "" && err != nil {
				t.Fatalf("Validate() = %v", err)
			}

			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("Validate() = %v, want it to contain %q", err, tt.wantErr)
			}

			if got := c.Sibling(); got != tt.wantSibling {
				t.Errorf("Sibling() = %v, want %v", got, tt.wantSibling)
			}
		})
	}

	// A hand-built config reads as the default.
	if (WorktreesConfig{}).Sibling() {
		t.Error("the zero WorktreesConfig is sibling, want inside")
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
		wantErr   string
		wantSince time.Time
	}{
		{days: 0},
		{days: 1, wantSince: now.Add(-24 * time.Hour)},
		{days: 7, wantSince: now.Add(-7 * 24 * time.Hour)},
		{days: 3650, wantSince: now.Add(-3650 * 24 * time.Hour)},
		{days: -1, wantErr: "pull_requests.review_requested_days: -1 is out of range 0-3650"},
		{days: 3651, wantErr: "pull_requests.review_requested_days: 3651 is out of range 0-3650"},
	}
	for _, tt := range tests {
		t.Run(strconv.Itoa(tt.days), func(t *testing.T) {
			t.Parallel()

			c := PullRequestsConfig{ReviewRequestedDays: tt.days}

			err := c.Validate()
			if tt.wantErr == "" && err != nil {
				t.Fatalf("Validate() = %v", err)
			}

			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("Validate() = %v, want %q", err, tt.wantErr)
				}

				return
			}

			if got := c.RequestedSince(now); !got.Equal(tt.wantSince) {
				t.Errorf("RequestedSince() = %v, want %v", got, tt.wantSince)
			}
		})
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
