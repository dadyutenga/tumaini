package catalog

import (
	"context"
	"encoding/csv"
	"io"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
	"github.com/xuri/excelize/v2"

	"bms/internal/kernel/audit"
	"bms/internal/platform/auth"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/ids"
	"bms/internal/platform/money"
)

type Service struct {
	DB    *db.DB
	Audit *audit.Service
}

func New(database *db.DB, aud *audit.Service) *Service {
	return &Service{DB: database, Audit: aud}
}

type Product struct {
	ID           uuid.UUID        `json:"id"`
	Name         string           `json:"name"`
	SKU          string           `json:"sku"`
	Barcode      *string          `json:"barcode"`
	CategoryID   *uuid.UUID       `json:"category_id"`
	UnitID       uuid.UUID        `json:"unit_id"`
	Unit         string           `json:"unit"`
	TaxRateID    *uuid.UUID       `json:"tax_rate_id"`
	Kind         string           `json:"kind"`
	SellPrice    decimal.Decimal  `json:"sell_price"`
	ReorderLevel decimal.Decimal  `json:"reorder_level"`
	IsActive     bool             `json:"is_active"`
	QtyOnHand    *decimal.Decimal `json:"qty_on_hand,omitempty"`
	AvgCost      *decimal.Decimal `json:"avg_cost,omitempty"`
	Description  *string          `json:"description,omitempty"`
}

type ProductInput struct {
	Name         string          `json:"name"`
	SKU          string          `json:"sku"`
	Barcode      string          `json:"barcode"`
	CategoryID   *uuid.UUID      `json:"category_id"`
	UnitID       *uuid.UUID      `json:"unit_id"`
	TaxRateID    *uuid.UUID      `json:"tax_rate_id"`
	Kind         string          `json:"kind"`
	SellPrice    decimal.Decimal `json:"sell_price"`
	ReorderLevel decimal.Decimal `json:"reorder_level"`
	Description  string          `json:"description"`
}

