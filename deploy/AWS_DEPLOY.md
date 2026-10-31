# Deploy NYK Support Portal on an existing AWS EC2 box — runbook for a local Claude

**Scenario this runbook is written for:** a **shared** EC2 instance that already
serves `new.digigramventures.com` for a Node app. We want to **replace that Node
app** with the NYK Support Portal, **reuse the same subdomain and port**, keep
the existing HTTPS/cert, and **not disturb any other site or service** on the box.

Hand this whole file to **Claude Code running on your machine** (where you have
the `.pem`). Claude must **inspect first, change nothing until it understands the
setup, and ask you before stopping anything.** Every step prints what it found.

---

## Step 0 — values you provide

```
PEM       = /path/to/your-key.pem
EC2_USER  = ubuntu                       # or ec2-user on Amazon Linux
EC2_HOST  = <EC2 public IP or DNS>
DOMAIN    = new.digigramventures.com      # reuse the existing subdomain
```

Prereqs on your machine: `git`, `ssh`, `scp`, and **Go 1.24+** (`go version`).

```bash
git clone https://github.com/ramim121/nyk-support-portal.git
cd nyk-support-portal
SSH="ssh -i $PEM $EC2_USER@$EC2_HOST"
```

DNS is already done (the subdomain points at this box). Confirm:

```bash
dig +short "$DOMAIN"        # should print the EC2 public IP
```

---

## Step 1 — DISCOVER the existing setup (read-only, changes nothing)

Run these over SSH and **read the output** before doing anything else:

```bash
echo "== listening sockets ==";        $SSH "sudo ss -ltnp | grep -E ':80|:443|:3000|:8080' || sudo ss -ltnp"
echo "== web servers / app managers =="; $SSH "systemctl list-units --type=service --state=running | grep -iE 'nginx|apache|httpd|caddy|node' ; command -v pm2 >/dev/null && pm2 list || true"
echo "== nginx vhost for this domain =="; $SSH "sudo nginx -T 2>/dev/null | sed -n '/server_name .*new.digigramventures.com/,/}/p' | head -60 || echo 'no nginx or domain not in nginx'"
echo "== TLS certs on box ==";          $SSH "sudo ls -la /etc/letsencrypt/live/ 2>/dev/null; sudo certbot certificates 2>/dev/null | head -40 || true"
echo "== TURN ports free? (want empty) =="; $SSH "sudo ss -lun | grep -E ':3478|:49160' ; sudo ss -ltn | grep -E ':3478|:5349' || echo 'turn ports appear free'"
```

**Interpret the results and pick a path:**

- **Path A — reverse proxy present (MOST LIKELY, recommended).** There is an
  nginx/Apache/caddy server block for `new.digigramventures.com` that terminates
  HTTPS and `proxy_pass`es to the Node app on a local port (e.g.
  `http://127.0.0.1:3000`). Note that **PORT** and the proxy's config file.
  → Go to **Step 2A**.

- **Path B — Node serves 443 directly** (nginx/etc. not in front; `:443` is the
  Node process; box has no other HTTPS vhosts). → Go to **Step 2B**.

If unsure, or if other vhosts share the proxy, **stop and report what you found
to the user** before changing anything.

---

## Step 2A — Reverse-proxy integration (keep existing TLS, reuse the port)

Let `PORT` = the local port the proxy forwards this subdomain to (from Step 1).

### 2A.1 Build the server + console on your machine (matching the box's arch)

```bash
ARCH=$($SSH uname -m)
case "$ARCH" in aarch64|arm64) GOARCH=arm64;; *) GOARCH=amd64;; esac
CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH go build -ldflags "-X main.Version=1.0.0" -o nyk-server ./server
( cd console/frontend && npm ci && npm run build )
```

### 2A.2 Upload (into a dedicated dir; touches nothing else)

```bash
$SSH "sudo mkdir -p /var/lib/nyk/console /var/lib/nyk/tls-cache"
scp -i "$PEM" nyk-server "$EC2_USER@$EC2_HOST:/tmp/nyk-server"
scp -i "$PEM" -r console/frontend/dist "$EC2_USER@$EC2_HOST:/tmp/console-web"
scp -i "$PEM" deploy/nyk-server.service "$EC2_USER@$EC2_HOST:/tmp/"
$SSH 'sudo mv /tmp/nyk-server /usr/local/bin/nyk-server && sudo chmod +x /usr/local/bin/nyk-server \
      && sudo rm -rf /var/lib/nyk/console && sudo mv /tmp/console-web /var/lib/nyk/console'
```

### 2A.3 Generate secrets

