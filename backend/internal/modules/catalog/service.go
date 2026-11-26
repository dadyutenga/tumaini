package catalog

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
	"github.com/xuri/excelize/v2"

	"bms/internal/kernel/audit"
	"bms/internal/platform/auth"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/money"
	"bms/internal/platform/sqlq"
)

type Service struct {
	DB    *db.DB
	Audit *audit.Service
}

func New(database *db.DB, aud *audit.Service) *Service {
	return &Service{DB: database, Audit: aud}
}

// Product is a stock item (app.products). The approved model has no "service" kind: services are a
// separate catalogue (app.services) in Business B. Cost fields are filled only for callers who may
// see cost (shop.product.view_cost); app.v_staff_catalog exists for the same reason.
type Product struct {
	ID             uuid.UUID        `json:"id"`
	Name           string           `json:"name"`
	SKU            string           `json:"sku"`
	Barcode        *string          `json:"barcode"`
	CategoryID     *uuid.UUID       `json:"category_id"`
	Unit           string           `json:"unit"`
	TaxRateID      *uuid.UUID       `json:"tax_rate_id"`
	Kind           string           `json:"kind"`
	SellPrice      decimal.Decimal  `json:"sell_price"`
	BuyPrice       *decimal.Decimal `json:"buy_price,omitempty"`
	ReorderLevel   decimal.Decimal  `json:"reorder_level"`
	IsActive       bool             `json:"is_active"`
	QtyOnHand      *decimal.Decimal `json:"qty_on_hand,omitempty"`
	AvgCost        *decimal.Decimal `json:"avg_cost,omitempty"`
	Description    *string          `json:"description,omitempty"`
	WarrantyMonths int              `json:"warranty_months"`
	TrackSerial    bool             `json:"track_serial"`
	PhotoFile      *uuid.UUID       `json:"photo_file,omitempty"`
}

type ProductInput struct {
	Name           string           `json:"name"`
	SKU            string           `json:"sku"`
	Barcode        string           `json:"barcode"`
	CategoryID     *uuid.UUID       `json:"category_id"`
	Unit           string           `json:"unit"`
	TaxRateID      *uuid.UUID       `json:"tax_rate_id"`
	Kind           string           `json:"kind"`
	SellPrice      decimal.Decimal  `json:"sell_price"`
	BuyPrice       *decimal.Decimal `json:"buy_price"`
	ReorderLevel   decimal.Decimal  `json:"reorder_level"`
	Description    string           `json:"description"`
	WarrantyMonths *int             `json:"warranty_months"`
	TrackSerial    *bool            `json:"track_serial"`
	PhotoFile      *uuid.UUID       `json:"photo_file"`
	// Reason is recorded with a selling-price change (app.product_price_changes.reason).
	Reason string `json:"reason"`
}

var unitCode = regexp.MustCompile(`^[a-z]{1,10}$`)

// productColumns is the SELECT list shared by every product read. $1 is the active location (may be
// NULL); stock is that location's balance, or the total across locations when none is selected.
const productColumns = `
	p.id, p.name::text, coalesce(p.sku, ''), p.barcode, p.category_id, p.unit_of_measure, p.tax_rate_id, p.selling_price, p.buying_price,
	coalesce(p.min_stock_level, ts.low_stock_default), p.status = 'active',
	(SELECT sum(b.quantity_on_hand) FROM app.stock_balances b WHERE b.tenant_id = p.tenant_id AND b.product_id = p.id AND ($1::uuid IS NULL OR b.location_id = $1)),
	p.average_cost, p.description, p.warranty_months, p.tracks_serial, p.photo_file_id`

const productFrom = ` FROM app.products p JOIN app.tenant_settings ts ON ts.tenant_id = p.tenant_id`

func scanProduct(row pgx.Row, showCost bool) (Product, error) {
	var p Product
	var buy, cost decimal.Decimal
	err := row.Scan(&p.ID, &p.Name, &p.SKU, &p.Barcode, &p.CategoryID, &p.Unit, &p.TaxRateID, &p.SellPrice, &buy, &p.ReorderLevel, &p.IsActive,
		&p.QtyOnHand, &cost, &p.Description, &p.WarrantyMonths, &p.TrackSerial, &p.PhotoFile)
	p.Kind = "stock"
	if showCost {
		p.BuyPrice, p.AvgCost = &buy, &cost
	}
	return p, err
}

