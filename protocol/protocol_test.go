package protocol

import "testing"

func TestInputRoundTrip(t *testing.T) {
	cases := []InputEvent{
		{Op: OpMouseMove, X: 0, Y: 65535},
		{Op: OpMouseMove, X: 12345, Y: 54321},
		{Op: OpMouseBtn, Button: BtnLeft, Down: true},
		{Op: OpMouseBtn, Button: BtnRight, Down: false},
		{Op: OpWheel, DX: -120, DY: 240},
		{Op: OpKey, Scancode: 0x1E, Extended: false, Down: true},
		{Op: OpKey, Scancode: 0x48, Extended: true, Down: false},
	}
	for _, want := range cases {
		var raw []byte
		switch want.Op {
		case OpMouseMove:
			raw = EncodeMouseMove(want.X, want.Y)
		case OpMouseBtn:
			raw = EncodeMouseButton(want.Button, want.Down)
		case OpWheel:
			raw = EncodeWheel(want.DX, want.DY)
		case OpKey:
			raw = EncodeKey(want.Scancode, want.Extended, want.Down)
		}
		got, err := DecodeInput(raw)
		if err != nil {
			t.Fatalf("decode %v: %v", want, err)
		}
		if got != want {
			t.Errorf("round trip mismatch: got %+v want %+v", got, want)
		}
	}
}

func TestDecodeInputErrors(t *testing.T) {
	if _, err := DecodeInput(nil); err == nil {
		t.Error("expected error on empty frame")
	}
	if _, err := DecodeInput([]byte{byte(OpMouseMove), 1}); err == nil {
		t.Error("expected error on short move frame")
	}
	if _, err := DecodeInput([]byte{0xFF}); err == nil {
		t.Error("expected error on unknown opcode")
	}
}

func TestFormatDeviceID(t *testing.T) {
	if got := FormatDeviceID("123456789"); got != "123 456 789" {
		t.Errorf("got %q", got)
	}
	if got := FormatDeviceID("123"); got != "123" {
		t.Errorf("short id should pass through, got %q", got)
	}
}