func (s *Service) List(ctx context.Context, p *auth.Principal, q string, category *uuid.UUID, active, low *bool, page httpx.Page, showCost bool) ([]Product, int, error) {
	var out []Product
	var total int
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		where := `WHERE ($1='' OR p.name ILIKE '%'||$1||'%' OR p.sku ILIKE '%'||$1||'%' OR p.barcode ILIKE '%'||$1||'%')
			AND ($2::uuid IS NULL OR p.category_id=$2)
			AND ($3::bool IS NULL OR p.is_active=$3)`
		args := []any{q, category, active}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM products p `+where, args...).Scan(&total); err != nil {
			return err
		}
		branch := p.BranchID
		sql := `SELECT p.id, p.name, p.sku, p.barcode, p.category_id, p.unit_id, u.short_name, p.tax_rate_id, p.kind, p.sell_price, p.reorder_level, p.is_active, sl.qty_on_hand, sl.avg_cost, p.description
			FROM products p JOIN units u ON u.id=p.unit_id
			LEFT JOIN stock_levels sl ON sl.product_id=p.id AND sl.branch_id=$4 ` + where
		args = append(args, branch)
		if low != nil && *low {
			sql += ` AND p.kind='stock' AND COALESCE(sl.qty_on_hand,0) <= p.reorder_level`
		}
		sql += ` ORDER BY p.name LIMIT $5 OFFSET $6`
		args = append(args, page.PerPage, page.Offset)
		rows, err := tx.Query(ctx, sql, args...)
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
	if out == nil {
		out = []Product{}
	}
	return out, total, err
}

func scanProduct(row pgx.Row, showCost bool) (Product, error) {
	var p Product
	var cost *decimal.Decimal
	err := row.Scan(&p.ID, &p.Name, &p.SKU, &p.Barcode, &p.CategoryID, &p.UnitID, &p.Unit, &p.TaxRateID, &p.Kind, &p.SellPrice, &p.ReorderLevel, &p.IsActive, &p.QtyOnHand, &cost, &p.Description)
	if showCost {
		p.AvgCost = cost
	}
	return p, err
}

func (s *Service) Lookup(ctx context.Context, p *auth.Principal, code string) (Product, error) {
	var pr Product
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			SELECT p.id, p.name, p.sku, p.barcode, p.category_id, p.unit_id, u.short_name, p.tax_rate_id, p.kind,
			       COALESCE(bp.sell_price, p.sell_price), p.reorder_level, p.is_active, sl.qty_on_hand, sl.avg_cost, p.description
			FROM products p JOIN units u ON u.id=p.unit_id
			LEFT JOIN product_branch_prices bp ON bp.product_id=p.id AND bp.branch_id=$2
			LEFT JOIN stock_levels sl ON sl.product_id=p.id AND sl.branch_id=$2
			WHERE p.barcode=$1 OR p.sku=$1
		`, code, p.BranchID).Scan(&pr.ID, &pr.Name, &pr.SKU, &pr.Barcode, &pr.CategoryID, &pr.UnitID, &pr.Unit, &pr.TaxRateID, &pr.Kind, &pr.SellPrice, &pr.ReorderLevel, &pr.IsActive, &pr.QtyOnHand, &pr.AvgCost, &pr.Description)
		if err != nil {
			return httpx.NotFound("not_found", "No product matches that barcode or SKU")
		}
		if _, ok := p.Can("products.view_cost"); !ok {
			pr.AvgCost = nil
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
	var levels []map[string]any
	err = s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT b.id, b.name, COALESCE(sl.qty_on_hand,0), COALESCE(sl.avg_cost,0)
			FROM branches b LEFT JOIN stock_levels sl ON sl.branch_id=b.id AND sl.product_id=$1
			WHERE b.is_active ORDER BY b.name
		`, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		_, show := p.Can("products.view_cost")
		for rows.Next() {
			var bid uuid.UUID
			var name string
			var qty, cost decimal.Decimal
			if err := rows.Scan(&bid, &name, &qty, &cost); err != nil {
				return err
			}
			row := map[string]any{"branch_id": bid, "branch": name, "qty_on_hand": qty}
			if show {
				row["avg_cost"] = cost
			}
			levels = append(levels, row)
		}
		return rows.Err()
	})
	if levels == nil {
		levels = []map[string]any{}
	}
	return map[string]any{"product": pr, "stock": levels}, err
}

func (s *Service) one(ctx context.Context, p *auth.Principal, id uuid.UUID) (Product, error) {
	var pr Product
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			SELECT p.id, p.name, p.sku, p.barcode, p.category_id, p.unit_id, u.short_name, p.tax_rate_id, p.kind, p.sell_price, p.reorder_level, p.is_active, NULL::numeric, NULL::numeric, p.description
			FROM products p JOIN units u ON u.id=p.unit_id WHERE p.id=$1
		`, id).Scan(&pr.ID, &pr.Name, &pr.SKU, &pr.Barcode, &pr.CategoryID, &pr.UnitID, &pr.Unit, &pr.TaxRateID, &pr.Kind, &pr.SellPrice, &pr.ReorderLevel, &pr.IsActive, &pr.QtyOnHand, &pr.AvgCost, &pr.Description)
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
	if in.Kind == "" {
		in.Kind = "stock"
	}
	id := ids.New()
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		unit := in.UnitID
		if unit == nil {
			var uid uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT id FROM units WHERE name='piece' LIMIT 1`).Scan(&uid); err != nil {
				return httpx.BadRequest("invalid_body", "Create a unit before adding products")
			}
			unit = &uid
		}
		tax := in.TaxRateID
		if tax == nil {
			var tid uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT id FROM tax_rates WHERE is_default LIMIT 1`).Scan(&tid); err == nil {
				tax = &tid
			}
		}
		sku := strings.TrimSpace(in.SKU)
		if sku == "" {
			sku = "SKU-" + strings.ToUpper(strings.ReplaceAll(id.String(), "-", ""))
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO products (id, business_id, category_id, unit_id, tax_rate_id, sku, barcode, name, description, kind, sell_price, reorder_level, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,NULLIF($9,''),$10,$11,$12,$13)
		`, id, p.BusinessID, in.CategoryID, unit, tax, sku, strings.TrimSpace(in.Barcode), strings.TrimSpace(in.Name), in.Description, in.Kind, money.Money(in.SellPrice), in.ReorderLevel, p.UserID); err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "SKU or barcode already exists")
			}
			return err
		}
		return s.Audit.Record(ctx, tx, p, "product.created", "product", id, nil, map[string]any{"name": in.Name, "sku": sku, "sell_price": in.SellPrice})
	})
	if err != nil {
		return Product{}, err
	}
	return s.one(ctx, p, id)
}

func (s *Service) Update(ctx context.Context, p *auth.Principal, id uuid.UUID, in ProductInput) (Product, error) {
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var old decimal.Decimal
		if err := tx.QueryRow(ctx, `SELECT sell_price FROM products WHERE id=$1`, id).Scan(&old); err != nil {
			return httpx.NotFound("not_found", "Product not found")
		}
		price := money.Money(in.SellPrice)
		if _, err := tx.Exec(ctx, `
			UPDATE products SET name=COALESCE(NULLIF($2,''), name), barcode=COALESCE(NULLIF($3,''), barcode),
			category_id=COALESCE($4, category_id), unit_id=COALESCE($5, unit_id), tax_rate_id=COALESCE($6, tax_rate_id),
			kind=COALESCE(NULLIF($7,''), kind), sell_price=$8, reorder_level=$9, description=COALESCE(NULLIF($10,''), description)
			WHERE id=$1
		`, id, strings.TrimSpace(in.Name), strings.TrimSpace(in.Barcode), in.CategoryID, in.UnitID, in.TaxRateID, in.Kind, price, in.ReorderLevel, in.Description); err != nil {
			return err
		}
		if !old.Equal(price) {
			if _, err := tx.Exec(ctx, `
				INSERT INTO price_history (id, business_id, product_id, old_price, new_price, changed_by) VALUES ($1,$2,$3,$4,$5,$6)
			`, ids.New(), p.BusinessID, id, old, price, p.UserID); err != nil {
				return err
			}
		}
		return s.Audit.Record(ctx, tx, p, "product.updated", "product", id, map[string]any{"sell_price": old}, map[string]any{"sell_price": price})
	})
	if err != nil {
		return Product{}, err
	}
	return s.one(ctx, p, id)
}

func (s *Service) SetActive(ctx context.Context, p *auth.Principal, id uuid.UUID, active bool) error {
	return s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE products SET is_active=$2 WHERE id=$1`, id, active)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound("not_found", "Product not found")
		}
		return s.Audit.Record(ctx, tx, p, "product.updated", "product", id, nil, map[string]any{"is_active": active})
	})
}

