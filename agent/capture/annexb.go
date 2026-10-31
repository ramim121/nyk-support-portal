package capture

// Minimal Annex-B helpers: split an H.264 elementary stream into access units
// using AUD (NAL type 9) delimiters, which the test pattern is encoded with.

// nalUnit is a NAL with its 4-byte start code stripped for type inspection but
// kept in Raw (including a normalized 4-byte start code) for output.
type nalUnit struct {
	Type byte
	Raw  []byte // includes leading 00 00 00 01 start code
}

var startCode4 = []byte{0, 0, 0, 1}

// splitNALs scans Annex-B bytes and returns NAL units, each re-prefixed with a
// 4-byte start code.
func splitNALs(data []byte) []nalUnit {
	var out []nalUnit
	n := len(data)
	// Find all start-code offsets (3- or 4-byte).
	type pos struct{ at, scLen int }
	var starts []pos
	i := 0
	for i+3 <= n {
		if data[i] == 0 && data[i+1] == 0 && data[i+2] == 1 {
			scLen := 3
			if i > 0 && data[i-1] == 0 {
				// treat as 4-byte start code
				scLen = 4
				starts = append(starts, pos{at: i - 1, scLen: 4})
				i += 3
				continue
			}
			starts = append(starts, pos{at: i, scLen: scLen})
			i += 3
			continue
		}
		i++
	}
	for idx, s := range starts {
		payloadStart := s.at + s.scLen
		end := n
		if idx+1 < len(starts) {
			end = starts[idx+1].at
		}
		if payloadStart >= end {
			continue
		}
		nalType := data[payloadStart] & 0x1f
		raw := make([]byte, 0, 4+(end-payloadStart))
		raw = append(raw, startCode4...)
		raw = append(raw, data[payloadStart:end]...)
		out = append(out, nalUnit{Type: nalType, Raw: raw})
	}
	return out
}

// accessUnit groups NAL units into access units delimited by AUD (type 9).
// Each returned AU is a single Annex-B byte slice; keyframe is true if it
// contains an IDR slice (type 5).
type accessUnit struct {
	Data     []byte
	Keyframe bool
}

func groupAccessUnits(data []byte) []accessUnit {
	nals := splitNALs(data)
	var units []accessUnit
	var cur []byte
	var curKey bool
	flush := func() {
		if len(cur) > 0 {
			units = append(units, accessUnit{Data: cur, Keyframe: curKey})
			cur = nil
			curKey = false
		}
	}
	for _, nal := range nals {
		if nal.Type == 9 { // AUD starts a new access unit
			flush()
		}
		if nal.Type == 5 {
			curKey = true
		}
		cur = append(cur, nal.Raw...)
	}
	flush()
	return units
}
