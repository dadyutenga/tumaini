package services

import (
	"bms/internal/kernel/deps"
	"bms/internal/platform/jobs"
	"bms/internal/platform/sender"
)

// Build creates the module from the shared services and registers its daily
// reminder job. The app registers the public approval routes before the signed-in
// group and the private routes after it.
func Build(d deps.Deps) *Handler {
	_, sms := sender.FromConfig(d.Config)
	idSecret, idKeyID := d.Config.DataKey()
	svc := &Service{DB: d.DB, Audit: d.Audit, Notify: d.Notify, Outbound: d.Outbound, Clock: d.Clock, Store: d.Store, SMS: sms,
		IDs: NewIDProtector(idSecret, idKeyID)}
	base := ""
	if len(d.Config.CORSOrigins) > 0 {
		base = d.Config.CORSOrigins[0]
	}
	h := &Handler{Svc: svc, BaseURL: base}
	d.Jobs.Register(jobs.ServiceReminders, svc.RunReminders)
	d.Jobs.Daily(jobs.ServiceReminders, "08:15")
	d.Jobs.Register(jobs.ProtectNationalIDs, svc.ProtectNationalIDs)
	d.Jobs.Daily(jobs.ProtectNationalIDs, "00:00") // due as soon as a new release starts
	return h
}