func (s *Service) BranchPrices(ctx context.Context, p *auth.Principal, id uuid.UUID, prices []struct {
	BranchID uuid.UUID       `json:"branch_id"`
	Price    decimal.Decimal `json:"sell_price"`
}) error {
	return s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		for _, pr := range prices {
			var old decimal.Decimal
			err := tx.QueryRow(ctx, `SELECT sell_price FROM product_branch_prices WHERE product_id=$1 AND branch_id=$2`, id, pr.BranchID).Scan(&old)
			if _, err2 := tx.Exec(ctx, `
				INSERT INTO product_branch_prices (product_id, branch_id, business_id, sell_price) VALUES ($1,$2,$3,$4)
				ON CONFLICT (product_id, branch_id) DO UPDATE SET sell_price=EXCLUDED.sell_price
			`, id, pr.BranchID, p.BusinessID, money.Money(pr.Price)); err2 != nil {
				return err2
			}
			if err != nil {
				old = decimal.Zero
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO price_history (id, business_id, product_id, branch_id, old_price, new_price, changed_by) VALUES ($1,$2,$3,$4,$5,$6,$7)
			`, ids.New(), p.BusinessID, id, pr.BranchID, old, money.Money(pr.Price), p.UserID); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Service) PriceHistory(ctx context.Context, p *auth.Principal, id uuid.UUID) ([]map[string]any, error) {
	var out []map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, branch_id, old_price, new_price, changed_by, changed_at FROM price_history WHERE product_id=$1 ORDER BY changed_at DESC`, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var hid, by uuid.UUID
			var branch *uuid.UUID
			var old, newP decimal.Decimal
			var at any
			if err := rows.Scan(&hid, &branch, &old, &newP, &by, &at); err != nil {
				return err
			}
			out = append(out, map[string]any{"id": hid, "branch_id": branch, "old_price": old, "new_price": newP, "changed_by": by, "changed_at": at})
		}
		return rows.Err()
	})
	if out == nil {
		out = []map[string]any{}
	}
	return out, err
}

