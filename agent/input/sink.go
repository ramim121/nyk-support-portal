// Package input defines the agent's input abstraction: something that applies
// decoded mouse/keyboard events to the local machine.
//
// Implementations:
//   - LogSink: prints events (dev/CI; proves decoding and coordinate mapping).
//   - windows: injects real input via a maintained library (built separately).
//
// Coordinates arrive normalized to 0..65535 over the monitor currently being
// controlled; Apply is given that monitor's pixel rectangle so it can scale to
// absolute desktop pixels.
package input

import "github.com/nyk/support-portal/protocol"

// Sink applies input events to the host.
type Sink interface {
	Apply(ev protocol.InputEvent, mon protocol.Monitor)
	Close() error
}

// AbsPixel converts a normalized coordinate pair into absolute desktop pixels
// within mon. Shared by every Sink implementation so the mapping is identical
// in dev and production.
func AbsPixel(x, y uint16, mon protocol.Monitor) (int, int) {
	px := mon.X + int(float64(x)/65535.0*float64(mon.Width))
	py := mon.Y + int(float64(y)/65535.0*float64(mon.Height))
	return px, py
}
