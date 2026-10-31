// Package protocol defines the JSON messages exchanged over the signaling
// WebSocket between the server, the client agents, and the support consoles.
//
// Every frame on the wire is a JSON object with a "type" field. The payload
// fields that matter for a given type are documented on each struct below.
// Unused fields are omitted (omitempty) so the frames stay small.
package protocol

// Message type constants. Keep these in sync with protocol.ts on the frontend.
const (
	// Agent -> server: register this client PC as online.
	TypeHello = "hello"
	// Server -> console: a device came online / went offline / changed.
	TypeDeviceUpdate = "device_update"
	// Server -> console: the full current device list (sent right after login).
	TypeDeviceList = "device_list"

	// Console -> server: request a control session with a device.
	TypeConnect = "connect"
	// Server -> agent: a technician is asking to connect; show Allow/Deny.
	TypeConnectRequest = "connect_request"
	// Agent -> server: the user's answer to the Allow/Deny prompt.
	TypeConsent = "consent"
	// Server -> both sides: the session is authorized; here are the ICE servers.
	TypeSessionStart = "session_start"
	// Either side -> server (relayed to the peer): tear the session down.
	TypeEnd = "end"

	// WebRTC signaling, relayed verbatim between the two peers by sessionId.
	TypeOffer  = "offer"
	TypeAnswer = "answer"
	TypeICE    = "ice"

	// Server -> sender: something went wrong (bad auth, unknown device, etc.).
	TypeError = "error"
	// Either side -> server: keepalive; server ignores the body.
	TypePing = "ping"
	TypePong = "pong"
)

// Role identifies which kind of endpoint a socket belongs to.
type Role string

const (
	RoleAgent   Role = "agent"   // a client PC running the support app
	RoleConsole Role = "console" // an NYK support technician
)

// Monitor describes one display on the client PC, in that PC's virtual-desktop
// coordinate space (pixels). The console uses this to map cursor positions.
type Monitor struct {
	Index  int    `json:"index"`
	Label  string `json:"label,omitempty"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	// Primary marks the main display.
	Primary bool `json:"primary,omitempty"`
}

// Envelope is the common shape of every signaling frame. Specific messages are
// carried in the typed fields; a decoder reads Type first, then the relevant
// field. This keeps one struct on the wire and avoids a second decode pass.
type Envelope struct {
	Type string `json:"type"`

	// Set on messages that belong to an authorized session.
	SessionID string `json:"sessionId,omitempty"`

	// --- hello (agent -> server) ---
	Hello *Hello `json:"hello,omitempty"`

	// --- auth ---
	// OrgKey is the shared build-time secret an agent must present. Techs do
	// not use this; they authenticate over HTTP before opening the socket and
	// present their bearer token in the Authorization header.
	OrgKey string `json:"orgKey,omitempty"`

	// --- device list / updates (server -> console) ---
	Devices []Device `json:"devices,omitempty"`
	Device  *Device  `json:"device,omitempty"`

	// --- connect (console -> server) ---
	DeviceID string `json:"deviceId,omitempty"`

	// --- connect_request (server -> agent) ---
	TechName string `json:"techName,omitempty"`

	// --- consent (agent -> server) ---
	Allowed bool `json:"allowed,omitempty"`

	// --- session_start (server -> both) ---
	ICEServers []ICEServer `json:"iceServers,omitempty"`
	// PeerRole tells the recipient whether it should create the WebRTC offer.
	// The console is the offerer; the agent answers.
	Offerer bool `json:"offerer,omitempty"`

	// --- WebRTC signaling (relayed) ---
	SDP       string `json:"sdp,omitempty"`
	Candidate string `json:"candidate,omitempty"`

	// --- error ---
	Error string `json:"error,omitempty"`

	// --- end ---
	Reason string `json:"reason,omitempty"`
}

// Hello is the agent's registration payload.
type Hello struct {
	DeviceID string    `json:"deviceId"`
	Hostname string    `json:"hostname"`
	Username string    `json:"username"`
	OS       string    `json:"os"`
	Version  string    `json:"version"`
	Monitors []Monitor `json:"monitors"`
}

// Device is the server's view of a client PC, sent to consoles.
type Device struct {
	ID       string    `json:"id"`
	Hostname string    `json:"hostname"`
	Username string    `json:"username"`
	OS       string    `json:"os"`
	Version  string    `json:"version"`
	Nickname string    `json:"nickname,omitempty"`
	Online   bool      `json:"online"`
	LastSeen int64     `json:"lastSeen"` // unix seconds
	Monitors []Monitor `json:"monitors,omitempty"`
}

// ICEServer mirrors the browser RTCIceServer shape so the frontend can hand it
// straight to RTCPeerConnection.
type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// FormatDeviceID turns a raw 9-digit id into the grouped "123 456 789" form
// shown to users. Input shorter than 9 digits is returned unchanged.
func FormatDeviceID(id string) string {
	if len(id) != 9 {
		return id
	}
	return id[0:3] + " " + id[3:6] + " " + id[6:9]
}
