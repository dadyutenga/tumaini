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
			clientSQL := `
				SELECT c.id, c.full_name::text, COALESCE(c.phone::text, '')
				FROM app.customers c
				WHERE c.archived_at IS NULL AND c.client_status IS NOT NULL
				  AND (c.full_name ILIKE $1 OR c.phone::text ILIKE $1 OR c.email::text ILIKE $1)`
			clientArgs := []any{pat}
			if !p.Has("services.client.view_all") {
				clientSQL += ` AND (c.registered_by = $2 OR c.assigned_to = $2 OR EXISTS (SELECT 1 FROM app.projects pr WHERE pr.customer_id = c.id AND (pr.owner_id = $2 OR EXISTS (SELECT 1 FROM app.project_stages st WHERE st.project_id = pr.id AND st.owner_id = $2))))`
				clientArgs = append(clientArgs, p.MembershipID)
			}
			clientSQL += ` ORDER BY c.full_name LIMIT 6`
			if err := collect("client", "/clients/", clientSQL, clientArgs...); err != nil {
				return err
			}
		}
		if canAny("services.project.view_all", "services.project.manage") {
			projSQL := `
				SELECT pr.id, pr.project_no || ' · ' || pr.title::text, c.full_name::text
				FROM app.projects pr JOIN app.customers c ON c.id = pr.customer_id
				WHERE (pr.project_no ILIKE $1 OR pr.title ILIKE $1 OR c.full_name ILIKE $1)`
			projArgs := []any{pat}
			if !p.Has("services.project.view_all") {
				projSQL += ` AND (pr.owner_id = $2 OR pr.created_by = $2 OR EXISTS (SELECT 1 FROM app.project_stages st WHERE st.project_id = pr.id AND st.owner_id = $2))`
				projArgs = append(projArgs, p.MembershipID)
			}
			projSQL += ` ORDER BY pr.created_at DESC LIMIT 6`
			if err := collect("project", "/projects/", projSQL, projArgs...); err != nil {
				return err
			}
		}
		if canAny("services.design.view_all", "services.design.manage") {
			designSQL := `
				SELECT j.id, j.job_no || ' · ' || j.title::text, c.full_name::text
				FROM app.design_jobs j JOIN app.customers c ON c.id = j.customer_id
				WHERE (j.job_no ILIKE $1 OR j.title ILIKE $1 OR c.full_name ILIKE $1)`
			designArgs := []any{pat}
			if !p.Has("services.design.view_all") {
				designSQL += ` AND (j.designer_id = $2 OR j.created_by = $2)`
				designArgs = append(designArgs, p.MembershipID)
			}
			designSQL += ` ORDER BY j.created_at DESC LIMIT 6`
			if err := collect("design_job", "/design-jobs/", designSQL, designArgs...); err != nil {
				return err
			}
		}
		if canAny("shop.sale.view_all", "shop.sale.create") {
			saleSQL := `
				SELECT s.id, s.receipt_no, COALESCE(c.full_name::text, 'Walk-in') || ' · ' || s.total::text
				FROM app.sales s LEFT JOIN app.customers c ON c.id = s.customer_id
				WHERE (s.receipt_no ILIKE $1 OR COALESCE(c.full_name::text,'') ILIKE $1)`
			saleArgs := []any{pat}
			if !p.Has("shop.sale.view_all") {
				saleArgs = append(saleArgs, p.MembershipID)
				saleSQL += ` AND s.sold_by = $2`
			}
			if p.BranchID != nil {
				saleArgs = append(saleArgs, *p.BranchID)
				if len(saleArgs) == 2 {
					saleSQL += ` AND s.location_id = $2`
				} else {
					saleSQL += ` AND s.location_id = $3`
				}
			}
			saleSQL += ` ORDER BY s.created_at DESC LIMIT 6`
			if err := collect("sale", "/sales/", saleSQL, saleArgs...); err != nil {
				return err
			}
		}
		if canAny("services.invoice.manage", "services.invoice.draft") {
			invSQL := `
				SELECT i.id, i.invoice_no, c.full_name::text || ' · ' || i.total::text
				FROM app.invoices i JOIN app.customers c ON c.id = i.customer_id
				WHERE (i.invoice_no ILIKE $1 OR c.full_name ILIKE $1)`
			invArgs := []any{pat}
			if !p.Has("services.invoice.manage") {
				invSQL += ` AND i.created_by = $2`
				invArgs = append(invArgs, p.MembershipID)
			}
			invSQL += ` ORDER BY i.created_at DESC LIMIT 6`
			if err := collect("service_invoice", "/service-invoices?open=", invSQL, invArgs...); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}
