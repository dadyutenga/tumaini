package services

import (
	"io"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"bms/internal/platform/auth"
	"bms/internal/platform/export"
	"bms/internal/platform/httpx"
	"bms/internal/platform/middleware"
)

type Handler struct {
	Svc     *Service
	BaseURL string
}

// call runs a service function that returns one value.
func call(fn func(c fiber.Ctx, p *auth.Principal) (any, error)) fiber.Handler {
	return func(c fiber.Ctx) error {
		v, err := fn(c, middleware.Principal(c))
		return httpx.OK(c, v, err)
	}
}

func make201(fn func(c fiber.Ctx, p *auth.Principal) (any, error)) fiber.Handler {
	return func(c fiber.Ctx) error {
		v, err := fn(c, middleware.Principal(c))
		return httpx.Created(c, v, err)
	}
}

// send runs an idempotent service function that returns status and body.
func send(fn func(c fiber.Ctx, p *auth.Principal) (int, []byte, error)) fiber.Handler {
	return func(c fiber.Ctx) error {
		st, body, err := fn(c, middleware.Principal(c))
		return httpx.Send(c, st, body, err)
	}
}

func pdfOut(c fiber.Ctx, name string, body []byte, err error) error {
	if err != nil {
		return err
	}
	return export.File(c, name, "application/pdf", body)
}

func idp(c fiber.Ctx) (uuid.UUID, error) { return httpx.UUIDParam(c, "id") }

// bodyFn binds the request body into T and passes it with the :id param.
func withID[T any](fn func(c fiber.Ctx, p *auth.Principal, id uuid.UUID, in T) (any, error)) fiber.Handler {
	return call(func(c fiber.Ctx, p *auth.Principal) (any, error) {
		id, err := idp(c)
		if err != nil {
			return nil, err
		}
		var in T
		if err := httpx.Bind(c, &in); err != nil {
			return nil, err
		}
		return fn(c, p, id, in)
	})
}

func withIDOpt[T any](fn func(c fiber.Ctx, p *auth.Principal, id uuid.UUID, in T) (any, error)) fiber.Handler {
	return call(func(c fiber.Ctx, p *auth.Principal) (any, error) {
		id, err := idp(c)
		if err != nil {
			return nil, err
		}
		var in T
		if err := httpx.BindOptional(c, &in); err != nil {
			return nil, err
		}
		return fn(c, p, id, in)
	})
}

func withIDOnly(fn func(c fiber.Ctx, p *auth.Principal, id uuid.UUID) (any, error)) fiber.Handler {
	return call(func(c fiber.Ctx, p *auth.Principal) (any, error) {
		id, err := idp(c)
		if err != nil {
			return nil, err
		}
		return fn(c, p, id)
	})
}

type reasonIn struct {
	Reason string `json:"reason" validate:"required,max=300"`
}

func (h *Handler) paged(fn func(c fiber.Ctx, p *auth.Principal, page httpx.Page) ([]map[string]any, int, error)) fiber.Handler {
	return func(c fiber.Ctx) error {
		page := httpx.PageFrom(c)
		list, total, err := fn(c, middleware.Principal(c), page)
		return httpx.Paged(c, list, page, total, err)
	}
}

func uuidQ(c fiber.Ctx, key string) *uuid.UUID {
	id, err := httpx.QueryUUID(c, key)
	if err != nil {
		return nil
	}
	return id
}

