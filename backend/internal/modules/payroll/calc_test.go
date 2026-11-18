package payroll

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func dp(s string) *decimal.Decimal { v := d(s); return &v }

func eq(t *testing.T, want string, got decimal.Decimal, msg ...any) {
	t.Helper()
	require.True(t, got.Equal(d(want)), "want %s got %s %v", want, got, msg)
}

func TestMonthlyAcceptance(t *testing.T) {
	slip := Monthly(d("600000"), d("50000"), 26, 2, d("50000"))
	eq(t, "23076.92", slip.Daily)
	eq(t, "46153.84", slip.Absence)
	eq(t, "603846.16", slip.Gross)
	eq(t, "553846.16", slip.Net)
}

func TestBracketAmount(t *testing.T) {
	table := []Bracket{
		{From: d("0"), To: dp("270000"), Rate: d("0")},
		{From: d("270000.01"), To: dp("520000"), Rate: d("8"), Fixed: d("0")},
		{From: d("520000.01"), To: nil, Rate: d("20"), Fixed: d("20000")},
	}
	eq(t, "0", BracketAmount(table, d("200000")))
	eq(t, "8000.00", BracketAmount(table, d("370000"))) // 99999.99 x 8% = 7999.9992
	eq(t, "36000.00", BracketAmount(table, d("600000.01")))
}

func TestComputeFull(t *testing.T) {
	statutory, union, housing, meals, employer := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	loan := uuid.New()
	r := Compute(Input{
		PayType: "monthly", Base: d("520000"), WorkingDays: 26, UnpaidDays: d("1"),
		StandardHours: d("8"), OvertimeHours: d("4"), OvertimeMultiplier: d("1.5"),
		Components: []Component{
			{ID: union, Name: "Union", Kind: "deduction", Calc: "fixed", Value: d("5000"), Sort: 1},
			{ID: statutory, Name: "Social security", Kind: "deduction", Calc: "percent_of_gross", Value: d("10"), Statutory: true, Sort: 9},
			{ID: housing, Name: "Housing", Kind: "allowance", Calc: "percent_of_base", Value: d("10"), Taxable: true},
			{ID: meals, Name: "Meals", Kind: "allowance", Calc: "fixed", Value: d("20000"), Taxable: false},
			{ID: employer, Name: "Employer SSF", Kind: "employer_contribution", Calc: "percent_of_gross", Value: d("10")},
		},
		Commission: d("15000"),
		Bonuses:    []Item{{Label: "Target bonus", Amount: d("10000")}},
		Deductions: []Item{{Label: "Shortage", Amount: d("3000")}},
		Loans:      []LoanDue{{ID: loan, Label: "Loan", Due: d("40000")}},
	})
	// daily 20000, hourly 2500, absence 20000, prorated 500000
	eq(t, "20000", r.Daily)
	eq(t, "2500", r.Hourly)
	eq(t, "500000", r.ProratedBase)
	eq(t, "70000", r.Allowances)    // 50000 housing + 20000 meals
	eq(t, "15000", r.Overtime)      // 4 x 2500 x 1.5
	eq(t, "610000", r.Gross)        // 500000 + 70000 + 15000 overtime + 15000 commission + 10000 bonus
	eq(t, "590000", r.TaxableGross) // meals are not taxable
	// statutory 10% of taxable gross 59000, union 5000, shortage 3000, loan 40000
	eq(t, "40000", r.Loan)
	eq(t, "107000", r.TotalDeductions)
	eq(t, "503000", r.Net)
	eq(t, "61000", r.Employer) // 10% of gross
	require.Equal(t, "statutory", r.Lines[firstKind(r.Lines, "statutory")].Kind)
	require.Less(t, firstKind(r.Lines, "statutory"), firstKind(r.Lines, "deduction"), "statutory is taken first")
}

func firstKind(lines []Line, kind string) int {
	for i, l := range lines {
		if l.Kind == kind {
			return i
		}
	}
	return -1
}

func TestNetNeverNegativeAndLoanCapped(t *testing.T) {
	r := Compute(Input{
		PayType: "monthly", Base: d("100000"), WorkingDays: 26, UnpaidDays: d("20"),
		Deductions: []Item{{Label: "Damage", Amount: d("10000")}},
		Loans:      []LoanDue{{ID: uuid.New(), Label: "Loan", Due: d("50000")}},
	})
	// daily 3846.15, absence 76923.00, gross 23077.00, damage 10000, loan capped to 13077.00
	eq(t, "23077.00", r.Gross)
	eq(t, "13077.00", r.Loan)
	eq(t, "0", r.Net)
	require.False(t, r.Net.IsNegative())
}

func TestUnpaidDaysCappedAtWorkingDays(t *testing.T) {
	r := Compute(Input{PayType: "monthly", Base: d("260000"), WorkingDays: 26, UnpaidDays: d("40")})
	eq(t, "0", r.Gross)
	eq(t, "260000", r.Absence)
}

func TestDailyAndHourlyPay(t *testing.T) {
	daily := Compute(Input{PayType: "daily", Base: d("15000"), DaysWorked: d("20.5"), StandardHours: d("8"), OvertimeHours: d("2"), OvertimeMultiplier: d("2")})
	eq(t, "307500", daily.ProratedBase)
	eq(t, "1875", daily.Hourly)
	eq(t, "7500", daily.Overtime)
	hourly := Compute(Input{PayType: "hourly", Base: d("2000"), HoursWorked: d("100")})
	eq(t, "200000", hourly.Gross)
}

func TestLatenessDeduction(t *testing.T) {
	r := Compute(Input{PayType: "monthly", Base: d("520000"), WorkingDays: 26, StandardHours: d("8"), LateMinutes: 30, LatenessDeducts: true})
	eq(t, "1250", r.TotalDeductions) // hourly 2500 / 60 x 30
	off := Compute(Input{PayType: "monthly", Base: d("520000"), WorkingDays: 26, LateMinutes: 30})
	eq(t, "0", off.TotalDeductions)
}
