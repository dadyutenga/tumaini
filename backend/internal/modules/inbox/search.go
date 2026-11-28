package inbox

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/sync/errgroup"

	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/setting"
	"bms/internal/platform/sqlq"
)

// SearchHit is one record the search found.
type SearchHit struct {
	Kind     string    `json:"kind"`
	ID       uuid.UUID `json:"id"`
	Title    string    `json:"title"`
	Subtitle string    `json:"subtitle"`
	Link     string    `json:"link"`
	// Meta holds the facts shown beside the title; which keys exist depends on the kind. It never
	// carries a buying price or a cost.
	Meta json.RawMessage `json:"meta,omitempty"`
	// BusinessUnit is "A" (shop) or "B" (services).
	BusinessUnit *string `json:"business_unit"`
	// Highlight names the field the text matched.
	Highlight string `json:"highlight"`
	score     int
}

// SearchGroup is the matches of one kind: how many there are and the best few.
type SearchGroup struct {
	Kind  string      `json:"kind"`
	Total int         `json:"total"`
	Hits  []SearchHit `json:"hits"`
	// Partial means this kind did not answer within the time allowed.
	Partial bool `json:"partial,omitempty"`
}

// SearchResult is the reply of GET /search.
type SearchResult struct {
	// Best is the very document, barcode, SKU or phone number that was typed, when there is one.
	Best   *SearchHit    `json:"best"`
	Groups []SearchGroup `json:"groups"`
}

// SearchRequest is what a person asked for.
type SearchRequest struct {
	Text string
	// Scope limits the search to one kind.
	Scope string
	Limit int
}

const (
	searchBudget   = 800 * time.Millisecond
	searchParallel = 4
	defaultHits    = 5
	maxHits        = 25
	maxQuery       = 100
)

// How well a record matches; higher comes first.
const (
	matchContains = iota + 1
	matchSimilar
	matchPrefix
	matchExact
)

var (
	phoneNoise  = strings.NewReplacer(" ", "", "(", "", ")", "", ".", "", "-", "")
	phoneShapes = []*regexp.Regexp{
		regexp.MustCompile(`^\+?255([67][0-9]{0,8})$`),
		// With a leading 0 the start of a number is enough; without it only a whole one counts.
		regexp.MustCompile(`^0([67][0-9]{2,8})$`),
		regexp.MustCompile(`^([67][0-9]{8})$`),
	}
	likeEscape = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
)

// searchPhone writes a Tanzanian mobile number the way it is stored, so 0712…, 712…, 255712… and
// +255712… find the same person.
func searchPhone(text string) (string, bool) {
	compact := phoneNoise.Replace(text)
	for _, shape := range phoneShapes {
		if m := shape.FindStringSubmatch(compact); m != nil {
			return "+255" + m[1], true
		}
	}
	return "", false
}

type fieldType int

const (
	nameField  fieldType = iota // free text: prefix, similar and contains
	textField                   // prefix and contains only
	codeField                   // a number or code: also an exact match
	phoneField                  // a phone number: exact and partial, on the stored form
)

type searchField struct {
	name, expr string
	kind       fieldType
}

// searchSource is one kind of record: who may see it, where it lives and what it shows.
type searchSource struct {
	kind  string
	perms []string
	// title, subtitle, meta and unit are SQL expressions; recent orders equal matches.
	title, subtitle, meta, unit, recent string
	from                                string
	fields                              []searchField
	// where adds the caller's visibility rules.
	where func(q *sqlq.Q, p *auth.Principal)
	link  func(id uuid.UUID) string
	// bindBranch and bindToday supply the BRANCH and TODAY placeholders of from and meta. A value
	// is bound only when its query runs, so no query carries a value it does not use.
	bindBranch, bindToday func() string
}

func linkTo(prefix string) func(uuid.UUID) string {
	return func(id uuid.UUID) string { return prefix + id.String() }
}

