# Deployment

Production runs on a shared Ubuntu 24.04 server. Its name and address are deliberately not recorded here; keep
them with the server credentials.

```
Cloudflare (proxied) -> host nginx :443 -> 127.0.0.1:18501  web  (static frontend, nginx)
                                        -> 127.0.0.1:18500  api  (Go, /api/*, /healthz)
                                           postgres (internal network only, never published)
```

Everything lives in `/opt/tumaini` (compose file, `.env`, `deploy.sh`, `backup-prod.sh`, `restore.sh`). The host's nginx and
certbot (webroot, auto-renewing) terminate TLS; the vhost is `deploy/nginx/bms.conf`. The deploy workflow does not
install it: after changing that file, re-run `scripts/provision-server.sh`.

## Branches and releases

There are two branches. `main` is where work lands; `deploy` is the release line. To release:

```bash
git checkout deploy && git merge --ff-only main && git push
```

That push runs the Deploy workflow. Pushes to `main` only run the CI workflows.

## Pipeline

| Workflow | When | What |
| --- | --- | --- |
| `backend-ci.yml`, `frontend-ci.yml` | pull requests and pushes touching those paths | full checks (lint, `go test -race`, ...) |
| `deploy.yml` | push to the `deploy` branch, or run it by hand | both CI workflows (tests, vulnerability scans, lint, builds) -> build images -> scan them -> push `ghcr.io/<repo>/{api,web}:sha-<12>` -> SSH deploy |

A failing test, a known vulnerability or a high-severity image finding stops the release. Third-party actions are
pinned to commit hashes; update the hash and its version comment together.

The `deploy` user is in the `docker` group, which is equivalent to root, so whoever holds the CI key controls the
server. Add required reviewers to the `production` environment (repository Settings > Environments) so a push alone
cannot release.

`deploy.sh <tag>` on the server: pull images, start Postgres, take a pre-deploy backup, run migrations (as the
Postgres superuser, inside the private network), set the `bms_api` / `bms_jobs` passwords, start api and web, wait
for the API to report healthy. If the new tag is not healthy it restarts the previous one. Migrations are
forward-only, so a rollback restores the previous images, not the schema; restore a backup if a migration must be undone.

### Repository secrets (Settings > Secrets and variables > Actions)

| Secret | Value |
| --- | --- |
| `SSH_HOST` | the server address |
| `SSH_PORT` | the SSH port |
| `SSH_USER` | `deploy` |
| `SSH_PRIVATE_KEY` | private half of the CI key authorised for `deploy` in `/home/deploy/.ssh/authorized_keys` |
| `SSH_KNOWN_HOSTS` | `ssh-keyscan -p <port> -t ed25519 <address>` (output is `[host]:port` form) |

The workflow logs in to GHCR on the server with the job's `GITHUB_TOKEN` for the pull and logs out afterwards.
The workflow's `production` environment can hold required reviewers if deploys should need approval.

## First-time server setup

```bash
scripts/provision-server.sh <ssh-host> <domain> path/to/ci-key.pub admin@example.com
```

Idempotent. It installs Docker only if missing, creates the `deploy` user, writes `/opt/tumaini/.env` with generated
secrets (never overwritten), installs the backup cron (`/etc/cron.d/tumaini-backup`: nightly at 01:15 with 30 days kept in
`/var/backups/tumaini`, hourly database dumps, a monthly restore drill) and the nginx vhost plus certificate. It does not touch the firewall or other sites.

Bootstrap deploy without GHCR (build on the server, then `PULL=0 /opt/tumaini/deploy.sh <tag>` as `deploy`).

## Operations

```bash
ssh <ssh-host>
su - deploy -c 'cd /opt/tumaini && docker compose -f docker-compose.prod.yml ps'
su - deploy -c 'cd /opt/tumaini && docker compose -f docker-compose.prod.yml logs -f api'
su - deploy -c 'cd /opt/tumaini && ./backup-prod.sh manual'
```

Restore & DR runbook: see [backup-and-dr.md](./backup-and-dr.md) (encrypted backups, `restore-verify.sh`, RPO/RTO, rollback, and migration recovery).
Set SMTP and SMS values in `/opt/tumaini/.env` and redeploy to switch on email and SMS.

## Server checks

| Command (in `/opt/tumaini`) | What it does |
| --- | --- |
| `sudo ./host-check.sh` | Reports SSH, firewall, open ports, updates, file permissions and certificates. `--apply-updates` and `--apply-ssh` change those two things only when asked |
| `./data-check.sh` | Read-only: missing or altered uploads, debts hidden by archiving, clear-text national IDs, ledger, stock and audit integrity, backup settings |
| `./restore-drill.sh` | Restores the newest nightly backup into a temporary database (also monthly by cron) |
| `./verify-anchors.sh <file>` | Compares the audit trail with seal hashes saved in an earlier backup |

After a change to `deploy/server-setup.sh` or the nginx vhost (new cron jobs, the restricted CI key), re-run
`scripts/provision-server.sh`.

## Virus scanning (optional)

Set `CLAMD_ADDR=clamav:3310` in `.env` and deploy. ClamAV needs about 3 GB of free memory. While it starts, or if it
stops, uploads are refused rather than stored unchecked.
