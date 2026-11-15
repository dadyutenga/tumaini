package catalog

import (
	"strconv"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/middleware"
)

type Handler struct{ Svc *Service }

func showCost(p *auth.Principal) bool {
	_, ok := p.Can("products.view_cost")
	return ok
}

func (h *Handler) List(c fiber.Ctx) error {
	p := middleware.Principal(c)
	var cat *uuid.UUID
	if raw := c.Query("category_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return httpx.BadRequest("invalid_id", "category_id must be a UUID")
		}
		cat = &id
	}
	var active, low *bool
	if raw := c.Query("is_active"); raw != "" {
		v, _ := strconv.ParseBool(raw)
		active = &v
	}
	if c.Query("low_stock") == "true" {
		v := true
		low = &v
	}
	page := httpx.PageFrom(c)
	list, total, err := h.Svc.List(c.Context(), p, c.Query("q"), cat, active, low, page, showCost(p))
	if err != nil {
		return err
	}
	return c.JSON(httpx.List(list, httpx.Meta{Page: page.Page, PerPage: page.PerPage, Total: total}))
}

func (h *Handler) Lookup(c fiber.Ctx) error {
	code := c.Query("barcode")
	if code == "" {
		code = c.Query("q")
	}
	pr, err := h.Svc.Lookup(c.Context(), middleware.Principal(c), code)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(pr))
}

func (h *Handler) Create(c fiber.Ctx) error {
	var body ProductInput
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	pr, err := h.Svc.Create(c.Context(), middleware.Principal(c), body)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(httpx.Data(pr))
}

func (h *Handler) Get(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	pr, err := h.Svc.Get(c.Context(), middleware.Principal(c), id)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(pr))
}

func (h *Handler) Update(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body ProductInput
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	pr, err := h.Svc.Update(c.Context(), middleware.Principal(c), id, body)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(pr))
}

func (h *Handler) active(v bool) fiber.Handler {
	return func(c fiber.Ctx) error {
		id, err := httpx.UUIDParam(c, "id")
		if err != nil {
			return err
		}
		if err := h.Svc.SetActive(c.Context(), middleware.Principal(c), id, v); err != nil {
			return err
		}
		return c.JSON(httpx.Data(fiber.Map{"ok": true}))
	}
}

func (h *Handler) BranchPrices(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body struct {
		Prices []struct {
			BranchID uuid.UUID       `json:"branch_id"`
			Price    decimal.Decimal `json:"sell_price"`
		} `json:"prices"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	if err := h.Svc.BranchPrices(c.Context(), middleware.Principal(c), id, body.Prices); err != nil {
		return err
	}
	return c.JSON(httpx.Data(fiber.Map{"ok": true}))
}

func Register(r fiber.Router, h *Handler) {
	r.Get("/products", middleware.Require("products.view"), h.List)
	r.Get("/products/lookup", middleware.Require("products.view"), h.Lookup)
	r.Post("/products", middleware.Require("products.manage"), h.Create)
	r.Get("/products/:id", middleware.Require("products.view"), h.Get)
	r.Put("/products/:id", middleware.Require("products.manage"), h.Update)
	r.Put("/products/:id/branch-prices", middleware.Require("products.manage"), h.BranchPrices)
	r.Post("/products/:id/deactivate", middleware.Require("products.manage"), h.active(false))
	r.Post("/products/:id/activate", middleware.Require("products.manage"), h.active(true))
	r.Get("/products/:id/price-history", middleware.Require("products.view"), func(c fiber.Ctx) error {
		id, err := httpx.UUIDParam(c, "id")
		if err != nil {
			return err
		}
		list, err := h.Svc.PriceHistory(c.Context(), middleware.Principal(c), id)
		if err != nil {
			return err
		}
		return c.JSON(httpx.Data(list))
	})
	r.Post("/products/import", middleware.Require("products.import"), func(c fiber.Ctx) error {
		fh, err := c.FormFile("file")
		if err != nil {
			return httpx.BadRequest("invalid_body", "Upload a file field named file")
		}
		f, err := fh.Open()
		if err != nil {
			return err
		}
		defer f.Close()
		commit := c.Query("commit") == "true"
		rep, err := h.Svc.Import(c.Context(), middleware.Principal(c), fh.Filename, f, commit)
		if err != nil {
			return err
		}
		return c.JSON(httpx.Data(rep))
	})
	r.Get("/categories", middleware.Require("products.view"), func(c fiber.Ctx) error {
		list, err := h.Svc.Categories(c.Context(), middleware.Principal(c))
		if err != nil {
			return err
		}
		return c.JSON(httpx.Data(list))
	})
	r.Post("/categories", middleware.Require("categories.manage"), func(c fiber.Ctx) error {
		var body Category
		if err := c.Bind().Body(&body); err != nil {
			return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
		}
		cat, err := h.Svc.SaveCategory(c.Context(), middleware.Principal(c), nil, body)
		if err != nil {
			return err
		}
		return c.Status(fiber.StatusCreated).JSON(httpx.Data(cat))
	})
	r.Put("/categories/:id", middleware.Require("categories.manage"), func(c fiber.Ctx) error {
		id, err := httpx.UUIDParam(c, "id")
		if err != nil {
			return err
		}
		var body Category
		if err := c.Bind().Body(&body); err != nil {
			return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
		}
		cat, err := h.Svc.SaveCategory(c.Context(), middleware.Principal(c), &id, body)
		if err != nil {
			return err
		}
		return c.JSON(httpx.Data(cat))
	})
	r.Delete("/categories/:id", middleware.Require("categories.manage"), func(c fiber.Ctx) error {
		id, err := httpx.UUIDParam(c, "id")
		if err != nil {
			return err
		}
		if err := h.Svc.DeleteCategory(c.Context(), middleware.Principal(c), id); err != nil {
			return err
		}
		return c.JSON(httpx.Data(fiber.Map{"ok": true}))
	})
	r.Get("/units", middleware.Require("products.view"), func(c fiber.Ctx) error {
		list, err := h.Svc.Units(c.Context(), middleware.Principal(c))
		if err != nil {
			return err
		}
		return c.JSON(httpx.Data(list))
	})
	r.Post("/units", middleware.Require("categories.manage"), func(c fiber.Ctx) error {
		var body Unit
		if err := c.Bind().Body(&body); err != nil {
			return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
		}
		u, err := h.Svc.SaveUnit(c.Context(), middleware.Principal(c), nil, body)
		if err != nil {
			return err
		}
		return c.Status(fiber.StatusCreated).JSON(httpx.Data(u))
	})
	r.Put("/units/:id", middleware.Require("categories.manage"), func(c fiber.Ctx) error {
		id, err := httpx.UUIDParam(c, "id")
		if err != nil {
			return err
		}
		var body Unit
		if err := c.Bind().Body(&body); err != nil {
			return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
		}
		u, err := h.Svc.SaveUnit(c.Context(), middleware.Principal(c), &id, body)
		if err != nil {
			return err
		}
		return c.JSON(httpx.Data(u))
	})
}