// checkQ validates the uuid query parameters up front so a bad value is 400, not ignored.
func checkQ(c fiber.Ctx, keys ...string) error {
	for _, k := range keys {
		if _, err := httpx.QueryUUID(c, k); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) Register(r fiber.Router) {
	s := h.Svc
	anyService := middleware.RequireAny("clients.view", "projects.view", "service_catalog.manage", "service_billing.view", "design.view", "service_billing.manage")

	// Clients
	r.Get("/clients", middleware.Require("clients.view"), h.paged(func(c fiber.Ctx, p *auth.Principal, page httpx.Page) ([]map[string]any, int, error) {
		return s.ListClients(c.Context(), p, c.Query("q"), c.Query("status"), httpx.QueryBool(c, "is_active"), page)
	}))
	r.Post("/clients", middleware.Require("clients.manage"), make201(func(c fiber.Ctx, p *auth.Principal) (any, error) {
		var in ClientIn
		if err := httpx.Bind(c, &in); err != nil {
			return nil, err
		}
		return s.CreateClient(c.Context(), p, in)
	}))
	r.Get("/clients/:id", middleware.Require("clients.view"), withIDOnly(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID) (any, error) {
		return s.GetClient(c.Context(), p, id)
	}))
	r.Put("/clients/:id", middleware.Require("clients.manage"), withID(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID, in ClientIn) (any, error) {
		return s.UpdateClient(c.Context(), p, id, in)
	}))
	r.Post("/clients/:id/convert", middleware.Require("clients.manage"), withIDOnly(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID) (any, error) {
		return s.SetClientStatus(c.Context(), p, id, "active")
	}))
	r.Post("/clients/:id/deactivate", middleware.Require("clients.manage"), withIDOnly(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID) (any, error) {
		return s.DeactivateClient(c.Context(), p, id)
	}))
	r.Get("/clients/:id/statement", middleware.Require("service_billing.view"), func(c fiber.Ctx) error {
		id, err := idp(c)
		if err != nil {
			return err
		}
		from, to, err := httpx.Range(c)
		if err != nil {
			return err
		}
		data, err := s.ClientStatement(c.Context(), middleware.Principal(c), id, from, to)
		if err != nil {
			return err
		}
		switch strings.ToLower(c.Query("format")) {
		case "", "json":
			return c.JSON(httpx.Data(data))
		case "pdf":
			return export.File(c, "client-statement.pdf", "application/pdf", statementPDF(data))
		}
		return httpx.BadRequest("invalid_query", "format must be json or pdf")
	})

	// Service catalog
	r.Get("/service-catalog", anyService, h.paged(func(c fiber.Ctx, p *auth.Principal, page httpx.Page) ([]map[string]any, int, error) {
		all := false
		if b := httpx.QueryBool(c, "all"); b != nil {
			all = *b
		}
		return s.ListCatalog(c.Context(), p, c.Query("q"), c.Query("category"), all, page)
	}))
	r.Post("/service-catalog", middleware.Require("service_catalog.manage"), make201(func(c fiber.Ctx, p *auth.Principal) (any, error) {
		var in CatalogIn
		if err := httpx.Bind(c, &in); err != nil {
			return nil, err
		}
		return s.CreateCatalog(c.Context(), p, in)
	}))
	r.Get("/service-catalog/:id", anyService, withIDOnly(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID) (any, error) {
		return s.GetCatalog(c.Context(), p, id)
	}))
	r.Put("/service-catalog/:id", middleware.Require("service_catalog.manage"), withID(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID, in CatalogIn) (any, error) {
		return s.UpdateCatalog(c.Context(), p, id, in, in.Stages != nil || in.Documents != nil || in.PackageServices != nil)
	}))
	r.Post("/service-catalog/:id/deactivate", middleware.Require("service_catalog.manage"), withIDOnly(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID) (any, error) {
		return s.SetCatalogActive(c.Context(), p, id, false)
	}))
	r.Post("/service-catalog/:id/activate", middleware.Require("service_catalog.manage"), withIDOnly(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID) (any, error) {
		return s.SetCatalogActive(c.Context(), p, id, true)
	}))

	// Projects
	r.Get("/projects", middleware.Require("projects.view"), h.paged(func(c fiber.Ctx, p *auth.Principal, page httpx.Page) ([]map[string]any, int, error) {
		if err := checkQ(c, "client_id", "owner_id"); err != nil {
			return nil, 0, err
		}
		late := httpx.QueryBool(c, "late")
		return s.ListProjects(c.Context(), p, ProjectFilter{Q: c.Query("q"), Status: c.Query("status"), ClientID: uuidQ(c, "client_id"), OwnerID: uuidQ(c, "owner_id"), Late: late != nil && *late}, page)
	}))
	r.Get("/projects/board", middleware.Require("projects.view"), call(func(c fiber.Ctx, p *auth.Principal) (any, error) { return s.ProjectBoard(c.Context(), p) }))
	r.Post("/projects", middleware.Require("projects.manage"), make201(func(c fiber.Ctx, p *auth.Principal) (any, error) {
		var in ProjectIn
		if err := httpx.Bind(c, &in); err != nil {
			return nil, err
		}
		return s.CreateProject(c.Context(), p, in)
	}))
	r.Get("/projects/:id", middleware.Require("projects.view"), withIDOnly(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID) (any, error) {
		return s.GetProject(c.Context(), p, id)
	}))
	r.Put("/projects/:id", middleware.Require("projects.manage"), withID(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID, in ProjectPatch) (any, error) {
		return s.UpdateProject(c.Context(), p, id, in)
	}))
	r.Post("/projects/:id/stages", middleware.Require("projects.manage"), withID(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID, in StageIn2) (any, error) {
		return s.AddStage(c.Context(), p, id, in)
	}))
	r.Put("/projects/:id/stages/:stage_id", middleware.Require("projects.manage"), call(func(c fiber.Ctx, p *auth.Principal) (any, error) {
		id, err := idp(c)
		if err != nil {
			return nil, err
		}
		stage, err := httpx.UUIDParam(c, "stage_id")
		if err != nil {
			return nil, err
		}
		var in StagePatch
		if err := httpx.Bind(c, &in); err != nil {
			return nil, err
		}
		return s.UpdateStage(c.Context(), p, id, stage, in)
	}))
	r.Post("/projects/:id/stages/:stage_id/design-jobs", middleware.Require("design.manage"), make201(func(c fiber.Ctx, p *auth.Principal) (any, error) {
		id, err := idp(c)
		if err != nil {
			return nil, err
		}
		stage, err := httpx.UUIDParam(c, "stage_id")
		if err != nil {
			return nil, err
		}
		var in BrandingIn
		if err := httpx.Bind(c, &in); err != nil {
			return nil, err
		}
		return s.CreateStageJobs(c.Context(), p, id, stage, in)
	}))
	r.Get("/projects/:id/notes", middleware.Require("projects.view"), withIDOnly(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID) (any, error) {
		return s.ListNotes(c.Context(), p, id)
	}))
	r.Post("/projects/:id/notes", middleware.Require("projects.manage"), func(c fiber.Ctx) error {
		id, err := idp(c)
		if err != nil {
			return err
		}
		var in NoteIn
		if err := httpx.Bind(c, &in); err != nil {
			return err
		}
		out, err := s.AddNote(c.Context(), middleware.Principal(c), id, in)
		return httpx.Created(c, out, err)
	})
	r.Post("/projects/:id/updates", middleware.Require("projects.manage"), withID(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID, in UpdateIn) (any, error) {
		return s.SendUpdate(c.Context(), p, id, in)
	}))
	r.Post("/projects/:id/payments", middleware.Require("service_billing.manage"), middleware.Idempotent(), send(func(c fiber.Ctx, p *auth.Principal) (int, []byte, error) {
		id, err := idp(c)
		if err != nil {
			return 0, nil, err
		}
		var in PayIn
		if err := httpx.Bind(c, &in); err != nil {
			return 0, nil, err
		}
		return s.PayProject(c.Context(), p, id, in)
	}))

	// Documents
	docRead := middleware.RequireAny("clients.view", "projects.view")
	docWrite := middleware.RequireAny("clients.manage", "projects.manage")
	r.Get("/client-documents", docRead, h.paged(func(c fiber.Ctx, p *auth.Principal, page httpx.Page) ([]map[string]any, int, error) {
		if err := checkQ(c, "client_id", "project_id"); err != nil {
			return nil, 0, err
		}
		return s.ListDocuments(c.Context(), p, uuidQ(c, "client_id"), uuidQ(c, "project_id"), c.Query("status"), c.Query("q"), page)
	}))
	r.Post("/client-documents", docWrite, make201(func(c fiber.Ctx, p *auth.Principal) (any, error) {
		var in DocumentIn
		if err := httpx.Bind(c, &in); err != nil {
			return nil, err
		}
		return s.CreateDocument(c.Context(), p, in)
	}))
	r.Put("/client-documents/:id", docWrite, withID(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID, in DocumentPatch) (any, error) {
		return s.UpdateDocument(c.Context(), p, id, in)
	}))

	// Tasks
	r.Get("/tasks", middleware.Require("tasks.view"), h.paged(func(c fiber.Ctx, p *auth.Principal, page httpx.Page) ([]map[string]any, int, error) {
		if err := checkQ(c, "assignee_id", "project_id"); err != nil {
			return nil, 0, err
		}
		f := TaskFilter{Status: c.Query("status"), AssigneeID: uuidQ(c, "assignee_id"), ProjectID: uuidQ(c, "project_id"), From: c.Query("from"), To: c.Query("to")}
		if b := httpx.QueryBool(c, "overdue"); b != nil {
			f.Overdue = *b
		}
		if b := httpx.QueryBool(c, "open"); b != nil {
			f.Open = *b
		}
		return s.ListTasks(c.Context(), p, f, page)
	}))
	r.Post("/tasks", middleware.Require("tasks.manage"), make201(func(c fiber.Ctx, p *auth.Principal) (any, error) {
		var in TaskIn
		if err := httpx.Bind(c, &in); err != nil {
			return nil, err
		}
		return s.CreateTask(c.Context(), p, in)
	}))
	r.Put("/tasks/:id", middleware.RequireAny("tasks.manage", "tasks.view"), withID(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID, in TaskIn) (any, error) {
		_, manage := p.Can("tasks.manage")
		return s.UpdateTask(c.Context(), p, id, in, !manage && !p.IsOwner)
	}))

	// Renewals
	r.Get("/renewals", middleware.Require("renewals.view"), h.paged(func(c fiber.Ctx, p *auth.Principal, page httpx.Page) ([]map[string]any, int, error) {
		if err := checkQ(c, "client_id"); err != nil {
			return nil, 0, err
		}
		within := 0
		if v := c.Query("within_days"); v != "" {
			for _, ch := range v {
				if ch < '0' || ch > '9' {
					return nil, 0, httpx.BadRequest("invalid_query", "within_days must be a number")
				}
				within = within*10 + int(ch-'0')
			}
		}
		return s.ListRenewals(c.Context(), p, c.Query("status"), uuidQ(c, "client_id"), within, page)
	}))
	r.Post("/renewals", middleware.Require("renewals.manage"), make201(func(c fiber.Ctx, p *auth.Principal) (any, error) {
		var in RenewalIn
		if err := httpx.Bind(c, &in); err != nil {
			return nil, err
		}
		return s.CreateRenewal(c.Context(), p, in)
	}))
	r.Put("/renewals/:id", middleware.Require("renewals.manage"), withID(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID, in RenewalPatch) (any, error) {
		return s.UpdateRenewal(c.Context(), p, id, in)
	}))
	r.Post("/renewals/:id/renew", middleware.Require("renewals.manage"), withIDOpt(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID, in RenewIn) (any, error) {
		return s.Renew(c.Context(), p, id, in)
	}))
	r.Post("/renewals/:id/cancel", middleware.Require("renewals.manage"), withIDOnly(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID) (any, error) {
		return s.CancelRenewal(c.Context(), p, id)
	}))
	r.Post("/renewals/:id/remind", middleware.Require("renewals.manage"), withIDOnly(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID) (any, error) {
		return s.RemindClient(c.Context(), p, id)
	}))

	// Quotations
	r.Get("/service-quotations", middleware.Require("service_billing.view"), h.paged(func(c fiber.Ctx, p *auth.Principal, page httpx.Page) ([]map[string]any, int, error) {
		if err := checkQ(c, "client_id"); err != nil {
			return nil, 0, err
		}
		return s.ListQuotations(c.Context(), p, c.Query("status"), uuidQ(c, "client_id"), c.Query("q"), page)
	}))
	r.Post("/service-quotations", middleware.Require("service_billing.manage"), make201(func(c fiber.Ctx, p *auth.Principal) (any, error) {
		var in QuotationIn
		if err := httpx.Bind(c, &in); err != nil {
			return nil, err
		}
		return s.CreateQuotation(c.Context(), p, in)
	}))
	r.Get("/service-quotations/:id", middleware.Require("service_billing.view"), withIDOnly(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID) (any, error) {
		return s.GetQuotation(c.Context(), p, id)
	}))
	r.Put("/service-quotations/:id", middleware.Require("service_billing.manage"), withID(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID, in QuotationIn) (any, error) {
		return s.UpdateQuotation(c.Context(), p, id, in)
	}))
	r.Post("/service-quotations/:id/send", middleware.Require("service_billing.manage"), withIDOnly(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID) (any, error) {
		return s.MoveQuotation(c.Context(), p, id, "sent")
	}))
	r.Post("/service-quotations/:id/reject", middleware.Require("service_billing.manage"), withIDOnly(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID) (any, error) {
		return s.MoveQuotation(c.Context(), p, id, "rejected")
	}))
	r.Post("/service-quotations/:id/accept", middleware.Require("service_billing.manage"), withIDOpt(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID, in AcceptIn) (any, error) {
		return s.AcceptQuotation(c.Context(), p, id, in)
	}))
	r.Get("/service-quotations/:id/pdf", middleware.Require("service_billing.view"), func(c fiber.Ctx) error {
		id, err := idp(c)
		if err != nil {
			return err
		}
		body, err := s.QuotationPDF(c.Context(), middleware.Principal(c), id)
		return pdfOut(c, "quotation.pdf", body, err)
	})

	// Invoices
	r.Get("/service-invoices", middleware.Require("service_billing.view"), h.paged(func(c fiber.Ctx, p *auth.Principal, page httpx.Page) ([]map[string]any, int, error) {
		if err := checkQ(c, "client_id", "project_id"); err != nil {
			return nil, 0, err
		}
		od := httpx.QueryBool(c, "overdue")
		return s.ListInvoices(c.Context(), p, c.Query("status"), uuidQ(c, "client_id"), uuidQ(c, "project_id"), od != nil && *od, c.Query("q"), page)
	}))
	r.Post("/service-invoices", middleware.Require("service_billing.manage"), make201(func(c fiber.Ctx, p *auth.Principal) (any, error) {
		var in InvoiceIn
		if err := httpx.Bind(c, &in); err != nil {
			return nil, err
		}
		return s.CreateInvoice(c.Context(), p, in)
	}))
	r.Get("/service-invoices/:id", middleware.Require("service_billing.view"), withIDOnly(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID) (any, error) {
		return s.GetInvoice(c.Context(), p, id)
	}))
	r.Put("/service-invoices/:id", middleware.Require("service_billing.manage"), withID(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID, in InvoiceIn) (any, error) {
		return s.UpdateInvoice(c.Context(), p, id, in)
	}))
	r.Post("/service-invoices/:id/issue", middleware.Require("service_billing.manage"), withIDOpt(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID, in struct {
		DueDate string `json:"due_date" validate:"omitempty,date"`
	}) (any, error) {
		return s.IssueInvoice(c.Context(), p, id, in.DueDate)
	}))
	r.Post("/service-invoices/:id/void", middleware.Require("service_billing.manage"), withID(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID, in reasonIn) (any, error) {
		return s.VoidInvoice(c.Context(), p, id, in.Reason)
	}))
	r.Post("/service-invoices/:id/payments", middleware.Require("service_billing.manage"), middleware.Idempotent(), send(func(c fiber.Ctx, p *auth.Principal) (int, []byte, error) {
		id, err := idp(c)
		if err != nil {
			return 0, nil, err
		}
		var in PayIn
		if err := httpx.Bind(c, &in); err != nil {
			return 0, nil, err
		}
		return s.PayInvoice(c.Context(), p, id, in)
	}))
	r.Get("/service-invoices/:id/pdf", middleware.Require("service_billing.view"), func(c fiber.Ctx) error {
		id, err := idp(c)
		if err != nil {
			return err
		}
		body, err := s.InvoicePDF(c.Context(), middleware.Principal(c), id)
		return pdfOut(c, "invoice.pdf", body, err)
	})

	// Payments and costs
	r.Get("/service-payments", middleware.Require("service_billing.view"), h.paged(func(c fiber.Ctx, p *auth.Principal, page httpx.Page) ([]map[string]any, int, error) {
		if err := checkQ(c, "client_id", "project_id", "design_job_id"); err != nil {
			return nil, 0, err
		}
		from, to, err := httpx.Range(c)
		if err != nil {
			return nil, 0, err
		}
		return s.ListPayments(c.Context(), p, uuidQ(c, "client_id"), uuidQ(c, "project_id"), uuidQ(c, "design_job_id"), from, to, page)
	}))
	r.Post("/service-payments/:id/void", middleware.Require("service_billing.manage"), withID(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID, in reasonIn) (any, error) {
		return s.VoidPayment(c.Context(), p, id, in.Reason)
	}))
	r.Get("/service-costs", middleware.Require("service_billing.view"), h.paged(func(c fiber.Ctx, p *auth.Principal, page httpx.Page) ([]map[string]any, int, error) {
		if err := checkQ(c, "project_id", "design_job_id", "contractor_id"); err != nil {
			return nil, 0, err
		}
		return s.ListCosts(c.Context(), p, uuidQ(c, "project_id"), uuidQ(c, "design_job_id"), uuidQ(c, "contractor_id"), page)
	}))
	r.Post("/service-costs", middleware.Require("service_billing.manage"), middleware.Idempotent(), send(func(c fiber.Ctx, p *auth.Principal) (int, []byte, error) {
		var in CostIn
		if err := httpx.Bind(c, &in); err != nil {
			return 0, nil, err
		}
		return s.AddCost(c.Context(), p, in)
	}))
	r.Post("/service-costs/:id/void", middleware.Require("service_billing.manage"), withID(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID, in reasonIn) (any, error) {
		return s.VoidCost(c.Context(), p, id, in.Reason)
	}))

	// Contractors
	r.Get("/contractors", middleware.Require("contractors.view"), h.paged(func(c fiber.Ctx, p *auth.Principal, page httpx.Page) ([]map[string]any, int, error) {
		all := false
		if b := httpx.QueryBool(c, "all"); b != nil {
			all = *b
		}
		return s.ListContractors(c.Context(), p, c.Query("q"), all, page)
	}))
	r.Post("/contractors", middleware.Require("contractors.manage"), make201(func(c fiber.Ctx, p *auth.Principal) (any, error) {
		var in ContractorIn
		if err := httpx.Bind(c, &in); err != nil {
			return nil, err
		}
		return s.CreateContractor(c.Context(), p, in)
	}))
	r.Get("/contractors/:id", middleware.Require("contractors.view"), withIDOnly(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID) (any, error) {
		return s.GetContractor(c.Context(), p, id)
	}))
	r.Put("/contractors/:id", middleware.Require("contractors.manage"), withID(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID, in ContractorIn) (any, error) {
		return s.UpdateContractor(c.Context(), p, id, in, nil)
	}))
	r.Post("/contractors/:id/deactivate", middleware.Require("contractors.manage"), withIDOnly(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID) (any, error) {
		f := false
		return s.UpdateContractor(c.Context(), p, id, ContractorIn{}, &f)
	}))
	r.Post("/contractors/:id/activate", middleware.Require("contractors.manage"), withIDOnly(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID) (any, error) {
		t := true
		return s.UpdateContractor(c.Context(), p, id, ContractorIn{}, &t)
	}))
	r.Get("/contractors/:id/statement", middleware.Require("contractors.view"), withIDOnly(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID) (any, error) {
		return s.ContractorStatement(c.Context(), p, id)
	}))
	r.Post("/contractors/:id/payments", middleware.Require("contractors.manage"), middleware.Idempotent(), send(func(c fiber.Ctx, p *auth.Principal) (int, []byte, error) {
		id, err := idp(c)
		if err != nil {
			return 0, nil, err
		}
		var in ContractorPayIn
		if err := httpx.Bind(c, &in); err != nil {
			return 0, nil, err
		}
		return s.PayContractor(c.Context(), p, id, in)
	}))

	// Design jobs
	r.Get("/design-jobs", middleware.Require("design.view"), h.paged(func(c fiber.Ctx, p *auth.Principal, page httpx.Page) ([]map[string]any, int, error) {
		if err := checkQ(c, "designer_id", "client_id", "project_id"); err != nil {
			return nil, 0, err
		}
		od := httpx.QueryBool(c, "overdue")
		return s.ListJobs(c.Context(), p, JobFilter{Q: c.Query("q"), Status: c.Query("status"), DesignerID: uuidQ(c, "designer_id"), ClientID: uuidQ(c, "client_id"), ProjectID: uuidQ(c, "project_id"), Overdue: od != nil && *od}, page)
	}))
	r.Get("/design-jobs/board", middleware.Require("design.view"), call(func(c fiber.Ctx, p *auth.Principal) (any, error) {
		if err := checkQ(c, "designer_id"); err != nil {
			return nil, err
		}
		return s.JobBoard(c.Context(), p, uuidQ(c, "designer_id"))
	}))
	r.Post("/design-jobs", middleware.Require("design.manage"), make201(func(c fiber.Ctx, p *auth.Principal) (any, error) {
		var in DesignJobIn
		if err := httpx.Bind(c, &in); err != nil {
			return nil, err
		}
		return s.CreateJob(c.Context(), p, in)
	}))
	r.Get("/design-jobs/:id", middleware.Require("design.view"), withIDOnly(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID) (any, error) { return s.GetJob(c.Context(), p, id) }))
	r.Put("/design-jobs/:id", middleware.Require("design.manage"), withID(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID, in DesignJobPatch) (any, error) {
		return s.UpdateJob(c.Context(), p, id, in)
	}))
	r.Post("/design-jobs/:id/status", middleware.Require("design.manage"), withID(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID, in struct {
		Status string `json:"status" validate:"required,oneof=in_design internal_review with_client changes_requested approved printing delivered cancelled"`
		Note   string `json:"note" validate:"max=1000"`
	}) (any, error) {
		return s.MoveJob(c.Context(), p, id, in.Status, clean(in.Note))
	}))
	r.Post("/design-jobs/:id/versions", middleware.Require("design.manage"), func(c fiber.Ctx) error {
		id, err := idp(c)
		if err != nil {
			return err
		}
		var in VersionIn
		if err := httpx.Bind(c, &in); err != nil {
			return err
		}
		out, err := s.AddVersion(c.Context(), middleware.Principal(c), id, in)
		return httpx.Created(c, out, err)
	})
	r.Post("/design-jobs/:id/versions/:version_id/send", middleware.Require("design.manage"), call(func(c fiber.Ctx, p *auth.Principal) (any, error) {
		id, err := idp(c)
		if err != nil {
			return nil, err
		}
		v, err := httpx.UUIDParam(c, "version_id")
		if err != nil {
			return nil, err
		}
		return s.SendForApproval(c.Context(), p, id, v, h.BaseURL)
	}))
	r.Post("/design-jobs/:id/feedback", middleware.Require("design.view"), func(c fiber.Ctx) error {
		id, err := idp(c)
		if err != nil {
			return err
		}
		var in FeedbackIn
		if err := httpx.Bind(c, &in); err != nil {
			return err
		}
		out, err := s.AddFeedback(c.Context(), middleware.Principal(c), id, in)
		return httpx.Created(c, out, err)
	})
	r.Post("/design-jobs/:id/payments", middleware.Require("service_billing.manage"), middleware.Idempotent(), send(func(c fiber.Ctx, p *auth.Principal) (int, []byte, error) {
		id, err := idp(c)
		if err != nil {
			return 0, nil, err
		}
		var in PayIn
		if err := httpx.Bind(c, &in); err != nil {
			return 0, nil, err
		}
		return s.PayDesignJob(c.Context(), p, id, in)
	}))
	r.Post("/design-jobs/:id/timer/start", middleware.Require("design.view"), withIDOnly(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID) (any, error) {
		return s.StartTimer(c.Context(), p, id)
	}))
	r.Post("/design-jobs/:id/timer/stop", middleware.Require("design.view"), withIDOnly(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID) (any, error) {
		return s.StopTimer(c.Context(), p, id)
	}))
	r.Post("/design-jobs/:id/print-orders", middleware.Require("design.manage"), make201(func(c fiber.Ctx, p *auth.Principal) (any, error) {
		id, err := idp(c)
		if err != nil {
			return nil, err
		}
		var in PrintOrderIn
		if err := httpx.Bind(c, &in); err != nil {
			return nil, err
		}
		return s.CreatePrintOrder(c.Context(), p, id, in)
	}))
	r.Put("/print-orders/:id", middleware.Require("design.manage"), withID(func(c fiber.Ctx, p *auth.Principal, id uuid.UUID, in struct {
		Status string `json:"status" validate:"required,oneof=ready delivered cancelled"`
	}) (any, error) {
		return s.UpdatePrintOrder(c.Context(), p, id, in.Status)
	}))

	// Dashboard and reports
	r.Get("/services/dashboard", middleware.RequireAny("projects.view", "tasks.view", "renewals.view", "design.view", "service_billing.view", "contractors.view"), call(func(c fiber.Ctx, p *auth.Principal) (any, error) {
		return s.Dashboard(c.Context(), p)
	}))
	r.Get("/reports/projects", middleware.Require("reports.services"), func(c fiber.Ctx) error {
		from, to, err := httpx.Range(c)
		if err != nil {
			return err
		}
		t, err := s.ProjectsReport(c.Context(), middleware.Principal(c), from, to)
		return export.Respond(c, t, nil, err)
	})
	r.Get("/reports/design-jobs", middleware.Require("reports.services"), func(c fiber.Ctx) error {
		from, to, err := httpx.Range(c)
		if err != nil {
			return err
		}
		t, err := s.DesignReport(c.Context(), middleware.Principal(c), from, to)
		return export.Respond(c, t, nil, err)
	})
}

