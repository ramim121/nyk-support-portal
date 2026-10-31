package protocol

import "encoding/binary"

// Screen updates travel over a dedicated reliable, ordered WebRTC DataChannel
// named "screen". Instead of a video codec, the agent sends compressed image
// tiles: on a keyframe every tile of the current display, and on deltas only
// the tiles whose pixels changed. The console draws each tile onto a canvas at
// its (x,y). This is pure-Go on the client (no codec library) and very
// efficient on typical, mostly-static support screens.
//
// Two message kinds share the channel, distinguished by the first byte:
//
//	ScreenMsgInfo (2): the current display's identity and size. Sent whenever
//	    the captured monitor or its resolution changes (and before the first
//	    keyframe) so the console can size its canvas.
//	    layout: [1]=2 [2..3]=monitorIndex [4..5]=width [6..7]=height
//
//	ScreenMsgTile (1): one image tile.
//	    layout: [1]=1 [2..3]=x [4..5]=y [6..7]=w [8..9]=h [10]=codec [11..]=data
//	    codec 0 = JPEG.
const (
	ScreenMsgTile byte = 1
	ScreenMsgInfo byte = 2

	TileCodecJPEG byte = 0
)

// BuildScreenInfo encodes a ScreenMsgInfo message.
func BuildScreenInfo(monitorIndex, width, height int) []byte {
	b := make([]byte, 8)
	b[0] = ScreenMsgInfo
	binary.LittleEndian.PutUint16(b[1:], uint16(monitorIndex))
	binary.LittleEndian.PutUint16(b[3:], uint16(width))
	binary.LittleEndian.PutUint16(b[5:], uint16(height))
	return b
}

// BuildScreenTile encodes a ScreenMsgTile message around already-compressed
// image bytes (e.g. JPEG). The data is appended, not copied again by callers.
func BuildScreenTile(x, y, w, h int, codec byte, data []byte) []byte {
	b := make([]byte, 11+len(data))
	b[0] = ScreenMsgTile
	binary.LittleEndian.PutUint16(b[1:], uint16(x))
	binary.LittleEndian.PutUint16(b[3:], uint16(y))
	binary.LittleEndian.PutUint16(b[5:], uint16(w))
	binary.LittleEndian.PutUint16(b[7:], uint16(h))
	b[9] = codec
	// b[10] reserved (0) for future flags; data starts at 11.
	copy(b[11:], data)
	return b
}

// ScreenInfo and ScreenTile are decoded views (used by tests / Go consumers).
type ScreenInfo struct {
	MonitorIndex int
	Width        int
	Height       int
}

type ScreenTile struct {
	X, Y, W, H int
	Codec      byte
	Data       []byte
}

// ParseScreenInfo decodes a ScreenMsgInfo message.
func ParseScreenInfo(b []byte) (ScreenInfo, bool) {
	if len(b) < 7 || b[0] != ScreenMsgInfo {
		return ScreenInfo{}, false
	}
	return ScreenInfo{
		MonitorIndex: int(binary.LittleEndian.Uint16(b[1:])),
		Width:        int(binary.LittleEndian.Uint16(b[3:])),
		Height:       int(binary.LittleEndian.Uint16(b[5:])),
	}, true
}

// ParseScreenTile decodes a ScreenMsgTile message. Data aliases the input slice.
func ParseScreenTile(b []byte) (ScreenTile, bool) {
	if len(b) < 11 || b[0] != ScreenMsgTile {
		return ScreenTile{}, false
	}
	return ScreenTile{
		X:     int(binary.LittleEndian.Uint16(b[1:])),
		Y:     int(binary.LittleEndian.Uint16(b[3:])),
		W:     int(binary.LittleEndian.Uint16(b[5:])),
		H:     int(binary.LittleEndian.Uint16(b[7:])),
		Codec: b[9],
		Data:  b[11:],
	}, true
}
