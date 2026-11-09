#!/bin/bash
# E-Meal online for real, on your own address (e.g. https://bord.emealkitchen.com),
# through a free Cloudflare Tunnel. No server to rent: E-Meal runs on this
# computer (a Mac at the restaurant that stays on), and Cloudflare carries
# guests and staff to it over https.
#
# What you need once:
#   - a free Cloudflare account, with your domain (e.g. emealkitchen.com)
#     added to it (Cloudflare shows how; the domain's DNS moves to Cloudflare)
#   - Stripe keys in .env for takeaway payments (or turn takeaway payments off)
#
# Double-click this file in Finder. The first time it asks a few questions
# and sets everything up. After that it just starts E-Meal and the tunnel.
# Answer "y" to "start by itself" and E-Meal keeps running in the background,
# also after a restart (log in to the Mac, or turn on automatic login).
#
# Your data stays in data/ on this computer (with nightly backups, see
# BACKUP_DIR in .env). Nothing here is put on GitHub.

cd "$(dirname "$0")" || exit 1
DIR="$(pwd)"
export PATH="$PATH:/usr/local/go/bin:/opt/homebrew/bin:/usr/local/bin:$HOME/go/bin"
mkdir -p data
CONF="data/go-live.conf"
NAME="emeal"

say() { printf '\n\033[1m%s\033[0m\n' "$*"; }
stop() { say "$1"; read -r -p "Press Return to close." _; exit 1; }
ask() { local a; read -r -p "$1 " a; printf '%s' "$a"; }

command -v go >/dev/null || stop "Go isn't installed (or can't be found). Install it from https://go.dev/dl/ and try again."

# Cloudflare's tunnel tool, downloaded once into your home folder.
CF="$HOME/cloudflared"
if [ ! -x "$CF" ]; then
	arch=arm64
	[ "$(uname -m)" = "x86_64" ] && arch=amd64
	say "Downloading Cloudflare's tunnel tool (once, about 20 MB)…"
	curl -fL -o "$HOME/cloudflared.tgz" "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-darwin-$arch.tgz" &&
		tar -xzf "$HOME/cloudflared.tgz" -C "$HOME" || stop "The download didn't work. Check the internet and try again."
fi

# ---- .env: the settings file (never on GitHub) ----
[ -f .env ] || cp .env.example .env
envget() { grep -E "^$1=" .env | tail -1 | cut -d= -f2-; }
envset() { # envset KEY VALUE: replace the line, or add it
	if grep -qE "^$1=" .env; then
		awk -v k="$1" -v v="$2" 'BEGIN{FS=OFS="="} $1==k{print k"="v; next} {print}' .env >.env.tmp && mv .env.tmp .env
	else
		printf '%s=%s\n' "$1" "$2" >>.env
	fi
}

# ---- The address ----
HOST=""
[ -f "$CONF" ] && HOST=$(grep -E '^HOST=' "$CONF" | cut -d= -f2-)
if [ -z "$HOST" ]; then
	say "Which address should E-Meal have?"
	echo "Use a part of your own domain, for example: bord.emealkitchen.com"
	HOST=$(ask "Address:")
	HOST=$(printf '%s' "$HOST" | sed -e 's#^https\?://##' -e 's#/.*$##' | tr 'A-Z' 'a-z')
	case "$HOST" in *.*) ;; *) stop "That isn't an address like bord.emealkitchen.com. Start again.";; esac
	printf 'HOST=%s\n' "$HOST" >"$CONF"
fi

# ---- Cloudflare: log in once, make the tunnel once, point the address at it ----
CFDIR="$HOME/.cloudflared"
if [ ! -f "$CFDIR/cert.pem" ]; then
	say "Log in to Cloudflare in the browser window that opens, and pick your domain."
	"$CF" tunnel login || stop "Cloudflare login didn't finish. Try again."
fi
TID=$("$CF" tunnel list --name "$NAME" --output json 2>/dev/null | grep -o '"id": *"[^"]*"' | head -1 | sed 's/.*"\([^"]*\)"$/\1/')
if [ -z "$TID" ]; then
	say "Making the tunnel…"
	"$CF" tunnel create "$NAME" || stop "The tunnel couldn't be made. The lines above say why."
	TID=$("$CF" tunnel list --name "$NAME" --output json 2>/dev/null | grep -o '"id": *"[^"]*"' | head -1 | sed 's/.*"\([^"]*\)"$/\1/')
fi
[ -n "$TID" ] || stop "Couldn't find the tunnel. Try again."
[ -f "$CFDIR/$TID.json" ] || stop "The tunnel's key file ($CFDIR/$TID.json) is missing on this computer. Delete the tunnel \"$NAME\" in Cloudflare (Zero Trust → Networks → Tunnels) and run this again."
if ! grep -q "^DNS_DONE=$HOST$" "$CONF"; then
	say "Pointing $HOST at the tunnel…"
	if "$CF" tunnel route dns "$NAME" "$HOST"; then
		printf 'DNS_DONE=%s\n' "$HOST" >>"$CONF"
	else
		echo "If it says the address already exists: delete the old $HOST record in Cloudflare (DNS), then run this again."
		stop "The address couldn't be set."
	fi