type Category struct {
	ID       uuid.UUID  `json:"id"`
	ParentID *uuid.UUID `json:"parent_id"`
	Name     string     `json:"name"`
	IsActive bool       `json:"is_active"`
}

func (s *Service) Categories(ctx context.Context, p *auth.Principal) ([]Category, error) {
	var out []Category
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, parent_id, name, is_active FROM categories ORDER BY name`)
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
	if out == nil {
		out = []Category{}
	}
	return out, err
}

func (s *Service) SaveCategory(ctx context.Context, p *auth.Principal, id *uuid.UUID, in Category) (Category, error) {
	var outID uuid.UUID
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if id == nil {
			outID = ids.New()
			_, err := tx.Exec(ctx, `INSERT INTO categories (id, business_id, parent_id, name) VALUES ($1,$2,$3,$4)`, outID, p.BusinessID, in.ParentID, strings.TrimSpace(in.Name))
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "That category already exists")
			}
			return err
		}
		outID = *id
		_, err := tx.Exec(ctx, `UPDATE categories SET name=COALESCE(NULLIF($2,''), name), parent_id=$3, is_active=$4 WHERE id=$1`, outID, in.Name, in.ParentID, in.IsActive)
		return err
	})
	if err != nil {
		return Category{}, err
	}
	var c Category
	_ = s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id, parent_id, name, is_active FROM categories WHERE id=$1`, outID).Scan(&c.ID, &c.ParentID, &c.Name, &c.IsActive)
	})
	return c, nil
}

func (s *Service) DeleteCategory(ctx context.Context, p *auth.Principal, id uuid.UUID) error {
	return s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM products WHERE category_id=$1`, id).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return httpx.Conflict("invalid_status", "A category with products cannot be deleted")
		}
		tag, err := tx.Exec(ctx, `DELETE FROM categories WHERE id=$1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound("not_found", "Category not found")
		}
		return nil
	})
}

type Unit struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	ShortName    string    `json:"short_name"`
	AllowDecimal bool      `json:"allow_decimal"`
}

func (s *Service) Units(ctx context.Context, p *auth.Principal) ([]Unit, error) {
	var out []Unit
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, name, short_name, allow_decimal FROM units ORDER BY name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var u Unit
			if err := rows.Scan(&u.ID, &u.Name, &u.ShortName, &u.AllowDecimal); err != nil {
				return err
			}
			out = append(out, u)
		}
		return rows.Err()
	})
	if out == nil {
		out = []Unit{}
	}
	return out, err
}

func (s *Service) SaveUnit(ctx context.Context, p *auth.Principal, id *uuid.UUID, in Unit) (Unit, error) {
	var outID uuid.UUID
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if id == nil {
			outID = ids.New()
			_, err := tx.Exec(ctx, `INSERT INTO units (id, business_id, name, short_name, allow_decimal) VALUES ($1,$2,$3,$4,$5)`, outID, p.BusinessID, in.Name, in.ShortName, in.AllowDecimal)
			return err
		}
		outID = *id
		_, err := tx.Exec(ctx, `UPDATE units SET name=COALESCE(NULLIF($2,''), name), short_name=COALESCE(NULLIF($3,''), short_name), allow_decimal=$4 WHERE id=$1`, outID, in.Name, in.ShortName, in.AllowDecimal)
		return err
	})
	if err != nil {
		return Unit{}, err
	}
	var u Unit
	_ = s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id, name, short_name, allow_decimal FROM units WHERE id=$1`, outID).Scan(&u.ID, &u.Name, &u.ShortName, &u.AllowDecimal)
	})
	return u, nil
}

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
	header := map[string]int{}
	for i, h := range records[0] {
		header[strings.ToLower(strings.TrimSpace(h))] = i
	}
	var report []ImportRow
	var saved int
	err = s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
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
				continue
			}
			saved++
		}
		if commit {
			return s.Audit.Record(ctx, tx, p, "product.imported", "product", p.BusinessID, nil, map[string]any{"saved": saved})
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
