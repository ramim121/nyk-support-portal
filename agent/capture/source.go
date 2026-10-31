// Package capture defines the agent's video source abstraction: something that
// produces a stream of H.264 access units (Annex-B, with start codes) to be
// sent over the WebRTC video track.
//
// Two implementations exist:
//   - fake: replays an embedded test pattern (dev/CI, any OS, no cgo).
//   - windows: real desktop capture + H.264 encode (built separately).
//
// Keeping both behind this interface means the rest of the agent — signaling,
// the peer connection, input handling — is identical in dev and in production.
package capture

import (
	"context"
	"time"

	"github.com/nyk/support-portal/protocol"
)

// Frame is one encoded H.264 access unit.
type Frame struct {
	Data     []byte        // Annex-B access unit (may include SPS/PPS/AUD)
	Duration time.Duration // display duration of this frame
	Keyframe bool          // true if this AU contains an IDR slice
}

// Source produces encoded video frames from some display.
type Source interface {
	// Monitors lists the displays available to capture.
	Monitors() []protocol.Monitor

	// Start begins producing frames on the returned channel until ctx is
	// cancelled or Close is called. The channel is closed when production ends.
	Start(ctx context.Context) (<-chan Frame, error)

	// SelectMonitor switches which display is captured (by Monitor.Index).
	SelectMonitor(index int)

	// SetQuality adjusts the target frame rate / bitrate.
	SetQuality(preset protocol.QualityPreset)

	// RequestKeyframe asks the encoder to emit an IDR as soon as possible,
	// e.g. in response to a viewer's PLI / packet loss.
	RequestKeyframe()

	// Close releases resources.
	Close() error
}
