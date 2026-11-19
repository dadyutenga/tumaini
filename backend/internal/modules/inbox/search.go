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
// projects and design jobs by name, phone, number, SKU or barcode (SRS Section 3).
// Only modules the caller holds view permission for are searched.
func (s *Service) GlobalSearch(ctx context.Context, p *auth.Principal, q string) ([]SearchHit, error) {
	q = strings.TrimSpace(q)
	if len(q) < 2 {
		return []SearchHit{}, nil
	}
	pat := "%" + q + "%"
	out := []SearchHit{}
	can := func(perm string) bool {
		if p.IsOwner {
			return true
		}
		_, ok := p.Perms[perm]
		return ok
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

		if can("products.view") {
			if err := collect("product", "/products?q="+q, `
				SELECT id, name, COALESCE(NULLIF(barcode,''), COALESCE(sku,''), '')
				FROM products
				WHERE is_active AND (name ILIKE $1 OR sku ILIKE $1 OR barcode ILIKE $1)
				ORDER BY name LIMIT 6`, pat); err != nil {
				return err
			}
		}
		if can("customers.view") {
			if err := collect("customer", "/customers/", `
				SELECT id, name, COALESCE(phone, '')
				FROM customers
				WHERE is_active AND NOT is_walk_in AND (name ILIKE $1 OR phone ILIKE $1)
				ORDER BY name LIMIT 6`, pat); err != nil {
				return err
			}
		}
		if can("clients.view") {
			if err := collect("client", "/clients/", `
				SELECT id, name, COALESCE(phone, '')
				FROM svc_clients
				WHERE is_active AND (name ILIKE $1 OR phone ILIKE $1 OR email::text ILIKE $1)
				ORDER BY name LIMIT 6`, pat); err != nil {
				return err
			}
		}
		if can("projects.view") {
			if err := collect("project", "/projects/", `
				SELECT pr.id, pr.number || ' · ' || pr.name, c.name
				FROM svc_projects pr JOIN svc_clients c ON c.id = pr.client_id
				WHERE pr.number ILIKE $1 OR pr.name ILIKE $1 OR c.name ILIKE $1
				ORDER BY pr.created_at DESC LIMIT 6`, pat); err != nil {
				return err
			}
		}
		if can("design.view") {
			if err := collect("design_job", "/design-jobs/", `
				SELECT j.id, j.number || ' · ' || j.title, c.name
				FROM design_jobs j JOIN svc_clients c ON c.id = j.client_id
				WHERE j.number ILIKE $1 OR j.title ILIKE $1 OR c.name ILIKE $1
				ORDER BY j.created_at DESC LIMIT 6`, pat); err != nil {
				return err
			}
		}
		if can("sales.view") {
			if err := collect("sale", "/sales/", `
				SELECT s.id, s.number, COALESCE(c.name, 'Walk-in') || ' · ' || s.total::text
				FROM sales s LEFT JOIN customers c ON c.id = s.customer_id
				WHERE s.number ILIKE $1 OR COALESCE(c.name,'') ILIKE $1
				ORDER BY s.created_at DESC LIMIT 6`, pat); err != nil {
				return err
			}
		}
		if can("service_billing.view") {
			if err := collect("service_invoice", "/service-invoices?open=", `
				SELECT i.id, i.number, c.name || ' · ' || i.total::text
				FROM svc_invoices i JOIN svc_clients c ON c.id = i.client_id
				WHERE i.number ILIKE $1 OR c.name ILIKE $1
				ORDER BY i.created_at DESC LIMIT 6`, pat); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}
