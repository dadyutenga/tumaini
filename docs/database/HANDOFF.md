# Agent Handoff — what is left after the schema transition

**Last updated**: 2026-11-26
**Branch**: `main`

Backend integration tests (Batches 1–5), the OpenAPI contract, and the web client re-audit for services, reports, inbox, and system are done and pushed.

Two checklist items in `docs/database/database-transition.md` are still open:

- Legacy data migration tool, mapping, and reconciliation. There is no production database to move; the legacy development database was dropped.
- Deployment, cutover, rollback, and backup documents. Not written, because there is no production cutover to describe yet.

Do not reopen the ported modules or the integration tests unless a new bug shows up.