// searchSources are listed in the order their groups appear. today is the business date.
func searchSources(p *auth.Principal, today string, q *sqlq.Q) []searchSource {
	// The branch chosen in the top bar. Only shop records are kept per branch on their list pages.
	branch := func() string { return q.Arg(p.BranchID) }
	date := func() string { return q.Arg(today) + "::date" }
	return []searchSource{
		{
			kind: "product", perms: []string{"shop.product.view"}, bindBranch: branch,
			title: "pr.name::text", subtitle: "coalesce(nullif(pr.barcode, ''), pr.sku::text, '')",
			from: ` FROM app.products pr
				CROSS JOIN app.business_settings bs
				LEFT JOIN app.business_units bu ON bu.id = pr.business_unit_id
				CROSS JOIN LATERAL (SELECT coalesce(sum(b.quantity_on_hand), 0) AS qty FROM app.stock_balances b
					WHERE b.product_id = pr.id AND (BRANCH::uuid IS NULL OR b.location_id = BRANCH)) st`,
			meta: `jsonb_build_object('sku', pr.sku, 'barcode', pr.barcode, 'price', pr.selling_price::text, 'stock', st.qty::text,
				'low_stock', st.qty <= coalesce(pr.min_stock_level, bs.low_stock_default))`,
			unit: "bu.code::text", recent: "NULL::timestamptz",
			fields: []searchField{{"name", "pr.name", nameField}, {"sku", "pr.sku", codeField}, {"barcode", "pr.barcode", codeField}},
			where:  func(q *sqlq.Q, _ *auth.Principal) { q.Add("pr.status = 'active'") },
			link:   linkTo("/products/"),
		},
		{
			kind: "customer", perms: []string{"shop.customer.view_all", "shop.customer.manage"},
			title: "c.full_name::text", subtitle: "coalesce(c.phone::text, '')",
			from: ` FROM app.customers c`,
			meta: `jsonb_build_object('phone', c.phone::text, 'balance', coalesce((SELECT sum(s.balance_due) FROM app.sales s
				WHERE s.customer_id = c.id AND s.status = 'completed'), 0)::numeric(18,2)::text)`,
			unit: "'A'", recent: "c.created_at",
			fields: []searchField{{"name", "c.full_name", nameField}, {"phone", "c.phone", phoneField}},
			where: func(q *sqlq.Q, p *auth.Principal) {
				q.Add("c.archived_at IS NULL AND c.is_shop_customer")
				// The same rule as the customer list: without "view all", only those you registered.
				q.If(!p.Has("shop.customer.view_all"), "c.registered_by = ?", p.MembershipID)
				// A person who is also a services client is listed once, under clients, when the
				// caller sees every client.
				q.If(p.Has("services.client.view_all"), "c.client_status IS NULL")
			},
			link: linkTo("/customers/"),
		},
		{
			kind: "client", perms: []string{"services.client.view_all", "services.client.manage"},
			title: "c.full_name::text", subtitle: "coalesce(c.phone::text, '')",
			from: ` FROM app.customers c`,
			meta: `jsonb_build_object('phone', c.phone::text, 'balance', coalesce((SELECT sum(i.balance_due) FROM app.invoices i
				WHERE i.customer_id = c.id AND i.status IN ('issued', 'partially_paid')), 0)::numeric(18,2)::text)`,
			unit: "'B'", recent: "c.created_at",
			fields: []searchField{{"name", "c.full_name", nameField}, {"phone", "c.phone", phoneField}, {"email", "c.email::text", textField}},
			where: func(q *sqlq.Q, p *auth.Principal) {
				q.Add("c.archived_at IS NULL AND c.client_status IS NOT NULL")
				if !p.Has("services.client.view_all") {
					m := p.MembershipID
					q.Add(`(c.registered_by = ? OR c.assigned_to = ? OR EXISTS (SELECT 1 FROM app.projects pr WHERE pr.customer_id = c.id AND (pr.owner_id = ? OR EXISTS (SELECT 1 FROM app.project_stages st WHERE st.project_id = pr.id AND st.owner_id = ?))))`, m, m, m, m)
				}
			},
			link: linkTo("/clients/"),
		},
		{
			kind: "sale", perms: []string{"shop.sale.view_all", "shop.sale.create"},
			title: "s.receipt_no", subtitle: "coalesce(c.full_name::text, '')",
			from: ` FROM app.sales s
				LEFT JOIN app.customers c ON c.id = s.customer_id
				LEFT JOIN app.business_units bu ON bu.id = s.business_unit_id`,
			meta: `jsonb_build_object('date', s.business_date::text, 'total', s.total::text, 'status', s.status::text, 'client', c.full_name::text)`,
			unit: "bu.code::text", recent: "s.created_at",
			fields: []searchField{{"number", "s.receipt_no", codeField}, {"client", "c.full_name", nameField}, {"phone", "c.phone", phoneField}},
			where: func(q *sqlq.Q, p *auth.Principal) {
				q.Add("coalesce(s.receipt_no, '') <> ''")
				q.If(!p.Has("shop.sale.view_all"), "s.sold_by = ?", p.MembershipID)
				q.If(p.BranchID != nil, "s.location_id = ?", p.BranchID)
			},
			link: linkTo("/sales/"),
		},
		{
			kind: "service_invoice", perms: []string{"services.invoice.manage", "services.invoice.draft"}, bindToday: date,
			title: "i.invoice_no", subtitle: "c.full_name::text",
			from: ` FROM app.invoices i
				JOIN app.customers c ON c.id = i.customer_id
				LEFT JOIN app.business_units bu ON bu.id = i.business_unit_id`,
			meta: `jsonb_build_object('client', c.full_name::text, 'total', i.total::text, 'balance', i.balance_due::text,
				'status', CASE WHEN i.status = 'partially_paid' THEN 'partial' ELSE i.status::text END, 'due_date', i.due_date::text,
				'late', coalesce(i.status IN ('issued', 'partially_paid') AND i.due_date < TODAY, false))`,
			unit: "bu.code::text", recent: "i.created_at",
			fields: []searchField{{"number", "i.invoice_no", codeField}, {"client", "c.full_name", nameField}, {"phone", "c.phone", phoneField}},
			where: func(q *sqlq.Q, p *auth.Principal) {
				q.If(!p.Has("services.invoice.manage"), "i.created_by = ?", p.MembershipID)
			},
			link: linkTo("/service-invoices?open="),
		},
		{
			kind: "quotation", perms: []string{"services.quotation.manage", "services.quotation.draft"},
			title: "qu.quotation_no", subtitle: "c.full_name::text",
			from: ` FROM app.quotations qu
				JOIN app.customers c ON c.id = qu.customer_id
				LEFT JOIN app.business_units bu ON bu.id = qu.business_unit_id`,
			meta: `jsonb_build_object('client', c.full_name::text, 'total', qu.total::text, 'status', qu.status::text, 'date', qu.issue_date::text)`,
			unit: "bu.code::text", recent: "qu.created_at",
			fields: []searchField{{"number", "qu.quotation_no", codeField}, {"client", "c.full_name", nameField}, {"phone", "c.phone", phoneField}},
			where: func(q *sqlq.Q, p *auth.Principal) {
				q.If(!p.Has("services.quotation.manage"), "qu.created_by = ?", p.MembershipID)
			},
			link: linkTo("/service-quotations?open="),
		},
		{
			kind: "project", perms: []string{"services.project.view_all", "services.project.manage"}, bindToday: date,
			title: "pr.project_no || ' · ' || pr.title::text", subtitle: "c.full_name::text",
			from: ` FROM app.projects pr
				JOIN app.customers c ON c.id = pr.customer_id
				LEFT JOIN app.business_units bu ON bu.id = pr.business_unit_id`,
			meta: `jsonb_build_object('client', c.full_name::text, 'status', pr.status::text, 'due_date', pr.target_end_date::text,
				'late', coalesce(pr.status IN ('not_started', 'in_progress') AND pr.target_end_date < TODAY, false))`,
			unit: "bu.code::text", recent: "pr.created_at",
			fields: []searchField{{"number", "pr.project_no", codeField}, {"title", "pr.title", nameField}, {"client", "c.full_name", nameField}},
			where: func(q *sqlq.Q, p *auth.Principal) {
				if !p.Has("services.project.view_all") {
					m := p.MembershipID
					q.Add(`(pr.owner_id = ? OR pr.created_by = ? OR EXISTS (SELECT 1 FROM app.project_stages st WHERE st.project_id = pr.id AND st.owner_id = ?))`, m, m, m)
				}
			},
			link: linkTo("/projects/"),
		},
		{
			kind: "design_job", perms: []string{"services.design.view_all", "services.design.manage"}, bindToday: date,
			title: "j.job_no || ' · ' || j.title::text", subtitle: "c.full_name::text",
			from: ` FROM app.design_jobs j
				JOIN app.customers c ON c.id = j.customer_id
				LEFT JOIN app.business_units bu ON bu.id = j.business_unit_id`,
			meta: `jsonb_build_object('client', c.full_name::text, 'status', j.status::text, 'due_date', j.deadline::text,
				'late', coalesce(j.status NOT IN ('approved', 'printing', 'delivered', 'cancelled') AND j.deadline < TODAY, false))`,
			unit: "bu.code::text", recent: "j.created_at",
			fields: []searchField{{"number", "j.job_no", codeField}, {"title", "j.title", nameField}, {"client", "c.full_name", nameField}},
			where: func(q *sqlq.Q, p *auth.Principal) {
				q.If(!p.Has("services.design.view_all"), "(j.designer_id = ? OR j.created_by = ?)", p.MembershipID, p.MembershipID)
			},
			link: linkTo("/design-jobs/"),
		},
	}
}

