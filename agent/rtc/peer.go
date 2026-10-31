// Package rtc wraps the agent's side of a WebRTC session. The agent is the
// answerer: it accepts the console's offer, then over the data channels the
// console created it (a) sends the screen as compressed tiles on "screen",
// (b) receives input on "input", and (c) exchanges small messages on "control".
// There is no WebRTC media track — the screen travels as tiles, which keeps the
// client pure Go.
package rtc

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/nyk/support-portal/agent/capture"
	"github.com/nyk/support-portal/agent/encode"
	"github.com/nyk/support-portal/agent/input"
	"github.com/nyk/support-portal/protocol"
)

// maxScreenBuffer caps the "screen" channel's outgoing buffer. When the network
// can't keep up, we drop frames rather than let latency grow unbounded.
const maxScreenBuffer = 2 * 1024 * 1024 // 2 MB

// Callbacks surfaces events the signaling layer forwards or reacts to.
type Callbacks struct {
	SendICE     func(candidateJSON string)
	SendAnswer  func(sdp string)
	OnClosed    func(reason string)
	OnClipboard func(text string)
}

// Peer is one agent-side WebRTC session.
type Peer struct {
	pc     *webrtc.PeerConnection
	source capture.Source
	enc    *encode.TileEncoder
	sink   input.Sink
	cb     Callbacks

	mu         sync.Mutex
	monitors   []protocol.Monitor
	curMonitor protocol.Monitor
	control    *webrtc.DataChannel
	screen     *webrtc.DataChannel
	fps        int
	started    bool

	ctx    context.Context
	cancel context.CancelFunc
	closed sync.Once
}

func NewPeer(iceServers []protocol.ICEServer, source capture.Source, sink input.Sink, cb Callbacks) (*Peer, error) {
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{ICEServers: toPionICE(iceServers)})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	mons := source.Monitors()
	p := &Peer{
		pc: pc, source: source, enc: encode.NewTileEncoder(), sink: sink, cb: cb,
		monitors: mons, fps: 15, ctx: ctx, cancel: cancel,
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
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed, webrtc.PeerConnectionStateDisconnected:
			p.Close("connection " + s.String())
		}
	})
	pc.OnDataChannel(p.onDataChannel)
	return p, nil
}

func (p *Peer) HandleOffer(sdp string) error {
	if err := p.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdp}); err != nil {
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
	case "screen":
		p.mu.Lock()
		p.screen = dc
		p.mu.Unlock()
		dc.OnOpen(func() { p.startScreen() })
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
		p.enc.ForceKeyframe()
	case protocol.CtrlSetQuality:
		p.applyQuality(c.Quality)
	case protocol.CtrlRequestKeyframe:
		p.enc.ForceKeyframe()
	case protocol.CtrlClipboardText:
		if p.cb.OnClipboard != nil {
			p.cb.OnClipboard(c.Text)
		}
	}
}

func (p *Peer) applyQuality(name string) {
	q, fps := 55, 15
	switch name {
	case "low":
		q, fps = 35, 10
	case "med":
		q, fps = 55, 20
	case "high":
		q, fps = 75, 30
	}
	p.enc.SetQuality(q)
	p.source.SetFPS(fps)
	p.mu.Lock()
	p.fps = fps
	p.mu.Unlock()
	p.enc.ForceKeyframe()
}

// startScreen begins capturing and sending tiles. Runs once per peer.
func (p *Peer) startScreen() {
	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		return
	}
	p.started = true
	p.mu.Unlock()

	frames, err := p.source.Start(p.ctx)
	if err != nil {
		log.Printf("rtc: source start: %v", err)
		p.Close("source error")
		return
	}
	go func() {
		var sentFrames int
		statsTick := time.NewTicker(time.Second)
		defer statsTick.Stop()
		for {
			select {
			case <-p.ctx.Done():
				return
			case <-statsTick.C:
				p.sendStats(sentFrames)
				sentFrames = 0
			case fr, ok := <-frames:
				if !ok {
					return
				}
				p.mu.Lock()
				dc := p.screen
				p.mu.Unlock()
				if dc == nil || dc.ReadyState() != webrtc.DataChannelStateOpen {
					continue
				}
				// Backpressure: if the send buffer is deep, drop this frame.
				if dc.BufferedAmount() > maxScreenBuffer {
					continue
				}
				msgs := p.enc.Encode(fr.Pix, fr.Width, fr.Height, fr.MonitorIndex)
				for _, m := range msgs {
					if err := dc.Send(m); err != nil {
						log.Printf("rtc: screen send: %v", err)
						return
					}
				}
				if len(msgs) > 0 {
					sentFrames++
				}
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
