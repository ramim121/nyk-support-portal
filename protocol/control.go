package protocol

// Control messages travel as JSON over a separate "control" DataChannel (not
// the high-frequency binary "input" channel). These are low-rate and benefit
// from being self-describing.

const (
	CtrlSelectMonitor  = "selectMonitor"  // console -> agent: switch captured display
	CtrlSetQuality     = "setQuality"     // console -> agent: low | med | high
	CtrlClipboardText  = "clipboardText"  // both ways: sync clipboard text
	CtrlRequestKeyframe = "requestKeyframe" // console -> agent: force an IDR frame
	CtrlCtrlAltDel     = "ctrlAltDel"     // console -> agent: (v1: unsupported notice)
	CtrlCursor         = "cursor"         // agent -> console: cursor shape/pos hint
	CtrlStats          = "stats"          // agent -> console: fps/bitrate for the HUD
)

// Quality presets cap the encoder. Values are starting points; the encoder may
// adapt downward under loss.
type QualityPreset struct {
	Name    string `json:"name"`
	FPS     int    `json:"fps"`
	Bitrate int    `json:"bitrate"` // bits per second
}

var QualityPresets = map[string]QualityPreset{
	"low":  {Name: "low", FPS: 10, Bitrate: 600_000},
	"med":  {Name: "med", FPS: 20, Bitrate: 1_500_000},
	"high": {Name: "high", FPS: 30, Bitrate: 4_000_000},
}

// Control is the JSON shape on the control channel. Only the fields relevant to
// Kind are set.
type Control struct {
	Kind string `json:"kind"`

	Monitor int    `json:"monitor,omitempty"` // CtrlSelectMonitor
	Quality string `json:"quality,omitempty"` // CtrlSetQuality
	Text    string `json:"text,omitempty"`    // CtrlClipboardText

	// CtrlStats
	FPS     int `json:"fps,omitempty"`
	Bitrate int `json:"bitrate,omitempty"`
}
