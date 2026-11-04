package theme

import (
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/styles"
)

func TestSyntaxMatchesChroma(t *testing.T) {
	for _, th := range Builtin() {
		t.Run(th.Name, func(t *testing.T) {
			bundled := styles.Get(th.Name)
			if bundled.Name != th.Name {
				t.Fatalf("styles.Get(%q) returned %q; Chroma has no such style", th.Name, bundled.Name)
			}

			ours, err := th.Syntax()
			if err != nil {
				t.Fatalf("Syntax(): %v", err)
			}

			if got, want := len(ours.Types()), len(bundled.Types()); got != want {
				t.Errorf("ours has %d token types, bundled has %d", got, want)
			}

			types := map[chroma.TokenType]bool{}
			for _, tt := range append(bundled.Types(), ours.Types()...) {
				types[tt] = true
			}

			for tt := range types {
				if got, want := ours.Get(tt), bundled.Get(tt); got != want {
					t.Errorf("%s: ours %q, bundled %q", tt, got, want)
				}
			}
		})
	}
}

func TestSyntaxColoursFromPalette(t *testing.T) {
	for _, th := range Builtin() {
		t.Run(th.Name, func(t *testing.T) {
			ours, err := th.Syntax()
			if err != nil {
				t.Fatalf("Syntax(): %v", err)
			}

			inPalette := map[chroma.Colour]bool{}
			for _, h := range paletteFields(th.Palette) {
				inPalette[chroma.MustParseColour(string(h))] = true
			}

			for _, tt := range ours.Types() {
				e := ours.Get(tt)
				for _, c := range []chroma.Colour{e.Colour, e.Background, e.Border} {
					if c.IsSet() && !inPalette[c] {
						t.Errorf("%s: colour %s is not in the palette", tt, c)
					}
				}
			}
		})
	}
}
