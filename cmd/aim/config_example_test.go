package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2/unstable"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/core/tiling"
	"github.com/mhersson/aim/internal/ui/theme"
)

const examplePath = "../../config.example.toml"

type exampleKind int

const (
	kindBlank exampleKind = iota
	kindProse
	kindHeader
	kindSetting
	kindRemap
)

// exampleLine is one classified line of the example. For a setting or remap,
// key is the dotted key (a remap's is the table plus the quoted key) and
// prose and allowed hold the "##" lines directly above it.
type exampleLine struct {
	n       int
	raw     string
	kind    exampleKind
	table   string
	key     string // the key as written, unquoted
	value   string // a remap's command
	dotted  string
	prose   int
	allowed []string
}

var headerRe = regexp.MustCompile(`^\[[a-z_]+(\.[a-z_]+)*\]$`)

// parseSetting reads the text after "# " as exactly one "key = value" with no
// trailing comment, returning the key and, for a string value, the value.
func parseSetting(rest string) (key, str string, err error) {
	var p unstable.Parser

	p.KeepComments = true
	p.Reset([]byte(rest))

	var (
		parts []string
		count int
	)

	for p.NextExpression() {
		e := p.Expression()

		count++
		if count > 1 || e.Kind != unstable.KeyValue {
			return "", "", fmt.Errorf("not one key = value")
		}

		if e.Next().Valid() {
			return "", "", fmt.Errorf("a trailing comment follows the setting")
		}

		for it := e.Key(); it.Next(); {
			parts = append(parts, string(it.Node().Data))
		}

		if v := e.Value(); v.Kind == unstable.String {
			str = string(v.Data)
		}
	}

	if err := p.Error(); err != nil {
		return "", "", fmt.Errorf("not TOML: %w", err)
	}

	if count == 0 {
		return "", "", fmt.Errorf("not one key = value")
	}

	if len(parts) != 1 {
		return "", "", fmt.Errorf("a dotted key")
	}

	return parts[0], str, nil
}

// scanExample classifies every line and returns the format problems.
func scanExample(text string) ([]exampleLine, []string) {
	var (
		lines    []exampleLine
		problems []string
		table    string
		prose    int
		allowed  []string
	)

	for i, raw := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		l := exampleLine{n: i + 1, raw: raw, table: table}
		bad := func(why string) {
			problems = append(problems, fmt.Sprintf("line %d: %s: %q", l.n, why, raw))
		}

		switch {
		case raw == "":
			l.kind = kindBlank

			if len(allowed) > 0 {
				bad("a values or range line is attached to no setting")
			}

			prose, allowed = 0, nil
		case raw == "##" || strings.HasPrefix(raw, "## "):
			l.kind = kindProse

			body := strings.TrimPrefix(strings.TrimPrefix(raw, "##"), " ")
			if strings.HasPrefix(body, "values: ") || strings.HasPrefix(body, "range: ") {
				allowed = append(allowed, body)
			} else {
				prose++
			}
		case headerRe.MatchString(raw):
			l.kind = kindHeader

			if len(allowed) > 0 {
				bad("a values or range line is attached to no setting")
			}

			table = strings.Trim(raw, "[]")
			l.table = table
			prose, allowed = 0, nil
		case strings.HasPrefix(raw, "# "):
			key, str, err := parseSetting(raw[2:])
			if err != nil {
				bad(err.Error())

				continue
			}

			l.kind = kindSetting
			l.key, l.value, l.prose, l.allowed = key, str, prose, allowed

			l.dotted = key
			if table != "" {
				l.dotted = table + "." + key
			}

			if strings.HasPrefix(table, "keys.") {
				l.kind = kindRemap
			}

			prose, allowed = 0, nil
		default:
			bad("not prose, a setting, a header or blank")

			continue
		}

		lines = append(lines, l)
	}

	if len(allowed) > 0 {
		problems = append(problems, "end of file: a values or range line is attached to no setting")
	}

	return lines, problems
}

