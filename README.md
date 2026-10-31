# NYK Support Portal

A lightweight, self-hosted remote-support tool for the NYK support desk, in the
spirit of AnyDesk / UltraViewer but minimal and under your own control.

**Attended support only.** The customer runs a small Windows app, their PC
appears in the support console's live list, and **every session requires the
customer to click _Allow_**. While a session is live, the customer sees a
persistent "being controlled" banner with a **Disconnect** button. There is no
background service and no unattended access in this version.

## Components

| Component | What it is | Runs on |
|---|---|---|
| **server** | Signaling + web server. Authenticates technicians, routes connections, hands out TURN credentials, keeps an audit log. Holds **no** video — media is peer-to-peer. | Your Ubuntu VPS |
| **coturn** | STUN/TURN relay for when direct peer-to-peer fails (strict NATs). | Same VPS |
| **console** | The NYK technician app: log in, see online client PCs, view and control one. Also available in any browser at `https://<your-domain>/console`. | Support staff PCs |
| **agent** | The customer app: one small `.exe`, no install. Shows an ID, the Allow/Deny prompt, and the on-screen banner. | Customer Windows PCs |

## How a session works

```
1. Customer runs the agent  ->  PC shows up Online in the console list.
2. Technician logs in, clicks the PC, clicks Connect.
3. Customer sees an Allow / Deny prompt (auto-denies after 30s).
4. On Allow: a direct encrypted WebRTC link forms (TURN relay as fallback).
5. Technician sees the screen and controls mouse + keyboard.
   Customer sees a red "Controlled by <tech>" banner with Disconnect.
6. Either side disconnects; the agent returns to idle. Closing the app ends all.
```

## Status

- [x] Shared protocol (signaling + input encoding)
- [x] Server: auth, signaling hub, SQLite store, TURN credentials, audit log, `adduser`
- [ ] Console app (login, device list, viewer)
- [ ] Agent core (signaling, WebRTC, test-pattern capture)
- [ ] Agent consent UI + banner
- [ ] Windows screen capture + input (via `kbinani/screenshot`, `go-vgo/robotgo`)
- [ ] Deploy files + CI build + full runbook

## Security model

- Agents must present a build-time **org key** to register, so random clients
  can't appear in your list.
- Technicians authenticate with a username/password (bcrypt); the server issues
  short-lived bearer tokens.
- All transport is TLS (`wss://`); WebRTC media is DTLS-SRTP encrypted.
- The server only relays signaling for sessions it authorized, and writes every
  connect / consent / start / end to the audit log.

See `deploy/` for VPS setup and `docs` sections below once filled in.

## Build (developers)

```bash
# Server (Linux)
go build -o bin/nyk-server ./server

# Create the first technician login
./bin/nyk-server adduser alice

# Run in dev mode (plain HTTP on :8080)
NYK_ORG_KEY=dev-key ./bin/nyk-server -dev
```

Windows `agent.exe` and `console.exe` are produced by the GitHub Actions
workflow in `.github/workflows/` (see the Deploy section of this README once
complete).
