package app_test

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

func TestCatalogProductsLookupFiltersAndCost(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	staff, _ := e.user(t, "staff")

	// Validation: a name and a non-negative price are required.
	require.Equal(t, 400, o.call(t, http.MethodPost, "/api/v1/products", map[string]any{"name": "", "sell_price": "10"}, "").code)
	require.Equal(t, 400, o.call(t, http.MethodPost, "/api/v1/products", map[string]any{"name": "Neg", "sell_price": "-1"}, "").code)
	require.Equal(t, 400, o.call(t, http.MethodPost, "/api/v1/products", map[string]any{"name": "Odd kind", "sell_price": "1", "kind": "gadget"}, "").code)

	cat := o.call(t, http.MethodPost, "/api/v1/categories", map[string]any{"name": "Grains"}, "")
	require.Equal(t, 201, cat.code, cat.raw)
	rice := o.call(t, http.MethodPost, "/api/v1/products", map[string]any{"name": "Rice 1kg", "sell_price": "2500", "barcode": "6001", "category_id": cat.data["id"], "reorder_level": "5"}, "")
	require.Equal(t, 201, rice.code, rice.raw)
	require.NotEmpty(t, str(rice.data["sku"]), "a SKU is generated when left empty")
	sugar := o.call(t, http.MethodPost, "/api/v1/products", map[string]any{"name": "Sugar", "sku": "SUG-1", "sell_price": "3000", "barcode": "6002"}, "")
	require.Equal(t, 201, sugar.code, sugar.raw)
	service := o.call(t, http.MethodPost, "/api/v1/products", map[string]any{"name": "Delivery", "sell_price": "1000", "kind": "service"}, "")
	require.Equal(t, 201, service.code, service.raw)

	// SKU and barcode are unique within the business.
	require.Equal(t, 409, o.call(t, http.MethodPost, "/api/v1/products", map[string]any{"name": "Clash", "sku": "SUG-1", "sell_price": "1"}, "").code)
	require.Equal(t, 409, o.call(t, http.MethodPost, "/api/v1/products", map[string]any{"name": "Clash 2", "barcode": "6001", "sell_price": "1"}, "").code)
	require.Equal(t, 409, o.call(t, http.MethodPut, "/api/v1/products/"+str(sugar.data["id"]), map[string]any{"barcode": "6001"}, "").code)

	// Barcode scan returns one exact match; SKU works too; unknown is 404.
	scan := staff.call(t, http.MethodGet, "/api/v1/products/lookup?barcode=6001", nil, "")
	require.Equal(t, 200, scan.code, scan.raw)
	require.Equal(t, "Rice 1kg", str(scan.data["name"]))
	require.Equal(t, "Sugar", str(staff.call(t, http.MethodGet, "/api/v1/products/lookup?barcode=SUG-1", nil, "").data["name"]))
	require.Equal(t, 404, staff.call(t, http.MethodGet, "/api/v1/products/lookup?barcode=000", nil, "").code)

	// Stock arrives at a cost. Cost and margin are only for users with products.view_cost (FR-INV-08).
	in := o.call(t, http.MethodPost, "/api/v1/stock-entries", map[string]any{"direction": "in", "reason": "opening", "items": []map[string]any{{"product_id": rice.data["id"], "qty": "3", "unit_cost": "1200"}}}, key())
	require.Equal(t, 201, in.code, in.raw)
	own := o.call(t, http.MethodGet, "/api/v1/products/lookup?barcode=6001", nil, "")
	require.Equal(t, "1200.0000", str(own.data["avg_cost"]))
	require.Nil(t, scan.data["avg_cost"])
	require.Nil(t, staff.call(t, http.MethodGet, "/api/v1/products/lookup?barcode=6001", nil, "").data["avg_cost"])
	detail := staff.call(t, http.MethodGet, "/api/v1/products/"+str(rice.data["id"]), nil, "")
	require.Equal(t, 200, detail.code, detail.raw)
	require.NotContains(t, detail.raw, "avg_cost\":\"1200")

	// List: search, category, active and low-stock filters, with pagination meta.
	list := staff.call(t, http.MethodGet, "/api/v1/products?q=rice", nil, "")
	require.Equal(t, 200, list.code, list.raw)
	require.Len(t, asList(list.body["data"]), 1)
	require.EqualValues(t, 1, dec(list.body["meta"].(map[string]any)["total"]).IntPart())
	require.Len(t, asList(staff.call(t, http.MethodGet, "/api/v1/products?category_id="+str(cat.data["id"]), nil, "").body["data"]), 1)
	low := staff.call(t, http.MethodGet, "/api/v1/products?low_stock=true", nil, "")
	require.Equal(t, 200, low.code, low.raw)
	require.Len(t, asList(low.body["data"]), 1, "3 on hand is at or below the reorder level of 5")
	paged := staff.call(t, http.MethodGet, "/api/v1/products?per_page=2&page=2", nil, "")
	require.Equal(t, 200, paged.code, paged.raw)
	require.Len(t, asList(paged.body["data"]), 1)
	require.EqualValues(t, 3, dec(paged.body["meta"].(map[string]any)["total"]).IntPart())
	require.Equal(t, 400, staff.call(t, http.MethodGet, "/api/v1/products?category_id=nope", nil, "").code)

	// Staff may view but not manage.
	require.Equal(t, 403, staff.call(t, http.MethodPost, "/api/v1/products", map[string]any{"name": "No", "sell_price": "1"}, "").code)
	require.Equal(t, 403, staff.call(t, http.MethodPut, "/api/v1/products/"+str(rice.data["id"]), map[string]any{"sell_price": "1"}, "").code)

	// A price change is logged with old and new price and the user (FR-INV-09).
	upd := o.call(t, http.MethodPut, "/api/v1/products/"+str(rice.data["id"]), map[string]any{"sell_price": "2800"}, "")
	require.Equal(t, 200, upd.code, upd.raw)
	hist := o.call(t, http.MethodGet, "/api/v1/products/"+str(rice.data["id"])+"/price-history", nil, "")
	require.Equal(t, 200, hist.code, hist.raw)
	rows := asList(hist.body["data"])
	require.NotEmpty(t, rows)
	require.Equal(t, "2500.00", str(rows[0]["old_price"]))
	require.Equal(t, "2800.00", str(rows[0]["new_price"]))

	// A product with stock history cannot switch between stock and service.
	require.Equal(t, 409, o.call(t, http.MethodPut, "/api/v1/products/"+str(rice.data["id"]), map[string]any{"kind": "service"}, "").code)

	// Deactivating hides it from sale: it cannot be priced or sold, but history stays.
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/products/"+str(sugar.data["id"])+"/deactivate", nil, "").code)
	preview := o.call(t, http.MethodPost, "/api/v1/sales/preview", map[string]any{"items": []map[string]any{{"product_id": sugar.data["id"], "qty": "1"}}}, "")
	require.Equal(t, 404, preview.code, preview.raw)
	inactive := o.call(t, http.MethodGet, "/api/v1/products?is_active=false", nil, "")
	require.Len(t, asList(inactive.body["data"]), 1)
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/products/"+str(sugar.data["id"])+"/activate", nil, "").code)
	require.Equal(t, 404, o.call(t, http.MethodPost, "/api/v1/products/"+key()+"/deactivate", nil, "").code)

	// Another business sees none of it.
	other := newEnv(t, defaultNow)
	require.Equal(t, 404, other.owner.call(t, http.MethodGet, "/api/v1/products/"+str(rice.data["id"]), nil, "").code)
	require.Equal(t, 404, other.owner.call(t, http.MethodGet, "/api/v1/products/lookup?barcode=6001", nil, "").code)
}

