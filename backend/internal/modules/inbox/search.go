package inbox

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"bms/internal/platform/auth"
)

type SearchHit struct {
	Kind     string    `json:"kind"`
	ID       uuid.UUID `json:"id"`
	Title    string    `json:"title"`
	Subtitle string    `json:"subtitle"`
	Link     string    `json:"link"`
}

// GlobalSearch finds clients, customers, products, sales/receipts, invoices,
// projects and design jobs by name, phone, number, SKU or barcode.
// Only modules the caller holds view permission for are searched.
func (s *Service) GlobalSearch(ctx context.Context, p *auth.Principal, q string) ([]SearchHit, error) {
	q = strings.TrimSpace(q)
	if len(q) < 2 {
		return []SearchHit{}, nil
	}
	pat := "%" + q + "%"
	out := []SearchHit{}
	canAny := func(perms ...string) bool {
		if p.IsOwner {
			return true
		}
		for _, perm := range perms {
			if p.Has(perm) {
				return true
			}
		}
		return false
	}

	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		collect := func(kind, linkPrefix, sql string, args ...any) error {
			rows, err := tx.Query(ctx, sql, args...)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var id uuid.UUID
				var title, subtitle string
				if err := rows.Scan(&id, &title, &subtitle); err != nil {
					return err
				}
				link := linkPrefix
				if strings.HasSuffix(linkPrefix, "/") || strings.HasSuffix(linkPrefix, "=") {
					link = linkPrefix + id.String()
				}
				out = append(out, SearchHit{Kind: kind, ID: id, Title: title, Subtitle: subtitle, Link: link})
			}
			return rows.Err()
		}

		if canAny("shop.product.view") {
			if err := collect("product", "/products?q="+q, `
				SELECT id, name, COALESCE(NULLIF(barcode,''), COALESCE(sku,''), '')
				FROM app.products
				WHERE status = 'active' AND (name ILIKE $1 OR sku ILIKE $1 OR barcode ILIKE $1)
				ORDER BY name LIMIT 6`, pat); err != nil {
				return err
			}
		}
		if canAny("shop.customer.view_all", "shop.customer.manage") {
			if err := collect("customer", "/customers/", `
				SELECT id, full_name::text, COALESCE(phone::text, '')
				FROM app.customers
				WHERE archived_at IS NULL AND is_shop_customer AND client_status IS NULL AND (full_name ILIKE $1 OR phone::text ILIKE $1)
				ORDER BY full_name LIMIT 6`, pat); err != nil {
				return err
			}
		}
		if canAny("services.client.view_all", "services.client.manage") {
			if err := collect("client", "/clients/", `
				SELECT id, full_name::text, COALESCE(phone::text, '')
				FROM app.customers
				WHERE archived_at IS NULL AND client_status IS NOT NULL AND (full_name ILIKE $1 OR phone::text ILIKE $1 OR email::text ILIKE $1)
				ORDER BY full_name LIMIT 6`, pat); err != nil {
				return err
			}
		}
		if canAny("services.project.view_all", "services.project.manage") {
			if err := collect("project", "/projects/", `
				SELECT pr.id, pr.project_no || ' · ' || pr.title::text, c.full_name::text
				FROM app.projects pr JOIN app.customers c ON c.tenant_id = pr.tenant_id AND c.id = pr.customer_id
				WHERE pr.project_no ILIKE $1 OR pr.title ILIKE $1 OR c.full_name ILIKE $1
				ORDER BY pr.created_at DESC LIMIT 6`, pat); err != nil {
				return err
			}
		}
		if canAny("services.design.view_all", "services.design.manage") {
			if err := collect("design_job", "/design-jobs/", `
				SELECT j.id, j.job_no || ' · ' || j.title::text, c.full_name::text
				FROM app.design_jobs j JOIN app.customers c ON c.tenant_id = j.tenant_id AND c.id = j.customer_id
				WHERE j.job_no ILIKE $1 OR j.title ILIKE $1 OR c.full_name ILIKE $1
				ORDER BY j.created_at DESC LIMIT 6`, pat); err != nil {
				return err
			}
		}
		if canAny("shop.sale.view_all", "shop.sale.create") {
			if err := collect("sale", "/sales/", `
				SELECT s.id, s.receipt_no, COALESCE(c.full_name::text, 'Walk-in') || ' · ' || s.total::text
				FROM app.sales s LEFT JOIN app.customers c ON c.tenant_id = s.tenant_id AND c.id = s.customer_id
				WHERE s.receipt_no ILIKE $1 OR COALESCE(c.full_name::text,'') ILIKE $1
				ORDER BY s.created_at DESC LIMIT 6`, pat); err != nil {
				return err
			}
		}
		if canAny("services.invoice.manage", "services.invoice.draft") {
			if err := collect("service_invoice", "/service-invoices?open=", `
				SELECT i.id, i.invoice_no, c.full_name::text || ' · ' || i.total::text
				FROM app.invoices i JOIN app.customers c ON c.tenant_id = i.tenant_id AND c.id = i.customer_id
				WHERE i.invoice_no ILIKE $1 OR c.full_name ILIKE $1
				ORDER BY i.created_at DESC LIMIT 6`, pat); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}