func (s *Service) List(ctx context.Context, p *auth.Principal, q string, category *uuid.UUID, active, low *bool, page httpx.Page, showCost bool) ([]Product, int, error) {
	out := []Product{}
	var total int
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		f := sqlq.New(p.BranchID)
		f.Add("($1::uuid IS NULL OR $1::uuid IS NOT NULL)")
		q = strings.TrimSpace(q)
		f.If(q != "", "(p.name ILIKE '%' || ? || '%' OR p.sku ILIKE '%' || ? || '%' OR p.barcode = ?)", q, q, q)
		f.If(category != nil, "p.category_id = ?", category)
		f.If(active != nil, "(p.status = 'active') = ?", active)
		if low != nil && *low {
			f.Add(`coalesce((SELECT sum(b.quantity_on_hand) FROM app.stock_balances b WHERE b.tenant_id = p.tenant_id AND b.product_id = p.id AND ($1::uuid IS NULL OR b.location_id = $1)), 0)
			       <= coalesce(p.min_stock_level, ts.low_stock_default)`)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*)`+productFrom+f.Where(), f.Args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT`+productColumns+productFrom+f.Where()+` ORDER BY p.name, p.id`+f.Page(page.PerPage, page.Offset), f.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			pr, err := scanProduct(rows, showCost)
			if err != nil {
				return err
			}
			out = append(out, pr)
		}
		return rows.Err()
	})
	return out, total, err
}

func (s *Service) Lookup(ctx context.Context, p *auth.Principal, code string) (Product, error) {
	var pr Product
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) (err error) {
		pr, err = scanProduct(tx.QueryRow(ctx, `SELECT`+productColumns+productFrom+` WHERE p.barcode = $2 OR p.sku = $2`, p.BranchID, code), p.Has("shop.product.view_cost"))
		if err != nil {
			return httpx.NotFound("not_found", "No product matches that barcode or SKU")
		}
		return nil
	})
	return pr, err
}

