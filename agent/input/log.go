package input

import (
	"log"
	"sync/atomic"

	"github.com/nyk/support-portal/protocol"
)

// LogSink prints each decoded input event with its computed absolute pixel
// position. It is the dev/CI sink: it proves the input path end-to-end
// (DataChannel -> decode -> coordinate mapping) without touching the host.
type LogSink struct {
	count atomic.Int64
}

func NewLogSink() *LogSink { return &LogSink{} }

func (s *LogSink) Apply(ev protocol.InputEvent, mon protocol.Monitor) {
	n := s.count.Add(1)
	switch ev.Op {
	case protocol.OpMouseMove:
		px, py := AbsPixel(ev.X, ev.Y, mon)
		log.Printf("[input %d] move norm=(%d,%d) -> px=(%d,%d)", n, ev.X, ev.Y, px, py)
	case protocol.OpMouseBtn:
		log.Printf("[input %d] button=%d down=%v", n, ev.Button, ev.Down)
	case protocol.OpWheel:
		log.Printf("[input %d] wheel dx=%d dy=%d", n, ev.DX, ev.DY)
	case protocol.OpKey:
		log.Printf("[input %d] key scancode=0x%02x extended=%v down=%v", n, ev.Scancode, ev.Extended, ev.Down)
	}
}

// Count returns how many events have been applied (used by tests).
func (s *LogSink) Count() int64 { return s.count.Load() }

func (s *LogSink) Close() error { return nil }
