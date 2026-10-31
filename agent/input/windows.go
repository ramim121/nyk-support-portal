//go:build windows

package input

import (
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/nyk/support-portal/protocol"
)

// WindowsSink applies the technician's decoded input to the local desktop using
// the standard Win32 SendInput API — the same mechanism accessibility tools and
// every remote-support product use. It runs only inside an authorized, consented
// session (the agent gates it) and never persists or runs in the background.
//
// This is pure Go (syscalls via golang.org/x/sys/windows); no cgo.

var (
	user32           = windows.NewLazySystemDLL("user32.dll")
	procSendInput    = user32.NewProc("SendInput")
	procGetSystemMet = user32.NewProc("GetSystemMetrics")
)

// Win32 constants.
const (
	inputMouse    = 0
	inputKeyboard = 1

	mouseeventfMove        = 0x0001
	mouseeventfLeftDown    = 0x0002
	mouseeventfLeftUp      = 0x0004
	mouseeventfRightDown   = 0x0008
	mouseeventfRightUp     = 0x0010
	mouseeventfMiddleDown  = 0x0020
	mouseeventfMiddleUp    = 0x0040
	mouseeventfWheel       = 0x0800
	mouseeventfHWheel      = 0x1000
	mouseeventfAbsolute    = 0x8000
	mouseeventfVirtualDesk = 0x4000

	keyeventfKeyUp    = 0x0002
	keyeventfScancode = 0x0008
	keyeventfExtended = 0x0001

	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79
)

// INPUT layout for 64-bit Windows: a DWORD type, 4 bytes of padding to align the
// union, then a 32-byte union (large enough for MOUSEINPUT).
type winInput struct {
	typ   uint32
	_     [4]byte
	union [32]byte
}

type mouseInput struct {
	dx          int32
	dy          int32
	mouseData   uint32
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

type keybdInput struct {
	wVk         uint16
	wScan       uint16
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

// NewWindows returns the desktop input sink.
func NewWindows() *WindowsSink { return &WindowsSink{} }

type WindowsSink struct{}

func (s *WindowsSink) Apply(ev protocol.InputEvent, mon protocol.Monitor) {
	switch ev.Op {
	case protocol.OpMouseMove:
		px, py := AbsPixel(ev.X, ev.Y, mon)
		s.moveMouse(px, py)
	case protocol.OpMouseBtn:
		s.mouseButton(ev.Button, ev.Down)
	case protocol.OpWheel:
		s.wheel(ev.DX, ev.DY)
	case protocol.OpKey:
		s.key(ev.Scancode, ev.Extended, ev.Down)
	}
}

func (s *WindowsSink) Close() error { return nil }

// moveMouse positions the cursor at an absolute desktop pixel. SendInput's
// absolute mode wants coordinates normalized to 0..65535 across the whole
// virtual desktop, so we re-map the pixel through the virtual-screen metrics.
func (s *WindowsSink) moveMouse(px, py int) {
	vx := getSystemMetrics(smXVirtualScreen)
	vy := getSystemMetrics(smYVirtualScreen)
	vw := getSystemMetrics(smCXVirtualScreen)
	vh := getSystemMetrics(smCYVirtualScreen)
	if vw <= 1 || vh <= 1 {
		return
	}
	nx := int32((int64(px-vx) * 65535) / int64(vw-1))
	ny := int32((int64(py-vy) * 65535) / int64(vh-1))
	mi := mouseInput{dx: nx, dy: ny, dwFlags: mouseeventfMove | mouseeventfAbsolute | mouseeventfVirtualDesk}
	sendMouse(mi)
}

func (s *WindowsSink) mouseButton(button byte, down bool) {
	var flag uint32
	switch button {
	case protocol.BtnRight:
		flag = pick(down, mouseeventfRightDown, mouseeventfRightUp)
	case protocol.BtnMiddle:
		flag = pick(down, mouseeventfMiddleDown, mouseeventfMiddleUp)
	default:
		flag = pick(down, mouseeventfLeftDown, mouseeventfLeftUp)
	}
	sendMouse(mouseInput{dwFlags: flag})
}

func (s *WindowsSink) wheel(dx, dy int16) {
	if dy != 0 {
		sendMouse(mouseInput{mouseData: uint32(int32(dy)), dwFlags: mouseeventfWheel})
	}
	if dx != 0 {
		sendMouse(mouseInput{mouseData: uint32(int32(dx)), dwFlags: mouseeventfHWheel})
	}
}

func (s *WindowsSink) key(scancode uint16, extended, down bool) {
	flags := uint32(keyeventfScancode)
	if extended {
		flags |= keyeventfExtended
	}
	if !down {
		flags |= keyeventfKeyUp
	}
	sendKey(keybdInput{wScan: scancode, dwFlags: flags})
}

// --- low-level SendInput wrappers ---

func sendMouse(mi mouseInput) {
	in := winInput{typ: inputMouse}
	*(*mouseInput)(unsafe.Pointer(&in.union)) = mi
	sendInputs(&in)
}

func sendKey(ki keybdInput) {
	in := winInput{typ: inputKeyboard}
	*(*keybdInput)(unsafe.Pointer(&in.union)) = ki
	sendInputs(&in)
}

func sendInputs(in *winInput) {
	procSendInput.Call(1, uintptr(unsafe.Pointer(in)), unsafe.Sizeof(*in))
}

func getSystemMetrics(index int) int {
	r, _, _ := procGetSystemMet.Call(uintptr(index))
	return int(int32(r))
}

func pick(cond bool, a, b uint32) uint32 {
	if cond {
		return a
	}
	return b
}