// uncomment strips the leading "# " of the setting and remap lines pred picks.
func uncomment(lines []exampleLine, pred func(exampleLine) bool) string {
	out := make([]string, len(lines))

	for i, l := range lines {
		out[i] = l.raw
		if (l.kind == kindSetting || l.kind == kindRemap) && pred(l) {
			out[i] = l.raw[2:]
		}
	}

	return strings.Join(out, "\n") + "\n"
}

func loadChecked(t *testing.T, text string) (core.Config, error) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := core.LoadConfig(path)
	if err != nil {
		return cfg, err //nolint:wrapcheck // the message is the problem text
	}

	return cfg, validateConfig(cfg)
}

func normalized(c core.Config) core.Config {
	c.Icons = new(c.IconsOn())
	c.Clipboard.OSC52 = new(c.Clipboard.OSC52On())
	c.Clipboard.Native = new(c.Clipboard.NativeOn())
	c.Shell.Integration = new(c.Shell.IntegrationOn())
	c.Layout.MasterGrid = new(c.Layout.MasterGrid == nil || *c.Layout.MasterGrid)
	c.Keys.WhichKeyDelay = new(int(c.Keys.Delay() / time.Millisecond))
	c.Keys.LeaderKey = c.Keys.LeaderName()
	c.Keys.Order = nil

	for _, m := range []*map[string]string{
		&c.Keys.Leader, &c.Keys.Drawer, &c.Keys.Rail, &c.Keys.PRList,
		&c.Keys.Checks, &c.Keys.Reader, &c.Keys.Merge, &c.Keys.Compose,
	} {
		if len(*m) == 0 {
			*m = nil
		}
	}

	return c
}

// allowedSpec is what the code allows for one key.
type allowedSpec struct {
	values []string // set when the key takes names
	lo, hi int      // set when it takes a number; hi < 0 means no upper bound
	unit   string
}

func names[T ~string](in []T) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = string(v)
	}

	return out
}

func allowedByKey() map[string]allowedSpec {
	bools := allowedSpec{values: []string{"true", "false"}}

	return map[string]allowedSpec{
		"theme":                     {values: theme.Names()},
		"icons":                     bools,
		"layout.default_mode":       {values: names(core.LayoutModes())},
		"layout.auto_scheme":        {values: core.AutoSchemes()},
		"layout.master_ratio":       {lo: core.MinMasterRatio, hi: core.MaxMasterRatio},
		"layout.master_position":    {values: tiling.MasterPositions()},
		"layout.master_count":       {lo: core.MinMasterCount, hi: core.MaxMasterCount},
		"layout.master_grid":        bools,
		"layout.column_width":       {lo: core.MinColumnWidth, hi: core.MaxColumnWidth},
		"mouse.focus_follows_mouse": bools,
		"mouse.copy_on_select":      bools,
		"clipboard.osc52":           bools,
		"clipboard.native":          bools,
		"shell.integration":         bools,
		"diff.layout":               {values: core.DiffLayouts()},
		"diff.mode":                 {values: names(core.ReviewModes())},
		"diff.max_files":            {lo: core.MinDiffLimit, hi: -1},
		"diff.max_bytes":            {lo: core.MinDiffLimit, hi: -1, unit: "bytes"},
		"diff.max_lines":            {lo: core.MinDiffLimit, hi: -1},
		"worktrees.root":            {values: core.WorktreeRoots()},
		"pull_requests.review_requested_days": {
			lo: core.MinReviewRequestedDays, hi: core.MaxReviewRequestedDays, unit: "days",
		},
		"keys.which_key_delay": {lo: core.MinWhichKeyDelay, hi: core.MaxWhichKeyDelay, unit: "milliseconds"},
	}
}

// proseOnly keys state their rule in prose and carry no values or range line.
var proseOnly = []string{"plans.paths", "keys.leader_key"}

var (
	rangeRe  = regexp.MustCompile(`^range: (\d+)-(\d+)(?: (\S+))?$`)
	atLeastR = regexp.MustCompile(`^range: (\d+) or more(?: (\S+))?$`)
)

