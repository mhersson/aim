package kitty

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"testing"
)

func TestFit(t *testing.T) {
	t.Parallel()

	cell := Cell{W: 10, H: 20}

	tests := []struct {
		name         string
		w, h         int
		cell         Cell
		maxC, maxR   int
		wantC, wantR int
	}{
		{"square", 220, 220, cell, 100, 20, 22, 11},
		{"height limited", 1847, 948, cell, 96, 20, 78, 20},
		{"wide", 50, 10, cell, 100, 20, 5, 1},
		{"width limited", 4000, 100, cell, 40, 20, 40, 1},
		{"zero width", 0, 10, cell, 100, 20, 0, 0},
		{"zero cols", 100, 10, cell, 0, 20, 0, 0},
		{"zero cell", 100, 10, Cell{}, 10, 20, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, r := Fit(tt.w, tt.h, tt.cell, tt.maxC, tt.maxR)
			if c != tt.wantC || r != tt.wantR {
				t.Fatalf("Fit = %dx%d, want %dx%d", c, r, tt.wantC, tt.wantR)
			}
		})
	}
}

// pngHeaderDepth is a truecolor PNG header of the given bit depth.
func pngHeaderDepth(w, h uint32, depth byte) []byte {
	ihdr := make([]byte, 0, 17)
	ihdr = append(ihdr, "IHDR"...)
	ihdr = binary.BigEndian.AppendUint32(ihdr, w)
	ihdr = binary.BigEndian.AppendUint32(ihdr, h)
	ihdr = append(ihdr, depth, 2, 0, 0, 0)
	out := []byte("\x89PNG\r\n\x1a\n")
	out = binary.BigEndian.AppendUint32(out, 13)
	out = append(out, ihdr...)

	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(ihdr))
}

func TestSizeLimits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		w, h    uint32
		depth   byte
		wantErr error
	}{
		{"pixels", 30000, 30000, 8, ErrTooBig},
		{"side", 16385, 1, 8, ErrTooBig},
		{"at limit", 16384, 1, 8, nil},
		{"8-bit at the byte cap", 10000, 4000, 8, nil},
		{"8-bit over the byte cap", 10000, 4001, 8, ErrTooBig},
		{"16-bit at the byte cap", 10000, 2000, 16, nil},
		{"16-bit just over the byte cap", 10000, 2001, 16, ErrTooBig},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w, h, err := Size(pngHeaderDepth(tt.w, tt.h, tt.depth))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}

			if err == nil && (w != int(tt.w) || h != int(tt.h)) {
				t.Fatalf("size = %dx%d", w, h)
			}
		})
	}
}

func encode(t *testing.T, enc func(*bytes.Buffer) error) []byte {
	t.Helper()

	var b bytes.Buffer
	if err := enc(&b); err != nil {
		t.Fatal(err)
	}

	return b.Bytes()
}

func solid(w, h int, c color.Color) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}

	return img
}

func TestRenderFormats(t *testing.T) {
	t.Parallel()

	webp, err := os.ReadFile("testdata/gopher.webp")
	if err != nil {
		t.Fatal(err)
	}

	src := solid(200, 100, color.NRGBA{R: 200, G: 30, B: 30, A: 128})
	pal := image.NewPaletted(image.Rect(0, 0, 200, 100), color.Palette{color.RGBA{R: 255, A: 255}, color.RGBA{B: 255, A: 255}})
	tests := []struct {
		name string
		data []byte
	}{
		{"png", encode(t, func(b *bytes.Buffer) error { return png.Encode(b, src) })},
		{"jpeg", encode(t, func(b *bytes.Buffer) error { return jpeg.Encode(b, src, nil) })},
		{"gif", encode(t, func(b *bytes.Buffer) error { return gif.Encode(b, pal, nil) })},
		{"webp", webp},
	}
	cell := Cell{W: 10, H: 20}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sw, sh, err := Size(tt.data)
			if err != nil {
				t.Fatal(err)
			}

			out, err := Render(tt.data, 5, 3, cell)
			if err != nil {
				t.Fatal(err)
			}

			got, err := png.Decode(bytes.NewReader(out))
			if err != nil {
				t.Fatalf("output is not a PNG: %v", err)
			}

			gw, gh := got.Bounds().Dx(), got.Bounds().Dy()
			if gw > 50 || gh > 60 {
				t.Fatalf("output %dx%d exceeds 50x60", gw, gh)
			}

			want := float64(sw) * float64(gh) / float64(sh)
			if d := want - float64(gw); d > 1 || d < -1 {
				t.Fatalf("output %dx%d changes the aspect of %dx%d", gw, gh, sw, sh)
			}
		})
	}
}

func TestRenderGIFFirstFrame(t *testing.T) {
	t.Parallel()

	pal := color.Palette{color.RGBA{R: 255, A: 255}, color.RGBA{B: 255, A: 255}}
	red := image.NewPaletted(image.Rect(0, 0, 20, 20), pal)

	blue := image.NewPaletted(image.Rect(0, 0, 20, 20), pal)
	for i := range blue.Pix {
		blue.Pix[i] = 1
	}

	data := encode(t, func(b *bytes.Buffer) error {
		return gif.EncodeAll(b, &gif.GIF{Image: []*image.Paletted{red, blue}, Delay: []int{1, 1}})
	})

	out, err := Render(data, 1, 1, Cell{W: 10, H: 20})
	if err != nil {
		t.Fatal(err)
	}

	got, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}

	r, g, b, _ := got.At(0, 0).RGBA()
	if r>>8 < 200 || g != 0 || b != 0 {
		t.Fatalf("first pixel = %d,%d,%d, want red", r>>8, g>>8, b>>8)
	}
}

func TestRenderRejects(t *testing.T) {
	t.Parallel()
	good := encode(t, func(b *bytes.Buffer) error { return png.Encode(b, solid(100, 100, color.White)) })

	tests := []struct {
		name    string
		data    []byte
		wantErr error
		anyErr  bool
	}{
		{"svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`), ErrFormat, false},
		{"truncated", good[:100], nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := Render(tt.data, 5, 3, Cell{W: 10, H: 20})
			if tt.anyErr {
				if err == nil {
					t.Fatal("want an error")
				}

				return
			}

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