fi
PORT=$(envget PORT)
[ -n "$PORT" ] || PORT=8080
cat >"$CFDIR/$NAME.yml" <<EOF
tunnel: $TID
credentials-file: $CFDIR/$TID.json
ingress:
  - hostname: $HOST
    service: http://localhost:$PORT
  - service: http_status:404
EOF

# ---- Settings for a real site ----
envset BASE_URL "https://$HOST"
envset TRUST_PROXY 1 # Cloudflare passes on visitors' addresses
[ -n "$(envget PORT)" ] || envset PORT "$PORT"
if [ -z "$(envget SESSION_SECRET)" ]; then
	envset SESSION_SECRET "$(openssl rand -hex 32)"
fi
NEWPW=""
CURPW=$(envget ADMIN_PASSWORD)
if [ "${#CURPW}" -lt 12 ]; then # empty, or too short for the internet
	NEWPW=$(openssl rand -base64 18 | tr -d '/+=' | cut -c1-16)
	envset ADMIN_PASSWORD "$NEWPW"
fi
if [ -z "$(envget STRIPE_SECRET_KEY)" ] && [ "$(envget DEMO_MODE)" != "1" ]; then
	say "No Stripe keys in .env yet."
	echo "Booking a table online is free, but takeaway orders are paid online (25 %)."
	echo "Without Stripe keys those payments would only be pretend, so E-Meal won't open"
	echo "to the internet like that. Add STRIPE_SECRET_KEY and STRIPE_WEBHOOK_SECRET to .env"
	echo "(a free Stripe account; Stripe takes a small fee per card payment), then run this again."
	A=$(ask "Or open it anyway as a demo, where nobody really pays? (y/N)")
	[ "$A" = "y" ] || [ "$A" = "Y" ] || stop "Stopped. Add the Stripe keys and run this again."
	envset DEMO_MODE 1
fi

ADMUSER=$(envget ADMIN_USER)

say "Building E-Meal…"
go build -o emeal-booking . || stop "E-Meal didn't build. The lines above say why."

# ---- Start by itself? (macOS LaunchAgents: start at login, restart if they stop) ----
AGENTS="$HOME/Library/LaunchAgents"
APP_PLIST="$AGENTS/ax.emeal.app.plist"
TUN_PLIST="$AGENTS/ax.emeal.tunnel.plist"
if [ ! -f "$APP_PLIST" ]; then
	A=$(ask "Start E-Meal by itself in the background, also after a restart? (y/N)")
	if [ "$A" = "y" ] || [ "$A" = "Y" ]; then
		mkdir -p "$AGENTS"
		cat >"$APP_PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>ax.emeal.app</string>
  <key>ProgramArguments</key><array><string>$DIR/emeal-booking</string></array>
  <key>WorkingDirectory</key><string>$DIR</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>$DIR/data/emeal.log</string>
  <key>StandardErrorPath</key><string>$DIR/data/emeal.log</string>
</dict></plist>
EOF
		cat >"$TUN_PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>ax.emeal.tunnel</string>
  <key>ProgramArguments</key><array><string>$CF</string><string>tunnel</string><string>--no-autoupdate</string><string>--config</string><string>$CFDIR/$NAME.yml</string><string>run</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>$DIR/data/tunnel.log</string>
  <key>StandardErrorPath</key><string>$DIR/data/tunnel.log</string>
</dict></plist>
EOF
	fi
fi
if [ -f "$APP_PLIST" ]; then
	launchctl unload "$APP_PLIST" 2>/dev/null
	launchctl unload "$TUN_PLIST" 2>/dev/null
	launchctl load -w "$APP_PLIST" && launchctl load -w "$TUN_PLIST" || stop "macOS didn't start it. The lines above say why."
	say "E-Meal runs in the background now: https://$HOST"
	[ -n "$NEWPW" ] && echo "Manager login: ${ADMUSER:-admin} / $NEWPW   (saved in .env; write it down)"
	echo "It starts by itself after a restart. Keep the Mac awake: System Settings → Energy → Prevent automatic sleeping."
	echo "To update after a git pull: run this file again (it rebuilds and restarts)."
	echo "To stop it for good: launchctl unload -w \"$APP_PLIST\" \"$TUN_PLIST\""
	read -r -p "Press Return to close." _
	exit 0
fi

# ---- Or run it in this window ----
cleanup() {
	[ -n "$TUN" ] && kill "$TUN" 2>/dev/null
	[ -n "$SRV" ] && kill "$SRV" 2>/dev/null
}
trap cleanup EXIT
trap 'exit 0' INT TERM HUP
./emeal-booking >>data/emeal.log 2>&1 &
SRV=$!
for _ in $(seq 1 30); do
	curl -s -o /dev/null "http://localhost:$PORT/" && break
	sleep 1
done
curl -s -o /dev/null "http://localhost:$PORT/" || { tail -20 data/emeal.log; stop "E-Meal didn't start. The lines above say why."; }
caffeinate -dims -w $$ & # keeps the Mac awake while this window is open
"$CF" tunnel --no-autoupdate --config "$CFDIR/$NAME.yml" run >>data/tunnel.log 2>&1 &
TUN=$!
say "E-Meal is online: https://$HOST"
[ -n "$NEWPW" ] && echo "Manager login: ${ADMUSER:-admin} / $NEWPW   (saved in .env; write it down)"
echo "Keep this window open. Close it (or press Ctrl+C) to stop."
wait "$TUN"
