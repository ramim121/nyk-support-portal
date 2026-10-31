package protocol

import (
	"bytes"
	"testing"
)

func TestScreenInfoRoundTrip(t *testing.T) {
	msg := BuildScreenInfo(1, 1920, 1080)
	info, ok := ParseScreenInfo(msg)
	if !ok {
		t.Fatal("parse failed")
	}
	if info.MonitorIndex != 1 || info.Width != 1920 || info.Height != 1080 {
		t.Fatalf("got %+v", info)
	}
	if msg[0] != ScreenMsgInfo {
		t.Fatalf("wrong type byte %d", msg[0])
	}
}

func TestScreenTileRoundTrip(t *testing.T) {
	data := []byte{0xFF, 0xD8, 0xFF, 0x01, 0x02, 0x03}
	msg := BuildScreenTile(256, 512, 200, 128, TileCodecJPEG, data)
	tile, ok := ParseScreenTile(msg)
	if !ok {
		t.Fatal("parse failed")
	}
	if tile.X != 256 || tile.Y != 512 || tile.W != 200 || tile.H != 128 || tile.Codec != TileCodecJPEG {
		t.Fatalf("header mismatch: %+v", tile)
	}
	if !bytes.Equal(tile.Data, data) {
		t.Fatalf("data mismatch: %v", tile.Data)
	}
}

func TestParseScreenRejectsShort(t *testing.T) {
	if _, ok := ParseScreenInfo([]byte{ScreenMsgInfo, 1}); ok {
		t.Error("should reject short info")
	}
	if _, ok := ParseScreenTile([]byte{ScreenMsgTile, 1, 2}); ok {
		t.Error("should reject short tile")
	}
}