// checkAllowed compares a setting's one allowed-values line with the code.
func checkAllowed(l exampleLine, want allowedSpec) string {
	if len(l.allowed) != 1 {
		return fmt.Sprintf("%s (line %d): wants one values or range line, has %d", l.dotted, l.n, len(l.allowed))
	}

	got := l.allowed[0]

	if want.values != nil {
		rest, ok := strings.CutPrefix(got, "values: ")
		if !ok {
			return fmt.Sprintf("%s (line %d): wants a values line, has %q", l.dotted, l.n, got)
		}

		have := strings.Split(rest, " | ")
		sorted := slices.Sorted(slices.Values(have))

		if len(slices.Compact(slices.Clone(sorted))) != len(sorted) {
			return fmt.Sprintf("%s (line %d): values repeat a name: %q", l.dotted, l.n, got)
		}

		if !slices.Equal(sorted, slices.Sorted(slices.Values(want.values))) {
			return fmt.Sprintf("%s (line %d): values %v differ from the code's %v", l.dotted, l.n, have, want.values)
		}

		return ""
	}

	var lo, hi int

	hi = -1

	if m := rangeRe.FindStringSubmatch(got); m != nil {
		lo, _ = strconv.Atoi(m[1])
		hi, _ = strconv.Atoi(m[2])

		if m[3] != want.unit {
			return fmt.Sprintf("%s (line %d): unit %q, want %q", l.dotted, l.n, m[3], want.unit)
		}
	} else if m := atLeastR.FindStringSubmatch(got); m != nil {
		lo, _ = strconv.Atoi(m[1])

		if m[2] != want.unit {
			return fmt.Sprintf("%s (line %d): unit %q, want %q", l.dotted, l.n, m[2], want.unit)
		}
	} else {
		return fmt.Sprintf("%s (line %d): %q is not a range line", l.dotted, l.n, got)
	}

	if lo != want.lo || hi != want.hi {
		return fmt.Sprintf("%s (line %d): range %d..%d differs from the code's %d..%d (-1 is no upper bound)", l.dotted, l.n, lo, hi, want.lo, want.hi)
	}

	return ""
}

// tomlKeys walks a struct's toml tags: leaves need a setting, maps a header.
func tomlKeys(t reflect.Type, prefix string, settings, tables *[]string) {
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("toml"), ",")
		if name == "-" || name == "" {
			continue
		}

		dotted := name
		if prefix != "" {
			dotted = prefix + "." + name
		}

		switch f.Type.Kind() {
		case reflect.Struct:
			tomlKeys(f.Type, dotted, settings, tables)
		case reflect.Map:
			*tables = append(*tables, dotted)
		default:
			*settings = append(*settings, dotted)
		}
	}
}

// exampleProblems lists every way text breaks the example's contract; empty means none.
func exampleProblems(t *testing.T, text []byte) []string {
	t.Helper()

	lines, problems := scanExample(string(text))
	if len(problems) > 0 {
		return problems
	}

	var (
		settings = map[string]exampleLine{}
		headers  = map[string]bool{}
		remaps   []exampleLine
	)

	for _, l := range lines {
		switch l.kind { //nolint:exhaustive // blank and prose lines need no bookkeeping
		case kindSetting:
			settings[l.dotted] = l
		case kindRemap:
			remaps = append(remaps, l)
		case kindHeader:
			headers[l.table] = true
		}
	}

	// Defaults: every setting outside [keys.<context>] uncommented is the default config.
	defaultsText := uncomment(lines, func(l exampleLine) bool { return l.kind == kindSetting })
	if cfg, err := loadChecked(t, defaultsText); err != nil {
		problems = append(problems, "defaults: the settings uncommented do not load: "+err.Error())
	} else if got, want := normalized(cfg), normalized(core.DefaultConfig()); !reflect.DeepEqual(got, want) {
		problems = append(problems, fmt.Sprintf("defaults: the settings uncommented give\n%+v\nwant the defaults\n%+v", got, want))
	}

	// Every key.
	var keys, tables []string

	tomlKeys(reflect.TypeFor[core.Config](), "", &keys, &tables)

	for _, k := range keys {
		if _, ok := settings[k]; !ok {
			problems = append(problems, fmt.Sprintf("%s: no setting line", k))
		}
	}

	for _, k := range tables {
		if !headers[k] {
			problems = append(problems, fmt.Sprintf("%s: no [%s] header", k, k))
		}
	}

	// Prose and values.
	want := allowedByKey()

	for _, l := range lines {
		if l.kind != kindSetting && l.kind != kindRemap {
			continue
		}

		name := l.dotted
		if l.kind == kindRemap {
			name = fmt.Sprintf("%s %q", l.table, l.key)
		}

		if l.prose == 0 {
			problems = append(problems, fmt.Sprintf("%s (line %d): no prose line above it", name, l.n))
		}

		spec, hasRow := want[l.dotted]

		switch {
		case l.kind == kindRemap:
			if len(l.allowed) > 0 {
				problems = append(problems, fmt.Sprintf("%s (line %d): a remap takes no values or range line", name, l.n))
			}
		case hasRow:
			if p := checkAllowed(l, spec); p != "" {
				problems = append(problems, p)
			}
		case slices.Contains(proseOnly, l.dotted):
			if len(l.allowed) > 0 {
				problems = append(problems, fmt.Sprintf("%s (line %d): takes no values or range line", name, l.n))
			}
		default:
			problems = append(problems, fmt.Sprintf("%s (line %d): neither a values or range row nor prose-only; teach this test", name, l.n))
		}
	}

	problems = append(problems, remapProblems(t, lines, remaps)...)

	return problems
}

