package diffview

import (
	"image/color"
	"math"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/ui/theme"
)

// The copied renderer was written against a colour toolkit; these are the
// parts of it the renderer calls, written here for AIM.

// Contrast floors in WCAG contrast ratio: text, and a mark such as a comment
// or a line number.
const (
	ContrastFloor = 4.5
	MarkFloor     = 3.0
)

// Depth is the colour depth a diff is drawn for.
type Depth int

// The depths, richest first so the zero value draws truecolor.
const (
	DepthTrue Depth = iota
	Depth256
	Depth16
)

// noColor is the terminal's own default colour.
type noColor uint8

// NoColor stands for the terminal's default colour: it paints nothing.
const NoColor noColor = 0

// RGBA implements color.Color; the value is never measured.
func (noColor) RGBA() (r, g, b, a uint32) { return 0, 0, 0, 0 }

// IsNoColor reports whether c is NoColor.
func IsNoColor(c color.Color) bool {
	_, ok := c.(noColor)

	return ok
}

// Slot is the terminal's i-th basic colour (0-15), painted from the user's
// own palette.
func Slot(i int) color.Color {
	return ansi.BasicColor(max(0, min(i, 15))) //nolint:gosec // clamped to 0-15
}

// Shown is c as the terminal shows it. AIM draws truecolor and Bubble
// Tea downsamples, so it is c itself.
func Shown(c color.Color) color.Color { return c }

// Ellipsis is the mark a cut line ends in.
func Ellipsis() string { return "…" }

// ContrastRatio is the WCAG contrast ratio of a and b, from 1 to 21.
func ContrastRatio(a, b color.Color) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}

	return (la + 0.05) / (lb + 0.05)
}

// ContrastText is black or white, whichever reads better on bg.
func ContrastText(bg color.Color) color.Color {
	if ContrastRatio(bg, color.Black) >= ContrastRatio(bg, color.White) {
		return color.Black
	}

	return color.White
}

// Readable is fg moved just far enough from bg to clear ContrastFloor.
func Readable(fg, bg color.Color) color.Color { return ReadableAt(fg, bg, ContrastFloor) }

// ReadableAt is fg, or the nearest colour of its hue that has at least floor
// contrast against bg, found by moving its lightness away from bg.
func ReadableAt(fg, bg color.Color, floor float64) color.Color {
	if ContrastRatio(fg, bg) >= floor {
		return fg
	}

	l, a, b := toOKLab(fg)

	toward := 1.0
	if luminance(bg) > 0.5 {
		toward = 0
	}

	// Bisect between the original lightness and the far end for the nearest
	// lightness that clears the floor.
	lo, hi := l, toward
	if fromEnd := fromOKLab(toward, a, b); ContrastRatio(fromEnd, bg) < floor {
		// Even the far end does not clear it (a mid-tone ground): use the
		// extreme that reads best.
		return ContrastText(bg)
	}

	for range 24 {
		mid := (lo + hi) / 2
		if ContrastRatio(fromOKLab(mid, a, b), bg) >= floor {
			hi = mid
		} else {
			lo = mid
		}
	}

	return fromOKLab(hi, a, b)
}

// Darker is c with its lightness divided by factor (factor > 1).
func Darker(c color.Color, factor float64) color.Color {
	l, a, b := toOKLab(c)

	return fromOKLab(l/factor, a, b)
}

// Lighter is c with its lightness multiplied by factor (factor > 1).
func Lighter(c color.Color, factor float64) color.Color {
	l, a, b := toOKLab(c)

	return fromOKLab(math.Min(l*factor, 1), a, b)
}

// MixColors is the share t of b mixed into a, interpolated in OKLab.
func MixColors(a, b color.Color, t float64) color.Color {
	switch {
	case t <= 0:
		return solid(a)
	case t >= 1:
		return solid(b)
	}

	l1, a1, b1 := toOKLab(a)
	l2, a2, b2 := toOKLab(b)

	return fromOKLab(l1+(l2-l1)*t, a1+(a2-a1)*t, b1+(b2-b1)*t)
}

// The Catppuccin Mocha colours a Palette falls back to, and the ones the
// default syntax colours take, one function per colour.

func mocha() theme.Palette {
	t, err := theme.Builtin().ByName("catppuccin-mocha")
	if err != nil {
		return theme.Palette{}
	}

	return t.Palette
}

func toneChar() color.Color    { return mocha().Base }
func toneCharple() color.Color { return mocha().Mauve }
func toneJulep() color.Color   { return mocha().Green }
func toneCherry() color.Color  { return mocha().Red }
func toneMauve() color.Color   { return mocha().Mauve }
func toneGuppy() color.Color   { return mocha().Yellow }
func toneMalibu() color.Color  { return mocha().Blue }
func toneTurtle() color.Color  { return mocha().Maroon }
func toneCumin() color.Color   { return mocha().Green }
func toneTang() color.Color    { return mocha().Peach }
func toneSquid() color.Color   { return mocha().Overlay1 }
func toneSalmon() color.Color  { return mocha().Sky }
func toneBengal() color.Color  { return mocha().Pink }
func toneHazy() color.Color    { return mocha().Lavender }

// unit returns c's sRGB channels in [0, 1].
func unit(c color.Color) (r, g, b float64) {
	ri, gi, bi, _ := c.RGBA()

	return float64(ri) / 0xffff, float64(gi) / 0xffff, float64(bi) / 0xffff
}

func toLinear(v float64) float64 {
	if v <= 0.04045 {
		return v / 12.92
	}

	return math.Pow((v+0.055)/1.055, 2.4)
}

func fromLinear(v float64) float64 {
	if v <= 0.0031308 {
		return v * 12.92
	}

	return 1.055*math.Pow(v, 1/2.4) - 0.055
}

// luminance is the WCAG relative luminance of c.
func luminance(c color.Color) float64 {
	r, g, b := unit(c)

	return 0.2126*toLinear(r) + 0.7152*toLinear(g) + 0.0722*toLinear(b)
}

func toOKLab(c color.Color) (l, a, b float64) {
	r, g, bl := unit(c)
	r, g, bl = toLinear(r), toLinear(g), toLinear(bl)

	lc := math.Cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*bl)
	mc := math.Cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*bl)
	sc := math.Cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*bl)

	return 0.2104542553*lc + 0.7936177850*mc - 0.0040720468*sc,
		1.9779984951*lc - 2.4285922050*mc + 0.4505937099*sc,
		0.0259040371*lc + 0.7827717662*mc - 0.8086757660*sc
}

func fromOKLab(l, a, b float64) color.Color {
	lc := cube(l + 0.3963377774*a + 0.2158037573*b)
	mc := cube(l - 0.1055613458*a - 0.0638541728*b)
	sc := cube(l - 0.0894841775*a - 1.2914855480*b)

	r := 4.0767416621*lc - 3.3077115913*mc + 0.2309699292*sc
	g := -1.2684380046*lc + 2.6097574011*mc - 0.3413193965*sc
	bl := -0.0041960863*lc - 0.7034186147*mc + 1.7076147010*sc

	return color.RGBA{R: channel(r), G: channel(g), B: channel(bl), A: 0xff}
}

func cube(v float64) float64 { return v * v * v }

func channel(linear float64) uint8 {
	v := math.Round(fromLinear(math.Max(0, math.Min(1, linear))) * 255)

	return uint8(math.Max(0, math.Min(255, v)))
}
