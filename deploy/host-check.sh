#!/usr/bin/env bash
# Runs ON THE SERVER as root: reports on the host settings that protect the database and the secrets.
#   ./host-check.sh                  report only (changes nothing)
#   ./host-check.sh --apply-updates  also install and enable automatic security updates (Debian/Ubuntu)
#   ./host-check.sh --apply-ssh      also turn off SSH password logins and root password logins
# The firewall is only reported: the host is shared, and closing a port another application needs
# would take it down.
set -euo pipefail
APPLY_UPDATES=0; APPLY_SSH=0
for a in "$@"; do
  case "$a" in
    --apply-updates) APPLY_UPDATES=1 ;;
    --apply-ssh) APPLY_SSH=1 ;;
    *) echo "usage: host-check.sh [--apply-updates] [--apply-ssh]" >&2; exit 2 ;;
  esac
done
[ "$(id -u)" = 0 ] || { echo "run as root" >&2; exit 1; }
APP_DIR="${APP_DIR:-/opt/tumaini}"
WARN=0
ok()   { echo "ok    $*"; }
warn() { echo "WARN  $*"; WARN=1; }

if [ "$APPLY_UPDATES" = 1 ]; then
  apt-get update -qq
  apt-get install -y -qq unattended-upgrades
  cat > /etc/apt/apt.conf.d/20auto-upgrades <<'CONF'
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
CONF
fi

if [ "$APPLY_SSH" = 1 ]; then
  # Refuse to lock everyone out: some account with administrator rights must already have a key.
  keyed=0
  for home in /root $(getent group sudo | cut -d: -f4 | tr ',' ' ' | xargs -r -n1 sh -c 'getent passwd "$0" | cut -d: -f6'); do
    [ -s "$home/.ssh/authorized_keys" ] && keyed=1
  done
  [ "$keyed" = 1 ] || { echo "no SSH key found for root or a sudo user; not changing SSH settings" >&2; exit 1; }
  conf=/etc/ssh/sshd_config.d/00-tumaini-hardening.conf # first value wins, so sort before other files
  cat > "$conf" <<'CONF'
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin prohibit-password
CONF
  if ! sshd -t; then rm -f "$conf"; echo "sshd rejected the settings; removed $conf" >&2; exit 1; fi
  systemctl reload ssh 2>/dev/null || systemctl reload sshd
  echo "SSH settings applied. Keep this session open and confirm a new login works before closing it."
fi

if cfg="$(sshd -T 2>/dev/null)"; then
  grep -qx 'passwordauthentication no' <<<"$cfg" && ok "SSH password logins are off" || warn "SSH accepts passwords"
  grep -qxE 'permitrootlogin (no|prohibit-password|without-password)' <<<"$cfg" && ok "SSH root password login is off" || warn "SSH allows root to log in with a password"
else
  warn "could not read the effective SSH settings (sshd -T)"
fi

if command -v ufw >/dev/null && ufw status | grep -q '^Status: active'; then
  ok "ufw firewall is active:"; ufw status | sed 's/^/        /'
elif command -v firewall-cmd >/dev/null && firewall-cmd --state >/dev/null 2>&1; then
  ok "firewalld is active:"; firewall-cmd --list-all | sed 's/^/        /'
else
  warn "no ufw or firewalld found active (check the provider's firewall). Note Docker's published ports bypass ufw"
fi

public="$(ss -ltnH 2>/dev/null | awk '{print $4}' | grep -vE '^(127\.|\[::1\])' | sed 's/.*://' | sort -un | tr '\n' ' ' || true)"
echo "      ports listening beyond loopback: ${public:-none}"
for p in 5432 5433 18500 18501; do
  grep -qw "$p" <<<"$public" && warn "port $p (database or application) is reachable beyond loopback"
done

if grep -qs 'Unattended-Upgrade "1"' /etc/apt/apt.conf.d/20auto-upgrades; then
  ok "automatic security updates are enabled"
else
  warn "automatic security updates are not enabled"
fi
[ -f /var/run/reboot-required ] && warn "a reboot is required to finish an update"

if [ -f "$APP_DIR/.env" ]; then
  mode="$(stat -c %a "$APP_DIR/.env")"
  [ "$mode" = 600 ] && ok "$APP_DIR/.env is readable only by its owner" || warn "$APP_DIR/.env has mode $mode, expected 600"
fi
echo "      members of the docker group (equivalent to root): $(getent group docker | cut -d: -f4)"

for cert in /etc/letsencrypt/live/*/fullchain.pem; do
  [ -f "$cert" ] || continue
  openssl x509 -checkend $((21 * 86400)) -noout -in "$cert" >/dev/null \
    && ok "certificate valid for over 21 days: $cert" || warn "certificate expires within 21 days: $cert"
done
if systemctl list-timers --all 2>/dev/null | grep -q certbot || [ -f /etc/cron.d/certbot ]; then
  ok "certificate renewal is scheduled"
else
  warn "no certificate renewal timer or cron entry found"
fi
exit "$WARN"
