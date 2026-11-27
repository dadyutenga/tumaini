// Package deps holds the shared services every module is built from.
// app.New builds one Deps value and passes it to each module's Mount function,
// so adding a module dependency never changes the wiring of other modules.
package deps

import (
	"bms/internal/config"
	"bms/internal/kernel/approval"
	"bms/internal/kernel/audit"
	"bms/internal/kernel/notify"
	"bms/internal/kernel/outbound"
	"bms/internal/platform/auth"
	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/jobs"
	"bms/internal/platform/storage"
)

type Deps struct {
	Config   config.Config
	DB       *db.DB
	Audit    *audit.Service
	Notify   *notify.Service
	Outbound *outbound.Service
	Approval *approval.Service
	Clock    clock.Clock
	Store    storage.Store
	// Uploads is where files supplied by people are saved. It is Store, behind the virus scanner when
	// one is configured; files the system writes itself go to Store.
	Uploads storage.Store
	Jobs    *jobs.Client
	Hash    auth.Params
}
