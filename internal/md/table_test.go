package md

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestTableFrame(t *testing.T) {
	t.Parallel()

	l := layoutOf("| a | bb |\n|---|----|\n| 1 | 2 |\n", 40, "")
	wantLines(t, l,
		"╭───┬────╮",
		"│ a │ bb │",
		"├───┼────┤",
		"│ 1 │ 2  │",
		"╰───┴────╯",
	)

	for _, i := range []int{0, 2, 4} {
		for _, r := range l.Lines[i] {
			if r.Role != RoleTableFrame || r.Bold || r.Link != -1 {
				t.Errorf("rule line %d run = %+v", i, r)
			}
		}
	}

	if r := l.Lines[1][0]; r.Role != RoleTableFrame || r.Text != "│ " {
		t.Errorf("frame run with its space = %+v", r)
	}

	if r := run(t, l, 1, "a"); r.Role != RoleTableHeader || !r.Bold {
		t.Errorf("header cell = %+v", r)
	}

	if r := run(t, l, 3, "1"); r.Role != RoleText || r.Bold {
		t.Errorf("body cell = %+v", r)
	}

	goldenText(t, "table_frame", l.debug())
	assertSafe(t, l, 40)

	// A table that fits keeps its natural width; spacing follows the other blocks.
	both := layoutOf("before\n\n| a |\n|---|\n| 1 |\n\nafter\n", 40, "")
	wantLines(t, both, "before", "", "╭───╮", "│ a │", "├───┤", "│ 1 │", "╰───╯", "", "after")
}

func TestTableAlign(t *testing.T) {
	t.Parallel()

	src := "| left | ctr | right | none |\n|:--|:-:|--:|---|\n| a | b | c | d |\n| | ab | | |\n"
	l := layoutOf(src, 60, "")
	wantLines(t, l,
		"╭──────┬─────┬───────┬──────╮",
		"│ left │ ctr │ right │ none │",
		"├──────┼─────┼───────┼──────┤",
		"│ a    │  b  │     c │ d    │",
		"│      │ ab  │       │      │",
		"╰──────┴─────┴───────┴──────╯",
	)

	// The remainder of an odd split goes to the right.
	odd := layoutOf("| title |\n|:-:|\n| ab |\n| abcd |\n", 40, "")
	wantLines(t, odd,
		"╭───────╮",
		"│ title │",
		"├───────┤",
		"│  ab   │",
		"│ abcd  │",
		"╰───────╯",
	)

	hdr := layoutOf("| x |\n|--:|\n| abc |\n", 40, "")
	wantLines(t, hdr, "╭─────╮", "│   x │", "├─────┤", "│ abc │", "╰─────╯")

	goldenText(t, "table_align", l.debug())
}

func TestTableEmptyAndShortRows(t *testing.T) {
	t.Parallel()

	l := layoutOf("| a | b | c |\n|---|---|---|\n| 1 |\n| | 22 | 3 |\n", 40, "")
	wantLines(t, l,
		"╭───┬────┬───╮",
		"│ a │ b  │ c │",
		"├───┼────┼───┤",
		"│ 1 │    │   │",
		"│   │ 22 │ 3 │",
		"╰───┴────┴───╯",
	)
}

