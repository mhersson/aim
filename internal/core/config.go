package core

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mhersson/aim/internal/core/tiling"
	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// DefaultTheme is the theme used when config.toml does not set one.
const DefaultTheme = "catppuccin-mocha"

// Config is the user's config.toml.
type Config struct {
	Theme string `toml:"theme"`
	// Icons is a pointer so an absent key (nil) can mean true.
	Icons        *bool              `toml:"icons"`
	Mouse        Mouse              `toml:"mouse"`
	Clipboard    Clipboard          `toml:"clipboard"`
	Shell        ShellConfig        `toml:"shell"`
	Layout       LayoutConfig       `toml:"layout"`
	Diff         DiffConfig         `toml:"diff"`
	Worktrees    WorktreesConfig    `toml:"worktrees"`
	Plans        PlansConfig        `toml:"plans"`
	PullRequests PullRequestsConfig `toml:"pull_requests"`
	Keys         KeysConfig         `toml:"keys"`
}

// LayoutConfig is the [layout] section. Validate rejects an out-of-range value
// from a file; the accessors read a zero field as its default so a
// hand-built LayoutConfig behaves as the defaults.
type LayoutConfig struct {
	DefaultMode    string `toml:"default_mode"`    // bsp | master-stack | scrolling
	AutoScheme     string `toml:"auto_scheme"`     // spiral | longest_side | alternate | smart_split
	MasterRatio    int    `toml:"master_ratio"`    // percent, 10-90
	MasterPosition string `toml:"master_position"` // left | right | top | bottom | center
	MasterCount    int    `toml:"master_count"`    // 1-9
	MasterGrid     *bool  `toml:"master_grid"`     // nil means true
	ColumnWidth    int    `toml:"column_width"`    // percent, 10-100
}

// Bounds of the numeric config keys.
const (
	MinMasterRatio = 10
	MaxMasterRatio = 90
	MinMasterCount = 1
	MaxMasterCount = 9
	MinColumnWidth = 10
	MaxColumnWidth = 100
	// MinDiffLimit is the lowest [diff] max_files, max_bytes and max_lines;
	// they have no upper bound.
	MinDiffLimit           = 1
	MinReviewRequestedDays = 0
	// MaxReviewRequestedDays is ten years; the bound also keeps the cutoff's
	// duration far from overflowing.
	MaxReviewRequestedDays = 3650
)

// AutoSchemes lists the allowed layout.auto_scheme values.
func AutoSchemes() []string { return []string{"spiral", "longest_side", "alternate", "smart_split"} }

// DiffLayouts lists the allowed diff.layout values.
func DiffLayouts() []string { return []string{"unified", "split"} }

// WorktreeRoots lists the allowed worktrees.root values.
func WorktreeRoots() []string { return []string{"inside", "sibling"} }

// Validate names the first key that is out of range or unknown.
func (c LayoutConfig) Validate() error {
	if !validMode(LayoutMode(c.DefaultMode)) {
		return fmt.Errorf("layout.default_mode: %q is not bsp, master-stack or scrolling", c.DefaultMode)
	}

	if !slices.Contains(AutoSchemes(), c.AutoScheme) {
		return fmt.Errorf("layout.auto_scheme: %q is not %s", c.AutoScheme, strings.Join(AutoSchemes(), ", "))
	}

	if err := inRange("layout.master_ratio", c.MasterRatio, MinMasterRatio, MaxMasterRatio); err != nil {
		return err
	}

	if !slices.Contains(tiling.MasterPositions(), c.MasterPosition) {
		return fmt.Errorf("layout.master_position: %q is not %s", c.MasterPosition, strings.Join(tiling.MasterPositions(), ", "))
	}

	if err := inRange("layout.master_count", c.MasterCount, MinMasterCount, MaxMasterCount); err != nil {
		return err
	}

	return inRange("layout.column_width", c.ColumnWidth, MinColumnWidth, MaxColumnWidth)
}

func inRange(key string, v, lo, hi int) error {
	if v < lo || v > hi {
		return fmt.Errorf("%s: %d is out of range %d-%d", key, v, lo, hi)
	}

	return nil
}

// Mode is the mode new tabs start in.
func (c LayoutConfig) Mode() LayoutMode {
	if c.DefaultMode == "" {
		return ModeBSP
	}

	return LayoutMode(c.DefaultMode)
}

// Scheme is the auto split scheme for BSP.
func (c LayoutConfig) Scheme() tiling.AutoScheme {
	if c.AutoScheme == "" {
		return tiling.SchemeSpiral
	}

	return tiling.ParseAutoScheme(c.AutoScheme)
}

func orDefault(v, def int) int {
	if v == 0 {
		return def
	}

	return v
}

// ratio is the masters' share of the body, 0.1-0.9.
func (c LayoutConfig) ratio() float64 {
	return float64(min(max(orDefault(c.MasterRatio, 50), MinMasterRatio), MaxMasterRatio)) / 100
}

