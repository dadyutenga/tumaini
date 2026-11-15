package ledger

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/platform/httpx"
	"bms/internal/platform/ids"
	"bms/internal/platform/money"
)

type Line struct {
	Code           string
	BranchID       *uuid.UUID
	MoneyAccountID *uuid.UUID
	PartyType      string
	PartyID        *uuid.UUID
	Debit          decimal.Decimal
	Credit         decimal.Decimal
}

type Service struct{}

func New() *Service { return &Service{} }

func (s *Service) Post(ctx context.Context, tx pgx.Tx, businessID uuid.UUID, entryDate string, sourceType string, sourceID uuid.UUID, description string, userID *uuid.UUID, lines []Line) (uuid.UUID, error) {
	if err := s.ensureOpen(ctx, tx, entryDate); err != nil {
		return uuid.Nil, err
	}
	cleaned := make([]Line, 0, len(lines))
	debit, credit := money.Zero, money.Zero
	for _, ln := range lines {
		ln.Debit = money.Money(ln.Debit)
		ln.Credit = money.Money(ln.Credit)
		if ln.Debit.IsZero() && ln.Credit.IsZero() {
			continue
		}
		if ln.Debit.IsPositive() && ln.Credit.IsPositive() {
			return uuid.Nil, fmt.Errorf("journal line %s has both sides", ln.Code)
		}
		debit = debit.Add(ln.Debit)
		credit = credit.Add(ln.Credit)
		cleaned = append(cleaned, ln)
	}
	if len(cleaned) < 2 {
		return uuid.Nil, fmt.Errorf("journal entry needs at least two lines")
	}
	if !debit.Equal(credit) {
		return uuid.Nil, fmt.Errorf("journal does not balance: debit %s credit %s", debit, credit)
	}
	accounts, err := s.codes(ctx, tx)
	if err != nil {
		return uuid.Nil, err
	}
	entryID := ids.New()
	var createdBy any
	if userID != nil {
		createdBy = *userID
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO journal_entries (id, business_id, entry_date, source_type, source_id, description, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
	`, entryID, businessID, entryDate, sourceType, sourceID, description, createdBy); err != nil {
		return uuid.Nil, err
	}
	for _, ln := range cleaned {
		acc, ok := accounts[ln.Code]
		if !ok {
			return uuid.Nil, fmt.Errorf("unknown ledger account %s", ln.Code)
		}
		var branch, moneyAcc, party any
		if ln.BranchID != nil {
			branch = *ln.BranchID
		}
		if ln.MoneyAccountID != nil {
			moneyAcc = *ln.MoneyAccountID
		}
		if ln.PartyID != nil {
			party = *ln.PartyID
		}
		var partyType any
		if ln.PartyType != "" {
			partyType = ln.PartyType
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO journal_lines (id, business_id, entry_id, branch_id, ledger_account_id, money_account_id, party_type, party_id, debit, credit, entry_date)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		`, ids.New(), businessID, entryID, branch, acc, moneyAcc, partyType, party, ln.Debit, ln.Credit, entryDate); err != nil {
			return uuid.Nil, err
		}
	}
	return entryID, nil
}

func (s *Service) ensureOpen(ctx context.Context, tx pgx.Tx, entryDate string) error {
	var locked string
	err := tx.QueryRow(ctx, `SELECT locked_until::text FROM period_locks`).Scan(&locked)
	if err != nil {
		if err.Error() == "no rows in result set" || err == pgx.ErrNoRows {
			return nil
		}
		return err
	}
	if entryDate <= locked {
		return httpx.Conflict("period_locked", "That date is in a locked period. Ask the owner to reopen it before posting.")
	}
	return nil
}

func (s *Service) codes(ctx context.Context, tx pgx.Tx) (map[string]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT code, id FROM ledger_accounts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]uuid.UUID{}
	for rows.Next() {
		var code string
		var id uuid.UUID
		if err := rows.Scan(&code, &id); err != nil {
			return nil, err
		}
		out[code] = id
	}
	return out, rows.Err()
}

func (s *Service) MoneyBalance(ctx context.Context, tx pgx.Tx, accountID uuid.UUID) (decimal.Decimal, error) {
	var bal decimal.Decimal
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(debit - credit), 0) FROM journal_lines WHERE money_account_id = $1
	`, accountID).Scan(&bal)
	return bal, err
}

func (s *Service) AccountCodeForMoney(ctx context.Context, tx pgx.Tx, accountID uuid.UUID) (string, bool, error) {
	var code string
	var overdraft bool
	err := tx.QueryRow(ctx, `
		SELECT la.code, ma.allow_overdraft
		FROM money_accounts ma JOIN ledger_accounts la ON la.id = ma.ledger_account_id
		WHERE ma.id = $1
	`, accountID).Scan(&code, &overdraft)
	if err != nil {
		return "", false, err
	}
	return code, overdraft, nil
}

// EnsureFunds rejects an outflow that would take a non-overdraft account below zero.
func (s *Service) EnsureFunds(ctx context.Context, tx pgx.Tx, accountID uuid.UUID, outflow decimal.Decimal) error {
	_, overdraft, err := s.AccountCodeForMoney(ctx, tx, accountID)
	if err != nil {
		return err
	}
	if overdraft {
		return nil
	}
	bal, err := s.MoneyBalance(ctx, tx, accountID)
	if err != nil {
		return err
	}
	if bal.LessThan(outflow) {
		return httpx.Conflict("insufficient_funds", "That account does not have enough money for this payment")
	}
	return nil
}

func (s *Service) Reverse(ctx context.Context, tx pgx.Tx, businessID uuid.UUID, sourceType string, sourceID uuid.UUID, entryDate, description string, userID *uuid.UUID) error {
	rows, err := tx.Query(ctx, `
		SELECT je.id
		FROM journal_entries je
		WHERE je.source_type = $1 AND je.source_id = $2
		  AND NOT EXISTS (SELECT 1 FROM journal_entries r WHERE r.reverses_id = je.id)
	`, sourceType, sourceID)
	if err != nil {
		return err
	}
	var entryIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		entryIDs = append(entryIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range entryIDs {
		if err := s.reverseOne(ctx, tx, businessID, id, entryDate, description, userID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) reverseOne(ctx context.Context, tx pgx.Tx, businessID, original uuid.UUID, entryDate, description string, userID *uuid.UUID) error {
	if err := s.ensureOpen(ctx, tx, entryDate); err != nil {
		return err
	}
	newID := ids.New()
	var createdBy any
	if userID != nil {
		createdBy = *userID
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO journal_entries (id, business_id, entry_date, source_type, source_id, description, reverses_id, created_by)
		SELECT $1, business_id, $2, source_type, source_id, $3, id, $4
		FROM journal_entries WHERE id = $5
	`, newID, entryDate, description, createdBy, original); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO journal_lines (id, business_id, entry_id, branch_id, ledger_account_id, money_account_id, party_type, party_id, debit, credit, entry_date)
		SELECT gen_random_uuid(), business_id, $1, branch_id, ledger_account_id, money_account_id, party_type, party_id, credit, debit, $2
		FROM journal_lines WHERE entry_id = $3
	`, newID, entryDate, original)
	return err
}
