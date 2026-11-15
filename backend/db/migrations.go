// Package db embeds the versioned SQL migrations.
package db

import "embed"

//go:embed migrations/*.sql
var Migrations embed.FS
