package encode

import (
	"testing"

	"github.com/nyk/support-portal/protocol"
)

func solidFrame(w, h int, r, g, b byte) []byte {
	pix := make([]byte, w*h*4)
	for i := 0; i < len(pix); i += 4 {
		pix[i], pix[i+1], pix[i+2], pix[i+3] = r, g, b, 255
	}
	return pix
}

func countKinds(msgs [][]byte) (info, tiles int) {
	for _, m := range msgs {
		switch m[0] {
		case protocol.ScreenMsgInfo:
			info++
		case protocol.ScreenMsgTile:
			tiles++
		}
	}
	return
}

func TestEncoderKeyframeThenNoChange(t *testing.T) {
	const w, h = 512, 512
	enc := NewTileEncoder()
	pix := solidFrame(w, h, 10, 20, 30)

	// First frame: keyframe -> info + every tile (2x2 grid at 256px).
	msgs := enc.Encode(pix, w, h, 0)
	info, tiles := countKinds(msgs)
	if info != 1 || tiles != 4 {
		t.Fatalf("keyframe: info=%d tiles=%d, want 1 and 4", info, tiles)
	}

	// Identical frame: nothing to send.
	msgs = enc.Encode(pix, w, h, 0)
	if len(msgs) != 0 {
		t.Fatalf("unchanged frame should emit nothing, got %d msgs", len(msgs))
	}
}

func TestEncoderSendsOnlyChangedTile(t *testing.T) {
	const w, h = 512, 512
	enc := NewTileEncoder()
	pix := solidFrame(w, h, 10, 20, 30)
	enc.Encode(pix, w, h, 0) // prime keyframe

	// Change one pixel in the bottom-right tile (x>=256, y>=256).
	px, py := 300, 400
	off := (py*w + px) * 4
	pix[off] = 200

	msgs := enc.Encode(pix, w, h, 0)
	info, tiles := countKinds(msgs)
	if info != 0 || tiles != 1 {
		t.Fatalf("delta: info=%d tiles=%d, want 0 and 1", info, tiles)
	}
	tile, _ := protocol.ParseScreenTile(msgs[0])
	if tile.X != 256 || tile.Y != 256 {
		t.Fatalf("changed tile at (%d,%d), want (256,256)", tile.X, tile.Y)
	}
}

func TestEncoderForceKeyframe(t *testing.T) {
	const w, h = 512, 512
	enc := NewTileEncoder()
	pix := solidFrame(w, h, 1, 2, 3)
	enc.Encode(pix, w, h, 0)

	enc.ForceKeyframe()
	msgs := enc.Encode(pix, w, h, 0)
	info, tiles := countKinds(msgs)
	if info != 1 || tiles != 4 {
		t.Fatalf("forced keyframe: info=%d tiles=%d, want 1 and 4", info, tiles)
	}
}

func TestEncoderResolutionChangeIsKeyframe(t *testing.T) {
	enc := NewTileEncoder()
	enc.Encode(solidFrame(256, 256, 0, 0, 0), 256, 256, 0)
	msgs := enc.Encode(solidFrame(512, 256, 0, 0, 0), 512, 256, 0)
	info, _ := countKinds(msgs)
	if info != 1 {
		t.Fatalf("resolution change should re-send info, got info=%d", info)
	}
}