// statement writes the query for one kind. exactOnly is for text too short to search by.
func (s searchSource) statement(q *sqlq.Q, p *auth.Principal, text string, limit int, exactOnly bool) (string, bool) {
	phone, isPhone := searchPhone(text)
	escaped := likeEscape.Replace(text)
	bound := map[string]string{}
	arg := func(v string) string {
		if ph, ok := bound[v]; ok {
			return ph
		}
		ph := q.Arg(v) + "::text"
		bound[v] = ph
		return ph
	}

	var exact, prefix, similar, contains, similarity, match []string
	var named [4][]string
	for _, f := range s.fields {
		value, like := text, escaped
		if f.kind == phoneField && isPhone {
			value, like = phone, likeEscape.Replace(phone)
		}
		if exactOnly && f.kind != codeField {
			continue
		}
		add := func(list *[]string, level int, condition string) {
			*list = append(*list, condition)
			named[level-1] = append(named[level-1], "WHEN "+condition+" THEN '"+f.name+"'")
		}
		if f.kind == codeField || (f.kind == phoneField && isPhone) {
			// The pattern has no wildcard, so this is an exact match that ignores case and can use the index.
			add(&exact, matchExact, f.expr+" ILIKE "+arg(like))
		}
		if exactOnly {
			continue
		}
		add(&prefix, matchPrefix, f.expr+" ILIKE "+arg(like+"%"))
		add(&contains, matchContains, f.expr+" ILIKE "+arg("%"+like+"%"))
		if f.kind == nameField {
			add(&similar, matchSimilar, f.expr+" % "+arg(value))
			similarity = append(similarity, "similarity(coalesce("+f.expr+"::text, ''), "+arg(value)+")")
		}
	}
	if exactOnly {
		match = exact
	} else {
		match = append(append(match, contains...), similar...)
	}
	if len(match) == 0 {
		return "", false
	}
	or := func(list []string) string {
		if len(list) == 0 {
			return "false"
		}
		return "(" + strings.Join(list, " OR ") + ")"
	}
	sim := "0::real"
	if len(similarity) > 0 {
		sim = "greatest(" + strings.Join(similarity, ", ") + ")"
	}
	var matched []string
	for level := matchExact; level >= matchContains; level-- {
		matched = append(matched, named[level-1]...)
	}

	q.Add(or(match))
	s.where(q, p)
	from, meta := s.from, s.meta
	if s.bindBranch != nil {
		from = strings.ReplaceAll(from, "BRANCH", s.bindBranch())
	}
	if s.bindToday != nil {
		meta = strings.ReplaceAll(meta, "TODAY", s.bindToday())
	}
	where := q.Where()
	return `SELECT x.id, x.title, x.subtitle, x.score, x.matched, x.unit, x.meta, count(*) OVER () FROM (
		SELECT ` + s.idColumn() + ` AS id, ` + s.title + ` AS title, ` + s.subtitle + ` AS subtitle,
			CASE WHEN ` + or(exact) + ` THEN ` + strconv.Itoa(matchExact) + ` WHEN ` + or(prefix) + ` THEN ` + strconv.Itoa(matchPrefix) +
		` WHEN ` + or(similar) + ` THEN ` + strconv.Itoa(matchSimilar) + ` ELSE ` + strconv.Itoa(matchContains) + ` END AS score,
			` + sim + ` AS similarity, CASE ` + strings.Join(matched, " ") + ` ELSE '' END AS matched,
			` + s.unit + ` AS unit, ` + meta + ` AS meta, ` + s.recent + ` AS recent` + from + where + `
	) x ORDER BY x.score DESC, x.similarity DESC, x.recent DESC NULLS LAST, x.title, x.id LIMIT ` + q.Arg(limit), true
}