func (s *Service) Get(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	pr, err := s.one(ctx, p, id)
	if err != nil {
		return nil, err
	}
	levels := []map[string]any{}
	err = s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT l.id, l.name::text, coalesce(b.quantity_on_hand, 0)
			FROM app.locations l
			LEFT JOIN app.stock_balances b ON b.tenant_id = l.tenant_id AND b.location_id = l.id AND b.product_id = $1
			WHERE l.tenant_id = $2 AND l.status = 'active' ORDER BY l.name`, id, p.TenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var bid uuid.UUID
			var name string
			var qty decimal.Decimal
			if err := rows.Scan(&bid, &name, &qty); err != nil {
				return err
			}
			row := map[string]any{"branch_id": bid, "branch": name, "qty_on_hand": qty}
			if pr.AvgCost != nil {
				row["avg_cost"] = *pr.AvgCost
			}
			levels = append(levels, row)
		}
		return rows.Err()
	})
	return map[string]any{"product": pr, "stock": levels}, err
}

func (s *Service) one(ctx context.Context, p *auth.Principal, id uuid.UUID) (Product, error) {
	var pr Product
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) (err error) {
		pr, err = scanProduct(tx.QueryRow(ctx, `SELECT`+productColumns+productFrom+` WHERE p.id = $2`, p.BranchID, id), p.Has("shop.product.view_cost"))
		if err != nil {
			return httpx.NotFound("not_found", "Product not found")
		}
		return nil
	})
	return pr, err
}

func (s *Service) Create(ctx context.Context, p *auth.Principal, in ProductInput) (Product, error) {
	if strings.TrimSpace(in.Name) == "" {
		return Product{}, httpx.BadRequest("invalid_body", "Product name is required")
	}
	if err := checkProductInput(in); err != nil {
		return Product{}, err
	}
	id := uuid.New()
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		tax := in.TaxRateID
		if tax == nil {
			var tid uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT id FROM app.tax_rates WHERE tenant_id = $1 AND is_default AND status = 'active'`, p.TenantID).Scan(&tid); err == nil {
				tax = &tid
			}
		}
		sku := strings.TrimSpace(in.SKU)
		if sku == "" {
			sku = "SKU-" + strings.ToUpper(strings.ReplaceAll(id.String(), "-", ""))[:12]
		}
		unit := in.Unit
		if unit == "" {
			unit = "pcs"
		}
		warranty := 0
		if in.WarrantyMonths != nil && *in.WarrantyMonths > 0 {
			warranty = *in.WarrantyMonths
		}
		trackSerial := in.TrackSerial != nil && *in.TrackSerial
		buy := decimal.Zero
		if in.BuyPrice != nil && p.Has("shop.product.view_cost") {
			buy = *in.BuyPrice
		}
		var reorder *decimal.Decimal
		if in.ReorderLevel.IsPositive() {
			reorder = &in.ReorderLevel
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.products (id, tenant_id, business_unit_id, category_id, sku, barcode, name, description, unit_of_measure, selling_price,
			                          buying_price, tax_rate_id, min_stock_level, warranty_months, tracks_serial, photo_file_id)
			VALUES ($1,$2,(SELECT id FROM app.business_units WHERE tenant_id = $2 AND code = 'A'),$3,$4,NULLIF($5,''),$6,NULLIF($7,''),$8,$9,$10,$11,$12,$13,$14,$15)`,
			id, p.TenantID, in.CategoryID, sku, strings.TrimSpace(in.Barcode), strings.TrimSpace(in.Name), in.Description, unit,
			money.Money(in.SellPrice), buy, tax, reorder, warranty, trackSerial, in.PhotoFile); err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "SKU or barcode already exists")
			}
			return err
		}
		return s.Audit.Record(ctx, tx, p, "product.created", "products", id, nil, map[string]any{"name": in.Name, "sku": sku, "sell_price": in.SellPrice})
	})
	if err != nil {
		return Product{}, err
	}
	return s.one(ctx, p, id)
}

// checkProductInput rejects values the database would refuse with a 500.
func checkProductInput(in ProductInput) error {
	if in.SellPrice.IsNegative() {
		return httpx.BadRequest("invalid_body", "Selling price cannot be negative")
	}
	if in.ReorderLevel.IsNegative() {
		return httpx.BadRequest("invalid_body", "Reorder level cannot be negative")
	}
	if in.BuyPrice != nil && in.BuyPrice.IsNegative() {
		return httpx.BadRequest("invalid_body", "Buying price cannot be negative")
	}
	if in.Kind != "" && in.Kind != "stock" {
		return httpx.BadRequest("invalid_body", "Products are stock items; services belong to the services catalogue")
	}
	if in.Unit != "" && !unitCode.MatchString(in.Unit) {
		return httpx.BadRequest("invalid_body", "Unit must be 1 to 10 lowercase letters, such as pcs, kg or litre")
	}
	if in.TrackSerial != nil && *in.TrackSerial && in.Unit != "" && in.Unit != "pcs" {
		return httpx.BadRequest("invalid_body", "Serial-tracked products are counted in pieces")
	}
	return nil
}

// Update changes a product. A selling-price change is logged by the database in
// app.product_price_changes and is refused unless the caller may change prices or an approval exists.
func (s *Service) Update(ctx context.Context, p *auth.Principal, id uuid.UUID, in ProductInput) (Product, error) {
	if err := checkProductInput(in); err != nil {
		return Product{}, err
	}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var old decimal.Decimal
		if err := tx.QueryRow(ctx, `SELECT selling_price FROM app.products WHERE id = $1 FOR UPDATE`, id).Scan(&old); err != nil {
			return httpx.NotFound("not_found", "Product not found")
		}
		price := money.Money(in.SellPrice)
		if !old.Equal(price) {
			reason := strings.TrimSpace(in.Reason)
			if len(reason) < 3 {
				reason = "price update"
			}
			if _, err := tx.Exec(ctx, `SELECT set_config('app.change_note', $1, true)`, reason); err != nil {
				return err
			}
		}
		var buy *decimal.Decimal
		if in.BuyPrice != nil && p.Has("shop.product.view_cost") {
			buy = in.BuyPrice
		}
		var reorder *decimal.Decimal
		if in.ReorderLevel.IsPositive() {
			reorder = &in.ReorderLevel
		}
		if _, err := tx.Exec(ctx, `
			UPDATE app.products SET name = coalesce(NULLIF($2,''), name), barcode = coalesce(NULLIF($3,''), barcode),
			  category_id = coalesce($4, category_id), unit_of_measure = coalesce(NULLIF($5,''), unit_of_measure),
			  tax_rate_id = coalesce($6, tax_rate_id), selling_price = $7, buying_price = coalesce($8, buying_price),
			  min_stock_level = coalesce($9, min_stock_level), description = coalesce(NULLIF($10,''), description),
			  warranty_months = coalesce($11, warranty_months), tracks_serial = coalesce($12, tracks_serial),
			  photo_file_id = coalesce($13, photo_file_id)
			WHERE id = $1`,
			id, strings.TrimSpace(in.Name), strings.TrimSpace(in.Barcode), in.CategoryID, in.Unit, in.TaxRateID, price, buy, reorder,
			in.Description, in.WarrantyMonths, in.TrackSerial, in.PhotoFile); err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "Another product already has that barcode")
			}
			return err
		}
		return s.Audit.Record(ctx, tx, p, "product.updated", "products", id, map[string]any{"sell_price": old}, map[string]any{"sell_price": price})
	})
	if err != nil {
		return Product{}, err
	}
	return s.one(ctx, p, id)
}

func (s *Service) SetActive(ctx context.Context, p *auth.Principal, id uuid.UUID, active bool) error {
	return s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE app.products SET status = CASE WHEN $2 THEN 'active' ELSE 'inactive' END::app.record_status WHERE id = $1`, id, active)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound("not_found", "Product not found")
		}
		return s.Audit.Record(ctx, tx, p, "product.updated", "products", id, nil, map[string]any{"is_active": active})
	})
}

