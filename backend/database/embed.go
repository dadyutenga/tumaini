// Package database embeds the approved BMS schema migrations and the frozen legacy ones.
package database

import "embed"

// Migrations is the authoritative schema: 000 (roles, extensions, schemas) then 010..150.
//
//go:embed migrations/*.sql
var Migrations embed.FS

// Legacy holds the pre-replacement schema (golang-migrate files). It is kept for auditability and
// so the legacy-data migration tool can rebuild a disposable copy of the old database.
//
//go:embed legacy/*.sql
var Legacy embed.FS