func TestTableInlineStyles(t *testing.T) {
	t.Parallel()

	src := "| *em* **st** | `code` | [docs](https://e.com) ~~x~~ |\n|---|---|---|\n| *em* **st** | `code` | [docs](https://e.com) ~~x~~ |\n"
	l := layoutOf(src, 80, "")

	for _, row := range []struct {
		line int
		head bool
	}{{1, true}, {3, false}} {
		text := RoleText
		if row.head {
			text = RoleTableHeader
		}

		if r := run(t, l, row.line, "em"); r.Role != text || !r.Italic || r.Bold != row.head {
			t.Errorf("line %d emphasis = %+v", row.line, r)
		}

		if r := run(t, l, row.line, "st"); r.Role != text || !r.Bold {
			t.Errorf("line %d strong = %+v", row.line, r)
		}

		if r := run(t, l, row.line, "code"); r.Role != RoleCode || r.Bold != row.head {
			t.Errorf("line %d code = %+v", row.line, r)
		}

		if r := run(t, l, row.line, "docs"); r.Role != RoleLink || !r.Underline || r.Link < 0 || r.Bold != row.head {
			t.Errorf("line %d link = %+v", row.line, r)
		}

		if r := run(t, l, row.line, "x"); !r.Strike || r.Role != text {
			t.Errorf("line %d strike = %+v", row.line, r)
		}
	}

	if len(l.Links) != 2 {
		t.Errorf("links = %v, want one per occurrence", l.Links)
	}

	// Padding next to a link or code run never carries its style.
	// A code run's own pad spaces are RoleCode, and sit next to its text.
	for i := 1; i <= 3; i += 2 {
		for k, r := range l.Lines[i] {
			if strings.Trim(r.Text, " ") == "" && r.Role != RoleTableFrame {
				pill := r.Role == RoleCode &&
					((k > 0 && l.Lines[i][k-1].Role == RoleCode && strings.Trim(l.Lines[i][k-1].Text, " ") != "") ||
						(k+1 < len(l.Lines[i]) && l.Lines[i][k+1].Role == RoleCode && strings.Trim(l.Lines[i][k+1].Text, " ") != ""))

				if r.Underline || r.Strike || r.Link != -1 || r.Role == RoleLink || (r.Role == RoleCode && !pill) {
					t.Errorf("line %d padding = %+v", i, r)
				}
			}
		}
	}

	goldenText(t, "table_inline", l.debug())
	assertSafe(t, l, 80)
}

func TestTableShrinksWidestFirst(t *testing.T) {
	t.Parallel()

	src := "| " + strings.Repeat("a", 20) + " | " + strings.Repeat("b", 10) + " | c |\n|---|---|---|\n| x | y | z |\n"
	l := layoutOf(src, 30, "")
	rule := "╭" + strings.Repeat("─", 11) + "┬" + strings.Repeat("─", 12) + "┬───╮"

	if got := l.Plain()[0]; got != rule {
		t.Errorf("top rule = %q, want %q", got, rule)
	}

	if w := lineWidth(l.Lines[0]); w != 30 {
		t.Errorf("table is %d wide, want 30", w)
	}

	assertSafe(t, l, 30)
	goldenText(t, "table_shrink", l.debug())

	// A column at 6 cells or narrower never shrinks, whatever the room.
	narrow := layoutOf("| abcdef | wider-column |\n|---|---|\n| x | y |\n", 14, "")
	if got := narrow.Plain()[0]; !strings.HasPrefix(got, "╭────────┬") {
		t.Errorf("a 6-cell column shrank: %q", got)
	}
}

func TestTableWrapsCells(t *testing.T) {
	t.Parallel()

	src := "| Name | Description | Note |\n|:--|---|--:|\n" +
		"| alpha | a *longer* description with `inline code` and [a link](https://e.com/x) that wraps | ok |\n" +
		"| beta | short | a second note that is long |\n"

	for _, w := range []int{40, 60} {
		l := layoutOf(src, w, "")
		assertSafe(t, l, w)
		goldenText(t, fmt.Sprintf("table_wrap.%d", w), l.debug())

		if len(l.Links) != 1 {
			t.Errorf("width %d: links = %v, want 1", w, l.Links)
		}

		// A wrapped cell keeps its row together: every line has the same frame.
		for i, p := range l.Plain() {
			if !strings.HasPrefix(p, "│") && !strings.HasPrefix(p, "╭") && !strings.HasPrefix(p, "├") && !strings.HasPrefix(p, "╰") {
				t.Errorf("width %d: line %d is outside the grid: %q", w, i, p)
			}
		}
	}

	// A short cell is top-aligned; the lines below its text are blank.
	l := layoutOf("| a | b |\n|---|---|\n| x | one two three four |\n", 22, "")
	wantLines(t, l,
		"╭───┬────────────────╮",
		"│ a │ b              │",
		"├───┼────────────────┤",
		"│ x │ one two three  │",
		"│   │ four           │",
		"╰───┴────────────────╯",
	)
}

func TestTableTooWideIsCut(t *testing.T) {
	t.Parallel()

	var head, delim, row strings.Builder

	for i := range 30 {
		fmt.Fprintf(&head, "| column%02d ", i)
		delim.WriteString("|---")
		fmt.Fprintf(&row, "| cell%02d ", i)
	}

	src := head.String() + "|\n" + delim.String() + "|\n" + row.String() + "|\n"
	l := layoutOf(src, 40, "")

	assertSafe(t, l, 40)

	// The 8-cell headers wrap to two lines in 6-cell columns.
	if len(l.Lines) != 6 {
		t.Fatalf("lines = %d, want 6", len(l.Lines))
	}

	for i, line := range l.Lines {
		if w := lineWidth(line); w != 40 {
			t.Errorf("line %d is %d wide, want 40", i, w)
		}

		if p := plainLine(line); !strings.HasSuffix(p, "…") {
			t.Errorf("line %d is not cut with an ellipsis: %q", i, p)
		}
	}

	goldenText(t, "table_cut", l.debug())
}