func TestCatalogBranchPricesCategoriesAndUnits(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	branchB := o.call(t, http.MethodPost, "/api/v1/branches", map[string]any{"name": "Kariakoo", "code": "KRK"}, "")
	require.Equal(t, 201, branchB.code, branchB.raw)
	bID := str(branchB.data["id"])
	p := o.call(t, http.MethodPost, "/api/v1/products", map[string]any{"name": "Soap", "sell_price": "1000"}, "")
	require.Equal(t, 201, p.code, p.raw)
	pid := str(p.data["id"])

	// A branch price overrides the base price in that branch only (FR-INV-04).
	set := o.call(t, http.MethodPut, "/api/v1/products/"+pid+"/branch-prices", map[string]any{"prices": []map[string]any{{"branch_id": bID, "sell_price": "1200"}}}, "")
	require.Equal(t, 200, set.code, set.raw)
	atMain := o.call(t, http.MethodPost, "/api/v1/sales/preview", map[string]any{"items": []map[string]any{{"product_id": pid, "qty": "1"}}}, "")
	require.Equal(t, 200, atMain.code, atMain.raw)
	require.Equal(t, "1000.00", str(atMain.data["total"]))
	b := &client{app: e.app.Fiber, token: o.token, branch: bID}
	atB := b.call(t, http.MethodPost, "/api/v1/sales/preview", map[string]any{"items": []map[string]any{{"product_id": pid, "qty": "1"}}}, "")
	require.Equal(t, 200, atB.code, atB.raw)
	require.Equal(t, "1200.00", str(atB.data["total"]))
	require.Equal(t, 400, o.call(t, http.MethodPut, "/api/v1/products/"+pid+"/branch-prices", map[string]any{"prices": []map[string]any{{"branch_id": bID, "sell_price": "-5"}}}, "").code)
	require.Equal(t, 404, o.call(t, http.MethodPut, "/api/v1/products/"+pid+"/branch-prices", map[string]any{"prices": []map[string]any{{"branch_id": key(), "sell_price": "5"}}}, "").code)
	hist := o.call(t, http.MethodGet, "/api/v1/products/"+pid+"/price-history", nil, "")
	require.NotEmpty(t, asList(hist.body["data"]), "branch price changes are logged too")

	// A manager of the main branch cannot set prices for a branch they are not in.
	mgr, _ := e.user(t, "manager")
	require.Equal(t, 404, mgr.call(t, http.MethodPut, "/api/v1/products/"+pid+"/branch-prices", map[string]any{"prices": []map[string]any{{"branch_id": bID, "sell_price": "1"}}}, "").code)

	// Categories nest, names are unique, a category with products cannot be deleted (FR-INV-10).
	parent := o.call(t, http.MethodPost, "/api/v1/categories", map[string]any{"name": "Household"}, "")
	require.Equal(t, 201, parent.code, parent.raw)
	child := o.call(t, http.MethodPost, "/api/v1/categories", map[string]any{"name": "Cleaning", "parent_id": parent.data["id"]}, "")
	require.Equal(t, 201, child.code, child.raw)
	require.Equal(t, str(parent.data["id"]), str(child.data["parent_id"]))
	require.Equal(t, 409, o.call(t, http.MethodPost, "/api/v1/categories", map[string]any{"name": "Household"}, "").code)
	require.Equal(t, 400, o.call(t, http.MethodPost, "/api/v1/categories", map[string]any{"name": " "}, "").code)
	ren := o.call(t, http.MethodPut, "/api/v1/categories/"+str(child.data["id"]), map[string]any{"name": "Cleaning supplies"}, "")
	require.Equal(t, 200, ren.code, ren.raw)
	require.Equal(t, "Cleaning supplies", str(ren.data["name"]))
	require.Equal(t, true, ren.data["is_active"], "renaming must not deactivate a category")
	require.Equal(t, str(parent.data["id"]), str(ren.data["parent_id"]), "renaming must keep the parent")
	moved := o.call(t, http.MethodPut, "/api/v1/categories/"+str(child.data["id"]), map[string]any{"parent_id": nil}, "")
	require.Equal(t, 200, moved.code, moved.raw)
	require.Nil(t, moved.data["parent_id"], "an explicit null moves it to the top level")
	require.Equal(t, 200, o.call(t, http.MethodPut, "/api/v1/categories/"+str(child.data["id"]), map[string]any{"parent_id": parent.data["id"]}, "").code)
	off := o.call(t, http.MethodPut, "/api/v1/categories/"+str(child.data["id"]), map[string]any{"is_active": false}, "")
	require.Equal(t, false, off.data["is_active"])
	require.Equal(t, "Cleaning supplies", str(off.data["name"]), "an update with one field leaves the others")
	o.call(t, http.MethodPut, "/api/v1/categories/"+str(child.data["id"]), map[string]any{"is_active": true}, "")
	require.Equal(t, 404, o.call(t, http.MethodPut, "/api/v1/categories/"+key(), map[string]any{"name": "Ghost"}, "").code)
	require.Equal(t, 400, o.call(t, http.MethodPut, "/api/v1/categories/"+str(child.data["id"]), map[string]any{"name": "  "}, "").code)
	require.Equal(t, 400, o.call(t, http.MethodPost, "/api/v1/categories", map[string]any{"name": "Orphan", "parent_id": key()}, "").code, "the parent must exist")
	require.Equal(t, 400, o.call(t, http.MethodPut, "/api/v1/categories/"+str(parent.data["id"]), map[string]any{"name": "Household", "parent_id": child.data["id"]}, "").code, "a category cannot sit under its own child")
	require.Equal(t, 400, o.call(t, http.MethodPut, "/api/v1/categories/"+str(parent.data["id"]), map[string]any{"name": "Household", "parent_id": parent.data["id"]}, "").code, "or under itself")
	o.call(t, http.MethodPut, "/api/v1/products/"+pid, map[string]any{"category_id": child.data["id"]}, "")
	require.Equal(t, 409, o.call(t, http.MethodDelete, "/api/v1/categories/"+str(child.data["id"]), nil, "").code)
	empty := o.call(t, http.MethodPost, "/api/v1/categories", map[string]any{"name": "Empty"}, "")
	require.Equal(t, 200, o.call(t, http.MethodDelete, "/api/v1/categories/"+str(empty.data["id"]), nil, "").code)
	require.Equal(t, 404, o.call(t, http.MethodDelete, "/api/v1/categories/"+key(), nil, "").code)
	staff, _ := e.user(t, "staff")
	require.Equal(t, 403, staff.call(t, http.MethodPost, "/api/v1/categories", map[string]any{"name": "Nope"}, "").code)
	require.Equal(t, 200, staff.call(t, http.MethodGet, "/api/v1/categories", nil, "").code)

	// Units are a managed list; the defaults exist.
	units := o.call(t, http.MethodGet, "/api/v1/units", nil, "")
	require.GreaterOrEqual(t, len(asList(units.body["data"])), 5)
	unit := o.call(t, http.MethodPost, "/api/v1/units", map[string]any{"name": "bundle", "short_name": "bdl", "allow_decimal": false}, "")
	require.Equal(t, 201, unit.code, unit.raw)
	up := o.call(t, http.MethodPut, "/api/v1/units/"+str(unit.data["id"]), map[string]any{"name": "bundle of 10", "short_name": "bdl", "allow_decimal": false}, "")
	require.Equal(t, 200, up.code, up.raw)
	require.Equal(t, "bundle of 10", str(up.data["name"]))
	dec1 := o.call(t, http.MethodPost, "/api/v1/units", map[string]any{"name": "sack", "short_name": "sk", "allow_decimal": true}, "")
	require.Equal(t, 201, dec1.code, dec1.raw)
	renamed := o.call(t, http.MethodPut, "/api/v1/units/"+str(dec1.data["id"]), map[string]any{"name": "big sack"}, "")
	require.Equal(t, 200, renamed.code, renamed.raw)
	require.Equal(t, true, renamed.data["allow_decimal"], "renaming a unit must not reset allow_decimal")
	require.Equal(t, "sk", str(renamed.data["short_name"]))
	require.Equal(t, 404, o.call(t, http.MethodPut, "/api/v1/units/"+key(), map[string]any{"name": "ghost"}, "").code)
	require.Equal(t, 409, o.call(t, http.MethodPost, "/api/v1/units", map[string]any{"name": "big sack", "short_name": "bs"}, "").code)
	require.Equal(t, 400, o.call(t, http.MethodPost, "/api/v1/units", map[string]any{"name": " ", "short_name": "x"}, "").code)
	require.Equal(t, 403, staff.call(t, http.MethodPost, "/api/v1/units", map[string]any{"name": "x", "short_name": "x"}, "").code)
}

