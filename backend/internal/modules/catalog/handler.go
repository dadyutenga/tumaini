package catalog

import (
	"strconv"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"bms/internal/platform/auth"
	"bms/internal/platform/export"
	"bms/internal/platform/httpx"
	"bms/internal/platform/middleware"
	"bms/internal/platform/pdf"
)

type Handler struct{ Svc *Service }

func showCost(p *auth.Principal) bool {
	_, ok := p.Can("shop.product.view_cost")
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

func (h *Handler) Export(c fiber.Ctx) error {
	p := middleware.Principal(c)
	list, _, err := h.Svc.List(c.Context(), p, c.Query("q"), nil, nil, nil, httpx.Page{Page: 1, PerPage: 10000}, showCost(p))
	if err != nil {
		return err
	}
	cost := showCost(p)
	cols := []export.Col{{Key: "sku", Label: "SKU"}, {Key: "barcode", Label: "Barcode"}, {Key: "name", Label: "Name"}, {Key: "kind", Label: "Kind"}, {Key: "unit", Label: "Unit"}, {Key: "sell_price", Label: "Sell price"}}
	if cost {
		cols = append(cols, export.Col{Key: "avg_cost", Label: "Avg cost"})
	}
	cols = append(cols, export.Col{Key: "qty_on_hand", Label: "Stock"}, export.Col{Key: "warranty_months", Label: "Warranty (m)"}, export.Col{Key: "is_active", Label: "Active"})
	rows := make([]map[string]any, 0, len(list))
	for _, pr := range list {
		bc := ""
		if pr.Barcode != nil {
			bc = *pr.Barcode
		}
		r := map[string]any{
			"sku": pr.SKU, "barcode": bc, "name": pr.Name, "kind": pr.Kind, "unit": pr.Unit,
			"sell_price": pr.SellPrice, "qty_on_hand": pr.QtyOnHand, "warranty_months": pr.WarrantyMonths, "is_active": pr.IsActive,
		}
		if cost {
			r["avg_cost"] = pr.AvgCost
		}
		rows = append(rows, r)
	}
	t := export.Table{Title: "Products", Columns: cols, Rows: rows}
	return export.Respond(c, t, nil, nil)
}

func (h *Handler) BarcodeLabel(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	res, err := h.Svc.Get(c.Context(), middleware.Principal(c), id)
	if err != nil {
		return err
	}
	pr := res["product"].(Product)
	code := pr.SKU
	if pr.Barcode != nil && *pr.Barcode != "" {
		code = *pr.Barcode
	}
	d := pdf.NewSized(226, 140, 12)
	d.Heading(pr.Name, 11)
	d.Text("Code: " + code)
	d.Heading("TZS "+pr.SellPrice.StringFixed(0), 12)
	if pr.WarrantyMonths > 0 {
		d.Text("Warranty: " + strconv.Itoa(pr.WarrantyMonths) + " months")
	}
	return export.File(c, "barcode-"+code+".pdf", "application/pdf", d.Bytes())
}

func Register(r fiber.Router, h *Handler) {
	r.Get("/products", middleware.Require("shop.product.view"), h.List)
	r.Get("/products/lookup", middleware.Require("shop.product.view"), h.Lookup)
	r.Get("/products/export", middleware.Require("shop.product.view"), h.Export)
	r.Post("/products", middleware.Require("shop.product.manage"), h.Create)
	r.Get("/products/:id", middleware.Require("shop.product.view"), h.Get)
	r.Get("/products/:id/barcode-label", middleware.Require("shop.product.view"), h.BarcodeLabel)
	r.Put("/products/:id", middleware.Require("shop.product.manage"), h.Update)
	r.Put("/products/:id/branch-prices", middleware.Require("shop.product.manage"), h.BranchPrices)
	r.Post("/products/:id/deactivate", middleware.Require("shop.product.manage"), h.active(false))
	r.Post("/products/:id/activate", middleware.Require("shop.product.manage"), h.active(true))
	r.Get("/products/:id/price-history", middleware.Require("shop.product.view"), func(c fiber.Ctx) error {
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
	r.Post("/products/import", middleware.Require("shop.product.manage"), func(c fiber.Ctx) error {
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
	r.Get("/categories", middleware.Require("shop.product.view"), func(c fiber.Ctx) error {
		list, err := h.Svc.Categories(c.Context(), middleware.Principal(c))
		if err != nil {
			return err
		}
		return c.JSON(httpx.Data(list))
	})
	r.Post("/categories", middleware.Require("shop.product.manage"), func(c fiber.Ctx) error {
		var body CategoryIn
		if err := c.Bind().Body(&body); err != nil {
			return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
		}
		cat, err := h.Svc.SaveCategory(c.Context(), middleware.Principal(c), nil, body)
		if err != nil {
			return err
		}
		return c.Status(fiber.StatusCreated).JSON(httpx.Data(cat))
	})
	r.Put("/categories/:id", middleware.Require("shop.product.manage"), func(c fiber.Ctx) error {
		id, err := httpx.UUIDParam(c, "id")
		if err != nil {
			return err
		}
		var body CategoryIn
		if err := c.Bind().Body(&body); err != nil {
			return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
		}
		cat, err := h.Svc.SaveCategory(c.Context(), middleware.Principal(c), &id, body)
		if err != nil {
			return err
		}
		return c.JSON(httpx.Data(cat))
	})
	r.Delete("/categories/:id", middleware.Require("shop.product.manage"), func(c fiber.Ctx) error {
		id, err := httpx.UUIDParam(c, "id")
		if err != nil {
			return err
		}
		if err := h.Svc.DeleteCategory(c.Context(), middleware.Principal(c), id); err != nil {
			return err
		}
		return c.JSON(httpx.Data(fiber.Map{"ok": true}))
	})
	r.Get("/units", middleware.Require("shop.product.view"), func(c fiber.Ctx) error {
		list, err := h.Svc.Units(c.Context(), middleware.Principal(c))
		if err != nil {
			return err
		}
		return c.JSON(httpx.Data(list))
	})
	r.Post("/units", middleware.Require("shop.product.manage"), func(c fiber.Ctx) error {
		var body UnitIn
		if err := c.Bind().Body(&body); err != nil {
			return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
		}
		u, err := h.Svc.SaveUnit(c.Context(), middleware.Principal(c), nil, body)
		if err != nil {
			return err
		}
		return c.Status(fiber.StatusCreated).JSON(httpx.Data(u))
	})
	r.Put("/units/:id", middleware.Require("shop.product.manage"), func(c fiber.Ctx) error {
		id, err := httpx.UUIDParam(c, "id")
		if err != nil {
			return err
		}
		var body UnitIn
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
