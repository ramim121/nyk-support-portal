// Package rtc wraps the agent's side of a WebRTC session. The agent is the
// *answerer*: it receives the console's offer, attaches an outbound H.264 video
// track fed by a capture.Source, and handles the inbound "input" and "control"
// data channels the console created.
package rtc

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"

	"github.com/nyk/support-portal/agent/capture"
	"github.com/nyk/support-portal/agent/input"
	"github.com/nyk/support-portal/protocol"
)

// Callbacks surfaces events the signaling layer must forward or react to.
type Callbacks struct {
	// SendICE forwards a locally gathered ICE candidate (JSON) to the peer.
	SendICE func(candidateJSON string)
	// SendAnswer forwards the local SDP answer to the peer.
	SendAnswer func(sdp string)
	// OnClosed fires once when the session ends for any reason.
	OnClosed func(reason string)
	// OnClipboard fires when the console sends clipboard text to apply locally.
	OnClipboard func(text string)
}

// Peer is one agent-side WebRTC session.
type Peer struct {
	pc     *webrtc.PeerConnection
	track  *webrtc.TrackLocalStaticSample
	source capture.Source
	sink   input.Sink
	cb     Callbacks

	mu         sync.Mutex
	monitors   []protocol.Monitor
	curMonitor protocol.Monitor

	ctx          context.Context
	cancel       context.CancelFunc
	closed       sync.Once
	control      *webrtc.DataChannel
	videoStarted bool
}

// NewPeer creates the PeerConnection and video track. source frames start
// flowing once the offer is answered and the connection is established.
func NewPeer(iceServers []protocol.ICEServer, source capture.Source, sink input.Sink, cb Callbacks) (*Peer, error) {
	cfg := webrtc.Configuration{ICEServers: toPionICE(iceServers)}
	pc, err := webrtc.NewPeerConnection(cfg)
	if err != nil {
		return nil, err
	}
	track, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264}, "video", "nyk-screen")
	if err != nil {
		_ = pc.Close()
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	mons := source.Monitors()
	p := &Peer{
		pc: pc, track: track, source: source, sink: sink, cb: cb,
		monitors: mons, ctx: ctx, cancel: cancel,
	}
	if len(mons) > 0 {
		p.curMonitor = mons[0]
		for _, m := range mons {
			if m.Primary {
				p.curMonitor = m
			}
		}
	}

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		b, _ := json.Marshal(c.ToJSON())
		cb.SendICE(string(b))
	})
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		log.Printf("rtc: connection state %s", s)
		switch s {
		case webrtc.PeerConnectionStateConnected:
			p.startVideo()
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed, webrtc.PeerConnectionStateDisconnected:
			p.Close("connection " + s.String())
		}
	})
	pc.OnDataChannel(p.onDataChannel)

	return p, nil
}

// HandleOffer applies the remote offer, attaches the video track, and produces
// the answer (sent via Callbacks.SendAnswer).
func (p *Peer) HandleOffer(sdp string) error {
	if err := p.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdp}); err != nil {
		return err
	}
	// Attach the outbound track after SetRemoteDescription so pion binds it to
	// the video transceiver the console offered.
	if _, err := p.pc.AddTrack(p.track); err != nil {
		return err
	}
	answer, err := p.pc.CreateAnswer(nil)
	if err != nil {
		return err
	}
	if err := p.pc.SetLocalDescription(answer); err != nil {
		return err
	}
	p.cb.SendAnswer(answer.SDP)
	return nil
}

// AddICECandidate feeds a remote ICE candidate (JSON) into the connection.
func (p *Peer) AddICECandidate(candidateJSON string) error {
	var init webrtc.ICECandidateInit
	if err := json.Unmarshal([]byte(candidateJSON), &init); err != nil {
		return err
	}
	return p.pc.AddICECandidate(init)
}

