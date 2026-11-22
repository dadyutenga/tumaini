#!/usr/bin/env bash
# One-time (re-runnable) server preparation, run from your machine.
#   scripts/provision-server.sh <ssh-host> <domain> [path/to/ci-key.pub] [admin-email]
# Copies deploy/ to /opt/tumaini on the server and runs deploy/server-setup.sh there as root.
set -euo pipefail
HOST="${1:?usage: provision-server.sh <ssh-host> <domain> [ci-public-key-file] [admin-email]}"
DOMAIN="${2:?domain}"
PUB="${3:-}"
EMAIL="${4:-}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

ssh "$HOST" 'install -d -m 755 /opt/tumaini'
tar -C "$ROOT/deploy" --exclude=.env --exclude=postgres --exclude=seed-dev.sh --exclude=backup.sh --exclude=docker-compose.yml -cf - . | ssh "$HOST" 'tar -C /opt/tumaini -xf -'
[ -z "$PUB" ] || KEY="$(cat "$PUB")"
ssh "$HOST" "DOMAIN='$DOMAIN' DEPLOY_PUBKEY='${KEY:-}' ADMIN_EMAIL='$EMAIL' bash /opt/tumaini/server-setup.sh"