// RegisterPublic registers the client approval page routes. They need no sign-in; the link is the secret.
func (h *Handler) RegisterPublic(r fiber.Router, limit fiber.Handler) {
	s := h.Svc
	r.Get("/design-approvals/:token", limit, func(c fiber.Ctx) error {
		out, err := s.PublicApproval(c.Context(), c.Params("token"))
		return httpx.OK(c, out, err)
	})
	r.Get("/design-approvals/:token/file", limit, func(c fiber.Ctx) error {
		name, mime, rc, err := s.PublicApprovalFile(c.Context(), c.Params("token"))
		if err != nil {
			return err
		}
		defer rc.Close()
		disp := "attachment"
		if strings.HasPrefix(mime, "image/") && mime != "image/svg+xml" || mime == "application/pdf" {
			disp = "inline"
		}
		c.Set("Content-Type", mime)
		c.Set("Content-Disposition", disp+`; filename="`+strings.NewReplacer(`"`, "", "\r", "", "\n", "").Replace(name)+`"`)
		c.Set("X-Content-Type-Options", "nosniff")
		c.Set("Cache-Control", "private, no-store")
		_, err = io.Copy(c, rc)
		return err
	})
	r.Post("/design-approvals/:token/decision", limit, func(c fiber.Ctx) error {
		var in DecisionIn
		if err := httpx.Bind(c, &in); err != nil {
			return err
		}
		out, err := s.PublicDecide(c.Context(), c.Params("token"), in)
		return httpx.OK(c, out, err)
	})
}
