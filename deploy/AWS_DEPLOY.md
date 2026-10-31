# Deploy NYK Support Portal on AWS EC2 — runbook for a local Claude

This is written to be executed by **Claude Code running on your own machine**,
where you have the EC2 `.pem` key and SSH access. Hand Claude this whole file
and the values in **Step 0**, then let it work through the steps. Every command
is copy-pasteable; Claude should run them, read the output, and only continue
when each step succeeds.

The server is **pure Go (amd64/arm64, no cgo)**, so we build it on your machine
and copy the binary up — no need to install Go or a compiler on the server.

---

## Step 0 — values you provide (fill these in first)

```
PEM        = /path/to/your-key.pem          # your EC2 SSH private key
EC2_USER   = ubuntu                          # Ubuntu AMIs use "ubuntu"
EC2_HOST   = <EC2 public IP or DNS>          # e.g. 13.51.x.x
DOMAIN     = support.yourdomain.com          # subdomain for the console/signaling
TURN_DOMAIN= turn.yourdomain.com             # subdomain for the TURN relay (can equal DOMAIN)
REGION     = <aws region>                    # e.g. eu-north-1 (only needed if using AWS CLI)
SG_ID      = <security group id>             # e.g. sg-0abc... (only needed if using AWS CLI)
```

Claude will generate two secrets in Step 3; you don't need to invent them.

Prerequisites on your machine: `git`, `ssh`, `scp`, and **Go 1.24+** (to build
the server/agent). Check: `go version`. If Go isn't installed, see
https://go.dev/dl/ or ask Claude to install it.

---

## Step 1 — get the code

```bash
git clone https://github.com/ramim121/nyk-support-portal.git
cd nyk-support-portal
```

---

## Step 2 — open the firewall ports (AWS security group)

The server needs these inbound ports. Open them on the instance's **security
group** — either in the AWS Console (EC2 → Security Groups → Inbound rules) or
with the AWS CLI if it's configured on your machine:

| Port | Protocol | Purpose |
|---|---|---|
| 22 | TCP | SSH (you likely already have this) |
| 80 | TCP | Let's Encrypt challenge + HTTPS redirect |
| 443 | TCP | Console web app + `wss://` signaling |
| 3478 | TCP & UDP | STUN/TURN |
| 5349 | TCP | TURN over TLS (optional) |
| 49160–49200 | UDP | TURN relay media |

