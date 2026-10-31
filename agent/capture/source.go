// Package capture defines the agent's screen source: something that produces a
// stream of raw RGBA frames for a chosen display. Compression into tiles is
// done separately (package encode), so the platform code here stays small and
// free of any codec.
//
// Implementations:
//   - fake: synthesizes animated RGBA frames (dev/CI, any OS, pure Go).
//   - windows: captures the real desktop via a pure-Go screenshot library.
package capture

import (
	"context"

	"github.com/nyk/support-portal/protocol"
)

// RawFrame is one uncompressed frame of a display, in 8-bit RGBA, row-major,
// with stride Width*4.
type RawFrame struct {
	Pix          []byte
	Width        int
	Height       int
	MonitorIndex int
}

// Source produces raw frames from a display.
type Source interface {
	// Monitors lists the displays available to capture.
	Monitors() []protocol.Monitor

	// Start produces frames on the returned channel until ctx is cancelled or
	// Close is called. The channel is closed when production stops. A frame is
	// emitted at most SetFPS times per second; a source may skip emitting when
	// nothing changed to save CPU, but is not required to.
	Start(ctx context.Context) (<-chan RawFrame, error)

	// SelectMonitor switches which display is captured (by Monitor.Index).
	SelectMonitor(index int)

	// SetFPS caps the capture rate.
	SetFPS(fps int)

	// Close releases resources.
	Close() error
}