func TestTableBounds(t *testing.T) {
	t.Parallel()

	// More than 64 columns: one paragraph per source row, the delimiter row left out.
	const cols = 65

	head := "|" + strings.Repeat("a|", cols) + "\n"
	delim := "|" + strings.Repeat("-|", cols) + "\n"
	body := "|" + strings.Repeat("b|", cols) + "\n"
	l := layoutOf(head+delim+body, 100, "")
	rowText := head[:len(head)-1]
	bodyText := body[:len(body)-1]

	wantLines(t, l, rowText[:100], rowText[100:], bodyText[:100], bodyText[100:])

	for _, line := range l.Lines {
		for _, r := range line {
			if r.Role != RoleText {
				t.Errorf("fallback run = %+v", r)
			}
		}
	}

	// 64 columns still make a grid.
	grid := layoutOf("|"+strings.Repeat("a|", 64)+"\n|"+strings.Repeat("-|", 64)+"\n", 100, "")
	if !strings.HasPrefix(grid.Plain()[0], "╭") {
		t.Errorf("64 columns = %q", grid.Plain()[0])
	}

	// Rows stop once the table holds 4,096 cells, header included.
	l = layoutOf(bigTable(10, 499), 100, "")
	rows := 0

	for _, p := range l.Plain() {
		if strings.HasPrefix(p, "│ r") {
			rows++
		}
	}

	if rows != 408 {
		t.Errorf("body rows shown = %d, want 408", rows)
	}

	plain := l.Plain()
	if got, want := plain[len(plain)-1], "91 more rows"; got != want {
		t.Errorf("last line = %q, want %q", got, want)
	}

	if got := plain[len(plain)-2]; !strings.HasPrefix(got, "╰") {
		t.Errorf("line before the count = %q", got)
	}

	if r := l.Lines[len(l.Lines)-1][0]; r.Role != RoleFoldHint {
		t.Errorf("count run = %+v", r)
	}

	l = layoutOf(bigTable(1, 4096), 100, "")
	plain = l.Plain()

	if got := plain[len(plain)-1]; got != "1 more row" {
		t.Errorf("last line = %q, want 1 more row", got)
	}

	l = layoutOf(bigTable(10, 408), 100, "")
	if got := l.Plain()[len(l.Lines)-1]; !strings.HasPrefix(got, "╰") {
		t.Errorf("a table at the bound has a count line: %q", got)
	}
}

// TestWideTableRowsStop checks that a table too wide for a grid stops at the
// same cell bound as a grid: 62 body rows of 65 cells after the header.
func TestWideTableRowsStop(t *testing.T) {
	t.Parallel()

	const cols = 65

	cells := func(first string) string {
		parts := []string{first}
		for i := 1; i < cols; i++ {
			parts = append(parts, fmt.Sprintf("c%d", i))
		}

		return "| " + strings.Join(parts, " | ") + " |\n"
	}

	build := func(rows int) string {
		var b strings.Builder

		b.WriteString(cells("h0") + "|" + strings.Repeat("-|", cols) + "\n")

		for i := range rows {
			b.WriteString(cells(fmt.Sprintf("r%d", i)))
		}

		return b.String()
	}

	l := layoutOf(build(200), 100, "")
	plain := l.Plain()
	shown := 0

	for _, p := range plain {
		if strings.HasPrefix(p, "| r") {
			shown++
		}

		if strings.HasPrefix(p, "╭") {
			t.Fatalf("a table of %d columns made a grid", cols)
		}
	}

	if shown != 62 {
		t.Errorf("body rows shown = %d, want 62", shown)
	}

	if slices.ContainsFunc(plain, func(p string) bool { return strings.HasPrefix(p, "| r62 |") }) {
		t.Error("row 62 is past the bound")
	}

	if got, want := plain[len(plain)-1], "138 more rows"; got != want {
		t.Errorf("last line = %q, want %q", got, want)
	}

	if r := l.Lines[len(l.Lines)-1][0]; r.Role != RoleFoldHint {
		t.Errorf("count run = %+v", r)
	}

	assertSafe(t, l, 100)

	// At the bound, and under it, no count line follows.
	for _, rows := range []int{62, 10} {
		last := layoutOf(build(rows), 100, "").Plain()
		if got := last[len(last)-1]; strings.HasSuffix(got, "more rows") || strings.HasSuffix(got, "more row") {
			t.Errorf("%d rows: count line %q", rows, got)
		}
	}

	if got := layoutOf(build(63), 100, "").Plain(); got[len(got)-1] != "1 more row" {
		t.Errorf("one row over: last line = %q", got[len(got)-1])
	}
}

