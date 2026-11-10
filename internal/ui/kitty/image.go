package kitty

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"  // registers the GIF decoder
	_ "image/jpeg" // registers the JPEG decoder
	"image/png"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // registers the WebP decoder
)

const (
	// MaxSide is the longest side, in pixels, that is decoded.
	MaxSide = 16384
	// MaxDecodedBytes is the most memory a decoded image may take: 40 megapixels
	// at 4 bytes, counted from the header's colour model.
	MaxDecodedBytes = 160_000_000
)

// MaxFileBytes is the most an image file may hold.
const MaxFileBytes = 5 << 20

var (
	// ErrFormat means the data is not a PNG, JPEG, GIF or WebP image.
	ErrFormat = errors.New("not a PNG, JPEG, GIF or WebP image")
	// ErrTooBig means the image is over MaxSide or MaxDecodedBytes.
	ErrTooBig = errors.New("image too large to decode")
)

// Cell is the size of one terminal cell in pixels.
type Cell struct{ W, H int }

// Size reads an image's size from its header without decoding the pixels.
func Size(data []byte) (w, h int, err error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if errors.Is(err, image.ErrFormat) {
		return 0, 0, ErrFormat
	}

	if err != nil {
		return 0, 0, fmt.Errorf("read image header: %w", err)
	}

	if cfg.Width < 1 || cfg.Height < 1 {
		return 0, 0, ErrFormat
	}

	if cfg.Width > MaxSide || cfg.Height > MaxSide || cfg.Width*cfg.Height*bytesPerPixel(cfg.ColorModel) > MaxDecodedBytes {
		return 0, 0, ErrTooBig
	}

	return cfg.Width, cfg.Height, nil
}

// bytesPerPixel is what a pixel of model takes once decoded.
func bytesPerPixel(m color.Model) int {
	switch m {
	case color.RGBA64Model, color.NRGBA64Model:
		return 8
	case color.Gray16Model:
		return 2
	case color.GrayModel:
		return 1
	}

	if _, ok := m.(color.Palette); ok {
		return 1
	}

	return 4
}

// Fit returns the cells a w by h pixel image takes: natural size, shrunk to
// fit maxCols by maxRows cells, never below 1x1. Any argument under 1 gives 0, 0.
func Fit(w, h int, c Cell, maxCols, maxRows int) (cols, rows int) {
	if w < 1 || h < 1 || c.W < 1 || c.H < 1 || maxCols < 1 || maxRows < 1 {
		return 0, 0
	}

	boxW, boxH := maxCols*c.W, maxRows*c.H
	switch {
	case w <= boxW && h <= boxH:
		cols, rows = ceilDiv(w, c.W), ceilDiv(h, c.H)
	case boxW*h <= boxH*w: // boxW/w <= boxH/h: the width binds
		cols, rows = maxCols, ceilDiv(h*boxW, w*c.H)
	default:
		cols, rows = ceilDiv(w*boxH, h*c.W), maxRows
	}

	return min(max(cols, 1), maxCols), min(max(rows, 1), maxRows)
}

func ceilDiv(a, b int) int { return (a + b - 1) / b }

// Render decodes the image (a GIF's first frame), scales it to fit inside
// cols by rows cells keeping its aspect, and encodes it as PNG.
func Render(data []byte, cols, rows int, c Cell) ([]byte, error) {
	w, h, err := Size(data)
	if err != nil {
		return nil, err
	}

	if cols < 1 || rows < 1 || c.W < 1 || c.H < 1 {
		return nil, fmt.Errorf("render image: bad target %dx%d cells of %dx%d", cols, rows, c.W, c.H)
	}

	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}

	boxW, boxH := cols*c.W, rows*c.H

	dw, dh := boxW, h*boxW/w
	if dh > boxH {
		dw, dh = w*boxH/h, boxH
	}

	dst := image.NewNRGBA(image.Rect(0, 0, max(dw, 1), max(dh, 1)))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)

	var buf bytes.Buffer

	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, dst); err != nil {
		return nil, fmt.Errorf("encode png: %w", err)
	}

	return buf.Bytes(), nil
}
