package main

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/nyk/support-portal/protocol"
)

// conn wraps one WebSocket endpoint (an agent or a console). All writes go
// through the send channel so a single goroutine owns the socket writer, which
// the coder/websocket library requires.
type conn struct {
	ws   *websocket.Conn
	role protocol.Role
	send chan protocol.Envelope

	// For agents: the device id and its latest hello. For consoles: the
	// technician's username.
	deviceID string
	hello    *protocol.Hello
	techName string

	closeOnce sync.Once
	done      chan struct{}
}

func newConn(ws *websocket.Conn, role protocol.Role) *conn {
	return &conn{
		ws:   ws,
		role: role,
		send: make(chan protocol.Envelope, 32),
		done: make(chan struct{}),
	}
}

// enqueue tries to send a frame without blocking the hub. If the peer's buffer
// is full it is dropped and the connection closed (a stuck peer must not stall
// the hub).
func (c *conn) enqueue(env protocol.Envelope) {
	select {
	case c.send <- env:
	case <-c.done:
	default:
		c.close()
	}
}

func (c *conn) close() {
	c.closeOnce.Do(func() { close(c.done) })
}

// session is one authorized control session between a console and an agent.
type session struct {
	id          string
	deviceID    string
	techName    string
	agent       *conn
	console     *conn
	active      bool // flipped true once consent granted
	createdAt   time.Time
	consentTick *time.Timer
}

// Hub is the central registry and message router. One Hub exists per server.
type Hub struct {
	mu       sync.Mutex
	agents   map[string]*conn    // deviceID -> agent conn
	consoles map[*conn]struct{}  // set of console conns
	sessions map[string]*session // sessionID -> session

	store *Store
	turn  turnConfig
}

func NewHub(store *Store, turn turnConfig) *Hub {
	return &Hub{
		agents:   make(map[string]*conn),
		consoles: make(map[*conn]struct{}),
		sessions: make(map[string]*session),
		store:    store,
		turn:     turn,
	}
}

// --- registration ---

func (h *Hub) addAgent(c *conn) {
	h.mu.Lock()
	// If the same device reconnects, drop the stale socket.
	if old, ok := h.agents[c.deviceID]; ok && old != c {
		old.close()
	}
	h.agents[c.deviceID] = c
	dev := h.deviceFromHello(c.hello)
	h.mu.Unlock()

	_ = h.store.UpsertDevice(dev)
	h.store.Audit("agent_online", "", c.deviceID, "", c.hello.Hostname)
	h.broadcastDevice(dev, true)
}

func (h *Hub) addConsole(c *conn) {
	h.mu.Lock()
	h.consoles[c] = struct{}{}
	h.mu.Unlock()
	// Send the current device list immediately.
	h.sendDeviceList(c)
}

// removeConn cleans up on disconnect and tears down any sessions the conn was
// part of.
func (h *Hub) removeConn(c *conn) {
	h.mu.Lock()
	var toEnd []*session
	switch c.role {
	case protocol.RoleAgent:
		if h.agents[c.deviceID] == c {
			delete(h.agents, c.deviceID)
		}
	case protocol.RoleConsole:
		delete(h.consoles, c)
	}
	for _, s := range h.sessions {
		if s.agent == c || s.console == c {
			toEnd = append(toEnd, s)
		}
	}
	h.mu.Unlock()

	for _, s := range toEnd {
		h.endSession(s.id, "peer_disconnected")
	}

	if c.role == protocol.RoleAgent && c.deviceID != "" {
		if dev, ok := h.store.GetDevice(c.deviceID); ok {
			h.store.Audit("agent_offline", "", c.deviceID, "", "")
			h.broadcastDevice(dev, false)
		}
	}
}

func (h *Hub) deviceFromHello(hello *protocol.Hello) protocol.Device {
	return protocol.Device{
		ID:       hello.DeviceID,
		Hostname: hello.Hostname,
		Username: hello.Username,
		OS:       hello.OS,
		Version:  hello.Version,
		Monitors: hello.Monitors,
		LastSeen: time.Now().Unix(),
		Online:   true,
	}
}

// --- device list push ---

func (h *Hub) sendDeviceList(c *conn) {
	devices, err := h.store.ListDevices()
	if err != nil {
		log.Printf("list devices: %v", err)
		return
	}
	h.mu.Lock()
	for i := range devices {
		if ac, ok := h.agents[devices[i].ID]; ok {
			devices[i].Online = true
			if ac.hello != nil {
				devices[i].Monitors = ac.hello.Monitors
			}
		}
	}
	h.mu.Unlock()
	c.enqueue(protocol.Envelope{Type: protocol.TypeDeviceList, Devices: devices})
}

func (h *Hub) broadcastDevice(dev protocol.Device, online bool) {
	dev.Online = online
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.consoles {
		c.enqueue(protocol.Envelope{Type: protocol.TypeDeviceUpdate, Device: &dev})
	}
}

// --- message handling ---