// BranchPrices is not supported: the approved model has one selling price per product, not one per
// branch. The route answers 410 so a client learns that instead of silently losing a price.
func (s *Service) BranchPrices(ctx context.Context, p *auth.Principal, id uuid.UUID, prices []struct {
	BranchID uuid.UUID       `json:"branch_id"`
	Price    decimal.Decimal `json:"sell_price"`
}) error {
	return httpx.New(410, "branch_prices_removed", "Products have one selling price for the whole business; per-branch prices are no longer supported")
}

func (s *Service) PriceHistory(ctx context.Context, p *auth.Principal, id uuid.UUID) ([]map[string]any, error) {
	out := []map[string]any{}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT c.id, c.old_selling_price, c.new_selling_price, c.old_buying_price, c.new_buying_price, c.reason, m.user_id, c.effective_at
			FROM app.product_price_changes c LEFT JOIN app.memberships m ON m.tenant_id = c.tenant_id AND m.id = c.created_by
			WHERE c.tenant_id = $1 AND c.product_id = $2 ORDER BY c.effective_at DESC`, p.TenantID, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		showCost := p.Has("shop.product.view_cost")
		for rows.Next() {
			var hid uuid.UUID
			var by *uuid.UUID
			var oldP, newP, oldB, newB decimal.Decimal
			var reason string
			var at any
			if err := rows.Scan(&hid, &oldP, &newP, &oldB, &newB, &reason, &by, &at); err != nil {
				return err
			}
			row := map[string]any{"id": hid, "branch_id": nil, "old_price": oldP, "new_price": newP, "reason": reason, "changed_by": by, "changed_at": at}
			if showCost {
				row["old_buy_price"], row["new_buy_price"] = oldB, newB
			}
			out = append(out, row)
		}
		return rows.Err()
	})
	return out, err
}

type Category struct {
	ID       uuid.UUID  `json:"id"`
	ParentID *uuid.UUID `json:"parent_id"`
	Name     string     `json:"name"`
	IsActive bool       `json:"is_active"`
}

func (s *Service) Categories(ctx context.Context, p *auth.Principal) ([]Category, error) {
	out := []Category{}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, parent_id, name::text, status = 'active' FROM app.product_categories WHERE tenant_id = $1 ORDER BY name`, p.TenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c Category
			if err := rows.Scan(&c.ID, &c.ParentID, &c.Name, &c.IsActive); err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}

// CategoryIn is a create or update body. On update every field is optional:
// parent_id is changed only when the key is sent (null moves the category to the top level).
type CategoryIn struct {
	Name     *string         `json:"name"`
	ParentID json.RawMessage `json:"parent_id"`
	IsActive *bool           `json:"is_active"`
}

// parent returns (id, set, error) for the parent_id key.
func (in CategoryIn) parent() (*uuid.UUID, bool, error) {
	if in.ParentID == nil {
		return nil, false, nil
	}
	if string(bytes.TrimSpace(in.ParentID)) == "null" {
		return nil, true, nil
	}
	var id uuid.UUID
	if err := json.Unmarshal(in.ParentID, &id); err != nil {
		return nil, false, httpx.BadRequest("invalid_body", "parent_id must be a UUID or null")
	}
	return &id, true, nil
}

