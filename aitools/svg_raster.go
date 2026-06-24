package aitools

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
)

const (
	svgDefaultDim = 1024
	svgMaxDim     = 2048
)

// rasterizeSVG renders SVG markup to PNG bytes so it can be sent to the model's
// vision channel. oksvg supports a subset of SVG (no full CSS, limited filters,
// gradients, and text) — complex documents may render blank or partially, in
// which case the caller falls back to surfacing the source via file_read.
func rasterizeSVG(data []byte) ([]byte, error) {
	icon, err := oksvg.ReadIconStream(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("parse svg: %w", err)
	}

	w, h := svgCanvasSize(icon.ViewBox.W, icon.ViewBox.H)
	icon.SetTarget(0, 0, float64(w), float64(h))

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	scanner := rasterx.NewScannerGV(w, h, img, img.Bounds())
	raster := rasterx.NewDasher(w, h, scanner)
	icon.Draw(raster, 1.0)

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encode png: %w", err)
	}
	return buf.Bytes(), nil
}

// svgCanvasSize scales the SVG's intrinsic viewBox so its longer side is
// svgDefaultDim (crisp output for small icons, downscaled for large art),
// clamped to svgMaxDim. A missing viewBox falls back to a square canvas.
func svgCanvasSize(vbW, vbH float64) (int, int) {
	if vbW <= 0 || vbH <= 0 {
		return svgDefaultDim, svgDefaultDim
	}
	scale := float64(svgDefaultDim) / math.Max(vbW, vbH)
	w := int(math.Round(vbW * scale))
	h := int(math.Round(vbH * scale))
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	if w > svgMaxDim {
		w = svgMaxDim
	}
	if h > svgMaxDim {
		h = svgMaxDim
	}
	return w, h
}
