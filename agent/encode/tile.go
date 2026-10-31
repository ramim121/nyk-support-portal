// Package encode turns raw RGBA frames into the "screen" channel's tile
// messages. It is pure Go (image/jpeg) with no codec library, so the agent
// cross-compiles to a single small binary with no cgo.
//
// The frame is divided into a fixed grid of tiles. On the first frame (or after
// a forced keyframe, or when the display size/monitor changes) every tile is
// sent; afterwards only tiles whose pixels changed since the previous frame are
// re-encoded and sent. On a typical mostly-static desktop that is a handful of
// small tiles per second.
package encode

import (
	"bytes"
	"image"
	"image/jpeg"

	"github.com/nyk/support-portal/protocol"
)

const defaultTileSize = 256

// TileEncoder holds the previous frame to diff against. It is not safe for
// concurrent use; drive it from a single goroutine.
type TileEncoder struct {
	tile    int
	quality int

	prev    []byte
	prevW   int
	prevH   int
	prevMon int
	haveKey bool

	forceKey bool
	buf      bytes.Buffer // reused JPEG scratch
}

func NewTileEncoder() *TileEncoder {
	return &TileEncoder{tile: defaultTileSize, quality: 55}
}

// SetQuality sets the JPEG quality (1..100).
func (e *TileEncoder) SetQuality(q int) {
	if q >= 1 && q <= 100 {
		e.quality = q
	}
}

// ForceKeyframe makes the next Encode resend every tile (e.g. a new viewer, or
// recovery after loss).
func (e *TileEncoder) ForceKeyframe() { e.forceKey = true }

// Encode compares the frame against the previous one and returns the messages
// to send (already framed by package protocol). An empty result means nothing
// changed. pix must be RGBA with stride w*4.
func (e *TileEncoder) Encode(pix []byte, w, h, monitorIndex int) [][]byte {
	if w <= 0 || h <= 0 || len(pix) < w*h*4 {
		return nil
	}
	key := e.forceKey || !e.haveKey || w != e.prevW || h != e.prevH || monitorIndex != e.prevMon
	e.forceKey = false

	var msgs [][]byte
	if key {
		msgs = append(msgs, protocol.BuildScreenInfo(monitorIndex, w, h))
	}

	full := &image.RGBA{Pix: pix, Stride: w * 4, Rect: image.Rect(0, 0, w, h)}

	for y := 0; y < h; y += e.tile {
		for x := 0; x < w; x += e.tile {
			tw := e.tile
			if x+tw > w {
				tw = w - x
			}
			th := e.tile
			if y+th > h {
				th = h - y
			}
			if !key && e.tilesEqual(pix, w, x, y, tw, th) {
				continue
			}
			data := e.encodeTile(full, x, y, tw, th)
			if data != nil {
				msgs = append(msgs, protocol.BuildScreenTile(x, y, tw, th, protocol.TileCodecJPEG, data))
			}
		}
	}

	// Remember this frame for the next diff.
	if cap(e.prev) < len(pix) {
		e.prev = make([]byte, len(pix))
	} else {
		e.prev = e.prev[:len(pix)]
	}
	copy(e.prev, pix)
	e.prevW, e.prevH, e.prevMon, e.haveKey = w, h, monitorIndex, true

	return msgs
}

// encodeTile JPEG-encodes one tile rectangle of the full image.
func (e *TileEncoder) encodeTile(full *image.RGBA, x, y, tw, th int) []byte {
	sub := full.SubImage(image.Rect(x, y, x+tw, y+th))
	e.buf.Reset()
	if err := jpeg.Encode(&e.buf, sub, &jpeg.Options{Quality: e.quality}); err != nil {
		return nil
	}
	out := make([]byte, e.buf.Len())
	copy(out, e.buf.Bytes())
	return out
}

// tilesEqual reports whether a tile's pixels match the previous frame.
func (e *TileEncoder) tilesEqual(pix []byte, w, x, y, tw, th int) bool {
	if len(e.prev) != len(pix) {
		return false
	}
	rowBytes := tw * 4
	for row := y; row < y+th; row++ {
		off := (row*w + x) * 4
		if !bytes.Equal(pix[off:off+rowBytes], e.prev[off:off+rowBytes]) {
			return false
		}
	}
	return true
}