func (c LayoutConfig) count() int { return orDefault(c.MasterCount, 1) }

func (c LayoutConfig) position() string {
	if c.MasterPosition == "" {
		return tiling.MasterPositionLeft
	}

	return c.MasterPosition
}

func (c LayoutConfig) grid() bool { return c.MasterGrid == nil || *c.MasterGrid }

// columnWidth is a new scrolling column's share of the body, 0.1-1.0.
func (c LayoutConfig) columnWidth() float64 {
	return float64(min(max(orDefault(c.ColumnWidth, 50), MinColumnWidth), MaxColumnWidth)) / 100
}

// DiffConfig is the [diff] section. Validate rejects an unknown name or a
// non-positive limit from a file; the accessors read a zero field as its
// default so a hand-built DiffConfig behaves as the defaults.
type DiffConfig struct {
	Layout   string `toml:"layout"`    // unified | split: the layout a drawer starts in
	Mode     string `toml:"mode"`      // turn | branch: what a new drawer opens in
	MaxFiles int    `toml:"max_files"` // the engine's limits
	MaxBytes int    `toml:"max_bytes"`
	MaxLines int    `toml:"max_lines"`
}

// Validate names the first key that is unknown or not positive.
func (c DiffConfig) Validate() error {
	if !slices.Contains(DiffLayouts(), c.Layout) {
		return fmt.Errorf("diff.layout: %q is not unified or split", c.Layout)
	}

	if !ReviewMode(c.Mode).Valid() {
		return fmt.Errorf("diff.mode: %q is not turn or branch", c.Mode)
	}

	for _, l := range []struct {
		key string
		n   int
	}{{"diff.max_files", c.MaxFiles}, {"diff.max_bytes", c.MaxBytes}, {"diff.max_lines", c.MaxLines}} {
		if l.n < MinDiffLimit {
			return fmt.Errorf("%s: %d is not a positive number", l.key, l.n)
		}
	}

	return nil
}

// Split reports whether a new drawer starts side by side.
func (c DiffConfig) Split() bool { return c.Layout == "split" }

// BranchFirst reports whether a new drawer opens in branch mode even when the
// pane has turns.
func (c DiffConfig) BranchFirst() bool { return c.Mode != string(ReviewTurn) }

// WorktreesConfig is the [worktrees] section. Validate rejects an unknown root
// from a file; Sibling reads a zero field as the default, so a hand-built
// WorktreesConfig behaves as the defaults.
type WorktreesConfig struct {
	Root string `toml:"root"` // inside | sibling: where a new worktree goes
}

// Validate names the key when the root is unknown.
func (c WorktreesConfig) Validate() error {
	if !slices.Contains(WorktreeRoots(), c.Root) {
		return fmt.Errorf("worktrees.root: %q is not inside or sibling", c.Root)
	}

	return nil
}

// Sibling reports whether a new worktree goes beside the main checkout rather
// than inside it. internal/git owns the layouts, so the UI maps this to its
// root style.
func (c WorktreesConfig) Sibling() bool { return c.Root == "sibling" }

// PlansConfig is the [plans] section: folder paths matched anywhere in the path
// of an edited file; markdown files under one count as plans.
type PlansConfig struct {
	Paths []string `toml:"paths"`
}

// Validate names the first entry that is not a relative folder path.
func (c PlansConfig) Validate() error {
	for _, p := range c.Paths {
		clean := filepath.Clean(p)
		if p == "" || filepath.IsAbs(p) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("plans.paths: %q is not a relative folder path", p)
		}
	}

	return nil
}

// PullRequestsConfig is the [pull_requests] section. ReviewRequestedDays
// limits the Review requested tab to pull requests updated in that many days;
// 0, the zero value, is no limit.
type PullRequestsConfig struct {
	ReviewRequestedDays int `toml:"review_requested_days"`
}

// Validate names the key when the days are out of range.
func (c PullRequestsConfig) Validate() error {
	return inRange("pull_requests.review_requested_days", c.ReviewRequestedDays, MinReviewRequestedDays, MaxReviewRequestedDays)
}

// RequestedSince is the oldest update a review request may have at now, or
// the zero time for no limit. Whole days of 24 hours keep a DST change from
// moving the cutoff.
func (c PullRequestsConfig) RequestedSince(now time.Time) time.Time {
	if c.ReviewRequestedDays <= 0 {
		return time.Time{}
	}

	return now.Add(-time.Duration(c.ReviewRequestedDays) * 24 * time.Hour)
}

// Mouse is the [mouse] section; both switches are off by default.
type Mouse struct {
	FocusFollowsMouse bool `toml:"focus_follows_mouse"`
	CopyOnSelect      bool `toml:"copy_on_select"`
}