// bigTable is a table of cols columns with a header and rows body rows.
func bigTable(cols, rows int) string {
	var b strings.Builder

	for range cols {
		b.WriteString("|h")
	}

	b.WriteString("|\n")

	for range cols {
		b.WriteString("|-")
	}

	b.WriteString("|\n")

	for range rows {
		for range cols {
			b.WriteString("|r")
		}

		b.WriteString("|\n")
	}

	return b.String()
}

func TestTableHostileCells(t *testing.T) {
	t.Parallel()

	src := "| prose | code | links |\n|---|---|---|\n" +
		"| red\x1b[31m text\u202e gone | `a\x1b[2Jb\u202ec` | [xlink](javascript:alert(1)) [ylink](data:text/html,hi) [ok](https://e.com) |\n"
	l := layoutOf(src, 100, "")

	assertSafe(t, l, 100)

	plain := strings.Join(l.Plain(), "\n")
	for _, bad := range []string{"\x1b", "\u202e"} {
		if strings.Contains(plain, bad) {
			t.Errorf("layout holds %q", bad)
		}
	}

	if !strings.Contains(plain, "red[31m text gone") {
		t.Errorf("prose cell was not cleaned: %q", plain)
	}

	for _, tag := range []string{"⟨U+001B⟩", "⟨U+202E⟩"} {
		if r := run(t, l, 3, tag); r.Role != RoleHidden || !r.Bold {
			t.Errorf("%s = %+v", tag, r)
		}
	}

	for _, s := range []string{"xlink", "ylink"} {
		if r := run(t, l, 3, s); r.Link != -1 || r.Role != RoleLink || !r.Underline {
			t.Errorf("unsafe link text %q = %+v", s, r)
		}
	}

	if len(l.Links) != 1 || l.Links[0].URL != "https://e.com" {
		t.Errorf("links = %v, want the https link only", l.Links)
	}

	// Hostile alt text and a header with controls.
	alt := layoutOf("| h\x1b[0m\u202e |\n|---|\n| ![al\x1bt\u202e](https://e.com/i.png) |\n", 60, "")
	assertSafe(t, alt, 60)
}

func TestTableBreakInCell(t *testing.T) {
	t.Parallel()

	l := layoutOf("| a | b |\n|---|---|\n| one<br>two three | x |\n", 40, "")
	wantLines(t, l,
		"╭───────────┬───╮",
		"│ a         │ b │",
		"├───────────┼───┤",
		"│ one       │ x │",
		"│ two three │   │",
		"╰───────────┴───╯",
	)
}

func TestTableLinkWrapsAcrossCellLines(t *testing.T) {
	t.Parallel()

	l := layoutOf("| [one two three four](https://e.com) | x |\n|---|---|\n| y | z |\n", 20, "")
	assertSafe(t, l, 20)

	if len(l.Links) != 1 {
		t.Fatalf("links = %v, want one entry", l.Links)
	}

	linked := 0

	for _, line := range l.Lines {
		for _, r := range line {
			if r.Link == 0 && strings.TrimSpace(r.Text) != "" {
				linked++
			}
		}
	}

	if linked != 2 {
		t.Errorf("linked runs = %d, want one per wrapped line", linked)
	}
}

