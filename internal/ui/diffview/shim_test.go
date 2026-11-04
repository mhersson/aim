package diffview

import (
	"image/color"
	"math"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/ui/theme"
)

func mochaTheme(t *testing.T) theme.Palette {
	t.Helper()

	th, err := theme.Builtin().ByName("catppuccin-mocha")
	if err != nil {
		t.Fatal(err)
	}

	return th.Palette
}

func rgba8(c color.Color) [3]uint32 {
	r, g, b, _ := c.RGBA()

	return [3]uint32{r >> 8, g >> 8, b >> 8}
}

func TestMixColorsEnds(t *testing.T) {
	p := mochaTheme(t)
	a, b := p.Base, p.Green

	if got, want := rgba8(MixColors(a, b, 0)), rgba8(a); got != want {
		t.Errorf("MixColors(a, b, 0) = %v, want %v", got, want)
	}

	if got, want := rgba8(MixColors(a, b, 1)), rgba8(b); got != want {
		t.Errorf("MixColors(a, b, 1) = %v, want %v", got, want)
	}

	mid := rgba8(MixColors(a, b, 0.5))
	if mid == rgba8(a) || mid == rgba8(b) {
		t.Errorf("MixColors(a, b, 0.5) = %v, want a colour between the ends", mid)
	}
}

func TestContrastRatioBlackWhite(t *testing.T) {
	if got := ContrastRatio(color.Black, color.White); math.Abs(got-21) > 0.01 {
		t.Errorf("ContrastRatio(black, white) = %.3f, want 21", got)
	}

	if got := ContrastRatio(color.White, color.White); math.Abs(got-1) > 0.01 {
		t.Errorf("ContrastRatio(white, white) = %.3f, want 1", got)
	}
}

func TestReadableAtRaisesContrast(t *testing.T) {
	p := mochaTheme(t)

	got := ReadableAt(p.Surface1, p.Base, 4.5)
	if r := ContrastRatio(got, p.Base); r < 4.5 {
		t.Errorf("contrast after ReadableAt = %.2f, want >= 4.5", r)
	}

	if got := ReadableAt(p.Text, p.Base, 4.5); rgba8(got) != rgba8(p.Text) {
		t.Errorf("ReadableAt changed a colour that already clears the floor: %v", rgba8(got))
	}
}

func TestShownIsIdentityAtTrueColor(t *testing.T) {
	for _, c := range []color.Color{color.RGBA{R: 1, G: 2, B: 3, A: 255}, ansi.BasicColor(3), NoColor} {
		if Shown(c) != c {
			t.Errorf("Shown(%v) changed the colour", c)
		}
	}
}

func TestNoColorAndSlot(t *testing.T) {
	if !IsNoColor(NoColor) || IsNoColor(color.Black) || IsNoColor(nil) {
		t.Error("IsNoColor does not tell NoColor from a colour")
	}

	if got := Slot(5); got != ansi.BasicColor(5) {
		t.Errorf("Slot(5) = %v, want basic colour 5", got)
	}
}
