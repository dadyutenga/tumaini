package stock

import (
	"context"
	"errors"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/platform/httpx"
	"bms/internal/platform/ids"
	"bms/internal/platform/money"
)

type Line struct {
	ProductID uuid.UUID
	Qty       decimal.Decimal
	UnitCost  decimal.Decimal
}

type Service struct{}

func New() *Service { return &Service{} }

type result struct {
	QtyAfter decimal.Decimal
	AvgAfter decimal.Decimal
	UnitCost decimal.Decimal
}

func (s *Service) allowNegative(ctx context.Context, tx pgx.Tx) (bool, error) {
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT value FROM settings WHERE key = 'system.allow_negative_stock'`).Scan(&raw)
	if err != nil {
		return false, nil
	}
	return string(raw) == "true", nil
}

func (s *Service) Receive(ctx context.Context, tx pgx.Tx, businessID, branchID uuid.UUID, userID *uuid.UUID, moveType, sourceType string, sourceID uuid.UUID, lines []Line) error {
	_, err := s.apply(ctx, tx, businessID, branchID, userID, moveType, sourceType, sourceID, lines, true)
	return err
}

func (s *Service) Deduct(ctx context.Context, tx pgx.Tx, businessID, branchID uuid.UUID, userID *uuid.UUID, moveType, sourceType string, sourceID uuid.UUID, lines []Line) (map[uuid.UUID]decimal.Decimal, error) {
	return s.apply(ctx, tx, businessID, branchID, userID, moveType, sourceType, sourceID, lines, false)
}

// ReturnAtCost puts stock back at the cost it left at.
func (s *Service) ReturnAtCost(ctx context.Context, tx pgx.Tx, businessID, branchID uuid.UUID, userID *uuid.UUID, moveType, sourceType string, sourceID uuid.UUID, lines []Line) error {
	_, err := s.apply(ctx, tx, businessID, branchID, userID, moveType, sourceType, sourceID, lines, true)
	return err
}

func (s *Service) apply(ctx context.Context, tx pgx.Tx, businessID, branchID uuid.UUID, userID *uuid.UUID, moveType, sourceType string, sourceID uuid.UUID, lines []Line, inbound bool) (map[uuid.UUID]decimal.Decimal, error) {
	merged := map[uuid.UUID]Line{}
	for _, ln := range lines {
		if ln.Qty.LessThanOrEqual(money.Zero) {
			continue
		}
		cur := merged[ln.ProductID]
		cur.ProductID = ln.ProductID
		cur.Qty = cur.Qty.Add(ln.Qty)
		if inbound {
			// Value-weighted if the same product is received at two costs in one document.
			value := cur.UnitCost.Mul(cur.Qty.Sub(ln.Qty)).Add(ln.UnitCost.Mul(ln.Qty))
			if cur.Qty.IsZero() {
				cur.UnitCost = ln.UnitCost
			} else {
				cur.UnitCost = value.Div(cur.Qty)
			}
		}
		merged[ln.ProductID] = cur
	}
	idsSorted := make([]uuid.UUID, 0, len(merged))
	for id := range merged {
		idsSorted = append(idsSorted, id)
	}
	sort.Slice(idsSorted, func(i, j int) bool { return idsSorted[i].String() < idsSorted[j].String() })
	allowNeg, err := s.allowNegative(ctx, tx)
	if err != nil {
		return nil, err
	}
	costs := map[uuid.UUID]decimal.Decimal{}
	for _, id := range idsSorted {
		ln := merged[id]
		res, err := s.one(ctx, tx, businessID, branchID, userID, id, ln, moveType, sourceType, sourceID, inbound, allowNeg)
		if err != nil {
			return nil, err
		}
		costs[id] = res.UnitCost
	}
	return costs, nil
}

func (s *Service) one(ctx context.Context, tx pgx.Tx, businessID, branchID uuid.UUID, userID *uuid.UUID, productID uuid.UUID, ln Line, moveType, sourceType string, sourceID uuid.UUID, inbound, allowNeg bool) (result, error) {
	if _, err := tx.Exec(ctx, `
		INSERT INTO stock_levels (business_id, branch_id, product_id, qty_on_hand, avg_cost)
		VALUES ($1,$2,$3,0,0)
		ON CONFLICT (branch_id, product_id) DO NOTHING
	`, businessID, branchID, productID); err != nil {
		return result{}, err
	}
	var qty, avg decimal.Decimal
	if err := tx.QueryRow(ctx, `
		SELECT qty_on_hand, avg_cost FROM stock_levels
		WHERE branch_id = $1 AND product_id = $2
		FOR UPDATE
	`, branchID, productID).Scan(&qty, &avg); err != nil {
		return result{}, err
	}
	var newQty, newAvg, unitCost, signed decimal.Decimal
	if inbound {
		unitCost = money.Cost(ln.UnitCost)
		newQty = qty.Add(ln.Qty)
		newAvg = money.WeightedAverage(qty, avg, ln.Qty, unitCost)
		signed = ln.Qty
	} else {
		if !allowNeg && qty.LessThan(ln.Qty) {
			return result{}, httpx.Conflict("insufficient_stock", "There is not enough stock for this product")
		}
		unitCost = avg
		newQty = qty.Sub(ln.Qty)
		if newQty.IsZero() {
			newAvg = money.Zero
		} else {
			newAvg = avg
		}
		signed = ln.Qty.Neg()
	}
	var createdBy any
	if userID != nil {
		createdBy = *userID
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO stock_movements (id, business_id, branch_id, product_id, type, qty, unit_cost, qty_after, avg_cost_after, source_type, source_id, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
	`, ids.New(), businessID, branchID, productID, moveType, signed, unitCost, newQty, newAvg, sourceType, sourceID, createdBy); err != nil {
		return result{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE stock_levels SET qty_on_hand = $3, avg_cost = $4, low_notified = CASE WHEN $3 > (SELECT reorder_level FROM products WHERE id = $2) THEN false ELSE low_notified END
		WHERE branch_id = $1 AND product_id = $2
	`, branchID, productID, newQty, newAvg); err != nil {
		return result{}, err
	}
	return result{QtyAfter: newQty, AvgAfter: newAvg, UnitCost: unitCost}, nil
}

// DeductAtCost removes stock valued at a given unit cost instead of the
// current average, for purchase returns that leave at the price they came in.
// The remaining stock keeps its total value: new average =
// (qty x avg - out x cost) / (qty - out), never below zero.
func (s *Service) DeductAtCost(ctx context.Context, tx pgx.Tx, businessID, branchID uuid.UUID, userID *uuid.UUID, moveType, sourceType string, sourceID uuid.UUID, lines []Line) error {
	allowNeg, err := s.allowNegative(ctx, tx)
	if err != nil {
		return err
	}
	sorted := append([]Line(nil), lines...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ProductID.String() < sorted[j].ProductID.String() })
	for _, ln := range sorted {
		if ln.Qty.LessThanOrEqual(money.Zero) {
			continue
		}
		var qty, avg decimal.Decimal
		if err := tx.QueryRow(ctx, `
			SELECT qty_on_hand, avg_cost FROM stock_levels WHERE branch_id = $1 AND product_id = $2 FOR UPDATE
		`, branchID, ln.ProductID).Scan(&qty, &avg); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return httpx.Conflict("insufficient_stock", "There is not enough stock for this product")
			}
			return err
		}
		if !allowNeg && qty.LessThan(ln.Qty) {
			return httpx.Conflict("insufficient_stock", "There is not enough stock for this product")
		}
		cost := money.Cost(ln.UnitCost)
		newQty := qty.Sub(ln.Qty)
		newAvg := money.Zero
		if newQty.IsPositive() {
			newAvg = money.Cost(qty.Mul(avg).Sub(ln.Qty.Mul(cost)).Div(newQty))
			if newAvg.IsNegative() {
				newAvg = money.Zero
			}
		}
		var createdBy any
		if userID != nil {
			createdBy = *userID
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO stock_movements (id, business_id, branch_id, product_id, type, qty, unit_cost, qty_after, avg_cost_after, source_type, source_id, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		`, ids.New(), businessID, branchID, ln.ProductID, moveType, ln.Qty.Neg(), cost, newQty, newAvg, sourceType, sourceID, createdBy); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE stock_levels SET qty_on_hand = $3, avg_cost = $4 WHERE branch_id = $1 AND product_id = $2`, branchID, ln.ProductID, newQty, newAvg); err != nil {
			return err
		}
	}
	return nil
}

// Level returns quantity and average cost of a product in a branch (zero when never stocked).
func (s *Service) Level(ctx context.Context, tx pgx.Tx, branchID, productID uuid.UUID) (decimal.Decimal, decimal.Decimal, error) {
	var qty, avg decimal.Decimal
	err := tx.QueryRow(ctx, `SELECT qty_on_hand, avg_cost FROM stock_levels WHERE branch_id = $1 AND product_id = $2`, branchID, productID).Scan(&qty, &avg)
	if errors.Is(err, pgx.ErrNoRows) {
		return money.Zero, money.Zero, nil
	}
	return qty, avg, err
}