func (s searchSource) idColumn() string {
	switch s.kind {
	case "product", "project":
		return "pr.id"
	case "sale":
		return "s.id"
	case "service_invoice":
		return "i.id"
	case "quotation":
		return "qu.id"
	case "design_job":
		return "j.id"
	default:
		return "c.id"
	}
}

func searchKinds() map[string]bool {
	out := map[string]bool{}
	for _, s := range searchSources(&auth.Principal{}, "", sqlq.New()) {
		out[s.kind] = true
	}
	return out
}

func maySearch(p *auth.Principal, perms []string) bool {
	for _, perm := range perms {
		if p.Has(perm) {
			return true
		}
	}
	return false
}

// timedOut reports whether a query failed only because it ran out of time.
func timedOut(ctx context.Context, err error) bool {
	var pe *pgconn.PgError
	return ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &pe) && pe.Code == "57014")
}

// Search finds records by name, phone, number, SKU or barcode. Only the kinds the caller may view
// are queried, each limited to the records they may see. The kinds are searched side by side
// within one deadline; a kind that does not finish is reported as partial.
func (s *Service) Search(ctx context.Context, p *auth.Principal, in SearchRequest) (SearchResult, error) {
	out := SearchResult{Groups: []SearchGroup{}}
	text := strings.TrimSpace(in.Text)
	if utf8.RuneCountInString(text) > maxQuery {
		return out, httpx.BadRequest("invalid_query", "That search text is too long")
	}
	if in.Scope != "" && !searchKinds()[in.Scope] {
		return out, httpx.BadRequest("invalid_query", "Unknown search scope")
	}
	if text == "" {
		return out, nil
	}
	limit := in.Limit
	if limit <= 0 {
		limit = defaultHits
	}
	limit = min(limit, maxHits)
	// One character only finds a document number or code that is exactly that.
	exactOnly := utf8.RuneCountInString(text) < 2

	var today string
	if err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		zone, err := time.LoadLocation(setting.String(ctx, tx, "system.timezone", "Africa/Dar_es_Salaam"))
		if err != nil {
			zone = time.UTC
		}
		today = s.Clock.Now().In(zone).Format("2006-01-02")
		return nil
	}); err != nil {
		return out, err
	}

	var wanted []string
	for _, src := range searchSources(p, today, sqlq.New()) {
		if maySearch(p, src.perms) && (in.Scope == "" || in.Scope == src.kind) {
			wanted = append(wanted, src.kind)
		}
	}

	ctx, cancel := context.WithTimeout(ctx, searchBudget)
	defer cancel()
	groups := make([]SearchGroup, len(wanted))
	var run errgroup.Group
	run.SetLimit(searchParallel)
	for i, kind := range wanted {
		run.Go(func() error {
			group, err := s.searchKind(ctx, p, today, kind, text, limit, exactOnly)
			if err != nil {
				if !timedOut(ctx, err) {
					return err
				}
				group = SearchGroup{Kind: kind, Hits: []SearchHit{}, Partial: true}
			}
			groups[i] = group
			return nil
		})
	}
	if err := run.Wait(); err != nil {
		return out, err
	}
	for _, g := range groups {
		if g.Total > 0 || g.Partial {
			out.Groups = append(out.Groups, g)
		}
	}
	out.Best = bestHit(out.Groups)
	return out, nil
}