func remapProblems(t *testing.T, lines []exampleLine, remaps []exampleLine) []string {
	t.Helper()

	var problems []string

	for _, ctx := range core.KeyContexts() {
		if !slices.ContainsFunc(remaps, func(l exampleLine) bool { return l.table == "keys."+ctx }) {
			problems = append(problems, fmt.Sprintf("keys.%s: no example remap", ctx))
		}
	}

	allText := uncomment(lines, func(exampleLine) bool { return true })
	if cfg, err := loadChecked(t, allText); err != nil {
		problems = append(problems, "remaps: every line uncommented does not load: "+err.Error())
	} else {
		for _, r := range remaps {
			ctx := strings.TrimPrefix(r.table, "keys.")
			if !slices.Contains(cfg.Keys.Binds(ctx), core.KeyBind{Key: r.key, Command: r.value}) {
				problems = append(problems, fmt.Sprintf("%s %q (line %d): not in the loaded binds", r.table, r.key, r.n))
			}
		}
	}

	// One line at a time.
	for _, l := range lines {
		if l.kind != kindSetting && l.kind != kindRemap {
			continue
		}

		text := uncomment(lines, func(o exampleLine) bool { return o.n == l.n })
		if _, err := loadChecked(t, text); err != nil {
			problems = append(problems, fmt.Sprintf("line %d (%s) alone: %v", l.n, l.dotted, err))
		}
	}

	return problems
}

func TestConfigExample(t *testing.T) {
	t.Parallel()

	text, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range exampleProblems(t, text) {
		t.Error(p)
	}
}

func TestConfigExampleCatches(t *testing.T) {
	t.Parallel()

	text, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		old, new string
		want     string
	}{
		{"theme value dropped", "catppuccin-latte | ", "", "theme"},
		{"range high bound", "## range: 10-90", "## range: 10-80", "layout.master_ratio"},
		{"setting deleted", "## range: 1 or more\n# max_lines = 5000\n", "", "diff.max_lines"},
		{"default changed", "# master_count = 1\n", "# master_count = 2\n", "defaults:"},
		{"remap to a wrong command", `"o" = "review.browser"`, `"o" = "pr.list"`, "keys.drawer"},
		{"values line attached to no setting", "# max_lines = 5000\n", "## values: a | b\n\n# max_lines = 5000\n", "attached to no setting"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if !strings.Contains(string(text), tt.old) {
				t.Fatalf("the example no longer holds %q", tt.old)
			}

			mutated := strings.Replace(string(text), tt.old, tt.new, 1)

			problems := exampleProblems(t, []byte(mutated))
			if !slices.ContainsFunc(problems, func(p string) bool { return strings.Contains(p, tt.want) }) {
				t.Errorf("no problem holds %q; got %q", tt.want, problems)
			}
		})
	}
}
