package app_test

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func searchFor(t *testing.T, c *client, query string) result {
	t.Helper()
	res := c.call(t, http.MethodGet, "/api/v1/search?"+query, nil, "")
	require.Equal(t, 200, res.code, res.raw)
	return res
}

func searchQ(text string) string { return "q=" + url.QueryEscape(text) }

// searchGroup returns one group of a search reply, or nil.
func searchGroup(res result, kind string) map[string]any {
	for _, g := range asList(res.data["groups"]) {
		if str(g["kind"]) == kind {
			return g
		}
	}
	return nil
}

// searchHits returns the hits of one kind, or of every kind when kind is empty.
func searchHits(res result, kind string) []map[string]any {
	var out []map[string]any
	for _, g := range asList(res.data["groups"]) {
		if kind == "" || str(g["kind"]) == kind {
			out = append(out, asList(g["hits"])...)
		}
	}
	return out
}

func bestHit(res result) map[string]any {
	m, _ := res.data["best"].(map[string]any)
	return m
}

func TestSearchRanksExactMatchesFirst(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	addProduct := func(body map[string]any) string {
		res := o.call(t, http.MethodPost, "/api/v1/products", body, "")
		require.Equal(t, 201, res.code, res.raw)
		return str(res.data["id"])
	}
	scanned := addProduct(map[string]any{"name": "Charger", "sell_price": "5000", "barcode": "6001234567890"})
	addProduct(map[string]any{"name": "Cable 6001234567890 pack", "sell_price": "2000"})

	res := searchFor(t, o, searchQ("6001234567890"))
	hits := searchHits(res, "product")
	require.Len(t, hits, 2, res.raw)
	require.Equal(t, scanned, str(hits[0]["id"]), "an exact barcode beats a name that contains it")
	require.Equal(t, "barcode", str(hits[0]["highlight"]))
	require.Equal(t, scanned, str(bestHit(res)["id"]), res.raw)
	require.Equal(t, "/products/"+scanned, str(bestHit(res)["link"]))
	require.Equal(t, "A", str(bestHit(res)["business_unit"]))
	meta := bestHit(res)["meta"].(map[string]any)
	require.Equal(t, "5000.00", str(meta["price"]))
	require.Equal(t, true, meta["low_stock"], res.raw)

	// Prefix beats contains, and a misspelt name is still found.
	addProduct(map[string]any{"name": "Mini Radio", "sell_price": "1"})
	prefix := addProduct(map[string]any{"name": "Radio Mini", "sell_price": "1"})
	sugar := addProduct(map[string]any{"name": "Sukari", "sell_price": "1"})
	require.Equal(t, prefix, str(searchHits(searchFor(t, o, searchQ("radio")), "product")[0]["id"]))
	fuzzy := searchHits(searchFor(t, o, searchQ("sukri")), "product")
	require.NotEmpty(t, fuzzy)
	require.Equal(t, sugar, str(fuzzy[0]["id"]))
	require.Nil(t, bestHit(searchFor(t, o, searchQ("radio"))), "only an exact number, code or phone is a best match")

	// The total counts every match; the group shows at most the limit.
	for _, n := range []string{"1", "2", "3", "4", "5", "6", "7"} {
		addProduct(map[string]any{"name": "Lamp " + n, "sell_price": "1"})
	}
	lamps := searchGroup(searchFor(t, o, searchQ("lamp")), "product")
	require.EqualValues(t, 7, lamps["total"])
	require.Len(t, asList(lamps["hits"]), 5)
	require.Len(t, searchHits(searchFor(t, o, searchQ("lamp")+"&limit=25"), "product"), 7)

	// Too little to search by, and unknown input.
	require.Empty(t, asList(searchFor(t, o, searchQ("L")).data["groups"]))
	require.Empty(t, asList(searchFor(t, o, searchQ("")).data["groups"]))
	require.Empty(t, asList(searchFor(t, o, searchQ("100% _")).data["groups"]), "wildcards are ordinary characters")
	require.Equal(t, 400, o.call(t, http.MethodGet, "/api/v1/search?q=lamp&scope=nothing", nil, "").code)
	require.Equal(t, 400, o.call(t, http.MethodGet, "/api/v1/search?q="+strings.Repeat("a", 101), nil, "").code)
}

func TestSearchFindsAPhoneNumberHoweverItIsTyped(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	res := o.call(t, http.MethodPost, "/api/v1/customers", map[string]any{"name": "Asha Juma", "phone": "0712345678"}, "")
	require.Equal(t, 201, res.code, res.raw)
	id := str(res.data["id"])
	for _, typed := range []string{"0712345678", "712345678", "255712345678", "+255712345678", "0712 345-678"} {
		found := searchFor(t, o, searchQ(typed))
		require.NotNil(t, bestHit(found), typed+": "+found.raw)
		require.Equal(t, id, str(bestHit(found)["id"]), typed)
		require.Equal(t, "phone", str(bestHit(found)["highlight"]), typed)
	}
	start := searchHits(searchFor(t, o, searchQ("0712")), "customer")
	require.Len(t, start, 1)
	require.Equal(t, id, str(start[0]["id"]))
	require.Equal(t, id, str(searchHits(searchFor(t, o, searchQ("5678")), "customer")[0]["id"]), "digits from the middle")
}