```bash
ORG_KEY=$(openssl rand -hex 24);     echo "SAVE ORG_KEY=$ORG_KEY"
TURN_SECRET=$(openssl rand -hex 24); echo "SAVE TURN_SECRET=$TURN_SECRET"
```

### 2A.4 Write the server's env file (behind-proxy mode on the SAME port)

```bash
$SSH "DOMAIN='$DOMAIN' PORT='$PORT' ORG_KEY='$ORG_KEY' TURN_SECRET='$TURN_SECRET' bash -s" <<'REMOTE'
set -e
sudo tee /etc/nyk-server.env >/dev/null <<EOF
NYK_DOMAIN=$DOMAIN
NYK_BEHIND_PROXY=1
NYK_ADDR=127.0.0.1:$PORT
NYK_DB=/var/lib/nyk/nyk.db
NYK_CONSOLE_DIR=/var/lib/nyk/console
NYK_ORG_KEY=$ORG_KEY
NYK_TURN_HOST=$DOMAIN:3478
NYK_TURN_SECRET=$TURN_SECRET
EOF
sudo useradd --system --home /var/lib/nyk --create-home nyk 2>/dev/null || true
sudo chown -R nyk:nyk /var/lib/nyk
sudo chown root:nyk /etc/nyk-server.env && sudo chmod 640 /etc/nyk-server.env
echo "env written for 127.0.0.1:$PORT"
REMOTE
```

### 2A.5 Stop & disable the OLD Node app (frees the port). ASK THE USER FIRST.

Identify the Node app's manager from Step 1, then disable it so it won't restart
and re-grab the port. Use whichever applies:

```bash
# systemd service (replace NAME):
$SSH "sudo systemctl stop NAME && sudo systemctl disable NAME"
# OR pm2:
$SSH "pm2 stop NAME && pm2 delete NAME && pm2 save"
```

Confirm the port is now free:

```bash
$SSH "sudo ss -ltnp | grep ':$PORT' || echo 'port $PORT is free'"
```

### 2A.6 Install the NYK systemd service (behind-proxy: no privileged ports)

```bash
$SSH 'bash -s' <<'REMOTE'
set -e
# behind a proxy on a localhost port, drop the :80/:443 bind capability
sudo sed -i '/AmbientCapabilities/d; /CapabilityBoundingSet/d' /tmp/nyk-server.service 2>/dev/null || true
sudo mv /tmp/nyk-server.service /etc/systemd/system/nyk-server.service
sudo systemctl daemon-reload
sudo systemctl enable --now nyk-server
sleep 2
sudo systemctl --no-pager status nyk-server | head -6
sudo journalctl -u nyk-server --no-pager | tail -10
REMOTE
```

### 2A.7 Make sure the proxy forwards WebSockets for this subdomain

The signaling channel is a WebSocket, so the proxy's location for this subdomain
needs upgrade headers. In the nginx server block for `new.digigramventures.com`,
the `location / { ... }` that `proxy_pass`es to `127.0.0.1:$PORT` must include:

```nginx
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection $connection_upgrade;   # or the literal "upgrade"
    proxy_set_header Host $host;
    proxy_read_timeout 3600s;
```

If `$connection_upgrade` isn't defined, add this `map` in the `http {}` block:

```nginx
map $http_upgrade $connection_upgrade { default upgrade; '' close; }
```

Claude: edit **only** this subdomain's config file, then validate and reload
(this does not interrupt other sites):

```bash
$SSH "sudo nginx -t && sudo systemctl reload nginx"
```

### 2A.8 Verify

```bash
curl -sS "https://$DOMAIN/healthz" && echo        # expect: ok
curl -sS "https://$DOMAIN/api/config" && echo     # expect: {"name":"NYK Support Portal",...}
```

Then **Step 3** (coturn), **Step 4** (login), **Step 5** (Windows apps).

---

## Step 2B — Node served 443 directly (no proxy in front)

Only if Step 1 showed the Node process itself on `:443` and no other HTTPS
vhosts exist. Here the NYK server takes over 443 with its own automatic
Let's Encrypt cert.

