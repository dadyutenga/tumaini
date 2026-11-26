package system

import (
	"github.com/gofiber/fiber/v3"

	"bms/internal/kernel/deps"
	"bms/internal/platform/jobs"
	"bms/internal/platform/sender"
)

// Mount registers the system jobs and their daily schedule. It has no routes.
func Mount(_ fiber.Router, d deps.Deps) *Service {
	svc := &Service{DB: d.DB, Notify: d.Notify, Outbound: d.Outbound, Store: d.Store, Clock: d.Clock}
	svc.Email, svc.SMS = sender.FromConfig(d.Config)
	d.Jobs.Register(jobs.BusinessExport, svc.BusinessExport)
	d.Jobs.Register(jobs.DebtReminders, svc.DebtReminders)
	d.Jobs.Register(jobs.OwnerDailySummary, svc.OwnerDailySummary)
	d.Jobs.Register(jobs.LedgerBalanceCheck, svc.LedgerBalanceCheck)
	d.Jobs.Register(jobs.StockRebuildCheck, svc.StockRebuildCheck)
	d.Jobs.Register(jobs.SessionCleanup, svc.SessionCleanup)
	d.Jobs.Register(jobs.SendNotification, svc.SendNotification)
	d.Jobs.Daily(jobs.DebtReminders, "08:00")
	d.Jobs.Daily(jobs.OwnerDailySummary, "end")
	d.Jobs.Daily(jobs.LedgerBalanceCheck, "02:00")
	d.Jobs.Daily(jobs.StockRebuildCheck, "02:30")
	d.Jobs.Daily(jobs.SessionCleanup, "03:00")
	return svc
}