func TestSearchShowsOnlyWhatARoleMaySee(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	cash := e.cashAccount(t)
	product := e.product(t, "Zebra Router", "100000", "10", "61234")
	cust := o.call(t, http.MethodPost, "/api/v1/customers", map[string]any{"name": "Zebra Customer", "phone": uniquePhone()}, "")
	require.Equal(t, 201, cust.code, cust.raw)
	services := o.call(t, http.MethodPost, "/api/v1/clients", map[string]any{"name": "Zebra Client", "phone": uniquePhone()}, "")
	require.Equal(t, 201, services.code, services.raw)
	job := o.call(t, http.MethodPost, "/api/v1/design-jobs", map[string]any{"client_id": services.data["id"], "job_type": "logo", "title": "Zebra logo", "price": "1000"}, "")
	require.Equal(t, 201, job.code, job.raw)

	first, _ := e.user(t, "staff")
	second, _ := e.user(t, "staff")
	sell := func(c *client) (string, string) {
		res := c.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
			"items":    []map[string]any{{"product_id": product, "qty": "1"}},
			"payments": []map[string]any{{"method": "cash", "money_account_id": cash, "amount": "100000"}},
		}, key())
		require.Equal(t, 201, res.code, res.raw)
		return str(res.data["id"]), str(res.data["number"])
	}
	mine, myNumber := sell(first)
	_, theirNumber := sell(second)

	// Staff find their own sale by its number, in any letter case, and never a colleague's.
	own := searchFor(t, first, searchQ(strings.ToLower(myNumber)))
	require.Equal(t, mine, str(bestHit(own)["id"]), own.raw)
	other := searchFor(t, first, searchQ(theirNumber))
	require.Nil(t, searchGroup(other, "sale"), other.raw)
	require.NotNil(t, searchGroup(searchFor(t, o, searchQ(theirNumber)), "sale"))
	// Staff never see what the shop paid.
	for _, who := range []*client{first, second} {
		raw := searchFor(t, who, searchQ("zebra")).raw
		require.NotContains(t, raw, "61234.00")
		require.NotContains(t, raw, "cost")
		require.NotContains(t, raw, "buying")
	}
	// A job that is not theirs stays hidden unless they see every job.
	require.Nil(t, searchGroup(searchFor(t, first, searchQ("zebra logo")), "design_job"))

	// A shop-only role gets no services groups, not even their counts.
	role := o.call(t, http.MethodPost, "/api/v1/roles", map[string]any{"name": "Cashier only", "permissions": []map[string]any{
		{"key": "shop.product.view", "scope": "all"}, {"key": "shop.customer.view_all", "scope": "all"}, {"key": "shop.sale.create", "scope": "all"},
	}}, "")
	require.Equal(t, 201, role.code, role.raw)
	phone := uniquePhone()
	made := o.call(t, http.MethodPost, "/api/v1/users", map[string]any{"name": "Cashier", "phone": phone, "password": "cashier-pass-1", "role_id": role.data["id"], "branch_ids": []string{e.branch}}, "")
	require.Equal(t, 201, made.code, made.raw)
	cashier := &client{app: e.app.Fiber, token: login(t, e.app.Fiber, phone, "cashier-pass-1"), branch: e.branch}
	shop := searchFor(t, cashier, searchQ("zebra"))
	var kinds []string
	for _, g := range asList(shop.data["groups"]) {
		kinds = append(kinds, str(g["kind"]))
	}
	require.ElementsMatch(t, []string{"product", "customer"}, kinds, shop.raw)
	require.NotContains(t, shop.raw, "Zebra logo")
	for _, kind := range []string{"client", "project", "design_job", "service_invoice", "quotation"} {
		require.Empty(t, asList(searchFor(t, cashier, searchQ("zebra")+"&scope="+kind).data["groups"]), kind)
	}
	all := searchFor(t, o, searchQ("zebra"))
	for _, kind := range []string{"product", "customer", "client", "design_job"} {
		require.NotNil(t, searchGroup(all, kind), kind+": "+all.raw)
	}

	// The location parameter obeys the same branch access as the header.
	branch := o.call(t, http.MethodPost, "/api/v1/branches", map[string]any{"name": "Second"}, "")
	require.Equal(t, 201, branch.code, branch.raw)
	require.Equal(t, 403, cashier.call(t, http.MethodGet, "/api/v1/search?q=zebra&location="+str(branch.data["id"]), nil, "").code)
	elsewhere := searchFor(t, o, searchQ(myNumber)+"&location="+str(branch.data["id"]))
	require.Nil(t, searchGroup(elsewhere, "sale"), "sales are searched in the chosen branch")
	require.NotNil(t, searchGroup(searchFor(t, o, searchQ(myNumber)+"&location=all"), "sale"))
}

func TestSearchReportsAKindThatRunsOutOfTime(t *testing.T) {
	e := newEnv(t, defaultNow)
	e.product(t, "Slow Product", "100", "1", "50")
	res := e.owner.call(t, http.MethodPost, "/api/v1/customers", map[string]any{"name": "Slow Customer", "phone": uniquePhone()}, "")
	require.Equal(t, 201, res.code, res.raw)

	// Holding the products table makes that one query wait past the deadline.
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, os.Getenv("MIGRATE_DATABASE_URL"))
	require.NoError(t, err)
	defer func() { _ = admin.Close(ctx) }()
	tx, err := admin.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `LOCK TABLE app.products IN ACCESS EXCLUSIVE MODE`)
	require.NoError(t, err)

	started := time.Now()
	found := searchFor(t, e.owner, searchQ("slow"))
	require.Less(t, time.Since(started), 5*time.Second)
	products := searchGroup(found, "product")
	require.NotNil(t, products, found.raw)
	require.Equal(t, true, products["partial"], found.raw)
	require.NotEmpty(t, searchHits(found, "customer"), "the kinds that answered are returned: "+found.raw)
}
