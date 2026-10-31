// Package signaling is the agent's client for the server's signaling socket. It
// registers the device, handles consent prompts, and builds a WebRTC peer for
// each authorized session. It reconnects automatically with backoff.
package signaling

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/nyk/support-portal/agent/capture"
	"github.com/nyk/support-portal/agent/input"
	"github.com/nyk/support-portal/agent/rtc"
	"github.com/nyk/support-portal/protocol"
)

// Deps are everything the client needs, injected by main so this package has no
// knowledge of how consent is obtained or how input is applied.
type Deps struct {
	ServerURL string         // e.g. wss://support.example.com  (no path)
	OrgKey    string         // shared build-time secret
	Hello     protocol.Hello // this device's registration details

	// Consent is called for each incoming connect request. It may block (e.g.
	// to show a dialog). It must return within ~30s or the server times out.
	Consent func(sessionID, techName string) bool

	// NewSource creates a fresh capture source per session.
	NewSource func() capture.Source
	// Sink applies decoded input (shared across sessions).
	Sink input.Sink

	// UI hooks (optional). Called on the agent to show/hide the banner.
	OnSessionStart func(techName string)
	OnSessionEnd   func()
	// OnClipboardFromConsole applies clipboard text pushed by the technician.
	OnClipboardFromConsole func(text string)
}

// Client maintains the connection to the server.
type Client struct {
	deps Deps

	mu       sync.Mutex
	ws       *websocket.Conn
	sendCh   chan protocol.Envelope
	peer     *rtc.Peer
	session  string
	techName string
}

func New(deps Deps) *Client { return &Client{deps: deps} }

// Run connects and keeps the connection alive until ctx is cancelled.
func (c *Client) Run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		if err := c.connectOnce(ctx); err != nil {
			log.Printf("signaling: %v (retry in %s)", err, backoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > 15*time.Second {
			backoff = 15 * time.Second
		}
	}
}

func (c *Client) connectOnce(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(dialCtx, c.deps.ServerURL+"/ws/agent", nil)
	if err != nil {
		return err
	}
	defer ws.Close(websocket.StatusNormalClosure, "")

	connCtx, connCancel := context.WithCancel(ctx)
	defer connCancel()

	c.mu.Lock()
	c.ws = ws
	c.sendCh = make(chan protocol.Envelope, 32)
	c.mu.Unlock()

	go c.writeLoop(connCtx, ws)

	// Register.
	c.send(protocol.Envelope{Type: protocol.TypeHello, OrgKey: c.deps.OrgKey, Hello: &c.deps.Hello})
	log.Printf("signaling: connected, registered device %s", protocol.FormatDeviceID(c.deps.Hello.DeviceID))

	// Keepalive pings.
	go func() {
		t := time.NewTicker(25 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-connCtx.Done():
				return
			case <-t.C:
				c.send(protocol.Envelope{Type: protocol.TypePing})
			}
		}
	}()

	for {
		var env protocol.Envelope
		if err := wsjson.Read(connCtx, ws, &env); err != nil {
			c.teardownSession("disconnected")
			return err
		}
		c.handle(connCtx, env)
	}
}

func (c *Client) writeLoop(ctx context.Context, ws *websocket.Conn) {
	c.mu.Lock()
	ch := c.sendCh
	c.mu.Unlock()
	for {
		select {
		case <-ctx.Done():
			return
		case env := <-ch:
			wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := wsjson.Write(wctx, ws, env)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

func (c *Client) send(env protocol.Envelope) {
	c.mu.Lock()
	ch := c.sendCh
	c.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- env:
	default:
		log.Printf("signaling: send buffer full, dropping %s", env.Type)
	}
}

func (c *Client) handle(ctx context.Context, env protocol.Envelope) {
	switch env.Type {
	case protocol.TypePong:
		// ignore

	case protocol.TypeConnectRequest:
		go c.handleConnectRequest(env.SessionID, env.TechName)

	case protocol.TypeSessionStart:
		if !env.Offerer { // agent is the answerer
			c.startPeer(env)
		}

	case protocol.TypeOffer:
		c.withPeer(env.SessionID, func(p *rtc.Peer) {
			if err := p.HandleOffer(env.SDP); err != nil {
				log.Printf("rtc: handle offer: %v", err)
			}
		})

	case protocol.TypeICE:
		c.withPeer(env.SessionID, func(p *rtc.Peer) {
			if err := p.AddICECandidate(env.Candidate); err != nil {
				log.Printf("rtc: add ice: %v", err)
			}
		})

	case protocol.TypeEnd:
		c.teardownSession(env.Reason)

	case protocol.TypeError:
		log.Printf("signaling: server error: %s", env.Error)
	}
}

func (c *Client) handleConnectRequest(sessionID, techName string) {
	// Refuse a second controller while a session is active.
	c.mu.Lock()
	busy := c.peer != nil
	c.mu.Unlock()
	if busy {
		c.send(protocol.Envelope{Type: protocol.TypeConsent, SessionID: sessionID, Allowed: false})
		return
	}

	allowed := c.deps.Consent(sessionID, techName)
	c.send(protocol.Envelope{Type: protocol.TypeConsent, SessionID: sessionID, Allowed: allowed})
}

func (c *Client) startPeer(env protocol.Envelope) {
	source := c.deps.NewSource()
	sessionID := env.SessionID
	techName := env.TechName

	peer, err := rtc.NewPeer(env.ICEServers, source, c.deps.Sink, rtc.Callbacks{
		SendICE: func(cand string) {
			c.send(protocol.Envelope{Type: protocol.TypeICE, SessionID: sessionID, Candidate: cand})
		},
		SendAnswer: func(sdp string) {
			c.send(protocol.Envelope{Type: protocol.TypeAnswer, SessionID: sessionID, SDP: sdp})
		},
		OnClosed: func(reason string) {
			c.teardownSession(reason)
		},
		OnClipboard: func(text string) {
			if c.deps.OnClipboardFromConsole != nil {
				c.deps.OnClipboardFromConsole(text)
			}
		},
	})
	if err != nil {
		log.Printf("rtc: new peer: %v", err)
		_ = source.Close()
		c.send(protocol.Envelope{Type: protocol.TypeEnd, SessionID: sessionID, Reason: "peer error"})
		return
	}

	c.mu.Lock()
	c.peer = peer
	c.session = sessionID
	c.techName = techName
	c.mu.Unlock()

	if c.deps.OnSessionStart != nil {
		c.deps.OnSessionStart(techName)
	}
	log.Printf("session %s started (tech=%s)", sessionID, techName)
}

func (c *Client) withPeer(sessionID string, fn func(*rtc.Peer)) {
	c.mu.Lock()
	p := c.peer
	match := c.session == sessionID
	c.mu.Unlock()
	if p != nil && match {
		fn(p)
	}
}

// teardownSession closes the active peer (if any) and notifies the UI.
func (c *Client) teardownSession(reason string) {
	c.mu.Lock()
	p := c.peer
	sid := c.session
	c.peer = nil
	c.session = ""
	c.techName = ""
	c.mu.Unlock()

	if p == nil {
		return
	}
	// Tell the server the session ended (best effort).
	c.send(protocol.Envelope{Type: protocol.TypeEnd, SessionID: sid, Reason: reason})
	p.Close(reason)
	if c.deps.OnSessionEnd != nil {
		c.deps.OnSessionEnd()
	}
	log.Printf("session %s ended: %s", sid, reason)
}

// Disconnect ends any active session (used by the agent's Disconnect button).
func (c *Client) Disconnect() { c.teardownSession("ended by client") }
