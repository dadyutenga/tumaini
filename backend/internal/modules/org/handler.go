package org

import (
	"io"
	"mime"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"bms/internal/platform/httpx"
	"bms/internal/platform/middleware"
)

type Handler struct{ Svc *Service }

func (h *Handler) Business(c fiber.Ctx) error {
	b, err := h.Svc.GetBusiness(c.Context(), middleware.Principal(c))
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(b))
}

func (h *Handler) UpdateBusiness(c fiber.Ctx) error {
	var body Business
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	b, err := h.Svc.UpdateBusiness(c.Context(), middleware.Principal(c), body)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(b))
}

func (h *Handler) Logo(c fiber.Ctx) error {
	fh, err := c.FormFile("file")
	if err != nil {
		return httpx.BadRequest("invalid_body", "Upload a file field named file")
	}
	f, err := fh.Open()
	if err != nil {
		return err
	}
	defer f.Close()
	id, err := h.Svc.SaveLogo(c.Context(), middleware.Principal(c), fh.Filename, f)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(fiber.Map{"file_id": id}))
}

func (h *Handler) BusinessUnits(c fiber.Ctx) error {
	list, err := h.Svc.BusinessUnits(c.Context(), middleware.Principal(c))
	return httpx.OK(c, list, err)
}

func (h *Handler) UpdateBusinessUnit(c fiber.Ctx) error {
	var in BusinessUnitInput
	if err := httpx.Bind(c, &in); err != nil {
		return err
	}
	unit, err := h.Svc.UpdateBusinessUnit(c.Context(), middleware.Principal(c), strings.ToUpper(c.Params("code")), in)
	return httpx.OK(c, unit, err)
}

func (h *Handler) BusinessUnitLogo(c fiber.Ctx) error {
	fh, err := c.FormFile("file")
	if err != nil {
		return httpx.BadRequest("invalid_body", "Upload a file field named file")
	}
	f, err := fh.Open()
	if err != nil {
		return err
	}
	defer f.Close()
	id, err := h.Svc.SaveUnitLogo(c.Context(), middleware.Principal(c), strings.ToUpper(c.Params("code")), fh.Filename, f)
	return httpx.OK(c, fiber.Map{"file_id": id}, err)
}

func (h *Handler) Branches(c fiber.Ctx) error {
	list, err := h.Svc.ListBranches(c.Context(), middleware.Principal(c))
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(list))
}

func (h *Handler) CreateBranch(c fiber.Ctx) error {
	var body Branch
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	b, err := h.Svc.CreateBranch(c.Context(), middleware.Principal(c), body)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(httpx.Data(b))
}

func (h *Handler) Branch(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	b, err := h.Svc.GetBranch(c.Context(), middleware.Principal(c), id)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(b))
}

func (h *Handler) UpdateBranch(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body Branch
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	b, err := h.Svc.UpdateBranch(c.Context(), middleware.Principal(c), id, body)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(b))
}

func (h *Handler) Deactivate(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	if err := h.Svc.DeactivateBranch(c.Context(), middleware.Principal(c), id); err != nil {
		return err
	}
	return c.JSON(httpx.Data(fiber.Map{"ok": true}))
}

func (h *Handler) Staff(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	data, err := h.Svc.BranchStaff(c.Context(), middleware.Principal(c), id)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(data))
}

func (h *Handler) Settings(c fiber.Ctx) error {
	data, err := h.Svc.Settings(c.Context(), middleware.Principal(c))
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(data))
}

func (h *Handler) UpdateSettings(c fiber.Ctx) error {
	var body map[string]any
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	if err := h.Svc.UpdateSettings(c.Context(), middleware.Principal(c), c.Params("group"), body); err != nil {
		return err
	}
	return h.Settings(c)
}

func (h *Handler) Tax(c fiber.Ctx) error {
	list, err := h.Svc.ListTax(c.Context(), middleware.Principal(c))
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(list))
}

func (h *Handler) CreateTax(c fiber.Ctx) error {
	var body TaxIn
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	t, err := h.Svc.SaveTax(c.Context(), middleware.Principal(c), nil, body)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(httpx.Data(t))
}

func (h *Handler) UpdateTax(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body TaxIn
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	t, err := h.Svc.SaveTax(c.Context(), middleware.Principal(c), &id, body)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(t))
}

