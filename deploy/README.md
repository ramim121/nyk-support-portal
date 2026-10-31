# Deploying the NYK Support Portal server (Ubuntu 22.04 / 24.04)

This stands up the signaling/web server and the coturn relay on one VPS. Do it
once; afterwards you only hand customers the `agent.exe` and your team the
console (browser or `console.exe`).

Replace every `CHANGE-ME` with your own values as you go.

## 0. DNS (do this first)

Add two DNS **A records** pointing at your VPS's public IP:

| Host | Type | Value |
|---|---|---|
| `support.yourdomain.com` | A | `YOUR.VPS.IP` |
| `turn.yourdomain.com` | A | `YOUR.VPS.IP` |

(You can use the same name for both if you prefer; two is tidier.)

## 1. Firewall / cloud security group

Open these inbound ports:

| Port | Proto | Why |
|---|---|---|
| 80 | TCP | Let's Encrypt HTTP challenge + redirect to HTTPS |
| 443 | TCP | Console web app + `wss://` signaling |
| 3478 | UDP & TCP | STUN/TURN |
| 5349 | TCP | TURN over TLS (optional but recommended) |
| 49160–49200 | UDP | TURN relay media range |

```bash
sudo ufw allow 80,443,3478,5349/tcp
sudo ufw allow 3478/udp
sudo ufw allow 49160:49200/udp
sudo ufw enable
```

## 2. Generate secrets

```bash
openssl rand -hex 24   # -> use as NYK_ORG_KEY (also baked into the agent build)
openssl rand -hex 24   # -> use as NYK_TURN_SECRET (and coturn static-auth-secret)
```

## 3. coturn

```bash
sudo apt-get update && sudo apt-get install -y coturn
sudo sed -i 's/#TURNSERVER_ENABLED=1/TURNSERVER_ENABLED=1/' /etc/default/coturn
sudo cp deploy/coturn/turnserver.conf /etc/turnserver.conf
# Edit /etc/turnserver.conf: set realm/server-name to turn.yourdomain.com and
# static-auth-secret to the second secret above.
sudo systemctl restart coturn && sudo systemctl enable coturn
```

## 4. The server binary + console

Build on the VPS (install Go 1.24+) or copy the artifacts from CI:

```bash
# Build (on the VPS)
go build -o nyk-server ./server
# Build the console web app (needs Node 20+)
cd console/frontend && npm ci && npm run build && cd -

sudo cp nyk-server /usr/local/bin/
sudo useradd --system --home /var/lib/nyk --create-home nyk   # one time
sudo mkdir -p /var/lib/nyk/console
sudo cp -r console/frontend/dist/* /var/lib/nyk/console/
sudo chown -R nyk:nyk /var/lib/nyk
```

## 5. Service config

```bash
sudo cp deploy/nyk-server.service /etc/systemd/system/
sudo cp deploy/nyk-server.env /etc/nyk-server.env
sudo chmod 640 /etc/nyk-server.env && sudo chown root:nyk /etc/nyk-server.env
# Edit /etc/nyk-server.env: domain, both secrets, turn host.
sudo systemctl daemon-reload
sudo systemctl enable --now nyk-server
sudo systemctl status nyk-server
```

The first HTTPS request triggers Let's Encrypt issuance (give it ~30s). Check:

```bash
curl https://support.yourdomain.com/healthz   # -> ok
```

## 6. Create your first technician login

```bash
sudo -u nyk /usr/local/bin/nyk-server adduser -db /var/lib/nyk/nyk.db yourname
# (prompts for a password)
```

Sign in at `https://support.yourdomain.com/console`.

## 7. Build the agent for customers

In CI (recommended) the agent is built with the org key and server URL baked in
(see `.github/workflows/build.yml`). Hand the resulting `agent.exe` to
customers. When they run it, their PC appears in your console list.

## Updating later

```bash
sudo systemctl stop nyk-server
sudo cp nyk-server /usr/local/bin/           # new binary
sudo cp -r console/frontend/dist/* /var/lib/nyk/console/
sudo systemctl start nyk-server
```

## Logs & audit

- Service logs: `journalctl -u nyk-server -f`
- coturn logs: `/var/log/turnserver.log`
- Session audit log: stored in the SQLite DB (`audit_log` table) and shown in
  the console's audit view.
