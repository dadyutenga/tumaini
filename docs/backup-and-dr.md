# Backup, Disaster Recovery & Business Continuity Runbook

This runbook defines recovery objectives, backup schedules, encryption, offsite storage, restoration testing, and rollback procedures for Tumaini BMS.

## 1. Recovery Objectives

| Metric | Target | Mechanism |
| --- | --- | --- |
| **RPO** (Recovery Point Objective) | **≤ 1 hour** for the database, **≤ 24 hours** for uploaded files | Hourly database dump at `:45`, full backup nightly at `01:15` server time and before every release. Less than an hour needs WAL archiving, which is not set up |
| **RTO** (Recovery Time Objective) | **≤ 30 minutes** | `deploy/restore.sh` and `deploy/deploy.sh`. Both restore scripts print how long they took; the monthly drill log records it on production data |

The RPO only holds for a lost server when `BACKUP_UPLOAD_CMD` copies each backup elsewhere.

## 2. Backup Schedule & Retention Policy

| Backup Type | Schedule | Local Retention (`/var/backups/tumaini`) | Offsite Retention (`BACKUP_UPLOAD_CMD`) |
| --- | --- | --- | --- |
| Nightly database dump and uploaded files | Daily at `01:15` via `/etc/cron.d/tumaini-backup` (`deploy/backup-prod.sh nightly`) | 30 days (`BACKUP_KEEP_DAYS=30`) | 90 days daily + 12 months monthly snapshots (object storage lifecycle rule) |
| Hourly database dump | Every hour at `:45` (`backup-prod.sh hourly`) | 3 days (`BACKUP_HOURLY_KEEP_DAYS=3`) | 7 days (object storage lifecycle rule) |
| Pre-deploy snapshot | Before every release via `deploy/deploy.sh` (`deploy/backup-prod.sh predeploy`) | 30 days | Synced immediately via `BACKUP_UPLOAD_CMD` |
| Monthly restore drill | `02:30` on the 1st of every month (`restore-drill.sh`) | Temporary database (`bms_restore_verify_*`), dropped afterwards | Run it by hand on a copy fetched from offsite storage as well |

**Audit log and login history** are to be kept for **5 years** (decided 2026-11-27, matching the five-year tax record-keeping period; recorded in `app.retention_policies`). Nothing deletes them automatically, so today they are kept indefinitely. Any future deletion must archive entries first and keep seal verification working.

## 3. Backup Contents, Encryption & Offsite Storage

- **Contents**: each run writes the database (`bms-<label>-<time>.dump`). All but the hourly runs also write the uploaded files volume (`bms-files-<label>-<time>.tar`) and the audit seal hashes (`bms-anchors-<label>-<time>.tsv`). Each file has a `.sha256` file and, when encrypted, a `.mac` file.
- **Encryption at rest**: with `BACKUP_ENCRYPTION_KEY` set, both are encrypted while they are written (**AES-256-CBC, PBKDF2-SHA256, 600,000 iterations**, random salt) as `*.enc`, mode `0600` in a `0700` directory. The `.mac` file (HMAC-SHA256 under a key derived from the passphrase) is checked before anything is restored, so a file altered without the key is refused. A backup made before `.mac` files existed needs `BACKUP_ALLOW_UNAUTHENTICATED=1`. Use offsite storage with versioning or object lock so backups cannot simply be deleted.
- **Strict mode**: `BACKUP_REQUIRE_ENCRYPTION=1` makes a backup fail without a key. New servers get it from `.env.production.example`. **An existing server's `.env` is never rewritten**: add the key and this flag by hand.
- **Key custody**: keep a copy of the whole of `/opt/tumaini/.env` in a password manager or KMS, apart from both the server and the backup bucket. `BACKUP_ENCRYPTION_KEY` decrypts the backups; `DATA_ENCRYPTION_KEY` (older servers: `MFA_ENCRYPTION_KEY`; `JWT_SECRET` when both are empty) protects the national ID numbers inside them. Do not add or change that key on a live system.
- **Offsite storage**: set `BACKUP_UPLOAD_CMD`, single-quoted so the shell keeps `$1`, for example `BACKUP_UPLOAD_CMD='rclone copy "$1" offsite-s3:tumaini-backups/'`. Without it backups exist only on the server and the script warns.

## 4. Monitoring & Backup Failure Alerts

- A failed or refused backup removes partial files, logs to `/var/log/tumaini-backup.log` and, when `BACKUP_ALERT_WEBHOOK_URL` is set, posts a `backup_failed` JSON event to it.
- A backup that never runs (cron stopped, server down) sends no failure. Create two checks in an external monitor and set `BACKUP_HEARTBEAT_URL` (expected every 24 hours) and `BACKUP_HOURLY_HEARTBEAT_URL` (every hour); the monitor alerts when a request stops arriving.
- A failed monthly restore drill posts `restore_drill_failed` to the same webhook.

## 5. Automated Restore Verification & Post-Recovery Reconciliation

Verify any database backup without touching the live database:

```bash
cd /opt/tumaini
./restore-verify.sh /var/backups/tumaini/bms-nightly-YYYYMMDDTHHMMSSZ.dump.enc
```

It checks the SHA-256 digest and the `.mac`, decrypts into a `0600` temporary file, creates any missing database roles, restores into a temporary database and fails unless:

- every object in `app`, `iam` and `audit` is owned by `bms_owner` (row-level security depends on it);
- each money account balance equals the sum of its ledger entries;
- each stock balance equals the sum of its movements;
- no audit hash-chain seal is broken.

`restore-drill.sh` runs this monthly on the newest nightly backup. Also run it now and then on a copy fetched from offsite storage, with its `.sha256` and `.mac` files.

To check that audit history has not been rewritten, even by someone with database superuser access, compare the live database with seal hashes from an older offsite backup:

```bash
./verify-anchors.sh bms-anchors-nightly-<time>.tsv.enc
```

## 6. Production Disaster Recovery Procedure

1. On a new server run `scripts/provision-server.sh`, then replace the generated `/opt/tumaini/.env` with the saved one.
2. Fetch the newest database backup (hourly or nightly) and the newest files backup, each with its `.sha256` and `.mac` files.
3. Restore both. The script verifies the backup first, stops `api` and `web`, restores into a new database, keeps the previous one as `bms_before_restore_<time>`, sets the login role passwords and unpacks the files:
   ```bash
   cd /opt/tumaini
   ./restore.sh bms-nightly-<time>.dump.enc bms-files-nightly-<time>.tar.enc --confirm
   ```
   Do not restore by hand with `pg_restore --no-owner`: it leaves the objects owned by the superuser, which defeats row-level security.
4. Start the application with `./deploy.sh <known_good_tag>` and check `/readyz`.
5. Reconcile what happened after the backup was taken (cash counts, mobile money and bank statements) and re-enter it. Once satisfied, drop `bms_before_restore_<time>`.

## 7. Application Rollback & Migration Recovery

- **Application rollback**: `deploy/deploy.sh <tag>` records `.deployed_tag` and restarts the previous `api` and `web` images if the new ones are unhealthy. Manual rollback:
  ```bash
  cd /opt/tumaini && ./deploy.sh <previous_sha_tag>
  ```
- **Database migrations** are forward-only (`backend/database/migrations/`). Never edit an applied migration. After a bad schema release:
  1. If it is backward compatible, roll forward with a corrective migration (`NNN_*.sql`).
  2. Otherwise restore the `predeploy` backup with `restore.sh` and deploy the previous tag.
