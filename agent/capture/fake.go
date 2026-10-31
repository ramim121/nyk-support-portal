package capture

import (
	"context"
	"sync"
	"time"

	"github.com/nyk/support-portal/protocol"
)

// FakeSource synthesizes animated RGBA frames: a static gradient background
// with a moving block and a blinking corner. It drives the real tile encoder in
// dev/CI, so the whole screen pipeline is exercised without any capture
// hardware, on any OS, with pure Go.
type FakeSource struct {
	mu     sync.Mutex
	fps    int
	w, h   int
	frame  int
	closed chan struct{}
	once   sync.Once
}

func NewFake() *FakeSource {
	return &FakeSource{fps: 12, w: 640, h: 360, closed: make(chan struct{})}
}

func (f *FakeSource) Monitors() []protocol.Monitor {
	return []protocol.Monitor{{Index: 0, Label: "Virtual Display", X: 0, Y: 0, Width: f.w, Height: f.h, Primary: true}}
}

func (f *FakeSource) Start(ctx context.Context) (<-chan RawFrame, error) {
	out := make(chan RawFrame, 2)
	go func() {
		defer close(out)
		for {
			f.mu.Lock()
			fps := f.fps
			f.mu.Unlock()
			if fps <= 0 {
				fps = 12
			}
			frame := f.render()
			select {
			case out <- frame:
			case <-ctx.Done():
				return
			case <-f.closed:
				return
			}
			select {
			case <-time.After(time.Second / time.Duration(fps)):
			case <-ctx.Done():
				return
			case <-f.closed:
				return
			}
		}
	}()
	return out, nil
}

// render draws one animated frame into a fresh RGBA buffer.
func (f *FakeSource) render() RawFrame {
	f.mu.Lock()
	w, h := f.w, f.h
	n := f.frame
	f.frame++
	f.mu.Unlock()

	pix := make([]byte, w*h*4)
	// Background: horizontal gradient (static -> most tiles unchanged frame to
	// frame, so delta frames stay tiny).
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			pix[i+0] = byte(x * 255 / w)       // R
			pix[i+1] = byte(y * 255 / h)       // G
			pix[i+2] = 64                      // B
			pix[i+3] = 255                     // A
		}
	}
	// Moving block.
	bw, bh := 80, 60
	bx := (n * 6) % (w - bw)
	by := (h - bh) / 2
	drawRect(pix, w, bx, by, bw, bh, 255, 220, 0)
	// Blinking corner square (changes every frame).
	if n%2 == 0 {
		drawRect(pix, w, w-40, 0, 40, 40, 0, 200, 255)
	} else {
		drawRect(pix, w, w-40, 0, 40, 40, 20, 20, 20)
	}
	return RawFrame{Pix: pix, Width: w, Height: h, MonitorIndex: 0}
}

func drawRect(pix []byte, stride, x, y, w, h int, r, g, b byte) {
	for yy := y; yy < y+h; yy++ {
		for xx := x; xx < x+w; xx++ {
			i := (yy*stride + xx) * 4
			if i < 0 || i+3 >= len(pix) {
				continue
			}
			pix[i+0] = r
			pix[i+1] = g
			pix[i+2] = b
			pix[i+3] = 255
		}
	}
}

func (f *FakeSource) SelectMonitor(int) {}

func (f *FakeSource) SetFPS(fps int) {
	f.mu.Lock()
	if fps > 0 {
		f.fps = fps
	}
	f.mu.Unlock()
}

func (f *FakeSource) Close() error {
	f.once.Do(func() { close(f.closed) })
	return nil
}
