package money

import (
	"github.com/shopspring/decimal"
)

var Zero = decimal.Zero

func M(v string) decimal.Decimal {
	d, err := decimal.NewFromString(v)
	if err != nil {
		return decimal.Zero
	}
	return d
}

// Round half away from zero, which is half up for the positive amounts this system stores.
func Round(d decimal.Decimal, places int32) decimal.Decimal {
	return d.Round(places)
}

func Money(d decimal.Decimal) decimal.Decimal { return d.Round(2) }
func Qty(d decimal.Decimal) decimal.Decimal   { return d.Round(3) }
func Cost(d decimal.Decimal) decimal.Decimal  { return d.Round(4) }

type LineInput struct {
	Qty       decimal.Decimal
	UnitPrice decimal.Decimal
	Discount  decimal.Decimal // already an amount, not a percent
	TaxRate   decimal.Decimal // percent, for example 18
}

type LineResult struct {
	Gross    decimal.Decimal
	Discount decimal.Decimal
	Net      decimal.Decimal
	Tax      decimal.Decimal
	Total    decimal.Decimal
}

// PriceLine applies BR-07. Tax is calculated on the discounted amount.
// When prices include tax, Total is the discounted gross and Tax is extracted from it.
func PriceLine(in LineInput, pricesIncludeTax bool) LineResult {
	gross := Money(in.Qty.Mul(in.UnitPrice))
	discount := Money(in.Discount)
	if discount.GreaterThan(gross) {
		discount = gross
	}
	discounted := gross.Sub(discount)
	hundred := decimal.NewFromInt(100)
	var net, tax, total decimal.Decimal
	if in.TaxRate.LessThanOrEqual(Zero) {
		net, tax, total = discounted, Zero, discounted
	} else if pricesIncludeTax {
		tax = Money(discounted.Mul(in.TaxRate).Div(hundred.Add(in.TaxRate)))
		total = discounted
		net = total.Sub(tax)
	} else {
		net = discounted
		tax = Money(discounted.Mul(in.TaxRate).Div(hundred))
		total = net.Add(tax)
	}
	return LineResult{Gross: gross, Discount: discount, Net: net, Tax: tax, Total: total}
}

// DiscountAmount converts a percent or an absolute discount into money.
func DiscountAmount(base decimal.Decimal, kind string, value decimal.Decimal) decimal.Decimal {
	if kind == "percent" {
		return Money(base.Mul(value).Div(decimal.NewFromInt(100)))
	}
	return Money(value)
}

// WeightedAverage implements BR-05.
func WeightedAverage(oldQty, oldCost, inQty, inCost decimal.Decimal) decimal.Decimal {
	newQty := oldQty.Add(inQty)
	if newQty.IsZero() {
		return Zero
	}
	if oldQty.LessThanOrEqual(Zero) {
		return Cost(inCost)
	}
	return Cost(oldQty.Mul(oldCost).Add(inQty.Mul(inCost)).Div(newQty))
}