func csvFile(rows ...string) []byte { return []byte(strings.Join(rows, "\n") + "\n") }

func TestCatalogImportReportsThenCommits(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	app := e.app.Fiber
	staff, _ := e.user(t, "staff")
	body := csvFile("name,sku,barcode,sell_price",
		"Maize flour,MF-1,7001,1800",
		",NO-NAME,7002,100",
		"Beans,BN-1,7003,-5",
		"Cooking oil,CO-1,7004,4500",
		"Maize flour again,MF-1,7005,1900",
	)
	// The validation report comes first and saves nothing.
	code, raw := upload(t, app, o, "/api/v1/products/import", "products.csv", "text/csv", body)
	require.Equal(t, 200, code, raw)
	require.Contains(t, raw, `"saved":0`)
	require.Contains(t, raw, `"status":"error"`)
	require.Equal(t, 0, len(asList(o.call(t, http.MethodGet, "/api/v1/products", nil, "").body["data"])))

	// Committing saves the valid rows and reports each failure, including a duplicate SKU inside the file.
	code, raw = upload(t, app, o, "/api/v1/products/import?commit=true", "products.csv", "text/csv", body)
	require.Equal(t, 200, code, raw)
	require.Contains(t, raw, `"saved":2`)
	require.Equal(t, 2, len(asList(o.call(t, http.MethodGet, "/api/v1/products", nil, "").body["data"])))
	require.Contains(t, raw, "already exists")

	// Excel files are read too.
	f := excelize.NewFile()
	for i, row := range [][]any{{"name", "sku", "sell_price"}, {"Tea leaves", "TL-1", "900"}, {"Salt", "SL-1", "300"}} {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		require.NoError(t, f.SetSheetRow("Sheet1", cell, &row))
	}
	var buf bytes.Buffer
	require.NoError(t, f.Write(&buf))
	// An .xlsx is a zip, which the file allow-list for /files accepts but the importer reads itself.
	code, raw = upload(t, app, o, "/api/v1/products/import?commit=true", "products.xlsx", "application/octet-stream", buf.Bytes())
	require.Equal(t, 200, code, raw)
	require.Contains(t, raw, `"saved":2`)
	require.Equal(t, 4, len(asList(o.call(t, http.MethodGet, "/api/v1/products", nil, "").body["data"])))

	// Bad uploads: no rows, no file, too many rows, and a user without products.import.
	code, _ = upload(t, app, o, "/api/v1/products/import", "empty.csv", "text/csv", csvFile("name,sell_price"))
	require.Equal(t, 400, code)
	code, _ = upload(t, app, staff, "/api/v1/products/import", "products.csv", "text/csv", body)
	require.Equal(t, 403, code)
	var big strings.Builder
	big.WriteString("name,sell_price\n")
	for i := 0; i < 5001; i++ {
		fmt.Fprintf(&big, "P%d,1\n", i)
	}
	code, raw = upload(t, app, o, "/api/v1/products/import", "big.csv", "text/csv", []byte(big.String()))
	require.Equal(t, 400, code, raw)
	require.Contains(t, raw, "too_many_rows")
}
