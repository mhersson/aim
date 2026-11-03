package ui

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/ui/kitty"
)

func picRows(id uint32, x, y, n, cols int) []picRow {
	rows := make([]picRow, n)
	for i := range rows {
		rows[i] = picRow{X: x, Y: y + i, ID: id, Row: i, Cols: cols}
	}

	return rows
}

func TestFrameVisible(t *testing.T) {
	t.Parallel()

	box := func(x, y, w, h int) core.Rect { return core.Rect{X: x, Y: y, W: w, H: h} }

	tests := []struct {
		name  string
		build func(f *frameLayers)
		want  int
	}{
		{"no later box", func(f *frameLayers) { f.add(box(0, 0, 10, 10), picRows(1, 2, 2, 3, 4)) }, 3},
		{"later box over one row hides all", func(f *frameLayers) {
			f.add(box(0, 0, 10, 10), picRows(1, 2, 2, 3, 4))
			f.add(box(3, 3, 1, 1), nil)
		}, 0},
		{"earlier box leaves it", func(f *frameLayers) {
			f.add(box(0, 0, 10, 10), nil)
			f.add(box(0, 0, 10, 10), picRows(1, 2, 2, 3, 4))
		}, 3},
		{"later box beside it", func(f *frameLayers) {
			f.add(box(0, 0, 10, 10), picRows(1, 2, 2, 3, 4))
			f.add(box(6, 2, 3, 3), nil)
		}, 3},
		{"only the overlapped picture hides", func(f *frameLayers) {
			f.add(box(0, 0, 20, 10), append(picRows(1, 1, 1, 2, 3), picRows(2, 10, 1, 2, 3)...))
			f.add(box(10, 1, 2, 1), nil)
		}, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var f frameLayers

			tt.build(&f)

			if got := len(f.visible()); got != tt.want {
				t.Fatalf("visible rows = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestFrameLayersNilSafe(t *testing.T) {
	t.Parallel()

	var f *frameLayers

	f.add(core.Rect{W: 1, H: 1}, nil)

	if len(f.visible()) != 0 {
		t.Fatal("nil frame has visible rows")
	}
}

func TestPlacePictures(t *testing.T) {
	t.Parallel()

	const w, h = 20, 5

	blank := strings.TrimSuffix(strings.Repeat(strings.Repeat(".", w)+"\n", h), "\n")

	out, spliced := placePictures(blank, w, h, []picRow{
		{X: 3, Y: 1, ID: 42, Row: 0, Cols: 4},
		{X: 3, Y: 5, ID: 42, Row: 1, Cols: 4},
		{X: 0, Y: 3, ID: 43, Row: 0, Cols: 2, Ground: "#102030"},
	})
	if !spliced {
		t.Fatal("the compositor placed the pictures, want the splice")
	}

	rows := strings.Split(out, "\n")
	if len(rows) != h {
		t.Fatalf("rows = %d, want %d", len(rows), h)
	}

	for y, r := range rows {
		if got := ansi.StringWidth(r); got != w {
			t.Fatalf("row %d is %d cells, want %d", y, got, w)
		}
	}

	if !strings.Contains(rows[1], "38;2;0;0;42") {
		t.Fatalf("row 1 lacks the id foreground: %q", rows[1])
	}

	want := strings.Repeat(".", 3) + kitty.Placeholders(0, 4) + strings.Repeat(".", 13)
	if got := ansi.Strip(rows[1]); got != want {
		t.Fatalf("row 1 = %q, want %q", got, want)
	}

	if !strings.Contains(rows[3], "48;2;16;32;48") {
		t.Fatalf("row 3 lacks the ground background: %q", rows[3])
	}

	if strings.Contains(rows[1], "48;2;") {
		t.Fatalf("row 1 has a background without a ground: %q", rows[1])
	}

	for _, y := range []int{0, 2, 4} {
		if got := ansi.Strip(rows[y]); got != strings.Repeat(".", w) {
			t.Fatalf("row %d changed: %q", y, got)
		}
	}
}

// picWidth by picHeight is the screen the boundary cases draw on; most place
// one picture row of picCols cells at column picX of row picY.
const (
	picWidth, picHeight = 30, 5
	picX, picY, picCols = 10, 2, 6
)

// picFrame is a frame of full rows of "a", with rows replacing those it names.
func picFrame(rows map[int]string) string {
	out := make([]string, picHeight)
	for y := range out {
		r, ok := rows[y]
		if !ok {
			r = repeatA(picWidth)
		}

		out[y] = r
	}

	return strings.Join(out, "\n")
}

// The splice must leave the cells the compositor leaves, wherever the frame has
// something open, wide or zero-width at a picture's edges; a frame it cannot
// cut on cell edges goes through the compositor.
func TestPlacePicturesMatchesTheCompositor(t *testing.T) {
	t.Parallel()

	a, w := repeatA, picWidth
	at := func(y int, s string) map[int]string { return map[int]string{y: s} }
	row := func(s string) map[int]string { return at(picY, s) }
	one := []picRow{{X: picX, Y: picY, ID: 9, Row: 0, Cols: picCols, Ground: "#102030"}}

	tests := []struct {
		name    string
		rows    map[int]string
		pics    []picRow // nil places one
		spliced bool     // the splice applies; else the compositor draws
	}{
		{"plain", nil, nil, true},
		{"style open across the picture", row(sgrOpen + a(w) + reset), nil, true},
		{"style opens inside the picture", row(a(12) + sgrOpen + a(w-12) + reset), nil, true},
		{"style opens right after the picture", row(a(16) + sgrOpen + a(w-16) + reset), nil, true},
		{"style closes inside the picture", row(sgrOpen + a(12) + reset + a(w-12)), nil, true},
		{"styles add up across the picture", row("\x1b[31m" + a(12) + "\x1b[1;44m" + a(2) + "\x1b[22;4:3;58;5;99m" + a(w-14) + reset), nil, true},
		{"link open across the picture", row(linkOn + a(w) + linkOff), nil, true},
		{"link opens inside the picture", row(a(12) + linkOn + a(w-12) + linkOff), nil, true},
		{"link closes inside the picture", row(linkOn + a(12) + linkOff + a(w-12)), nil, true},
		{"link with an id across the picture", row("\x1b]8;id=7;https://example.com/b\x1b\\" + a(w) + linkOff), nil, true},
		{"style and link open across the picture", row(sgrOpen + linkOn + a(w) + linkOff + reset), nil, true},
		{"wide character inside", row(a(11) + "中" + a(w-13)), nil, true},
		{"wide character ends at the left edge", row(a(8) + "中" + a(w-10)), nil, true},
		{"wide character starts at the right edge", row(a(16) + "中" + a(w-18)), nil, true},
		{"wide characters all along the row", row(strings.Repeat("中", w/2)), nil, true},
		{"emoji inside", row("\x1b[32m" + a(12) + "😀" + a(w-14) + reset), nil, true},
		{"combining mark inside", row(a(12) + "e\u0301" + a(w-13)), nil, true},
		{"combining mark on the last cell", row(a(15) + "e\u0301" + a(w-16)), nil, true},
		{"combining mark on the cell before", row(a(9) + "e\u0301" + a(w-10)), nil, true},
		{"combining mark after an escape before the picture", row(a(10) + "\x1b[31m\u0301" + a(w-10) + reset), nil, true},
		{"combining mark after an escape inside the picture", row(a(12) + "\x1b[31m\u0301" + a(w-12) + reset), nil, true},
		{"control character inside", row(a(12) + "\x07" + a(w-12)), nil, true},
		{"previous row closed its style", at(picY-1, sgrOpen+a(w)+reset), nil, true},
		{"a row that ends where the picture does", row(a(16)), nil, true},
		{"a styled row that ends where the picture does", row(sgrOpen + a(16) + reset), nil, true},
		{"an APC in another row", at(0, a(5)+apc+a(w-5)), nil, true},
		{"two pictures on one row", row(sgrOpen + a(w) + reset), []picRow{
			{X: 2, Y: picY, ID: 9, Row: 0, Cols: 4}, {X: 20, Y: picY, ID: 10, Row: 0, Cols: 5, Ground: "#102030"},
		}, true},
		{"two pictures side by side", nil, []picRow{{X: 4, Y: picY, ID: 9, Cols: 4}, {X: 8, Y: picY, ID: 10, Cols: 4}}, true},
		{"two pictures given right to left", nil, []picRow{{X: 20, Y: picY, ID: 10, Cols: 5}, {X: 2, Y: picY, ID: 9, Cols: 4}}, true},
		{"style and link change between two pictures", row(a(6) + "\x1b[31m" + a(10) + "\x1b[1;4:3m" + linkOn + a(w-16) + linkOff + reset), []picRow{
			{X: 2, Y: picY, ID: 9, Cols: 4}, {X: 20, Y: picY, ID: 10, Cols: 5},
		}, true},
		{"style and link closed between two picture rows", map[int]string{1: sgrOpen + linkOn + a(w), 2: a(5) + linkOff + reset + a(w-5)}, []picRow{
			{X: picX, Y: 1, ID: 9, Cols: picCols}, {X: picX, Y: 3, ID: 9, Row: 2, Cols: picCols},
		}, true},
		{"a picture of three rows", at(1, sgrOpen+a(w)+reset), picRows(9, picX, 1, 3, picCols), true},
		{"at the right edge", nil, []picRow{{X: w - 6, Y: picY, ID: 9, Cols: 6}}, true},
		{"at the left edge", row(sgrOpen + a(w) + reset), []picRow{{X: 0, Y: picY, ID: 9, Cols: 6}}, true},
		{"across the whole row", nil, []picRow{{X: 0, Y: picY, ID: 9, Cols: w}}, true},
		{"on the first row", nil, []picRow{{X: 0, Y: 0, ID: 9, Cols: 6}}, true},
		{"on the last row", nil, []picRow{{X: w - 6, Y: picHeight - 1, ID: 9, Cols: 6}}, true},
		{"rows off the screen are dropped", nil, append(slices.Clone(one),
			picRow{X: 3, Y: picHeight, ID: 9, Row: 1, Cols: 4}, picRow{X: -1, Y: 0, ID: 9, Cols: 4},
			picRow{X: w, Y: 0, ID: 9, Cols: 4}, picRow{X: 3, Y: -1, ID: 9, Cols: 4}, picRow{X: 3, Y: 0, ID: 9}), true},

		// A wide cell across an edge would be cut in two.
		{"wide character straddles the left edge", row(a(9) + "中" + a(w-11)), nil, false},
		{"wide character straddles the right edge", row(a(15) + "中" + a(w-17)), nil, false},

		// A mark with no cell before it folds into the last cell drawn, which
		// the splice would make a placeholder.
		{"combining mark after an escape right after the picture", row(a(16) + "\x1b[31m\u0301" + a(w-16) + reset), nil, false},
		{"combining mark opens the row after a picture that ends its row", map[int]string{picY: a(16), picY + 1: "\u0301" + a(w-1)}, nil, false},
		{"combining mark after an escape between two pictures side by side", row(a(8) + "\x1b[31m\u0301" + a(w-8) + reset), []picRow{
			{X: 4, Y: picY, ID: 9, Cols: 4}, {X: 8, Y: picY, ID: 10, Cols: 4},
		}, false},
		{"an APC with no cell after it follows a picture that ends its row", map[int]string{picHeight - 2: a(16), picHeight - 1: apc}, []picRow{{X: picX, Y: picHeight - 2, ID: 9, Cols: picCols}}, false},

		// A string sequence rides on the next cell drawn, and a carriage return
		// moves the column.
		{"an APC just before the picture", row(a(picX) + apc + a(w-picX)), nil, false},
		{"a DCS in the row", row(a(12) + dcs + a(w-12)), nil, false},
		{"an APC at the end of the row before a picture at the left edge", at(picY-1, a(w)+apc), []picRow{{X: 0, Y: picY, ID: 9, Cols: 6}}, false},
		{"an APC between a picture row and a later one at the left edge", at(2, a(w)+apc), []picRow{
			{X: picX, Y: 1, ID: 9, Cols: picCols}, {X: 0, Y: 3, ID: 10, Cols: picCols},
		}, false},
		{"a carriage return rewinds over the picture", row(a(w) + "\r" + strings.Repeat("b", 12)), nil, false},

		// A style or link left open carries into the picture's row.
		{"previous row left a style open", at(picY-1, sgrOpen+a(w)), nil, false},
		{"previous row left a link open", at(picY-1, linkOn+a(w)), nil, false},
		{"a style left open two rows up", at(picY-2, sgrOpen+a(w)), nil, false},
		{"a picture row leaves a style open before the next one", at(1, sgrOpen+a(w)), picRows(9, picX, 1, 2, picCols), false},
		{"a style left open between two picture rows", at(2, sgrOpen+a(w)), []picRow{
			{X: picX, Y: 1, ID: 9, Cols: picCols}, {X: picX, Y: 3, ID: 9, Row: 2, Cols: picCols},
		}, false},

		// The splice cuts only cells the row has, and one picture at a time.
		{"a row shorter than the picture's end", row(a(12)), nil, false},
		{"an empty row", row(""), nil, false},
		{"two pictures overlap", nil, []picRow{{X: 4, Y: picY, ID: 9, Cols: 6}, {X: 8, Y: picY, ID: 10, Cols: 4}}, false},
		{"past the right edge", nil, []picRow{{X: w - 4, Y: picY, ID: 9, Cols: 6}}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pics := tt.pics
			if pics == nil {
				pics = one
			}

			base := picFrame(tt.rows)
			want := composePictures(base, picWidth, picHeight, pics)
			got, spliced := placePictures(base, picWidth, picHeight, pics)

			requireSameCells(t, got, want, picWidth, picHeight)

			if spliced != tt.spliced {
				t.Errorf("spliced = %v, want %v", spliced, tt.spliced)
			}

			if tt.spliced && got == base {
				t.Fatal("the pictures were not placed")
			}
		})
	}
}

// What the splice cannot cut it hands to the compositor, and the result is the
// compositor's; a frame with no picture on the screen is left as it is.
func TestPlacePicturesFallsBackToTheCompositor(t *testing.T) {
	t.Parallel()

	one := []picRow{{X: picX, Y: picY, ID: 9, Cols: picCols}}
	full := strings.Repeat(repeatA(picWidth)+"\n", picHeight+2)

	tests := []struct {
		name string
		base string
	}{
		{"the base has more rows than the screen", strings.TrimSuffix(full, "\n")},
		{"the base has fewer rows than the screen", strings.TrimSuffix(strings.Repeat(repeatA(picWidth)+"\n", picHeight-1), "\n")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			want := composePictures(tt.base, picWidth, picHeight, one)
			if got, spliced := placePictures(tt.base, picWidth, picHeight, one); got != want || spliced {
				t.Errorf("spliced = %v, or the compositor's result was not used", spliced)
			}
		})
	}

	t.Run("no picture on the screen", func(t *testing.T) {
		t.Parallel()

		base := picFrame(nil)
		if got, spliced := placePictures(base, picWidth, picHeight, []picRow{{X: 3, Y: picHeight, ID: 9, Cols: 4}}); got != base || !spliced {
			t.Errorf("spliced = %v, or the frame changed", spliced)
		}
	})
}

// drawPictures counts the frames the compositor places the pictures in, and
// places what the compositor places.
func TestDrawPicturesCountsFallbacks(t *testing.T) {
	t.Parallel()

	a := &App{width: picWidth, height: picHeight, pictures: newPictures(nil, nil, nil)}
	one := []picRow{{X: picX, Y: picY, ID: 9, Cols: picCols}}

	a.drawPictures(picFrame(nil), one)

	if a.pictures.fallbacks != 0 {
		t.Fatalf("%d fallbacks for a frame the splice covers, want 0", a.pictures.fallbacks)
	}

	held := picFrame(map[int]string{picY: repeatA(picX-1) + "中" + repeatA(picWidth-picX-1)})
	got := a.drawPictures(held, one)
	requireSameCells(t, got, composePictures(held, picWidth, picHeight, one), picWidth, picHeight)

	if a.pictures.fallbacks != 1 {
		t.Errorf("%d fallbacks after a row the splice declines, want 1", a.pictures.fallbacks)
	}
}

func TestOverlayAtRecords(t *testing.T) {
	t.Parallel()

	const w, h = 20, 5

	blank := strings.TrimSuffix(strings.Repeat(strings.Repeat(".", w)+"\n", h), "\n")
	line := strings.Repeat("x", 6)

	t.Run("box and rows", func(t *testing.T) {
		t.Parallel()

		var f frameLayers

		overlayAt(&f, blank, w, h, 2, 1, []string{line, line, line}, []picRow{{X: 1, Y: 1, ID: 7, Row: 0, Cols: 2}})

		if !reflect.DeepEqual(f.boxes, []core.Rect{{X: 2, Y: 1, W: 6, H: 3}}) {
			t.Fatalf("%s = %v, want %v", "f.boxes", f.boxes, []core.Rect{{X: 2, Y: 1, W: 6, H: 3}})
		}

		if !reflect.DeepEqual(f.pics, [][]picRow{{{X: 3, Y: 2, ID: 7, Row: 0, Cols: 2}}}) {
			t.Fatalf("%s = %v, want %v", "f.pics", f.pics, [][]picRow{{{X: 3, Y: 2, ID: 7, Row: 0, Cols: 2}}})
		}
	})

	t.Run("clipped by the bottom", func(t *testing.T) {
		t.Parallel()

		var f frameLayers

		overlayAt(&f, blank, w, h, 2, 3, []string{line, line, line}, []picRow{
			{X: 0, Y: 0, ID: 7, Row: 0, Cols: 2},
			{X: 0, Y: 2, ID: 7, Row: 2, Cols: 2},
		})

		if !reflect.DeepEqual(f.boxes, []core.Rect{{X: 2, Y: 3, W: 6, H: 2}}) {
			t.Fatalf("%s = %v, want %v", "f.boxes", f.boxes, []core.Rect{{X: 2, Y: 3, W: 6, H: 2}})
		}

		if len(f.pics[0]) != 1 || f.pics[0][0].Y != 3 {
			t.Fatalf("pics = %v", f.pics)
		}
	})
}

func TestDrawerRecordsPictures(t *testing.T) {
	t.Parallel()

	const w, h = 20, 5

	blank := strings.TrimSuffix(strings.Repeat(strings.Repeat(".", w)+"\n", h), "\n")
	r := core.Rect{X: 12, Y: 0, W: 8, H: h}
	lines := strings.Split(strings.TrimSuffix(strings.Repeat(strings.Repeat("y", 8)+"\n", h), "\n"), "\n")
	pics := []picRow{{X: 1, Y: 2, ID: 9, Row: 0, Cols: 3}}

	for _, fits := range []bool{true, false} {
		var f frameLayers

		_, spliced := overlayDrawer(&f, blank, w, h, r, lines, pics, fits)
		if spliced != fits {
			t.Fatalf("fits=%v spliced=%v", fits, spliced)
		}

		if !reflect.DeepEqual(f.boxes, []core.Rect{r}) || !reflect.DeepEqual(f.pics, [][]picRow{{{X: 13, Y: 2, ID: 9, Row: 0, Cols: 3}}}) {
			t.Fatalf("fits=%v recorded %v %v", fits, f.boxes, f.pics)
		}
	}
}

func TestSplicedDrawerRecordsAsRecordLayer(t *testing.T) {
	t.Parallel()

	const width, height = 20, 6

	blank := strings.TrimSuffix(strings.Repeat(strings.Repeat(".", width)+"\n", height), "\n")
	wide := string(rune(0x4e16)) // two cells

	pics := []picRow{
		{X: 0, Y: 0, ID: 3, Row: 0, Cols: 2},
		{X: 1, Y: 2, ID: 3, Row: 1, Cols: 2},
		{X: 0, Y: 4, ID: 5, Row: 0, Cols: 1},
		{X: 0, Y: -1, ID: 6, Row: 0, Cols: 1},
	}

	tests := []struct {
		name string
		r    core.Rect
		line string // every line, r.W cells
		n    int
	}{
		{"the screen's height", core.Rect{X: 12, Y: 0, W: 8, H: height}, "yyyyyyyy", height},
		{"below the top row", core.Rect{X: 12, Y: 1, W: 8, H: height - 1}, "yyyyyyyy", height - 1},
		{"wide cells, fewer lines than the rect", core.Rect{X: 14, Y: 0, W: 6, H: height}, strings.Repeat(wide, 3), 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			lines := slices.Repeat([]string{tt.line}, tt.n)

			var got, want frameLayers

			if _, spliced := overlayDrawer(&got, blank, width, height, tt.r, lines, pics, true); !spliced {
				t.Fatal("the compositor drew the drawer, want the splice")
			}

			recordLayer(&want, width, height, tt.r.X, tt.r.Y, lines, pics)

			if !reflect.DeepEqual(got, want) {
				t.Fatalf("the splice recorded %+v, recordLayer %+v", got, want)
			}
		})
	}
}

func TestOverlayLinesRecordPictures(t *testing.T) {
	t.Parallel()

	const w, h = 20, 6

	blank := strings.TrimSuffix(strings.Repeat(strings.Repeat(".", w)+"\n", h), "\n")
	pics := []picRow{{X: 0, Y: 0, ID: 3, Row: 0, Cols: 2}}

	var f frameLayers

	overlayLines(&f, blank, w, h, 2, []string{"aaaa", "bbbb"}, 4, pics)

	if !reflect.DeepEqual(f.boxes, []core.Rect{{X: 8, Y: 2, W: 4, H: 2}}) || f.pics[0][0].X != 8 || f.pics[0][0].Y != 2 {
		t.Fatalf("overlayLines recorded %v %v", f.boxes, f.pics)
	}
}