// bestHit picks the record that is exactly what was typed. Document numbers come before codes and
// phone numbers.
func bestHit(groups []SearchGroup) *SearchHit {
	order := []string{"sale", "service_invoice", "quotation", "project", "design_job", "product", "customer", "client"}
	for _, kind := range order {
		for _, g := range groups {
			if g.Kind == kind && len(g.Hits) > 0 && g.Hits[0].score == matchExact {
				hit := g.Hits[0]
				return &hit
			}
		}
	}
	return nil
}

func (s *Service) searchKind(ctx context.Context, p *auth.Principal, today, kind, text string, limit int, exactOnly bool) (SearchGroup, error) {
	group := SearchGroup{Kind: kind, Hits: []SearchHit{}}
	err := s.DB.InActorReadTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		// The database gives up a little before the request does, which keeps the connection usable.
		if deadline, ok := ctx.Deadline(); ok {
			ms := max(time.Until(deadline).Milliseconds()-50, 1)
			if _, err := tx.Exec(ctx, `SELECT set_config('statement_timeout', $1, true)`, strconv.FormatInt(ms, 10)); err != nil {
				return err
			}
		}
		q := sqlq.New()
		for _, src := range searchSources(p, today, q) {
			if src.kind != kind {
				continue
			}
			sql, ok := src.statement(q, p, text, limit, exactOnly)
			if !ok {
				return nil
			}
			rows, err := tx.Query(ctx, sql, q.Args...)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				hit := SearchHit{Kind: kind}
				if err := rows.Scan(&hit.ID, &hit.Title, &hit.Subtitle, &hit.score, &hit.Highlight, &hit.BusinessUnit, &hit.Meta, &group.Total); err != nil {
					return err
				}
				hit.Link = src.link(hit.ID)
				group.Hits = append(group.Hits, hit)
			}
			return rows.Err()
		}
		return nil
	})
	return group, err
}