// Clipboard is the [clipboard] section. The pointers let an absent key (nil)
// mean true.
type Clipboard struct {
	OSC52  *bool `toml:"osc52"`
	Native *bool `toml:"native"`
}

// OSC52On reports whether copies are sent as OSC 52; the default is on.
func (c Clipboard) OSC52On() bool { return c.OSC52 == nil || *c.OSC52 }

// NativeOn reports whether copies also go to native clipboard tools on a
// local session; the default is on.
func (c Clipboard) NativeOn() bool { return c.Native == nil || *c.Native }

// ShellConfig is the [shell] section. The pointer lets an absent key (nil)
// mean true.
type ShellConfig struct {
	Integration *bool `toml:"integration"`
}

// IntegrationOn reports whether a claude typed in a pane's shell gets AIM's
// hooks; the default is on.
func (c ShellConfig) IntegrationOn() bool { return c.Integration == nil || *c.Integration }

// IconsOn reports whether Nerd Font glyphs are drawn; the default is on.
func (c Config) IconsOn() bool {
	return c.Icons == nil || *c.Icons
}

// DefaultConfig returns the config used for keys the file does not set.
func DefaultConfig() Config {
	return Config{
		Theme: DefaultTheme,
		Layout: LayoutConfig{
			DefaultMode: string(ModeBSP), AutoScheme: "spiral", MasterRatio: 50, MasterPosition: tiling.MasterPositionLeft,
			MasterCount: 1, MasterGrid: new(true), ColumnWidth: 50,
		},
		Diff:         DiffConfig{Layout: "unified", Mode: string(ReviewBranch), MaxFiles: 400, MaxBytes: 2 << 20, MaxLines: 5000},
		Worktrees:    WorktreesConfig{Root: "inside"},
		Plans:        PlansConfig{Paths: []string{"docs/superpowers/plans/", "docs/superpowers/specs/"}},
		PullRequests: PullRequestsConfig{ReviewRequestedDays: 7},
	}
}

// LoadConfig reads the config file at path over the defaults. A missing file
// gives the defaults; unknown keys are errors, so a typo never goes unnoticed.
// Values such as theme names are not validated here.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return DefaultConfig(), nil
	}

	if err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}

	cfg := DefaultConfig()
	if err := DecodeStrict(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}

	cfg.Keys.Order = keyOrder(data)

	return cfg, nil
}

// DecodeStrict decodes the TOML in data into v, which must hold a field for
// every key. Its errors name the unknown keys, or the line and key of a value
// that does not decode, for the caller to put after the file's path.
func DecodeStrict(data []byte, v any) error {
	err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(v)

	// StrictMissingError unwraps to DecodeErrors, so it is matched first.
	var (
		strictErr *toml.StrictMissingError
		decodeErr *toml.DecodeError
	)
	switch {
	case err == nil:
		return nil
	case errors.As(err, &strictErr):
		keys := make([]string, len(strictErr.Errors))
		for i := range strictErr.Errors {
			keys[i] = strings.Join(strictErr.Errors[i].Key(), ".")
		}

		return fmt.Errorf("unknown key %s", strings.Join(keys, ", "))
	case errors.As(err, &decodeErr):
		return errors.New(decodeText(decodeErr))
	default:
		return err //nolint:wrapcheck // the caller names the file
	}
}

// decodeText is a decode error as "line N: key: problem".
func decodeText(e *toml.DecodeError) string {
	line, _ := e.Position()

	msg := strings.TrimPrefix(e.Error(), "toml: ")
	if key := e.Key(); len(key) > 0 {
		msg = strings.Join(key, ".") + ": " + msg
	}

	return fmt.Sprintf("line %d: %s", line, msg)
}

// ConfigWithTheme is a config file's data with its top-level theme set to name.
// Only the value of an uncommented top-level theme key changes; with none, a
// theme line goes first. Every other byte is kept.
func ConfigWithTheme(data []byte, name string) ([]byte, error) {
	if err := toml.Unmarshal(data, &map[string]any{}); err != nil {
		if de := (*toml.DecodeError)(nil); errors.As(err, &de) {
			return nil, fmt.Errorf("not valid TOML: %s", decodeText(de))
		}

		return nil, fmt.Errorf("not valid TOML: %w", err)
	}

	// Theme names are ASCII, where Go's quoting is TOML's.
	value := strconv.Quote(name)

	var p unstable.Parser

	p.Reset(data)

	for p.NextExpression() {
		e := p.Expression()
		if e.Kind == unstable.Table || e.Kind == unstable.ArrayTable {
			break
		}

		if e.Kind == unstable.KeyValue && slices.Equal(keyParts(e.Key()), []string{"theme"}) {
			r := e.Value().Raw

			return slices.Concat(data[:r.Offset], []byte(value), data[r.Offset+r.Length:]), nil
		}
	}

	return slices.Concat([]byte("theme = "+value+"\n"), data), nil
}