func (s *Service) SaveCategory(ctx context.Context, p *auth.Principal, id *uuid.UUID, in CategoryIn) (Category, error) {
	parent, parentSet, err := in.parent()
	if err != nil {
		return Category{}, err
	}
	var name string
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
	}
	if id == nil && name == "" {
		return Category{}, httpx.BadRequest("invalid_body", "Category name is required")
	}
	if in.Name != nil && name == "" {
		return Category{}, httpx.BadRequest("invalid_body", "Category name cannot be empty")
	}
	var outID uuid.UUID
	err = s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if parent != nil {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM app.product_categories WHERE tenant_id = $1 AND id = $2)`, p.TenantID, *parent).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return httpx.BadRequest("invalid_body", "Parent category not found")
			}
		}
		if id == nil {
			outID = uuid.New()
			_, err := tx.Exec(ctx, `INSERT INTO app.product_categories (id, tenant_id, parent_id, name) VALUES ($1,$2,$3,$4)`, outID, p.TenantID, parent, name)
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "That category already exists")
			}
			if err != nil {
				return err
			}
			return s.Audit.Record(ctx, tx, p, "category.created", "product_categories", outID, nil, map[string]any{"name": name})
		}
		outID = *id
		if parentSet && parent != nil {
			// A category cannot sit under itself or under one of its own descendants.
			var cycle bool
			if err := tx.QueryRow(ctx, `
				WITH RECURSIVE up AS (
					SELECT id, parent_id FROM app.product_categories WHERE id = $1
					UNION
					SELECT c.id, c.parent_id FROM app.product_categories c JOIN up ON c.id = up.parent_id
				) SELECT EXISTS (SELECT 1 FROM up WHERE id = $2)`, *parent, outID).Scan(&cycle); err != nil {
				return err
			}
			if cycle {
				return httpx.BadRequest("invalid_body", "A category cannot be placed under itself or one of its own subcategories")
			}
		}
		tag, err := tx.Exec(ctx, `
			UPDATE app.product_categories SET name = coalesce(NULLIF($2,''), name),
			  parent_id = CASE WHEN $3 THEN $4::uuid ELSE parent_id END,
			  status = coalesce(CASE WHEN $5::boolean THEN 'active' ELSE 'inactive' END::app.record_status, status)
			WHERE id = $1`, outID, name, parentSet, parent, in.IsActive)
		if db.IsUnique(err) {
			return httpx.Conflict("duplicate", "That category already exists")
		}
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound("not_found", "Category not found")
		}
		return s.Audit.Record(ctx, tx, p, "category.updated", "product_categories", outID, nil, map[string]any{"name": name})
	})
	if err != nil {
		return Category{}, err
	}
	var c Category
	err = s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id, parent_id, name::text, status = 'active' FROM app.product_categories WHERE id = $1`, outID).Scan(&c.ID, &c.ParentID, &c.Name, &c.IsActive)
	})
	return c, err
}

// DeleteCategory retires a category with no products; app.product_categories has no DELETE grant for
// the API, so "delete" sets it inactive.
func (s *Service) DeleteCategory(ctx context.Context, p *auth.Principal, id uuid.UUID) error {
	return s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.products WHERE tenant_id = $1 AND category_id = $2`, p.TenantID, id).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return httpx.Conflict("invalid_status", "A category with products cannot be deleted")
		}
		tag, err := tx.Exec(ctx, `UPDATE app.product_categories SET status = 'inactive' WHERE tenant_id = $1 AND id = $2`, p.TenantID, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound("not_found", "Category not found")
		}
		return s.Audit.Record(ctx, tx, p, "category.retired", "product_categories", id, nil, nil)
	})
}

// Unit is a unit of measure. The approved model stores it as a short lowercase code on the product
// (app.products.unit_of_measure) with no master table, so the list is the common codes plus any a
// product already uses.
type Unit struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	ShortName    string `json:"short_name"`
	AllowDecimal bool   `json:"allow_decimal"`
}

var baseUnits = []Unit{
	{"pcs", "piece", "pcs", false}, {"kg", "kilogram", "kg", true}, {"litre", "litre", "litre", true},
	{"box", "box", "box", false}, {"carton", "carton", "carton", false}, {"metre", "metre", "metre", true},
}

func (s *Service) Units(ctx context.Context, p *auth.Principal) ([]Unit, error) {
	out := append([]Unit{}, baseUnits...)
	known := map[string]bool{}
	for _, u := range out {
		known[u.ID] = true
	}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT DISTINCT unit_of_measure FROM app.products WHERE tenant_id = $1 ORDER BY 1`, p.TenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var code string
			if err := rows.Scan(&code); err != nil {
				return err
			}
			if !known[code] {
				out = append(out, Unit{ID: code, Name: code, ShortName: code, AllowDecimal: true})
			}
		}
		return rows.Err()
	})
	return out, err
}

