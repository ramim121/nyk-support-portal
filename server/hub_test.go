package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/nyk/support-portal/protocol"
)

func testHub(t *testing.T) *Hub {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return NewHub(store, turnConfig{}) // STUN-only ICE
}

// newTestConn makes a conn whose writes land in its send channel (no socket).
func newTestConn(role protocol.Role) *conn {
	return &conn{role: role, send: make(chan protocol.Envelope, 32), done: make(chan struct{})}
}

func recv(t *testing.T, c *conn, want string) protocol.Envelope {
	t.Helper()
	select {
	case env := <-c.send:
		if env.Type != want {
			t.Fatalf("got %q, want %q (%+v)", env.Type, want, env)
		}
		return env
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %q", want)
		return protocol.Envelope{}
	}
}

func drain(c *conn) {
	for {
		select {
		case <-c.send:
		default:
			return
		}
	}
}

func onlineAgent(h *Hub, id string) *conn {
	a := newTestConn(protocol.RoleAgent)
	a.deviceID = id
	a.hello = &protocol.Hello{DeviceID: id, Hostname: "PC-" + id, Username: "user", OS: "windows"}
	h.addAgent(a)
	return a
}

func TestConsentAllowFlow(t *testing.T) {
	h := testHub(t)
	agent := onlineAgent(h, "100200300")

	console := newTestConn(protocol.RoleConsole)
	console.techName = "alice"
	h.addConsole(console)
	recv(t, console, protocol.TypeDeviceList)

	// Console asks to connect.
	h.handle(console, protocol.Envelope{Type: protocol.TypeConnect, DeviceID: "100200300"})
	req := recv(t, agent, protocol.TypeConnectRequest)
	if req.TechName != "alice" || req.SessionID == "" {
		t.Fatalf("bad connect_request: %+v", req)
	}

	// Agent allows.
	h.handle(agent, protocol.Envelope{Type: protocol.TypeConsent, SessionID: req.SessionID, Allowed: true})
	cs := recv(t, console, protocol.TypeSessionStart)
	as := recv(t, agent, protocol.TypeSessionStart)
	if !cs.Offerer {
		t.Error("console should be the offerer")
	}
	if as.Offerer {
		t.Error("agent should not be the offerer")
	}
	if len(cs.ICEServers) == 0 {
		t.Error("expected ICE servers")
	}

	// Relay an offer console -> agent.
	h.handle(console, protocol.Envelope{Type: protocol.TypeOffer, SessionID: req.SessionID, SDP: "v=0"})
	off := recv(t, agent, protocol.TypeOffer)
	if off.SDP != "v=0" {
		t.Errorf("offer not relayed: %+v", off)
	}

	// End from console.
	h.handle(console, protocol.Envelope{Type: protocol.TypeEnd, SessionID: req.SessionID})
	recv(t, agent, protocol.TypeEnd)
	recv(t, console, protocol.TypeEnd)
}

func TestConsentDenyFlow(t *testing.T) {
	h := testHub(t)
	agent := onlineAgent(h, "111222333")
	console := newTestConn(protocol.RoleConsole)
	console.techName = "bob"
	h.addConsole(console)
	recv(t, console, protocol.TypeDeviceList)

	h.handle(console, protocol.Envelope{Type: protocol.TypeConnect, DeviceID: "111222333"})
	req := recv(t, agent, protocol.TypeConnectRequest)

	h.handle(agent, protocol.Envelope{Type: protocol.TypeConsent, SessionID: req.SessionID, Allowed: false})
	errEnv := recv(t, console, protocol.TypeError)
	if errEnv.Error == "" {
		t.Error("expected a decline error to the console")
	}
}

func TestConnectOfflineDevice(t *testing.T) {
	h := testHub(t)
	console := newTestConn(protocol.RoleConsole)
	console.techName = "carol"
	h.addConsole(console)
	recv(t, console, protocol.TypeDeviceList)

	h.handle(console, protocol.Envelope{Type: protocol.TypeConnect, DeviceID: "999999999"})
	errEnv := recv(t, console, protocol.TypeError)
	if errEnv.Error != "device offline" {
		t.Errorf("got %q", errEnv.Error)
	}
}

func TestAgentOnlineOfflineBroadcast(t *testing.T) {
	h := testHub(t)
	console := newTestConn(protocol.RoleConsole)
	console.techName = "dave"
	h.addConsole(console)
	recv(t, console, protocol.TypeDeviceList)

	agent := onlineAgent(h, "123123123")
	up := recv(t, console, protocol.TypeDeviceUpdate)
	if up.Device == nil || !up.Device.Online || up.Device.ID != "123123123" {
		t.Fatalf("expected online update: %+v", up)
	}

	h.removeConn(agent)
	down := recv(t, console, protocol.TypeDeviceUpdate)
	if down.Device == nil || down.Device.Online {
		t.Fatalf("expected offline update: %+v", down)
	}
}

func TestConsentTimeout(t *testing.T) {
	h := testHub(t)
	agent := onlineAgent(h, "555555555")
	console := newTestConn(protocol.RoleConsole)
	console.techName = "erin"
	h.addConsole(console)
	recv(t, console, protocol.TypeDeviceList)

	// Shorten the timeout by driving startConnect then firing the timer path
	// manually: we assert the session exists as pending, then simulate expiry.
	h.handle(console, protocol.Envelope{Type: protocol.TypeConnect, DeviceID: "555555555"})
	req := recv(t, agent, protocol.TypeConnectRequest)

	h.mu.Lock()
	s := h.sessions[req.SessionID]
	h.mu.Unlock()
	if s == nil || s.active {
		t.Fatal("expected a pending session")
	}
	// Fire the timeout immediately instead of waiting 30s.
	s.consentTick.Stop()
	h.endSession(req.SessionID, "consent_timeout")
	drain(agent)
	// endSession notifies remaining peers; console should receive an end.
	recv(t, console, protocol.TypeEnd)
}
