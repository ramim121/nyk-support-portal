package main

import (
	"image"
	"image/png"
	"os"
	"testing"
)

// TestRenderAssets renders the embedded brand SVGs to PNGs when ASSET_OUT is
// set, so the raster output can be eyeballed. It also fails if rendering errors.
func TestRenderAssets(t *testing.T) {
	logo, err := renderLogo(90)
	if err != nil {
		t.Fatalf("logo: %v", err)
	}
	icon, err := renderIcon(128)
	if err != nil {
		t.Fatalf("icon: %v", err)
	}
	if out := os.Getenv("ASSET_OUT"); out != "" {
		save(t, out+"/logo.png", logo)
		save(t, out+"/icon.png", icon)
	}
}

func save(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
