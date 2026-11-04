package ui

import (
	"image/color"
	"testing"

	"github.com/mhersson/aim/internal/ui/diffview"
	"github.com/mhersson/aim/internal/ui/theme"
)

func TestReviewPaletteFromTheme(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"catppuccin-mocha", "catppuccin-latte"} {
		th, err := theme.Builtin().ByName(name)
		if err != nil {
			t.Fatal(err)
		}

		p, u := th.Palette, th.UI
		pal := reviewPalette(th)

		if pal.Depth != diffview.DepthTrue {
			t.Errorf("%s: depth = %v, want true colour", name, pal.Depth)
		}

		for _, c := range []struct {
			what      string
			got, want color.Color
		}{
			{"Ground", pal.Ground, u.Bg},
			{"Fg", pal.Fg, u.Fg},
			{"Accent", pal.Accent, u.Accent},
			{"Add", pal.Add, u.Success},
			{"Delete", pal.Delete, u.Error},
			{"Keyword", pal.Syntax[diffview.Keyword], p.Mauve},
			{"Type", pal.Syntax[diffview.Type], p.Yellow},
			{"Func", pal.Syntax[diffview.Func], p.Blue},
			{"Builtin", pal.Syntax[diffview.Builtin], p.Sky},
			{"String", pal.Syntax[diffview.String], p.Green},
			{"Number", pal.Syntax[diffview.Number], p.Peach},
			{"Comment", pal.Syntax[diffview.Comment], p.Overlay0},
			{"Operator", pal.Syntax[diffview.Operator], p.Sky},
			{"Punct", pal.Syntax[diffview.Punct], p.Overlay2},
			{"Preproc", pal.Syntax[diffview.Preproc], p.Pink},
			{"Tag", pal.Syntax[diffview.Tag], p.Mauve},
			{"Attr", pal.Syntax[diffview.Attr], p.Blue},
			{"Heading", pal.Syntax[diffview.Heading], p.Peach},
			{"Meta", pal.Syntax[diffview.Meta], p.Overlay1},
			{"Plain", pal.Syntax[diffview.Plain], p.Text},
		} {
			if c.got == nil {
				t.Errorf("%s: %s is nil", name, c.what)

				continue
			}

			gr, gg, gb, ga := c.got.RGBA()
			wr, wg, wb, wa := c.want.RGBA()

			if gr != wr || gg != wg || gb != wb || ga != wa {
				t.Errorf("%s: %s = %v, want %v", name, c.what, c.got, c.want)
			}
		}
	}
}
