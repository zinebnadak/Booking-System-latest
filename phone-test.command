#!/bin/bash
# Ozmo on your phone: starts Ozmo and a temporary public link (a free
# Cloudflare quick tunnel, no account), so you can test from any phone.
#
# Double-click this file in Finder to start. Keep the window open while you
# test. Close the window (or press Ctrl+C) to stop everything: the link
# stops working at once.
#
# It uses its own test data (data/phone-test.json), its own port (8097) and
# new passwords each time, never admin/admin. The link and passwords are
# shown here and saved in data/phone-link.txt (deleted when you stop).

cd "$(dirname "$0")" || exit 1
export PATH="$PATH:/usr/local/go/bin:/opt/homebrew/bin:/usr/local/bin:$HOME/go/bin"
LINKFILE="data/phone-link.txt"
mkdir -p data

say() { printf '\n\033[1m%s\033[0m\n' "$*"; }
stop() { say "$1"; read -r -p "Press Return to close." _; exit 1; }

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

say "Building Ozmo…"
go build -o /tmp/ozmo-phone . || stop "Ozmo didn't build. The lines above say why."

pw() { printf '%s-%04d-%04d' "$1" $((RANDOM % 10000)) $((RANDOM % 10000)); }
MGR=$(pw manager)

cleanup() {
	[ -n "$TUN" ] && kill "$TUN" 2>/dev/null
	[ -n "$SRV" ] && kill "$SRV" 2>/dev/null
	rm -f "$LINKFILE"
}
trap cleanup EXIT
trap 'exit 0' INT TERM HUP

PORT=8097 DATA_FILE=data/phone-test.json ADMIN_PASSWORD="$MGR" \
	/tmp/ozmo-phone >data/phone-server.log 2>&1 &
SRV=$!
for _ in $(seq 1 30); do
	curl -s -o /dev/null http://localhost:8097/ && break
	sleep 1
done
curl -s -o /dev/null http://localhost:8097/ || { tail -20 data/phone-server.log; stop "Ozmo didn't start. The lines above say why."; }

say "Opening the public link…"
caffeinate -dims -w $$ & # keeps the Mac awake while this window is open
"$CF" tunnel --no-autoupdate --url http://localhost:8097 >data/phone-tunnel.log 2>&1 &
TUN=$!
URL=""
for _ in $(seq 1 60); do
	URL=$(grep -o 'https://[a-z0-9-]*\.trycloudflare\.com' data/phone-tunnel.log | head -1)
	[ -n "$URL" ] && break
	sleep 1
done
[ -n "$URL" ] || { tail -20 data/phone-tunnel.log; stop "The link didn't come up. The lines above say why."; }

# The address comes before Cloudflare is ready to carry anything: wait until
# the tunnel is connected, then a little more for the name to reach phones.
# (A phone that asks too early remembers "not found" for a while.)
say "Waiting for the link to be ready…"
for _ in $(seq 1 60); do
	grep -q 'Registered tunnel connection' data/phone-tunnel.log && break
	sleep 1
done
grep -q 'Registered tunnel connection' data/phone-tunnel.log || { tail -20 data/phone-tunnel.log; stop "Cloudflare didn't connect. The lines above say why."; }
sleep 15

printf 'Link: %s\nStaff login: %s/login\nManager: admin / %s\n' "$URL" "$URL" "$MGR" >"$LINKFILE"
say "Ozmo is online. Open this on your phone:"
cat "$LINKFILE"
say "Keep this window open while you test. Close it (or press Ctrl+C) to stop."
echo "If the phone says it can't find the site: wait a minute, then open the link in a private tab (or switch the phone to mobile data)."
wait "$TUN"
