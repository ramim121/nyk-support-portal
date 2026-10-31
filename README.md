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
4. On Allow: a direct encrypted WebRTC data link forms (TURN relay as fallback).
5. Technician sees the screen (streamed as image tiles) and controls mouse + keyboard.
   Customer sees a red "Controlled by <tech>" banner with Disconnect.
6. Either side disconnects; the agent returns to idle. Closing the app ends all.
```

## How the screen is streamed

The client captures its screen and sends only the **changed image tiles** (JPEG)
over an encrypted WebRTC **data channel**; the console draws them onto a canvas.
This is **100% pure Go** on the client — no video codec, no cgo — so the agent
is one small `.exe` that builds and cross-compiles trivially. On a typical,
mostly-static support screen only a few small tiles move per second. WebRTC
still provides the peer-to-peer path, encryption (DTLS/SCTP) and the TURN
fallback; only the media format differs from a classic video codec.

## Status

- [x] Shared protocol (signaling, binary input codec, screen-tile format) — unit-tested
- [x] Server: auth, signaling hub, SQLite store, TURN credentials, audit log, `adduser` — unit-tested
- [x] Console app (login, live device list, canvas viewer, input capture, toolbar)
- [x] Agent core: signaling, WebRTC, pure-Go tile encoder, fake source, input decode
- [x] **End-to-end pipeline verified on Linux** (headless Chromium): consent → tiles
      decode & animate on canvas → mouse/keyboard reach the client with correct mapping
- [x] Agent consent dialog + always-on-top status/banner window with Disconnect (`walk`)
- [x] Windows screen capture (`kbinani/screenshot`) + input injection (`SendInput`), pure Go
- [x] `console.exe` desktop wrapper (WebView2, pure Go)
- [x] CI builds `agent.exe`, `console.exe`, Linux `nyk-server` (all cross-compiled on Linux)
- [ ] **Real-Windows smoke test** of the agent window, capture and input (can't run on Linux CI)
- [ ] Code signing before customer rollout

### What is verified vs. not
The server, signaling, consent flow, tile streaming and input mapping are all
**verified end-to-end on Linux** with a headless browser. The Windows-only code
(screen capture, `SendInput` injection, and the `walk` status window) **compiles
and cross-compiles cleanly** but has not been run on a real Windows desktop in
this environment — that is the one remaining verification, and the first thing
to do with the CI-built `agent.exe`.

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

### Windows apps (cross-compiled from Linux/macOS — no cgo, no Windows needed)

```bash
# Customer agent — bake in your server + org key so it needs no config:
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build \
  -ldflags "-H windowsgui -X main.Version=1.0.0 \
            -X main.DefaultServer=wss://support.yourdomain.com \
            -X main.DefaultOrgKey=YOUR_ORG_KEY" \
  -o agent.exe ./agent

# Technician desktop console (points at the hosted console UI):
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build \
  -ldflags "-H windowsgui -X main.ServerURL=https://support.yourdomain.com/console" \
  -o console.exe ./console
```

CI (`.github/workflows/build.yml`) does all of this automatically and uploads
`agent.exe`, `console.exe`, the Linux `nyk-server`, and the console web bundle as
build artifacts. Set the repo variables `NYK_SERVER`, `NYK_CONSOLE_URL` and the
secret `NYK_ORG_KEY` so released binaries point at your server.

### Dev loop (Linux/macOS, no Windows)

```bash
# terminal 1: server
NYK_ORG_KEY=dev ./bin/nyk-server -dev -console-dir console/frontend/dist
./bin/nyk-server adduser alice           # once

# terminal 2: a fake client that streams a test pattern and logs input
./bin/nyk-agent -server ws://localhost:8080 -orgkey dev -fake-capture -auto-accept

# browser: http://localhost:8080  (log in, connect to the fake PC)
```
