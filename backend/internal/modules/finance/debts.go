package finance

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/platform/auth"
)

type DebtRow struct {
	ID     uuid.UUID       `json:"id"`
	Name   string          `json:"name"`
	Phone  string          `json:"phone"`
	Source string          `json:"source"`
	Link   string          `json:"link"`
	D0_30  decimal.Decimal `json:"d0_30"`
	D31_60 decimal.Decimal `json:"d31_60"`
	Over60 decimal.Decimal `json:"over_60"`
	Total  decimal.Decimal `json:"total"`
}

// UnifiedDebts returns Owed to us (Shop customers + Service clients) and We owe
// (Suppliers + Contractors) grouped with 0-30, 31-60 and >60 day aging (SRS Section 8).
func (s *Service) UnifiedDebts(ctx context.Context, p *auth.Principal) (map[string]any, error) {
	owedToUs := []DebtRow{}
	weOwe := []DebtRow{}

	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		today, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		collect := func(dest *[]DebtRow, source, linkPrefix, sql string) error {
			rows, err := tx.Query(ctx, sql, today)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var r DebtRow
				if err := rows.Scan(&r.ID, &r.Name, &r.Phone, &r.D0_30, &r.D31_60, &r.Over60, &r.Total); err != nil {
					return err
				}
				r.Source = source
				r.Link = linkPrefix + r.ID.String()
				*dest = append(*dest, r)
			}
			return rows.Err()
		}

		// Owed to us: shop customers (completed sales with a balance).
		if err := collect(&owedToUs, "shop_customer", "/customers/", `
			SELECT c.id, c.full_name::text, COALESCE(c.phone::text,''),
			       COALESCE(SUM(s.balance_due) FILTER (WHERE ($1::date - COALESCE(s.due_date, s.business_date)) <= 30), 0),
			       COALESCE(SUM(s.balance_due) FILTER (WHERE ($1::date - COALESCE(s.due_date, s.business_date)) BETWEEN 31 AND 60), 0),
			       COALESCE(SUM(s.balance_due) FILTER (WHERE ($1::date - COALESCE(s.due_date, s.business_date)) > 60), 0),
			       COALESCE(SUM(s.balance_due), 0)
			FROM app.sales s JOIN app.customers c ON c.tenant_id = s.tenant_id AND c.id = s.customer_id
			WHERE s.status = 'completed' AND s.balance_due > 0
			GROUP BY c.id, c.full_name, c.phone
			ORDER BY 7 DESC`); err != nil {
			return err
		}

		// Owed to us: service clients (issued invoices with a balance).
		if err := collect(&owedToUs, "service_client", "/clients/", `
			SELECT c.id, c.full_name::text, COALESCE(c.phone::text,''),
			       COALESCE(SUM(i.balance_due) FILTER (WHERE ($1::date - i.due_date) <= 30), 0),
			       COALESCE(SUM(i.balance_due) FILTER (WHERE ($1::date - i.due_date) BETWEEN 31 AND 60), 0),
			       COALESCE(SUM(i.balance_due) FILTER (WHERE ($1::date - i.due_date) > 60), 0),
			       COALESCE(SUM(i.balance_due), 0)
			FROM app.invoices i JOIN app.customers c ON c.tenant_id = i.tenant_id AND c.id = i.customer_id
			WHERE i.status IN ('issued','partially_paid') AND i.balance_due > 0
			GROUP BY c.id, c.full_name, c.phone
			ORDER BY 7 DESC`); err != nil {
			return err
		}

		// We owe: suppliers (received purchases with a balance).
		if err := collect(&weOwe, "supplier", "/purchasing", `
			SELECT sp.id, sp.name::text, COALESCE(sp.phone::text,''),
			       COALESCE(SUM(i.balance_due) FILTER (WHERE ($1::date - COALESCE(i.due_date, i.order_date)) <= 30), 0),
			       COALESCE(SUM(i.balance_due) FILTER (WHERE ($1::date - COALESCE(i.due_date, i.order_date)) BETWEEN 31 AND 60), 0),
			       COALESCE(SUM(i.balance_due) FILTER (WHERE ($1::date - COALESCE(i.due_date, i.order_date)) > 60), 0),
			       COALESCE(SUM(i.balance_due), 0)
			FROM app.purchases i JOIN app.suppliers sp ON sp.tenant_id = i.tenant_id AND sp.id = i.supplier_id
			WHERE i.status IN ('partially_received','received') AND i.balance_due > 0
			GROUP BY sp.id, sp.name, sp.phone
			ORDER BY 7 DESC`); err != nil {
			return err
		}

		// We owe: contractors (open bills).
		if err := collect(&weOwe, "contractor", "/contractors", `
			SELECT ct.id, ct.name::text, COALESCE(ct.phone::text,''),
			       COALESCE(SUM(k.balance_due) FILTER (WHERE ($1::date - k.bill_date) <= 30), 0),
			       COALESCE(SUM(k.balance_due) FILTER (WHERE ($1::date - k.bill_date) BETWEEN 31 AND 60), 0),
			       COALESCE(SUM(k.balance_due) FILTER (WHERE ($1::date - k.bill_date) > 60), 0),
			       COALESCE(SUM(k.balance_due), 0)
			FROM app.contractor_bills k JOIN app.contractors ct ON ct.tenant_id = k.tenant_id AND ct.id = k.contractor_id
			WHERE k.status IN ('open','partially_paid') AND k.balance_due > 0
			GROUP BY ct.id, ct.name, ct.phone
			ORDER BY 7 DESC`); err != nil {
			return err
		}
		// Clean links for suppliers & contractors (list pages rather than /purchasing<id>).
		for i := range weOwe {
			if weOwe[i].Source == "supplier" {
				weOwe[i].Link = "/purchasing"
			} else if weOwe[i].Source == "contractor" {
				weOwe[i].Link = "/contractors"
			}
		}
		return nil
	})
	return map[string]any{"owed_to_us": owedToUs, "we_owe": weOwe}, err
}
