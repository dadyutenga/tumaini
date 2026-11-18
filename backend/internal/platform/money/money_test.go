package money

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPriceLineTaxInclusiveMatchesSpecExample(t *testing.T) {
	// API reference example: 9,000 with 18% included tax carries 1,372.88 of tax.
	r := PriceLine(LineInput{Qty: M("1"), UnitPrice: M("9000"), TaxRate: M("18")}, true)
	require.Equal(t, "1372.88", r.Tax.StringFixed(2))
	require.Equal(t, "9000.00", r.Total.StringFixed(2))
	require.Equal(t, "7627.12", r.Net.StringFixed(2))
}

func TestPriceLineTaxOnDiscountedAmount(t *testing.T) {
	// BR-07: tax is calculated on the discounted line.
	r := PriceLine(LineInput{Qty: M("3"), UnitPrice: M("2500"), Discount: M("500"), TaxRate: M("18")}, false)
	require.Equal(t, "7500.00", r.Gross.StringFixed(2))
	require.Equal(t, "7000.00", r.Net.StringFixed(2))
	require.Equal(t, "1260.00", r.Tax.StringFixed(2))
	require.Equal(t, "8260.00", r.Total.StringFixed(2))
}

func TestDiscountNeverExceedsLine(t *testing.T) {
	r := PriceLine(LineInput{Qty: M("1"), UnitPrice: M("100"), Discount: M("150")}, false)
	require.True(t, r.Total.IsZero())
	require.Equal(t, "100.00", r.Discount.StringFixed(2))
}

func TestDiscountAmountAndRounding(t *testing.T) {
	require.Equal(t, "333.33", DiscountAmount(M("3333.33"), "percent", M("10")).StringFixed(2))
	require.Equal(t, "0.01", Money(M("0.005")).StringFixed(2), "half up")
}

func TestWeightedAverageBR05(t *testing.T) {
	require.Equal(t, "1500.0000", WeightedAverage(M("10"), M("1000"), M("10"), M("2000")).StringFixed(4))
	require.Equal(t, "2000.0000", WeightedAverage(M("0"), M("0"), M("5"), M("2000")).StringFixed(4))
	require.Equal(t, "2000.0000", WeightedAverage(M("-2"), M("900"), M("5"), M("2000")).StringFixed(4), "negative stock takes the incoming cost")
}