// handle dispatches one decoded frame from conn c.
func (h *Hub) handle(c *conn, env protocol.Envelope) {
	switch env.Type {
	case protocol.TypePing:
		c.enqueue(protocol.Envelope{Type: protocol.TypePong})

	case protocol.TypeConnect:
		if c.role != protocol.RoleConsole {
			return
		}
		h.startConnect(c, env.DeviceID)

	case protocol.TypeConsent:
		if c.role != protocol.RoleAgent {
			return
		}
		h.handleConsent(c, env)

	case protocol.TypeOffer, protocol.TypeAnswer, protocol.TypeICE:
		h.relay(c, env)

	case protocol.TypeEnd:
		h.endSession(env.SessionID, "ended_by_"+string(c.role))

	default:
		// Unknown types are ignored.
	}
}

// startConnect is a console asking to control a device. It creates a pending
// session and asks the agent for consent.
func (h *Hub) startConnect(console *conn, deviceID string) {
	h.mu.Lock()
	agent, ok := h.agents[deviceID]
	if !ok {
		h.mu.Unlock()
		console.enqueue(protocol.Envelope{Type: protocol.TypeError, Error: "device offline"})
		return
	}
	sid := randomToken(12)
	s := &session{
		id:        sid,
		deviceID:  deviceID,
		techName:  console.techName,
		agent:     agent,
		console:   console,
		createdAt: time.Now(),
	}
	// Auto-deny if the user doesn't answer within 30s.
	s.consentTick = time.AfterFunc(30*time.Second, func() {
		h.mu.Lock()
		cur, ok := h.sessions[sid]
		pending := ok && !cur.active
		h.mu.Unlock()
		if pending {
			console.enqueue(protocol.Envelope{Type: protocol.TypeError, SessionID: sid, Error: "client did not respond"})
			h.endSession(sid, "consent_timeout")
		}
	})
	h.sessions[sid] = s
	h.mu.Unlock()

	h.store.Audit("connect_request", console.techName, deviceID, sid, "")
	agent.enqueue(protocol.Envelope{
		Type:      protocol.TypeConnectRequest,
		SessionID: sid,
		TechName:  console.techName,
	})
}

// handleConsent processes the agent's Allow/Deny answer.
func (h *Hub) handleConsent(agent *conn, env protocol.Envelope) {
	h.mu.Lock()
	s, ok := h.sessions[env.SessionID]
	if !ok || s.agent != agent {
		h.mu.Unlock()
		return
	}
	if s.consentTick != nil {
		s.consentTick.Stop()
	}
	if !env.Allowed {
		delete(h.sessions, s.id)
		h.mu.Unlock()
		s.console.enqueue(protocol.Envelope{Type: protocol.TypeError, SessionID: s.id, Error: "client declined the session"})
		h.store.Audit("consent_denied", s.techName, s.deviceID, s.id, "")
		return
	}
	s.active = true
	ice := h.turn.iceServers(s.id)
	h.mu.Unlock()

	h.store.Audit("session_start", s.techName, s.deviceID, s.id, "")

	// The console creates the WebRTC offer; the agent answers.
	s.console.enqueue(protocol.Envelope{
		Type: protocol.TypeSessionStart, SessionID: s.id,
		ICEServers: ice, Offerer: true, DeviceID: s.deviceID,
	})
	s.agent.enqueue(protocol.Envelope{
		Type: protocol.TypeSessionStart, SessionID: s.id,
		ICEServers: ice, Offerer: false,
	})
}

// relay forwards WebRTC signaling to the other peer in the session.
func (h *Hub) relay(c *conn, env protocol.Envelope) {
	h.mu.Lock()
	s, ok := h.sessions[env.SessionID]
	if !ok || !s.active {
		h.mu.Unlock()
		return
	}
	var peer *conn
	switch c {
	case s.console:
		peer = s.agent
	case s.agent:
		peer = s.console
	default:
		h.mu.Unlock()
		return
	}
	h.mu.Unlock()
	if peer != nil {
		peer.enqueue(env)
	}
}

// endSession tears a session down and notifies whichever peer is still around.
func (h *Hub) endSession(sessionID, reason string) {
	h.mu.Lock()
	s, ok := h.sessions[sessionID]
	if !ok {
		h.mu.Unlock()
		return
	}
	delete(h.sessions, sessionID)
	if s.consentTick != nil {
		s.consentTick.Stop()
	}
	h.mu.Unlock()

	end := protocol.Envelope{Type: protocol.TypeEnd, SessionID: sessionID, Reason: reason}
	if s.console != nil {
		s.console.enqueue(end)
	}
	if s.agent != nil {
		s.agent.enqueue(end)
	}
	if s.active {
		h.store.Audit("session_end", s.techName, s.deviceID, s.id, reason)
	}
}

// writeLoop serializes all writes to the socket from the send channel.
func (h *Hub) writeLoop(ctx context.Context, c *conn) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case env := <-c.send:
			wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := wsjson.Write(wctx, c.ws, env)
			cancel()
			if err != nil {
				c.close()
				return
			}
		}
	}
}

// marshalControl is a small helper for tests and callers that need the raw JSON
// of a control message.
func marshalControl(c protocol.Control) []byte {
	b, _ := json.Marshal(c)
	return b
}