func (p *Peer) onDataChannel(dc *webrtc.DataChannel) {
	switch dc.Label() {
	case "input":
		dc.OnMessage(func(msg webrtc.DataChannelMessage) {
			ev, err := protocol.DecodeInput(msg.Data)
			if err != nil {
				return
			}
			p.mu.Lock()
			mon := p.curMonitor
			p.mu.Unlock()
			p.sink.Apply(ev, mon)
		})
	case "control":
		p.mu.Lock()
		p.control = dc
		p.mu.Unlock()
		dc.OnMessage(func(msg webrtc.DataChannelMessage) { p.onControl(msg.Data) })
	}
}

func (p *Peer) onControl(data []byte) {
	var c protocol.Control
	if err := json.Unmarshal(data, &c); err != nil {
		return
	}
	switch c.Kind {
	case protocol.CtrlSelectMonitor:
		p.mu.Lock()
		for _, m := range p.monitors {
			if m.Index == c.Monitor {
				p.curMonitor = m
			}
		}
		p.mu.Unlock()
		p.source.SelectMonitor(c.Monitor)
		p.source.RequestKeyframe()
	case protocol.CtrlSetQuality:
		if preset, ok := protocol.QualityPresets[c.Quality]; ok {
			p.source.SetQuality(preset)
		}
	case protocol.CtrlRequestKeyframe:
		p.source.RequestKeyframe()
	case protocol.CtrlClipboardText:
		if p.cb.OnClipboard != nil {
			p.cb.OnClipboard(c.Text)
		}
	}
}

// startVideo begins pumping encoded frames from the source into the track. It
// is guarded so it only runs once per peer.
func (p *Peer) startVideo() {
	p.mu.Lock()
	if p.videoStarted {
		p.mu.Unlock()
		return
	}
	p.videoStarted = true
	p.mu.Unlock()

	frames, err := p.source.Start(p.ctx)
	if err != nil {
		log.Printf("rtc: source start: %v", err)
		p.Close("source error")
		return
	}
	go func() {
		var count int
		statsTick := time.NewTicker(time.Second)
		defer statsTick.Stop()
		for {
			select {
			case <-p.ctx.Done():
				return
			case fr, ok := <-frames:
				if !ok {
					return
				}
				if err := p.track.WriteSample(media.Sample{Data: fr.Data, Duration: fr.Duration}); err != nil {
					log.Printf("rtc: write sample: %v", err)
					return
				}
				count++
			case <-statsTick.C:
				p.sendStats(count)
				count = 0
			}
		}
	}()
}

func (p *Peer) sendStats(fps int) {
	p.mu.Lock()
	dc := p.control
	p.mu.Unlock()
	if dc == nil || dc.ReadyState() != webrtc.DataChannelStateOpen {
		return
	}
	b, _ := json.Marshal(protocol.Control{Kind: protocol.CtrlStats, FPS: fps})
	_ = dc.SendText(string(b))
}

// SendClipboard pushes local clipboard text to the console.
func (p *Peer) SendClipboard(text string) {
	p.mu.Lock()
	dc := p.control
	p.mu.Unlock()
	if dc == nil || dc.ReadyState() != webrtc.DataChannelStateOpen {
		return
	}
	b, _ := json.Marshal(protocol.Control{Kind: protocol.CtrlClipboardText, Text: text})
	_ = dc.SendText(string(b))
}

// Close tears down the session exactly once.
func (p *Peer) Close(reason string) {
	p.closed.Do(func() {
		p.cancel()
		_ = p.source.Close()
		_ = p.pc.Close()
		if p.cb.OnClosed != nil {
			p.cb.OnClosed(reason)
		}
	})
}

func toPionICE(in []protocol.ICEServer) []webrtc.ICEServer {
	out := make([]webrtc.ICEServer, 0, len(in))
	for _, s := range in {
		ice := webrtc.ICEServer{URLs: s.URLs}
		if s.Username != "" {
			ice.Username = s.Username
			ice.Credential = s.Credential
		}
		out = append(out, ice)
	}
	return out
}