func (h *Handler) Methods(c fiber.Ctx) error {
	data, err := h.Svc.PaymentMethods(c.Context(), middleware.Principal(c))
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(data))
}

func (h *Handler) UpdateMethods(c fiber.Ctx) error {
	var body any
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	if err := h.Svc.UpdatePaymentMethods(c.Context(), middleware.Principal(c), body); err != nil {
		return err
	}
	return h.Methods(c)
}

func (h *Handler) Upload(c fiber.Ctx) error {
	fh, err := c.FormFile("file")
	if err != nil {
		return httpx.BadRequest("invalid_body", "Upload a file field named file")
	}
	f, err := fh.Open()
	if err != nil {
		return err
	}
	defer f.Close()
	id, err := h.Svc.Upload(c.Context(), middleware.Principal(c), fh.Filename, fh.Header.Get("Content-Type"), f)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(httpx.Data(fiber.Map{"id": id}))
}

func (h *Handler) Download(c fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return httpx.BadRequest("invalid_id", "id must be a UUID")
	}
	name, ctype, rc, err := h.Svc.OpenFile(c.Context(), middleware.Principal(c), id)
	if err != nil {
		return err
	}
	defer rc.Close()
	c.Set("Content-Type", ctype)
	c.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	c.Set("X-Content-Type-Options", "nosniff")
	c.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	_, err = io.Copy(c, rc)
	return err
}

func (h *Handler) ListAttachments(c fiber.Ctx) error {
	entityID, err := uuid.Parse(c.Query("entity_id"))
	if err != nil {
		return httpx.BadRequest("invalid_query", "entity_id must be a UUID")
	}
	list, err := h.Svc.ListAttachments(c.Context(), middleware.Principal(c), c.Query("entity_type"), entityID)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(list))
}

func (h *Handler) CreateAttachment(c fiber.Ctx) error {
	var body AttachmentIn
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	att, err := h.Svc.CreateAttachment(c.Context(), middleware.Principal(c), body)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(httpx.Data(att))
}

func (h *Handler) DeleteAttachment(c fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return httpx.BadRequest("invalid_id", "id must be a UUID")
	}
	if err := h.Svc.DeleteAttachment(c.Context(), middleware.Principal(c), id); err != nil {
		return err
	}
	return c.JSON(httpx.Data(fiber.Map{"ok": true}))
}

func Register(r fiber.Router, h *Handler) {
	r.Get("/business", h.Business)
	r.Put("/business", middleware.Require("settings.manage"), h.UpdateBusiness)
	r.Post("/business/logo", middleware.Require("settings.manage"), h.Logo)
	r.Get("/business-units", h.BusinessUnits)
	r.Patch("/business-units/:code", middleware.Require("settings.manage"), h.UpdateBusinessUnit)
	r.Post("/business-units/:code/logo", middleware.Require("settings.manage"), h.BusinessUnitLogo)
	r.Get("/branches", h.Branches)
	r.Post("/branches", middleware.Require("settings.manage"), h.CreateBranch)
	r.Get("/branches/:id", h.Branch)
	r.Put("/branches/:id", middleware.Require("settings.manage"), h.UpdateBranch)
	r.Post("/branches/:id/deactivate", middleware.Require("settings.manage"), h.Deactivate)
	r.Get("/branches/:id/staff", middleware.Require("settings.manage"), h.Staff)
	r.Get("/settings", middleware.Require("settings.manage"), h.Settings)
	r.Put("/settings/:group", middleware.Require("settings.manage"), h.UpdateSettings)
	r.Get("/tax-rates", h.Tax)
	r.Post("/tax-rates", middleware.Require("settings.manage"), h.CreateTax)
	r.Put("/tax-rates/:id", middleware.Require("settings.manage"), h.UpdateTax)
	r.Get("/payment-methods", h.Methods)
	r.Put("/payment-methods", middleware.Require("settings.manage"), h.UpdateMethods)
	r.Post("/files", h.Upload)
	r.Get("/files/:id", h.Download)
	r.Get("/attachments", h.ListAttachments)
	r.Post("/attachments", h.CreateAttachment)
	r.Delete("/attachments/:id", h.DeleteAttachment)
}
