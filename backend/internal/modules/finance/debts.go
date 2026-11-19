package finance

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/platform/auth"
)

type DebtRow struct {
	ID      uuid.UUID       `json:"id"`
	Name    string          `json:"name"`
	Phone   string          `json:"phone"`
	Source  string          `json:"source"`
	Link    string          `json:"link"`
	D0_30   decimal.Decimal `json:"d0_30"`
	D31_60  decimal.Decimal `json:"d31_60"`
	Over60  decimal.Decimal `json:"over_60"`
	Total   decimal.Decimal `json:"total"`
}

// UnifiedDebts returns Owed to us (Shop customers + Service clients) and We owe
// (Suppliers + Contractors) grouped with 0-30, 31-60 and >60 day aging (SRS Section 8).
func (s *Service) UnifiedDebts(ctx context.Context, p *auth.Principal) (map[string]any, error) {
	owedToUs := []DebtRow{}
	weOwe := []DebtRow{}

	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
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

		// Owed to us: Shop customers
		if err := collect(&owedToUs, "shop_customer", "/customers/", `
			SELECT c.id, c.name, COALESCE(c.phone,''),
			       COALESCE(SUM(s.balance) FILTER (WHERE ($1::date - COALESCE(s.due_date, s.business_date)) <= 30), 0),
			       COALESCE(SUM(s.balance) FILTER (WHERE ($1::date - COALESCE(s.due_date, s.business_date)) BETWEEN 31 AND 60), 0),
			       COALESCE(SUM(s.balance) FILTER (WHERE ($1::date - COALESCE(s.due_date, s.business_date)) > 60), 0),
			       COALESCE(SUM(s.balance), 0)
			FROM sales s JOIN customers c ON c.id = s.customer_id
			WHERE s.kind='sale' AND s.status IN ('completed','partially_refunded') AND s.balance > 0
			GROUP BY c.id, c.name, c.phone
			ORDER BY 7 DESC`); err != nil {
			return err
		}

		// Owed to us: Service clients
		if err := collect(&owedToUs, "service_client", "/clients/", `
			SELECT c.id, c.name, COALESCE(c.phone,''),
			       COALESCE(SUM(i.balance) FILTER (WHERE ($1::date - COALESCE(i.due_date, i.issue_date, i.created_at::date)) <= 30), 0),
			       COALESCE(SUM(i.balance) FILTER (WHERE ($1::date - COALESCE(i.due_date, i.issue_date, i.created_at::date)) BETWEEN 31 AND 60), 0),
			       COALESCE(SUM(i.balance) FILTER (WHERE ($1::date - COALESCE(i.due_date, i.issue_date, i.created_at::date)) > 60), 0),
			       COALESCE(SUM(i.balance), 0)
			FROM svc_invoices i JOIN svc_clients c ON c.id = i.client_id
			WHERE i.status IN ('issued','partial') AND i.balance > 0
			GROUP BY c.id, c.name, c.phone
			ORDER BY 7 DESC`); err != nil {
			return err
		}

		// We owe: Suppliers
		if err := collect(&weOwe, "supplier", "/purchasing", `
			SELECT sp.id, sp.name, COALESCE(sp.phone,''),
			       COALESCE(SUM(i.balance) FILTER (WHERE ($1::date - COALESCE(i.due_date, i.invoice_date)) <= 30), 0),
			       COALESCE(SUM(i.balance) FILTER (WHERE ($1::date - COALESCE(i.due_date, i.invoice_date)) BETWEEN 31 AND 60), 0),
			       COALESCE(SUM(i.balance) FILTER (WHERE ($1::date - COALESCE(i.due_date, i.invoice_date)) > 60), 0),
			       COALESCE(SUM(i.balance), 0)
			FROM purchase_invoices i JOIN suppliers sp ON sp.id = i.supplier_id
			WHERE i.status = 'posted' AND i.balance > 0
			GROUP BY sp.id, sp.name, sp.phone
			ORDER BY 7 DESC`); err != nil {
			return err
		}

		// We owe: Contractors
		if err := collect(&weOwe, "contractor", "/contractors", `
			SELECT ct.id, ct.name, COALESCE(ct.phone,''),
			       COALESCE(SUM(k.amount - k.settled) FILTER (WHERE ($1::date - k.cost_date) <= 30), 0),
			       COALESCE(SUM(k.amount - k.settled) FILTER (WHERE ($1::date - k.cost_date) BETWEEN 31 AND 60), 0),
			       COALESCE(SUM(k.amount - k.settled) FILTER (WHERE ($1::date - k.cost_date) > 60), 0),
			       COALESCE(SUM(k.amount - k.settled), 0)
			FROM svc_costs k JOIN contractors ct ON ct.id = k.contractor_id
			WHERE k.status = 'owed' AND k.amount > k.settled
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