```bash
# build + upload as in 2A.1–2A.2, then:
$SSH "sudo systemctl stop NAME && sudo systemctl disable NAME"   # stop the Node app (ASK FIRST)
$SSH "ORG_KEY='$ORG_KEY' TURN_SECRET='$TURN_SECRET' DOMAIN='$DOMAIN' bash -s" <<'REMOTE'
set -e
sudo tee /etc/nyk-server.env >/dev/null <<EOF
NYK_DOMAIN=$DOMAIN
NYK_DB=/var/lib/nyk/nyk.db
NYK_TLS_CACHE=/var/lib/nyk/tls-cache
NYK_CONSOLE_DIR=/var/lib/nyk/console
NYK_ORG_KEY=$ORG_KEY
NYK_TURN_HOST=$DOMAIN:3478
NYK_TURN_SECRET=$TURN_SECRET
EOF
sudo useradd --system --home /var/lib/nyk --create-home nyk 2>/dev/null || true
sudo chown -R nyk:nyk /var/lib/nyk
sudo chown root:nyk /etc/nyk-server.env && sudo chmod 640 /etc/nyk-server.env
sudo mv /tmp/nyk-server.service /etc/systemd/system/nyk-server.service
sudo systemctl daemon-reload && sudo systemctl enable --now nyk-server
REMOTE
# open 80/443 in the security group if not already, then:
sleep 25; curl -sS "https://$DOMAIN/healthz" && echo   # cert issuance ~30s
```

---

## Step 3 — coturn (TURN relay) — needed for clients behind strict NAT

coturn uses its own ports and does not touch the web server. First confirm the
ports are free (Step 1 showed this). Open them in the **AWS security group**:
`3478/tcp`, `3478/udp`, and `49160-49200/udp` (and `5349/tcp` if you want TURN
over TLS). Then:

```bash
$SSH "DOMAIN='$DOMAIN' TURN_SECRET='$TURN_SECRET' bash -s" <<'REMOTE'
set -e
sudo apt-get update -y && sudo apt-get install -y coturn
sudo sed -i 's/#TURNSERVER_ENABLED=1/TURNSERVER_ENABLED=1/' /etc/default/coturn
sudo tee /etc/turnserver.conf >/dev/null <<EOF
realm=$DOMAIN
server-name=$DOMAIN
listening-port=3478
min-port=49160
max-port=49200
use-auth-secret
static-auth-secret=$TURN_SECRET
no-cli
no-multicast-peers
denied-peer-ip=10.0.0.0-10.255.255.255
denied-peer-ip=172.16.0.0-172.31.255.255
denied-peer-ip=192.168.0.0-192.168.255.255
log-file=/var/log/turnserver.log
simple-log
EOF
sudo systemctl enable coturn && sudo systemctl restart coturn
sudo systemctl --no-pager status coturn | head -5
REMOTE
```

> coturn also binds UDP/TCP 3478 on the public interface — make sure nothing
> else on the shared box already uses it (Step 1 checked). If something does,
> pick another port and set `NYK_TURN_HOST` + coturn `listening-port` to match.

---

## Step 4 — Create your first technician login

```bash
$SSH "sudo -u nyk /usr/local/bin/nyk-server adduser -db /var/lib/nyk/nyk.db admin"
# prompts for a password
```

Sign in at **https://new.digigramventures.com/console**

---

## Step 5 — Build the Windows apps

On your machine (pure Go, cross-compiles anywhere):

```bash
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build \
  -ldflags "-H windowsgui -X main.Version=1.0.0 \
            -X main.DefaultServer=wss://$DOMAIN \
            -X main.DefaultOrgKey=$ORG_KEY" \
  -o agent.exe ./agent

GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build \
  -ldflags "-H windowsgui -X main.ServerURL=https://$DOMAIN/console" \
  -o console.exe ./console
```

Give `agent.exe` to a customer. Unsigned → SmartScreen may warn ("More info →
Run anyway"); sign before wider rollout.

---

## Step 6 — End-to-end smoke test (the one thing not yet verified on real Windows)

1. Run `agent.exe` on a Windows PC → it shows a support ID; the PC appears
   **Online** in your console at `https://$DOMAIN/console`.
2. Click **Connect** → customer sees **Allow/Deny** → **Allow**.
3. You should see their screen and control mouse + keyboard.
4. Test monitor switch, quality, "Send clipboard", and **Disconnect** (both sides).
5. If the screen is black or input doesn't land, run `agent.exe` from a `cmd`
   window and capture the logs — that's the Windows capture/input layer, which
   hasn't run on real hardware yet.

---

## Rollback (if anything goes wrong on the shared box)

```bash
# stop NYK, re-enable the old Node app
$SSH "sudo systemctl disable --now nyk-server"
$SSH "sudo systemctl enable --now NAME   # or: pm2 start NAME"
# if you edited nginx, restore the backup Claude made, then: sudo nginx -t && sudo systemctl reload nginx
```

Claude: **before editing any nginx file or stopping the Node app, make a backup**
(`sudo cp FILE FILE.bak-$(date +%s)`) and tell the user the exact path, so this
rollback is always possible.
```