func TestTableImageInCell(t *testing.T) {
	t.Parallel()

	src := "| pic |\n|---|\n| ![logo](https://img.example.com/a.png) |\n"
	l := layoutOf(src, 60, "")
	wantLines(t, l,
		"╭──────────────────────────────╮",
		"│ pic                          │",
		"├──────────────────────────────┤",
		"│ image: logo  img.example.com │",
		"╰──────────────────────────────╯",
	)

	if len(l.Images) != 0 {
		t.Errorf("images = %v, want none inside a grid cell", l.Images)
	}

	if r := run(t, l, 3, "image: logo"); r.Role != RoleImage {
		t.Errorf("image text = %+v", r)
	}

	if r := run(t, l, 3, "img.example.com"); r.Role != RoleImageHost {
		t.Errorf("image host = %+v", r)
	}

	linked := layoutOf("| pic |\n|---|\n| [![logo](https://img.example.com/a.png)](https://e.com) |\n", 60, "")

	if r := run(t, linked, 3, "image: logo"); r.Link != 0 || len(linked.Links) != 1 || len(linked.Images) != 0 {
		t.Errorf("linked image = %+v, links %v, images %v", r, linked.Links, linked.Images)
	}

	// An image wraps with the cell.
	narrow := layoutOf(src, 24, "")
	assertSafe(t, narrow, 24)
}

func TestTableInContainers(t *testing.T) {
	t.Parallel()

	list := layoutOf("- item\n\n  | a | b |\n  |---|---|\n  | 1 | 2 |\n", 30, "")
	wantLines(t, list,
		"  • item",
		"    ╭───┬───╮",
		"    │ a │ b │",
		"    ├───┼───┤",
		"    │ 1 │ 2 │",
		"    ╰───┴───╯",
	)

	quote := layoutOf("> | a | b |\n> |---|---|\n> | 1 | 2 |\n", 30, "")
	wantLines(t, quote,
		"▎ ╭───┬───╮",
		"▎ │ a │ b │",
		"▎ ├───┼───┤",
		"▎ │ 1 │ 2 │",
		"▎ ╰───┴───╯",
	)

	// The grid takes the container's width.
	narrow := layoutOf("> | alpha beta gamma | b |\n> |---|---|\n> | 1 | 2 |\n", 16, "")
	assertSafe(t, narrow, 16)
}

func TestTableHugeCell(t *testing.T) {
	t.Parallel()

	src := "| h | x |\n|---|---|\n| " + strings.Repeat("y", 1<<20) + " | z |\n"
	l := layoutOf(src, 60, "")

	assertSafe(t, l, 60)

	if len(l.Lines) == 0 {
		t.Fatal("no lines")
	}
}

func TestTableNarrowWidths(t *testing.T) {
	t.Parallel()

	src := "| 名前 | 😀 emoji | plain |\n|:-:|--:|---|\n| 日本語のテキスト | 👍👍👍 | a-very-long-unbreakable-word |\n"

	for _, w := range []int{1, 2, 3, 5, 10} {
		l := layoutOf(src, w, "")
		assertSafe(t, l, w)

		if len(l.Lines) == 0 {
			t.Errorf("width %d: no lines", w)
		}

		for i, line := range l.Lines {
			if lineWidth(line) > w {
				t.Errorf("width %d line %d: %q", w, i, ansi.Strip(plainLine(line)))
			}
		}
	}
}

func TestTableWideHostileFallback(t *testing.T) {
	t.Parallel()

	const cols = 65

	head := "|" + strings.Repeat("a|", cols-1) + "x\x1b[31my\u202ez|\n"
	delim := "|" + strings.Repeat("-|", cols) + "\n"
	l := layoutOf(head+delim+"|"+strings.Repeat("b|", cols)+"\n", 100, "")

	assertSafe(t, l, 100)

	plain := strings.Join(l.Plain(), "")
	if strings.Contains(plain, "\x1b") || strings.Contains(plain, "\u202e") {
		t.Errorf("fallback holds a control or bidi character: %q", plain)
	}

	if !strings.Contains(plain, "x[31myz|") {
		t.Errorf("text around the dropped characters is gone: %q", plain)
	}
}

func TestTableCJKAtTheFloor(t *testing.T) {
	t.Parallel()

	src := "| 名前 | 説明 |\n|:-:|--:|\n| 日本語のテキスト | 世界 |\n"
	l := layoutOf(src, 17, "")
	assertSafe(t, l, 17)
	goldenText(t, "table_cjk_floor", l.debug())

	// Every row puts its frame bars in the same cells.
	var want []int

	for i, p := range l.Plain() {
		if !strings.HasPrefix(p, "│") {
			continue
		}

		var cols []int

		x := 0

		for _, r := range p {
			if r == '│' {
				cols = append(cols, x)
			}

			x += ansi.StringWidth(string(r))
		}

		if want == nil {
			want = cols
		} else if fmt.Sprint(cols) != fmt.Sprint(want) {
			t.Errorf("line %d bars at %v, want %v: %q", i, cols, want, p)
		}
	}
}
