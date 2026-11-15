package payroll

import "github.com/shopspring/decimal"

type Slip struct {
	Base      decimal.Decimal
	Absence   decimal.Decimal
	Allowance decimal.Decimal
	Gross     decimal.Decimal
	Loan      decimal.Decimal
	Net       decimal.Decimal
	Daily     decimal.Decimal
}

// Monthly applies BR-16 and BR-17. Unpaid days are deducted at base / working days, rounded half up to cents.
func Monthly(base, allowance decimal.Decimal, workingDays, unpaidDays int, loan decimal.Decimal) Slip {
	if workingDays <= 0 {
		workingDays = 26
	}
	daily := base.Div(decimal.NewFromInt(int64(workingDays))).Round(2)
	if unpaidDays > workingDays {
		unpaidDays = workingDays
	}
	absence := daily.Mul(decimal.NewFromInt(int64(unpaidDays))).Round(2)
	prorated := base.Sub(absence)
	if prorated.IsNegative() {
		prorated = decimal.Zero
	}
	gross := prorated.Add(allowance).Round(2)
	repay := loan
	if repay.GreaterThan(gross) {
		repay = gross
	}
	if repay.IsNegative() {
		repay = decimal.Zero
	}
	return Slip{Base: base, Absence: absence, Allowance: allowance, Gross: gross, Loan: repay, Net: gross.Sub(repay), Daily: daily}
}
