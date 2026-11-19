package chart

import (
	"context"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/ledger"
	"bms/internal/platform/httpx"
	"bms/internal/platform/ids"
	"bms/internal/platform/money"
)

type Account struct {
	Code, Name, Type string
}

var System = []Account{
	{"1000", "Cash on hand", "asset"},
	{"1010", "Bank accounts", "asset"},
	{"1020", "Mobile money", "asset"},
	{"1030", "Petty cash", "asset"},
	{"1100", "Accounts receivable", "asset"},
	{"1200", "Inventory", "asset"},
	{"1210", "Goods in transit", "asset"},
	{"1300", "Staff loans and advances", "asset"},
	{"1400", "Supplier advances", "asset"},
	{"1500", "Input tax recoverable", "asset"},
	{"2000", "Accounts payable", "liability"},
	{"2050", "Goods received not invoiced", "liability"},
	{"2100", "Output tax payable", "liability"},
	{"2200", "Salaries payable", "liability"},
	{"2210", "Statutory deductions payable", "liability"},
	{"2300", "Customer credits", "liability"},
	{"2400", "Client deposits", "liability"},
	{"3000", "Owner equity", "equity"},
	{"3100", "Opening balance equity", "equity"},
	{"3200", "Owner drawings", "equity"},
	{"4000", "Sales revenue", "income"},
	{"4010", "Sales returns", "income"},
	{"4100", "Other income", "income"},
	{"4200", "Stock gain", "income"},
	{"4300", "Service revenue", "income"},
	{"5000", "Cost of goods sold", "expense"},
	{"5100", "Stock loss and adjustments", "expense"},
	{"5110", "Transfer loss", "expense"},
	{"5200", "Service costs", "expense"},
	{"6000", "Rent", "expense"},
	{"6010", "Utilities", "expense"},
	{"6020", "Transport", "expense"},
	{"6040", "Repairs", "expense"},
	{"6050", "Marketing", "expense"},
	{"6090", "Other expenses", "expense"},
	{"6100", "Salaries and wages", "expense"},
	{"6110", "Employer contributions", "expense"},
	{"6200", "Bad debts", "expense"},
	{"6300", "Cash over or short", "expense"},
	{"6400", "Bank and transfer charges", "expense"},
}

func Seed(ctx context.Context, tx pgx.Tx, businessID uuid.UUID) error {
	for _, a := range System {
		if _, err := tx.Exec(ctx, `
			INSERT INTO ledger_accounts (id, business_id, code, name, type, is_system)
			VALUES ($1,$2,$3,$4,$5,true)
		`, ids.New(), businessID, a.Code, a.Name, a.Type); err != nil {
			return err
		}
	}
	return nil
}

type MoneyInput struct {
	BusinessID     uuid.UUID
	BranchID       *uuid.UUID
	Name           string
	Type           string
	Provider       string
	AccountNumber  string
	CustodianID    *uuid.UUID
	Opening        decimal.Decimal
	AllowOverdraft bool
	UserID         uuid.UUID
	EntryDate      string
}

func CreateMoneyAccount(ctx context.Context, tx pgx.Tx, led *ledger.Service, in MoneyInput) (uuid.UUID, error) {
	parent, start, end, err := moneyRange(in.Type)
	if err != nil {
		return uuid.Nil, err
	}
	code, err := nextCode(ctx, tx, start, end)
	if err != nil {
		return uuid.Nil, err
	}
	ledgerID := ids.New()
	if _, err := tx.Exec(ctx, `
		INSERT INTO ledger_accounts (id, business_id, code, name, type, is_system, parent_code)
		VALUES ($1,$2,$3,$4,'asset',true,$5)
	`, ledgerID, in.BusinessID, code, in.Name, parent); err != nil {
		return uuid.Nil, err
	}
	id := ids.New()
	var branch, custodian, provider, number any
	if in.BranchID != nil {
		branch = *in.BranchID
	}
	if in.CustodianID != nil {
		custodian = *in.CustodianID
	}
	if in.Provider != "" {
		provider = in.Provider
	}
	if in.AccountNumber != "" {
		number = in.AccountNumber
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO money_accounts (id, business_id, branch_id, ledger_account_id, name, type, provider, account_number, custodian_id, allow_overdraft)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	`, id, in.BusinessID, branch, ledgerID, in.Name, in.Type, provider, number, custodian, in.AllowOverdraft); err != nil {
		return uuid.Nil, err
	}
	opening := money.Money(in.Opening)
	if opening.IsPositive() {
		if _, err := led.Post(ctx, tx, in.BusinessID, in.EntryDate, "opening_balance", id, "Opening balance "+in.Name, &in.UserID, []ledger.Line{
			{Code: code, BranchID: in.BranchID, MoneyAccountID: &id, Debit: opening},
			{Code: "3100", BranchID: in.BranchID, Credit: opening},
		}); err != nil {
			return uuid.Nil, err
		}
	}
	return id, nil
}

func moneyRange(typ string) (parent string, start, end int, err error) {
	switch typ {
	case "cash":
		return "1000", 1001, 1009, nil
	case "bank":
		return "1010", 1011, 1019, nil
	case "mobile_money":
		return "1020", 1021, 1029, nil
	case "petty_cash":
		return "1030", 1031, 1039, nil
	default:
		return "", 0, 0, httpx.BadRequest("invalid_body", "Unknown money account type")
	}
}

func nextCode(ctx context.Context, tx pgx.Tx, start, end int) (string, error) {
	var code string
	err := tx.QueryRow(ctx, `
		SELECT code FROM ledger_accounts
		WHERE code ~ '^[0-9]+$' AND code::int BETWEEN $1 AND $2
		ORDER BY code::int DESC LIMIT 1
	`, start, end).Scan(&code)
	next := start
	if err == nil {
		n, _ := strconv.Atoi(code)
		next = n + 1
	} else if err != pgx.ErrNoRows {
		return "", err
	}
	if next > end {
		return "", httpx.Conflict("invalid_status", "No free ledger code remains for this account type")
	}
	return fmt.Sprintf("%04d", next), nil
}
