//go:build windows

package capture

import (
	"context"
	"fmt"
	"image"
	"sync"
	"time"

	"github.com/kbinani/screenshot"

	"github.com/nyk/support-portal/protocol"
)

// WindowsSource captures the real desktop using kbinani/screenshot, which is
// pure Go (Win32 GDI via syscalls) — no cgo. Each CaptureDisplay call returns an
// *image.RGBA already in RGBA order, which is exactly what the tile encoder
// expects, so no pixel conversion is needed here.
type WindowsSource struct {
	mu     sync.Mutex
	fps    int
	cur    int // display index currently captured
	closed chan struct{}
	once   sync.Once
}

// NewWindows creates a desktop capture source.
func NewWindows() *WindowsSource {
	return &WindowsSource{fps: 15, cur: primaryDisplay(), closed: make(chan struct{})}
}

// primaryDisplay returns the index of the primary display (the one at origin),
// falling back to 0.
func primaryDisplay() int {
	n := screenshot.NumActiveDisplays()
	for i := 0; i < n; i++ {
		b := screenshot.GetDisplayBounds(i)
		if b.Min.X == 0 && b.Min.Y == 0 {
			return i
		}
	}
	return 0
}

func (s *WindowsSource) Monitors() []protocol.Monitor {
	n := screenshot.NumActiveDisplays()
	mons := make([]protocol.Monitor, 0, n)
	for i := 0; i < n; i++ {
		b := screenshot.GetDisplayBounds(i)
		mons = append(mons, protocol.Monitor{
			Index:   i,
			Label:   fmt.Sprintf("Display %d", i+1),
			X:       b.Min.X,
			Y:       b.Min.Y,
			Width:   b.Dx(),
			Height:  b.Dy(),
			Primary: b.Min.X == 0 && b.Min.Y == 0,
		})
	}
	if len(mons) == 0 {
		// No displays reported (headless/service context): report a nominal one.
		mons = append(mons, protocol.Monitor{Index: 0, Label: "Display 1", Width: 1920, Height: 1080, Primary: true})
	}
	return mons
}

func (s *WindowsSource) Start(ctx context.Context) (<-chan RawFrame, error) {
	out := make(chan RawFrame, 2)
	go func() {
		defer close(out)
		for {
			s.mu.Lock()
			fps := s.fps
			idx := s.cur
			s.mu.Unlock()
			if fps <= 0 {
				fps = 15
			}

			frame, ok := s.capture(idx)
			if ok {
				select {
				case out <- frame:
				case <-ctx.Done():
					return
				case <-s.closed:
					return
				}
			}

			select {
			case <-time.After(time.Second / time.Duration(fps)):
			case <-ctx.Done():
				return
			case <-s.closed:
				return
			}
		}
	}()
	return out, nil
}

// capture grabs one frame of display idx. On error (e.g. a transient GDI
// failure, common right after a resolution change) it returns ok=false and the
// loop simply tries again on the next tick.
func (s *WindowsSource) capture(idx int) (RawFrame, bool) {
	if idx < 0 || idx >= screenshot.NumActiveDisplays() {
		idx = primaryDisplay()
	}
	bounds := screenshot.GetDisplayBounds(idx)
	img, err := screenshot.CaptureRect(bounds)
	if err != nil || img == nil {
		return RawFrame{}, false
	}
	// Ensure a tightly packed RGBA buffer (stride == width*4). screenshot
	// returns stride == width*4 already, but normalize defensively.
	w, h := img.Rect.Dx(), img.Rect.Dy()
	pix := img.Pix
	if img.Stride != w*4 {
		pix = packRGBA(img, w, h)
	}
	return RawFrame{Pix: pix, Width: w, Height: h, MonitorIndex: idx}, true
}

func packRGBA(img *image.RGBA, w, h int) []byte {
	out := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		copy(out[y*w*4:(y+1)*w*4], img.Pix[y*img.Stride:y*img.Stride+w*4])
	}
	return out
}

func (s *WindowsSource) SelectMonitor(index int) {
	s.mu.Lock()
	s.cur = index
	s.mu.Unlock()
}

func (s *WindowsSource) SetFPS(fps int) {
	s.mu.Lock()
	if fps > 0 {
		s.fps = fps
	}
	s.mu.Unlock()
}

func (s *WindowsSource) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}