// UnitIn is accepted for compatibility.
type UnitIn struct {
	Name         *string `json:"name"`
	ShortName    *string `json:"short_name"`
	AllowDecimal *bool   `json:"allow_decimal"`
}

// SaveUnit validates a unit code and echoes it. There is no unit master table to write to: a code
// becomes available the first time a product uses it.
func (s *Service) SaveUnit(ctx context.Context, p *auth.Principal, id *uuid.UUID, in UnitIn) (Unit, error) {
	code := ""
	if in.ShortName != nil {
		code = strings.ToLower(strings.TrimSpace(*in.ShortName))
	}
	if code == "" && in.Name != nil {
		code = strings.ToLower(strings.TrimSpace(*in.Name))
	}
	if !unitCode.MatchString(code) {
		return Unit{}, httpx.BadRequest("invalid_body", "A unit is 1 to 10 lowercase letters, such as pcs, kg or litre")
	}
	allow := in.AllowDecimal != nil && *in.AllowDecimal
	return Unit{ID: code, Name: code, ShortName: code, AllowDecimal: allow}, nil
}

// maxImportRows bounds one import so a large file cannot hold a request open for minutes.
const maxImportRows = 5000

type ImportRow struct {
	Row     int    `json:"row"`
	Name    string `json:"name"`
	SKU     string `json:"sku"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

func (s *Service) Import(ctx context.Context, p *auth.Principal, filename string, r io.Reader, commit bool) (map[string]any, error) {
	records, err := readTabular(filename, r)
	if err != nil {
		return nil, httpx.BadRequest("invalid_body", err.Error())
	}
	if len(records) < 2 {
		return nil, httpx.BadRequest("invalid_body", "The file needs a header and at least one row")
	}
	if len(records) > maxImportRows+1 {
		return nil, httpx.BadRequest("too_many_rows", "Import at most 5,000 products at a time")
	}
	header := map[string]int{}
	for i, h := range records[0] {
		header[strings.ToLower(strings.TrimSpace(h))] = i
	}
	var report []ImportRow
	var saved int
	err = s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		for i, rec := range records[1:] {
			name := col(rec, header, "name")
			sku := col(rec, header, "sku")
			price := money.M(col(rec, header, "sell_price"))
			if name == "" || price.IsNegative() {
				report = append(report, ImportRow{Row: i + 2, Name: name, SKU: sku, Status: "error", Message: "name and a non-negative sell_price are required"})
				continue
			}
			report = append(report, ImportRow{Row: i + 2, Name: name, SKU: sku, Status: "ok"})
			if !commit {
				continue
			}
			if _, err := s.Create(ctx, p, ProductInput{Name: name, SKU: sku, Barcode: col(rec, header, "barcode"), SellPrice: price, Kind: "stock"}); err != nil {
				report[len(report)-1].Status = "error"
				report[len(report)-1].Message = err.Error()
				var he *httpx.Error
				if errors.As(err, &he) {
					report[len(report)-1].Message = he.Message
				}
				continue
			}
			saved++
		}
		if commit {
			return s.Audit.Record(ctx, tx, p, "product.imported", "products", uuid.Nil, nil, map[string]any{"saved": saved})
		}
		return nil
	})
	return map[string]any{"commit": commit, "saved": saved, "rows": report}, err
}

func col(rec []string, header map[string]int, name string) string {
	i, ok := header[name]
	if !ok || i >= len(rec) {
		return ""
	}
	return strings.TrimSpace(rec[i])
}

func readTabular(name string, r io.Reader) ([][]string, error) {
	if strings.HasSuffix(strings.ToLower(name), ".xlsx") {
		f, err := excelize.OpenReader(r)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		sheets := f.GetSheetList()
		if len(sheets) == 0 {
			return nil, io.EOF
		}
		return f.GetRows(sheets[0])
	}
	return csv.NewReader(r).ReadAll()
}
