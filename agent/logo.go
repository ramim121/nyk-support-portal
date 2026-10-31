package main

import (
	"bytes"
	_ "embed"
	"image"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
)

// The brand assets are embedded and rasterized at runtime with a pure-Go SVG
// renderer, so the agent window can show the logo and a taskbar icon without
// shipping extra image files or needing cgo.

//go:embed assets/logo.svg
var logoSVG []byte

//go:embed assets/icon.svg
var iconSVG []byte

// renderSVG rasterizes an SVG byte slice to an RGBA image of the given size.
func renderSVG(data []byte, w, h int) (*image.RGBA, error) {
	icon, err := oksvg.ReadIconStream(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	icon.SetTarget(0, 0, float64(w), float64(h))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	scanner := rasterx.NewScannerGV(w, h, img, img.Bounds())
	raster := rasterx.NewDasher(w, h, scanner)
	icon.Draw(raster, 1.0)
	return img, nil
}

// renderLogo renders the wide wordmark at the given height (2:1 aspect).
func renderLogo(height int) (*image.RGBA, error) { return renderSVG(logoSVG, height*2, height) }

// renderIcon renders the square monogram icon at size×size.
func renderIcon(size int) (*image.RGBA, error) { return renderSVG(iconSVG, size, size) }
