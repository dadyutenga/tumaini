package payroll

import (
	"sort"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var (
	hundred = decimal.NewFromInt(100)
	zero    = decimal.Zero
)

func r2(d decimal.Decimal) decimal.Decimal { return d.Round(2) }

// Bracket is one band of a bracket table. Amount = Fixed + (x - From) * Rate / 100
// for the band that contains x. To nil means no upper bound.
type Bracket struct {
	From  decimal.Decimal  `json:"from"`
	To    *decimal.Decimal `json:"to"`
	Rate  decimal.Decimal  `json:"rate"`
	Fixed decimal.Decimal  `json:"fixed"`
}

// BracketAmount applies a bracket table to x.
func BracketAmount(table []Bracket, x decimal.Decimal) decimal.Decimal {
	for _, b := range table {
		if x.LessThan(b.From) {
			continue
		}
		if b.To != nil && x.GreaterThan(*b.To) {
			continue
		}
		return r2(b.Fixed.Add(x.Sub(b.From).Mul(b.Rate).Div(hundred)))
	}
	return zero
}

// Component is a salary component as it applies to one employee.
type Component struct {
	ID        uuid.UUID
	Name      string
	Kind      string // allowance, deduction, employer_contribution
	Calc      string // fixed, percent_of_base, percent_of_gross, bracket
	Value     decimal.Decimal
	Brackets  []Bracket
	Taxable   bool
	Statutory bool
	Sort      int
}

// Item is a one-off bonus or deduction.
type Item struct {
	ID     *uuid.UUID
	Label  string
	Amount decimal.Decimal
}

// LoanDue is the scheduled repayment for one loan, already capped by its balance.
type LoanDue struct {
	ID    uuid.UUID
	Label string
	Due   decimal.Decimal
}

type Input struct {
	PayType            string // monthly, daily, hourly
	Base               decimal.Decimal
	WorkingDays        int
	UnpaidDays         decimal.Decimal // absent, unpaid leave and days not employed (monthly pay)
	DaysWorked         decimal.Decimal // daily pay units
	HoursWorked        decimal.Decimal // hourly pay units
	StandardHours      decimal.Decimal
	OvertimeHours      decimal.Decimal // approved only (BR-18)
	OvertimeMultiplier decimal.Decimal
	LateMinutes        int
	LatenessDeducts    bool
	Components         []Component
	Commission         decimal.Decimal
	Bonuses            []Item
	Deductions         []Item
	Loans              []LoanDue
}

type Line struct {
	Kind        string
	Label       string
	ComponentID *uuid.UUID
	LoanID      *uuid.UUID
	AdjustID    *uuid.UUID
	Amount      decimal.Decimal
}

type Result struct {
	Daily, Hourly   decimal.Decimal
	Absence         decimal.Decimal
	ProratedBase    decimal.Decimal
	Allowances      decimal.Decimal
	Overtime        decimal.Decimal
	Gross           decimal.Decimal
	TaxableGross    decimal.Decimal
	TotalDeductions decimal.Decimal // includes loan repayments
	Loan            decimal.Decimal
	Employer        decimal.Decimal
	Net             decimal.Decimal
	Lines           []Line
}

// Compute applies BR-16, BR-17 and BR-18 and FR-PAY-06..09.
//
//	gross = prorated base + allowances + overtime + commission + bonuses
//	net   = gross - deductions - loan repayment, never below zero
//
// Deductions are taken in order (statutory, other components, one-off
// deductions, lateness, loans); each is capped at what is left of net pay.
func Compute(in Input) Result {
	var res Result
	if in.WorkingDays <= 0 {
		in.WorkingDays = 26
	}
	if !in.StandardHours.IsPositive() {
		in.StandardHours = decimal.NewFromInt(8)
	}
	if !in.OvertimeMultiplier.IsPositive() {
		in.OvertimeMultiplier = decimal.NewFromInt(1)
	}
	wd := decimal.NewFromInt(int64(in.WorkingDays))
	add := func(l Line) {
		if l.Amount.IsPositive() {
			res.Lines = append(res.Lines, l)
		}
	}
	switch in.PayType {
	case "daily":
		res.Daily = r2(in.Base)
		res.Hourly = r2(in.Base.Div(in.StandardHours))
		res.ProratedBase = r2(in.Base.Mul(in.DaysWorked))
		add(Line{Kind: "base", Label: "Daily pay (" + in.DaysWorked.String() + " days)", Amount: res.ProratedBase})
	case "hourly":
		res.Hourly = r2(in.Base)
		res.Daily = r2(in.Base.Mul(in.StandardHours))
		res.ProratedBase = r2(in.Base.Mul(in.HoursWorked))
		add(Line{Kind: "base", Label: "Hourly pay (" + in.HoursWorked.String() + " hours)", Amount: res.ProratedBase})
	default:
		res.Daily = r2(in.Base.Div(wd))
		res.Hourly = r2(res.Daily.Div(in.StandardHours))
		unpaid := in.UnpaidDays
		if unpaid.GreaterThan(wd) {
			unpaid = wd
		}
		if unpaid.IsNegative() {
			unpaid = zero
		}
		res.Absence = r2(res.Daily.Mul(unpaid))
		if res.Absence.GreaterThan(in.Base) {
			res.Absence = in.Base
		}
		res.ProratedBase = in.Base.Sub(res.Absence)
		add(Line{Kind: "base", Label: "Base pay", Amount: r2(in.Base)})
		add(Line{Kind: "absence", Label: "Unpaid days (" + unpaid.String() + ")", Amount: res.Absence})
	}

	comps := append([]Component(nil), in.Components...)
	sort.SliceStable(comps, func(i, j int) bool {
		if comps[i].Statutory != comps[j].Statutory {
			return comps[i].Statutory
		}
		return comps[i].Sort < comps[j].Sort
	})

	nonTaxable := zero
	for _, c := range comps {
		if c.Kind != "allowance" {
			continue
		}
		amt := amountOf(c, res.ProratedBase, res.ProratedBase)
		res.Allowances = res.Allowances.Add(amt)
		if !c.Taxable {
			nonTaxable = nonTaxable.Add(amt)
		}
		id := c.ID
		add(Line{Kind: "allowance", Label: c.Name, ComponentID: &id, Amount: amt})
	}
	res.Overtime = r2(in.OvertimeHours.Mul(res.Hourly).Mul(in.OvertimeMultiplier))
	add(Line{Kind: "overtime", Label: "Overtime (" + in.OvertimeHours.String() + " h)", Amount: res.Overtime})
	commission := r2(in.Commission)
	add(Line{Kind: "commission", Label: "Commission", Amount: commission})
	bonuses := zero
	for _, b := range in.Bonuses {
		amt := r2(b.Amount)
		bonuses = bonuses.Add(amt)
		add(Line{Kind: "bonus", Label: b.Label, AdjustID: b.ID, Amount: amt})
	}
	res.Gross = r2(res.ProratedBase.Add(res.Allowances).Add(res.Overtime).Add(commission).Add(bonuses))
	res.TaxableGross = res.Gross.Sub(nonTaxable)

	left := res.Gross
	take := func(amount decimal.Decimal) decimal.Decimal {
		amount = r2(amount)
		if amount.GreaterThan(left) {
			amount = left
		}
		if amount.IsNegative() {
			amount = zero
		}
		left = left.Sub(amount)
		return amount
	}
	for _, c := range comps {
		if c.Kind != "deduction" {
			continue
		}
		amt := take(amountOf(c, res.ProratedBase, res.TaxableGross))
		kind := "deduction"
		if c.Statutory {
			kind = "statutory"
		}
		id := c.ID
		add(Line{Kind: kind, Label: c.Name, ComponentID: &id, Amount: amt})
	}
	for _, d := range in.Deductions {
		add(Line{Kind: "deduction", Label: d.Label, AdjustID: d.ID, Amount: take(d.Amount)})
	}
	if in.LatenessDeducts && in.LateMinutes > 0 {
		perMinute := res.Hourly.Div(decimal.NewFromInt(60))
		add(Line{Kind: "deduction", Label: "Lateness (" + decimal.NewFromInt(int64(in.LateMinutes)).String() + " min)", Amount: take(perMinute.Mul(decimal.NewFromInt(int64(in.LateMinutes))))})
	}
	for _, l := range in.Loans {
		id := l.ID
		amt := take(l.Due)
		res.Loan = res.Loan.Add(amt)
		add(Line{Kind: "loan", Label: l.Label, LoanID: &id, Amount: amt})
	}
	res.Net = left
	res.TotalDeductions = res.Gross.Sub(res.Net)

	for _, c := range comps {
		if c.Kind != "employer_contribution" {
			continue
		}
		amt := r2(amountOf(c, res.ProratedBase, res.Gross))
		res.Employer = res.Employer.Add(amt)
		id := c.ID
		add(Line{Kind: "employer_contribution", Label: c.Name, ComponentID: &id, Amount: amt})
	}
	return res
}

// amountOf evaluates a component. Percent of base uses the prorated base;
// percent of gross and brackets use the gross passed in (taxable gross for
// deductions).
func amountOf(c Component, base, gross decimal.Decimal) decimal.Decimal {
	switch c.Calc {
	case "percent_of_base":
		return r2(base.Mul(c.Value).Div(hundred))
	case "percent_of_gross":
		return r2(gross.Mul(c.Value).Div(hundred))
	case "bracket":
		if c.Kind == "allowance" {
			return BracketAmount(c.Brackets, base)
		}
		return BracketAmount(c.Brackets, gross)
	default:
		return r2(c.Value)
	}
}

// Slip is the simple monthly result kept for the acceptance calculation.
type Slip struct {
	Base      decimal.Decimal
	Absence   decimal.Decimal
	Allowance decimal.Decimal
	Gross     decimal.Decimal
	Loan      decimal.Decimal
	Net       decimal.Decimal
	Daily     decimal.Decimal
}

// Monthly is Compute for a monthly employee with one fixed allowance and one loan.
func Monthly(base, allowance decimal.Decimal, workingDays, unpaidDays int, loan decimal.Decimal) Slip {
	in := Input{PayType: "monthly", Base: base, WorkingDays: workingDays, UnpaidDays: decimal.NewFromInt(int64(unpaidDays))}
	if allowance.IsPositive() {
		in.Components = []Component{{Name: "Allowance", Kind: "allowance", Calc: "fixed", Value: allowance, Taxable: true}}
	}
	if loan.IsPositive() {
		in.Loans = []LoanDue{{Label: "Loan", Due: loan}}
	}
	r := Compute(in)
	return Slip{Base: base, Absence: r.Absence, Allowance: r.Allowances, Gross: r.Gross, Loan: r.Loan, Net: r.Net, Daily: r.Daily}
}
