package protocol

import (
	"encoding/binary"
	"errors"
)

// Input events travel over a WebRTC DataChannel as compact binary frames:
// a 1-byte opcode followed by a fixed little-endian payload. This keeps each
// mouse-move to 5 bytes so high-frequency movement stays cheap.
//
// The console encodes these; the agent decodes them and hands the decoded
// InputEvent to its platform input layer. Coordinates for moves are normalized
// to 0..65535 across the currently selected monitor, so they are resolution
// independent (the agent scales them to that monitor's pixel rect).

type OpCode byte

const (
	OpMouseMove OpCode = 1 // payload: uint16 x, uint16 y (normalized)
	OpMouseBtn  OpCode = 2 // payload: byte button, byte down(0/1)
	OpWheel     OpCode = 3 // payload: int16 dx, int16 dy
	OpKey       OpCode = 4 // payload: uint16 scancode, byte extended, byte down
)

// Mouse button identifiers used in OpMouseBtn payloads.
const (
	BtnLeft   byte = 0
	BtnRight  byte = 1
	BtnMiddle byte = 2
)

// InputEvent is the decoded form of a DataChannel input frame. Only the fields
// relevant to Op are populated.
type InputEvent struct {
	Op       OpCode
	X, Y     uint16 // normalized 0..65535 (OpMouseMove)
	Button   byte   // OpMouseBtn
	Down     bool   // OpMouseBtn, OpKey
	DX, DY   int16  // OpWheel
	Scancode uint16 // OpKey (Windows set-1 scancode)
	Extended bool   // OpKey (E0-prefixed key)
}

var errShort = errors.New("protocol: input frame too short")

// EncodeMouseMove builds a normalized move frame.
func EncodeMouseMove(x, y uint16) []byte {
	b := make([]byte, 5)
	b[0] = byte(OpMouseMove)
	binary.LittleEndian.PutUint16(b[1:], x)
	binary.LittleEndian.PutUint16(b[3:], y)
	return b
}

// EncodeMouseButton builds a button up/down frame.
func EncodeMouseButton(button byte, down bool) []byte {
	return []byte{byte(OpMouseBtn), button, b2i(down)}
}

// EncodeWheel builds a scroll frame.
func EncodeWheel(dx, dy int16) []byte {
	b := make([]byte, 5)
	b[0] = byte(OpWheel)
	binary.LittleEndian.PutUint16(b[1:], uint16(dx))
	binary.LittleEndian.PutUint16(b[3:], uint16(dy))
	return b
}

// EncodeKey builds a key up/down frame.
func EncodeKey(scancode uint16, extended, down bool) []byte {
	b := make([]byte, 5)
	b[0] = byte(OpKey)
	binary.LittleEndian.PutUint16(b[1:], scancode)
	b[3] = b2i(extended)
	b[4] = b2i(down)
	return b
}

// DecodeInput parses one binary input frame into an InputEvent.
func DecodeInput(b []byte) (InputEvent, error) {
	if len(b) < 1 {
		return InputEvent{}, errShort
	}
	ev := InputEvent{Op: OpCode(b[0])}
	switch ev.Op {
	case OpMouseMove:
		if len(b) < 5 {
			return ev, errShort
		}
		ev.X = binary.LittleEndian.Uint16(b[1:])
		ev.Y = binary.LittleEndian.Uint16(b[3:])
	case OpMouseBtn:
		if len(b) < 3 {
			return ev, errShort
		}
		ev.Button = b[1]
		ev.Down = b[2] != 0
	case OpWheel:
		if len(b) < 5 {
			return ev, errShort
		}
		ev.DX = int16(binary.LittleEndian.Uint16(b[1:]))
		ev.DY = int16(binary.LittleEndian.Uint16(b[3:]))
	case OpKey:
		if len(b) < 5 {
			return ev, errShort
		}
		ev.Scancode = binary.LittleEndian.Uint16(b[1:])
		ev.Extended = b[3] != 0
		ev.Down = b[4] != 0
	default:
		return ev, errors.New("protocol: unknown input opcode")
	}
	return ev, nil
}

func b2i(v bool) byte {
	if v {
		return 1
	}
	return 0
}
