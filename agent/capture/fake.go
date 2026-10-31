package capture

import (
	"context"
	_ "embed"
	"sync"
	"time"

	"github.com/nyk/support-portal/protocol"
)

// testPattern is a short H.264 clip (640x360, 12 fps, Annex-B with AUDs and
// frequent keyframes) generated with ffmpeg. It is embedded so the dev/CI
// pipeline streams real, decodable video with no capture hardware and no cgo.
//
//go:embed testdata/testpattern.h264
var testPattern []byte

// FakeSource replays the embedded test pattern in a loop. It implements the
// full Source interface; SelectMonitor/SetQuality are accepted but only affect
// the frame cadence (there is one virtual 640x360 display).
type FakeSource struct {
	mu     sync.Mutex
	fps    int
	units  []accessUnit
	closed chan struct{}
	once   sync.Once
}

// NewFake builds the fake source, pre-parsing the clip into access units.
func NewFake() *FakeSource {
	return &FakeSource{
		fps:    12,
		units:  groupAccessUnits(testPattern),
		closed: make(chan struct{}),
	}
}

func (f *FakeSource) Monitors() []protocol.Monitor {
	return []protocol.Monitor{{Index: 0, Label: "Virtual Display", X: 0, Y: 0, Width: 640, Height: 360, Primary: true}}
}

func (f *FakeSource) Start(ctx context.Context) (<-chan Frame, error) {
	out := make(chan Frame, 4)
	go func() {
		defer close(out)
		if len(f.units) == 0 {
			return
		}
		idx := 0
		for {
			f.mu.Lock()
			fps := f.fps
			f.mu.Unlock()
			if fps <= 0 {
				fps = 12
			}
			dur := time.Second / time.Duration(fps)

			u := f.units[idx]
			idx = (idx + 1) % len(f.units)

			frame := Frame{Data: u.Data, Duration: dur, Keyframe: u.Keyframe}
			select {
			case out <- frame:
			case <-ctx.Done():
				return
			case <-f.closed:
				return
			}

			select {
			case <-time.After(dur):
			case <-ctx.Done():
				return
			case <-f.closed:
				return
			}
		}
	}()
	return out, nil
}

func (f *FakeSource) SelectMonitor(int) {}

func (f *FakeSource) SetQuality(p protocol.QualityPreset) {
	f.mu.Lock()
	if p.FPS > 0 {
		f.fps = p.FPS
	}
	f.mu.Unlock()
}

func (f *FakeSource) RequestKeyframe() {}

func (f *FakeSource) Close() error {
	f.once.Do(func() { close(f.closed) })
	return nil
}
