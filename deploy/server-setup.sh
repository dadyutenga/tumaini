#!/usr/bin/env bash
# Runs ON THE SERVER as root. Idempotent; safe to re-run. Normally started by scripts/provision-server.sh,
# which first copies deploy/ to /opt/tumaini.
#
#   DOMAIN=bms.example.com [DEPLOY_PUBKEY='ssh-ed25519 AAAA... ci'] [ADMIN_EMAIL=you@example.com] ./server-setup.sh
#
# What it does (and nothing else on a shared host):
#   - installs Docker + compose plugin only if missing (Debian/Ubuntu)
#   - creates the `deploy` user (docker group) and authorises DEPLOY_PUBKEY for CI
#   - prepares /opt/tumaini, writes .env with generated secrets (never overwrites), nightly backup cron
#   - installs an nginx vhost for DOMAIN and gets a Let's Encrypt certificate (webroot)
# It does not touch the firewall, other nginx sites, or other containers.
set -euo pipefail
: "${DOMAIN:?set DOMAIN}"
APP_DIR=/opt/tumaini
BACKUP_DIR=/var/backups/tumaini
[ "$(id -u)" = 0 ] || { echo "run as root" >&2; exit 1; }

log() { printf '\n==> %s\n' "$*"; }

log "docker"
if ! command -v docker >/dev/null || ! docker compose version >/dev/null 2>&1; then
  . /etc/os-release
  case "$ID" in ubuntu|debian) ;; *) echo "install Docker + compose plugin manually on $ID" >&2; exit 1 ;; esac
  apt-get update -qq
  apt-get install -y -qq ca-certificates curl gnupg
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL "https://download.docker.com/linux/$ID/gpg" -o /etc/apt/keyrings/docker.asc
  echo "deb [signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/$ID $VERSION_CODENAME stable" > /etc/apt/sources.list.d/docker.list
  apt-get update -qq
  apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-compose-plugin
fi
docker --version; docker compose version

log "deploy user"
id deploy >/dev/null 2>&1 || useradd --create-home --shell /bin/bash deploy
usermod -aG docker deploy
if [ -n "${DEPLOY_PUBKEY:-}" ]; then
  install -d -m 700 -o deploy -g deploy /home/deploy/.ssh
  touch /home/deploy/.ssh/authorized_keys
  # "restrict": the CI key runs commands only, with no terminal or port, agent or X11 forwarding.
  # An earlier unrestricted copy of the same key is replaced.
  keys=/home/deploy/.ssh/authorized_keys
  { grep -vxF -e "$DEPLOY_PUBKEY" -e "restrict $DEPLOY_PUBKEY" "$keys" || true; echo "restrict $DEPLOY_PUBKEY"; } > "$keys.new"
  mv "$keys.new" "$keys"
  chown deploy:deploy /home/deploy/.ssh/authorized_keys; chmod 600 /home/deploy/.ssh/authorized_keys
fi

log "$APP_DIR"
install -d -m 750 -o deploy -g deploy "$APP_DIR"
install -d -m 700 -o deploy -g deploy "$BACKUP_DIR"
if [ ! -f "$APP_DIR/.env" ]; then
  sed -e "s|^CORS_ORIGINS=.*|CORS_ORIGINS=https://$DOMAIN|" \
      -e "s|^POSTGRES_PASSWORD=.*|POSTGRES_PASSWORD=$(openssl rand -hex 24)|" \
      -e "s|^BMS_API_DB_PASSWORD=.*|BMS_API_DB_PASSWORD=$(openssl rand -hex 24)|" \
      -e "s|^BMS_JOBS_DB_PASSWORD=.*|BMS_JOBS_DB_PASSWORD=$(openssl rand -hex 24)|" \
      -e "s|^JWT_SECRET=.*|JWT_SECRET=$(openssl rand -base64 48 | tr -d '\n=+/')|" \
      -e "s|^DATA_ENCRYPTION_KEY=.*|DATA_ENCRYPTION_KEY=$(openssl rand -hex 32)|" \
      -e "s|^BACKUP_ENCRYPTION_KEY=.*|BACKUP_ENCRYPTION_KEY=$(openssl rand -hex 32)|" \
      "$APP_DIR/.env.production.example" > "$APP_DIR/.env"
  echo "created $APP_DIR/.env with generated secrets"
fi
chown deploy:deploy "$APP_DIR/.env"; chmod 600 "$APP_DIR/.env"
chmod +x "$APP_DIR"/deploy.sh "$APP_DIR"/backup-prod.sh "$APP_DIR"/restore-verify.sh "$APP_DIR"/restore.sh \
  "$APP_DIR"/restore-drill.sh "$APP_DIR"/verify-anchors.sh "$APP_DIR"/data-check.sh "$APP_DIR"/host-check.sh
chown -R deploy:deploy "$APP_DIR"

cat > /etc/cron.d/tumaini-backup <<CRON
15 1 * * * deploy cd $APP_DIR && ./backup-prod.sh >> /var/log/tumaini-backup.log 2>&1
45 * * * * deploy cd $APP_DIR && ./backup-prod.sh hourly >> /var/log/tumaini-backup.log 2>&1
30 2 1 * * deploy cd $APP_DIR && ./restore-drill.sh >> /var/log/tumaini-backup.log 2>&1
CRON
touch /var/log/tumaini-backup.log; chown deploy:deploy /var/log/tumaini-backup.log

log "nginx + certificate for $DOMAIN"
SITE=/etc/nginx/sites-available/$DOMAIN
install -d /var/www/certbot
if [ ! -s "/etc/letsencrypt/live/$DOMAIN/fullchain.pem" ]; then
  # HTTP-only vhost first, so the ACME challenge can be answered.
  cat > "$SITE" <<NGINX
server {
    listen 80;
    server_name $DOMAIN;
    location /.well-known/acme-challenge/ { root /var/www/certbot; }
    location / { return 404; }
}
NGINX
  ln -sf "$SITE" "/etc/nginx/sites-enabled/$DOMAIN"
  nginx -t && systemctl reload nginx
  certbot certonly --webroot -w /var/www/certbot -d "$DOMAIN" --non-interactive --agree-tos \
    ${ADMIN_EMAIL:+-m "$ADMIN_EMAIL"} ${ADMIN_EMAIL:---register-unsafely-without-email} \
    || { rm -f "/etc/nginx/sites-enabled/$DOMAIN"; nginx -t && systemctl reload nginx; echo "certbot failed" >&2; exit 1; }
fi
sed "s|__DOMAIN__|$DOMAIN|g" "$APP_DIR/nginx/bms.conf" > "$SITE"
ln -sf "$SITE" "/etc/nginx/sites-enabled/$DOMAIN"
if ! nginx -t; then rm -f "/etc/nginx/sites-enabled/$DOMAIN"; echo "nginx config invalid; vhost removed" >&2; exit 1; fi
systemctl reload nginx

log "check the host settings with: sudo $APP_DIR/host-check.sh"
log "done. Next: deploy an image tag with $APP_DIR/deploy.sh <tag> (as the deploy user)"
