# Shared by backup-prod.sh, restore-verify.sh, restore.sh and the checks. Source it; do not run it.

bk_encrypt() { openssl enc -aes-256-cbc -pbkdf2 -iter 600000 -salt -pass env:BACKUP_ENCRYPTION_KEY; }
bk_decrypt() { openssl enc -d -aes-256-cbc -pbkdf2 -iter 600000 -pass env:BACKUP_ENCRYPTION_KEY -in "$1"; }

# bk_plain <file>: the backup's plaintext on stdout.
bk_plain() {
  if [[ "$1" == *.enc ]]; then
    : "${BACKUP_ENCRYPTION_KEY:?set BACKUP_ENCRYPTION_KEY to decrypt $1}"
    bk_decrypt "$1"
  else
    cat "$1"
  fi
}

# bk_mac <file>: HMAC-SHA256 of the file under a key stretched from the passphrase. CBC encryption
# does not detect tampering; this does. Key material only ever travels through pipes and shell
# builtins, never a command line that other users of the server could list.
bk_mac() {
  local file="$1" key pad ipad="" opad="" inner innerx="" i byte
  key="$(openssl enc -aes-256-cbc -pbkdf2 -iter 600000 -S 626d736d61637631 -P -pass env:BACKUP_ENCRYPTION_KEY | sed -n 's/^key=//p')"
  [ "${#key}" = 64 ] || { echo "could not derive the backup authentication key" >&2; return 1; }
  key+="$(printf '0%.0s' {1..64})" # pad to the 64-byte SHA-256 block
  for ((i = 0; i < 128; i += 2)); do
    byte=$((16#${key:i:2}))
    printf -v pad '\\x%02x' $((byte ^ 0x36)); ipad+="$pad"
    printf -v pad '\\x%02x' $((byte ^ 0x5c)); opad+="$pad"
  done
  inner="$({ printf '%b' "$ipad"; cat "$file"; } | sha256sum | cut -d' ' -f1)"
  for ((i = 0; i < 64; i += 2)); do innerx+="\\x${inner:i:2}"; done
  { printf '%b' "$opad"; printf '%b' "$innerx"; } | sha256sum | cut -d' ' -f1
}

# bk_check <file>: fails unless the file matches its checksum and, when encrypted, its .mac.
# Digests are compared, not paths, so a copy fetched from elsewhere verifies.
bk_check() {
  local file="$1" got
  if [ -f "$file.sha256" ]; then
    got="$(sha256sum "$file" | cut -d' ' -f1)"
    [ "$(cut -d' ' -f1 "$file.sha256")" = "$got" ] || { echo "checksum mismatch: $file" >&2; return 1; }
  else
    echo "WARNING: no checksum file for $file" >&2
  fi
  [[ "$file" == *.enc ]] || return 0
  : "${BACKUP_ENCRYPTION_KEY:?set BACKUP_ENCRYPTION_KEY to check $file}"
  if [ -f "$file.mac" ]; then
    got="$(bk_mac "$file")"
    [ "$(cat "$file.mac")" = "$got" ] || { echo "authentication failed (altered file or wrong key): $file" >&2; return 1; }
  elif [ "${BACKUP_ALLOW_UNAUTHENTICATED:-0}" != 1 ]; then
    echo "no .mac file for $file; for a backup made before .mac files, set BACKUP_ALLOW_UNAUTHENTICATED=1" >&2
    return 1
  fi
}

# bk_alert <event> <detail> <exit code>: POSTs to BACKUP_ALERT_WEBHOOK_URL when it is set.
bk_alert() {
  [ -n "${BACKUP_ALERT_WEBHOOK_URL:-}" ] || return 0
  curl -fsS -m 10 -X POST -H "Content-Type: application/json" \
    -d "{\"event\":\"$1\",\"label\":\"$2\",\"host\":\"$(hostname -f 2>/dev/null || hostname)\",\"exit_code\":$3,\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}" \
    "$BACKUP_ALERT_WEBHOOK_URL" >/dev/null || true
}