AWS CLI version (skip if you'll click in the console):

```bash
for p in 80 443 3478 5349; do
  aws ec2 authorize-security-group-ingress --region "$REGION" --group-id "$SG_ID" \
    --protocol tcp --port $p --cidr 0.0.0.0/0 2>/dev/null || true
done
aws ec2 authorize-security-group-ingress --region "$REGION" --group-id "$SG_ID" \
  --protocol udp --port 3478 --cidr 0.0.0.0/0 2>/dev/null || true
aws ec2 authorize-security-group-ingress --region "$REGION" --group-id "$SG_ID" \
  --protocol udp --port 49160-49200 --cidr 0.0.0.0/0 2>/dev/null || true
```

---

## Step 3 — DNS

Point both subdomains at the EC2 public IP with **A records** (do this at your
DNS provider):

```
DOMAIN        A   <EC2 public IP>
TURN_DOMAIN   A   <EC2 public IP>
```

Verify (wait for propagation if needed):

```bash
dig +short "$DOMAIN"        # should print the EC2 IP
dig +short "$TURN_DOMAIN"   # should print the EC2 IP
```

Generate the two shared secrets now and save them somewhere safe:

```bash
ORG_KEY=$(openssl rand -hex 24);  echo "ORG_KEY=$ORG_KEY"
TURN_SECRET=$(openssl rand -hex 24); echo "TURN_SECRET=$TURN_SECRET"
```

---

## Step 4 — build the server + console on your machine

Detect the server's CPU arch, then build a matching Linux binary and the console
web bundle:

```bash
ARCH=$(ssh -i "$PEM" "$EC2_USER@$EC2_HOST" uname -m)
case "$ARCH" in
  aarch64|arm64) GOARCH=arm64 ;;
  *)             GOARCH=amd64 ;;
esac
echo "building for $GOARCH"

CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH go build -ldflags "-X main.Version=1.0.0" -o nyk-server ./server

# console web app (needs Node 18+)
( cd console/frontend && npm ci && npm run build )
```

---

## Step 5 — copy everything up and lay out directories

```bash
# upload the binary, console bundle, and deploy files
scp -i "$PEM" nyk-server "$EC2_USER@$EC2_HOST:/tmp/nyk-server"
scp -i "$PEM" -r console/frontend/dist "$EC2_USER@$EC2_HOST:/tmp/console-web"
scp -i "$PEM" deploy/nyk-server.service deploy/nyk-server.env \
              deploy/coturn/turnserver.conf "$EC2_USER@$EC2_HOST:/tmp/"

ssh -i "$PEM" "$EC2_USER@$EC2_HOST" 'bash -s' <<'REMOTE'
set -e
sudo useradd --system --home /var/lib/nyk --create-home nyk 2>/dev/null || true
sudo mkdir -p /var/lib/nyk/console /var/lib/nyk/tls-cache
sudo mv /tmp/nyk-server /usr/local/bin/nyk-server
sudo chmod +x /usr/local/bin/nyk-server
sudo rm -rf /var/lib/nyk/console && sudo mv /tmp/console-web /var/lib/nyk/console
sudo chown -R nyk:nyk /var/lib/nyk
echo "server binary + console in place"
REMOTE
```

---

## Step 6 — install and configure coturn (the TURN relay)

```bash
ssh -i "$PEM" "$EC2_USER@$EC2_HOST" "TURN_DOMAIN='$TURN_DOMAIN' TURN_SECRET='$TURN_SECRET' bash -s" <<'REMOTE'
set -e
sudo apt-get update -y && sudo apt-get install -y coturn
sudo sed -i 's/#TURNSERVER_ENABLED=1/TURNSERVER_ENABLED=1/' /etc/default/coturn
sudo mv /tmp/turnserver.conf /etc/turnserver.conf
# fill in the domain + secret
sudo sed -i "s/turn.CHANGE-ME.example.com/$TURN_DOMAIN/g" /etc/turnserver.conf
sudo sed -i "s/CHANGE-ME-LONG-RANDOM-SECRET/$TURN_SECRET/g" /etc/turnserver.conf
sudo systemctl enable coturn && sudo systemctl restart coturn
sudo systemctl --no-pager status coturn | head -5
REMOTE
```

---

## Step 7 — configure and start the server

```bash
ssh -i "$PEM" "$EC2_USER@$EC2_HOST" \
  "DOMAIN='$DOMAIN' TURN_DOMAIN='$TURN_DOMAIN' ORG_KEY='$ORG_KEY' TURN_SECRET='$TURN_SECRET' bash -s" <<'REMOTE'
set -e
sudo mv /tmp/nyk-server.service /etc/systemd/system/nyk-server.service
sudo mv /tmp/nyk-server.env /etc/nyk-server.env

# fill the env file
sudo sed -i "s#support.CHANGE-ME.example.com#$DOMAIN#g" /etc/nyk-server.env
sudo sed -i "s#turn.CHANGE-ME.example.com#$TURN_DOMAIN#g" /etc/nyk-server.env
sudo sed -i "s#CHANGE-ME-ORG-KEY#$ORG_KEY#g" /etc/nyk-server.env
sudo sed -i "s#CHANGE-ME-LONG-RANDOM-SECRET#$TURN_SECRET#g" /etc/nyk-server.env
sudo chown root:nyk /etc/nyk-server.env && sudo chmod 640 /etc/nyk-server.env

sudo systemctl daemon-reload
sudo systemctl enable --now nyk-server
sleep 3
sudo systemctl --no-pager status nyk-server | head -8
echo "--- recent logs ---"
sudo journalctl -u nyk-server --no-pager | tail -15
REMOTE
```

The first HTTPS request triggers Let's Encrypt certificate issuance (~30s).
Verify from your machine:

```bash
sleep 20
curl -sS "https://$DOMAIN/healthz" && echo   # expect: ok
```

If `curl` fails with a TLS error, wait another 30s (cert still issuing) and retry.
Common gotchas: DNS not propagated yet (Step 3), or port 80/443 not open (Step 2).

---

## Step 8 — create your first technician login

```bash
ssh -i "$PEM" "$EC2_USER@$EC2_HOST" \
  "sudo -u nyk /usr/local/bin/nyk-server adduser -db /var/lib/nyk/nyk.db admin"
# it prompts for a password
```

Now sign in at:  **https://$DOMAIN/console**

---

## Step 9 — build the Windows apps for your team + customers

On your machine (pure Go, cross-compiles from macOS/Linux/Windows):

```bash
# Customer agent — server URL + org key baked in, so it just runs:
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build \
  -ldflags "-H windowsgui -X main.Version=1.0.0 \
            -X main.DefaultServer=wss://$DOMAIN \
            -X main.DefaultOrgKey=$ORG_KEY" \
  -o agent.exe ./agent

# Technician desktop console (optional; the browser at https://$DOMAIN/console also works):
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build \
  -ldflags "-H windowsgui -X main.ServerURL=https://$DOMAIN/console" \
  -o console.exe ./console
```

Give `agent.exe` to a customer. When they run it:
- A window shows their support ID and the PC appears **Online** in your console.
- When you click **Connect**, they get an **Allow / Deny** prompt.
- While connected they see a **"being controlled"** banner with **Disconnect**.

> Note: these `.exe`s are **unsigned**, so Windows SmartScreen/antivirus may warn
> on first run ("More info → Run anyway"). Before wider rollout, sign them with a
> code-signing certificate — ask Claude to wire signing into CI when you have one.

---

## Step 10 — end-to-end smoke test (the one thing not yet verified)

On a real Windows PC, run `agent.exe`, then from your console:
1. Confirm the PC shows **Online**.
2. Click **Connect** → the customer sees the Allow prompt → click **Allow**.
3. Confirm you see their screen, and your mouse + keyboard control it.
4. Switch monitors / quality; copy text with "Send clipboard".
5. Click **Disconnect** (and test the customer's Disconnect button too).
6. Optionally force the TURN relay path by blocking UDP between the two PCs.

If the screen is black or input doesn't land, grab the agent's console output
(run it from a `cmd` window to see logs) and share it — that's the Windows layer
that hasn't been exercised on real hardware yet.

---

## Updating later

```bash
# rebuild, re-upload, restart
CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH go build -o nyk-server ./server
scp -i "$PEM" nyk-server "$EC2_USER@$EC2_HOST:/tmp/nyk-server"
ssh -i "$PEM" "$EC2_USER@$EC2_HOST" \
  'sudo systemctl stop nyk-server && sudo mv /tmp/nyk-server /usr/local/bin/nyk-server && sudo chmod +x /usr/local/bin/nyk-server && sudo chown nyk:nyk /usr/local/bin/nyk-server && sudo systemctl start nyk-server'
```

## Troubleshooting quick reference

| Symptom | Check |
|---|---|
| `curl /healthz` TLS error | DNS A record resolves? ports 80/443 open? wait 30s for cert |
| Device never appears Online | agent's `-server` is `wss://$DOMAIN` and org key matches; `journalctl -u nyk-server` |
| Connects but screen black | Windows agent capture — run `agent.exe` from cmd and read logs |
| Works on same LAN, not across internet | TURN: coturn running, UDP 3478 + 49160-49200 open, secrets match |
| `journalctl -u nyk-server` shows "no technician logins" | run Step 8 |
