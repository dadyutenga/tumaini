package org

import (
	"io"

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
	var body TaxRate
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
	var body TaxRate
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
	name, mime, rc, err := h.Svc.OpenFile(c.Context(), middleware.Principal(c), id)
	if err != nil {
		return err
	}
	defer rc.Close()
	c.Set("Content-Type", mime)
	c.Set("Content-Disposition", "attachment; filename=\""+name+"\"")
	_, err = io.Copy(c, rc)
	return err
}

func Register(r fiber.Router, h *Handler) {
	r.Get("/business", middleware.Require("settings.view"), h.Business)
	r.Put("/business", middleware.Require("business.manage"), h.UpdateBusiness)
	r.Post("/business/logo", middleware.Require("business.manage"), h.Logo)
	r.Get("/branches", middleware.Require("branches.view"), h.Branches)
	r.Post("/branches", middleware.Require("branches.manage"), h.CreateBranch)
	r.Get("/branches/:id", middleware.Require("branches.view"), h.Branch)
	r.Put("/branches/:id", middleware.Require("branches.manage"), h.UpdateBranch)
	r.Post("/branches/:id/deactivate", middleware.Require("branches.manage"), h.Deactivate)
	r.Get("/branches/:id/staff", middleware.Require("branches.view"), h.Staff)
	r.Get("/settings", middleware.Require("settings.view"), h.Settings)
	r.Put("/settings/:group", middleware.Require("settings.manage"), h.UpdateSettings)
	r.Get("/tax-rates", middleware.Require("settings.view"), h.Tax)
	r.Post("/tax-rates", middleware.Require("settings.manage"), h.CreateTax)
	r.Put("/tax-rates/:id", middleware.Require("settings.manage"), h.UpdateTax)
	r.Get("/payment-methods", middleware.Require("settings.view"), h.Methods)
	r.Put("/payment-methods", middleware.Require("settings.manage"), h.UpdateMethods)
	r.Post("/files", h.Upload)
	r.Get("/files/:id", h.Download)
}
