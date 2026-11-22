# Deployment

Production runs at **https://bms.qarmalab.dev** on `rida-server` (Ubuntu 24.04, shared with other apps).

```
Cloudflare (proxied) -> host nginx :443 -> 127.0.0.1:18501  web  (static frontend, nginx)
                                        -> 127.0.0.1:18500  api  (Go, /api/*, /healthz)
                                           postgres (internal network only, never published)
```

Everything lives in `/opt/tumaini` (compose file, `.env`, `deploy.sh`, `backup-prod.sh`). The host's nginx and
certbot (webroot, auto-renewing) terminate TLS; the vhost is `deploy/nginx/bms.conf`.

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
| `deploy.yml` | push to the `deploy` branch, or run it by hand | verify (gofmt, vet, build, migrations on an empty DB, OpenAPI, frontend build and tests) -> build and push `ghcr.io/<repo>/{api,web}:sha-<12>` -> SSH deploy |

The deploy gate is deliberately the fast checks that pass today. The full Go integration suite and frontend lint
are in the PR workflows; make them green and add them to the `verify-*` jobs to gate deploys on them.

`deploy.sh <tag>` on the server: pull images, start Postgres, take a pre-deploy backup, run migrations (as the
Postgres superuser, inside the private network), set the `bms_api` / `bms_jobs` passwords, start api and web, wait
for the API to report healthy. If the new tag is not healthy it restarts the previous one. Migrations are
forward-only, so a rollback restores the previous images, not the schema; restore a backup if a migration must be undone.

### Repository secrets (Settings > Secrets and variables > Actions)

| Secret | Value |
| --- | --- |
| `SSH_HOST` | `37.60.238.125` |
| `SSH_PORT` | `6969` |
| `SSH_USER` | `deploy` |
| `SSH_PRIVATE_KEY` | private half of the CI key authorised for `deploy` in `/home/deploy/.ssh/authorized_keys` |
| `SSH_KNOWN_HOSTS` | `ssh-keyscan -p 6969 -t ed25519 37.60.238.125` (output is `[host]:port` form) |

The workflow logs in to GHCR on the server with the job's `GITHUB_TOKEN` for the pull and logs out afterwards.
The workflow's `production` environment can hold required reviewers if deploys should need approval.

## First-time server setup

```bash
scripts/provision-server.sh rida-server bms.qarmalab.dev path/to/ci-key.pub admin@example.com
```

Idempotent. It installs Docker only if missing, creates the `deploy` user, writes `/opt/tumaini/.env` with generated
secrets (never overwritten), installs the nightly backup cron (`/etc/cron.d/tumaini-backup`, 01:15, 30 days kept in
`/var/backups/tumaini`) and the nginx vhost plus certificate. It does not touch the firewall or other sites.

Bootstrap deploy without GHCR (build on the server, then `PULL=0 /opt/tumaini/deploy.sh <tag>` as `deploy`).

## Operations

```bash
ssh rida-server
su - deploy -c 'cd /opt/tumaini && docker compose -f docker-compose.prod.yml ps'
su - deploy -c 'cd /opt/tumaini && docker compose -f docker-compose.prod.yml logs -f api'
su - deploy -c 'cd /opt/tumaini && ./backup-prod.sh manual'
```

Restore: `docker compose -f docker-compose.prod.yml exec -T postgres pg_restore -U postgres -d bms --clean --if-exists < dump`.
Set SMTP and SMS values in `/opt/tumaini/.env` and redeploy to switch on email and SMS.

## Known issues found while deploying

- The backend image had no `tzdata`, so the API refused to start (`DEFAULT_TIMEZONE`). Fixed in `backend/Dockerfile`.
- The identity module still checks legacy permission keys (`users.manage`, `roles.view`) that the new schema does not
  define, so no role can list roles or create users through the API yet. Demo users were inserted directly.
- Many `internal/app` integration tests fail (for example `invalid_phone` in the registration helper) and the frontend
  has 13 prettier errors; both predate this pipeline.
