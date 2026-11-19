package identity

import "github.com/shopspring/decimal"

type seededPerm struct {
	Key   string
	Scope string
	Limit *decimal.Decimal
}

func lim(v string) *decimal.Decimal {
	d := decimal.RequireFromString(v)
	return &d
}

func managerPerms() []seededPerm {
	l := func(key, scope, limit string) seededPerm {
		var lp *decimal.Decimal
		if limit != "" {
			lp = lim(limit)
		}
		return seededPerm{key, scope, lp}
	}
	return []seededPerm{
		l("branches.view", "branch", ""), l("branches.manage", "branch", ""), l("settings.view", "branch", ""),
		l("products.view", "branch", ""), l("products.manage", "branch", ""), l("categories.manage", "branch", ""), l("products.import", "branch", ""),
		l("stock.view", "branch", ""), l("stock.in", "branch", ""), l("stock.out", "branch", ""),
		l("stock.adjust", "branch", "1000000"), l("stock.adjust_approve", "branch", ""),
		l("transfers.view", "branch", ""), l("transfers.create", "branch", ""), l("transfers.dispatch", "branch", ""), l("transfers.receive", "branch", ""),
		l("sales.create", "branch", ""), l("sales.view", "branch", ""), l("sales.discount", "branch", "20"),
		l("sales.void", "branch", "1000000"), l("sales.refund_request", "branch", ""), l("sales.refund_approve", "branch", "500000"),
		l("tills.open_close", "branch", ""), l("tills.view", "branch", ""), l("quotations.manage", "branch", ""),
		l("customers.view", "branch", ""), l("customers.manage", "branch", ""), l("customers.credit_limit", "branch", ""), l("customers.statement", "branch", ""),
		l("suppliers.view", "branch", ""), l("suppliers.manage", "branch", ""),
		l("po.view", "branch", ""), l("po.create", "branch", "5000000"), l("po.approve", "branch", ""),
		l("grn.create", "branch", ""), l("purchase_invoices.manage", "branch", ""), l("purchase_returns.create", "branch", ""),
		l("expenses.view", "branch", ""), l("expenses.create", "branch", "1000000"), l("expenses.approve", "branch", "1000000"),
		l("expenses.pay", "branch", ""), l("expenses.reverse", "branch", ""),
		l("expense_categories.manage", "branch", ""), l("petty_cash.manage", "branch", ""), l("recurring_expenses.manage", "branch", ""),
		l("accounts.view", "branch", ""), l("payments.view", "branch", ""), l("payments.receive", "branch", ""), l("payments.pay", "branch", "1000000"),
		l("transfers_money.create", "branch", ""), l("reconcile.manage", "branch", ""),
		l("employees.view", "branch", ""), l("employees.manage", "branch", ""), l("departments.manage", "branch", ""),
		l("leave.request", "branch", ""), l("leave.approve", "branch", ""),
		l("attendance.clock", "branch", ""), l("attendance.view", "branch", ""), l("attendance.edit", "branch", ""),
		l("overtime.approve", "branch", ""), l("schedules.manage", "branch", ""), l("payslips.view", "branch", ""),
		l("approvals.view", "branch", ""), l("approvals.decide", "branch", ""),
		l("reports.sales", "branch", ""), l("reports.inventory", "branch", ""), l("reports.purchases", "branch", ""),
		l("reports.expenses", "branch", ""), l("reports.debts", "branch", ""), l("reports.staff", "branch", ""), l("reports.branches", "branch", ""),
		l("dashboard.view", "branch", ""),
		l("clients.view", "branch", ""), l("clients.manage", "branch", ""), l("projects.view", "branch", ""), l("projects.manage", "branch", ""),
		l("service_catalog.manage", "branch", ""), l("service_billing.view", "branch", ""), l("service_billing.manage", "branch", ""),
		l("design.view", "branch", ""), l("design.manage", "branch", ""), l("tasks.view", "branch", ""), l("tasks.manage", "branch", ""),
		l("renewals.view", "branch", ""), l("renewals.manage", "branch", ""), l("contractors.view", "branch", ""), l("contractors.manage", "branch", ""),
		l("reports.services", "branch", ""),
	}
}

func staffPerms() []seededPerm {
	l := func(key, scope, limit string) seededPerm {
		var lp *decimal.Decimal
		if limit != "" {
			lp = lim(limit)
		}
		return seededPerm{key, scope, lp}
	}
	return []seededPerm{
		l("products.view", "branch", ""),
		l("sales.create", "branch", ""), l("sales.view", "own", ""), l("sales.discount", "branch", "5"), l("sales.refund_request", "branch", ""),
		l("tills.open_close", "branch", ""),
		l("customers.view", "branch", ""), l("customers.manage", "branch", ""),
		l("payments.receive", "branch", ""),
		l("expenses.view", "own", ""), l("expenses.create", "branch", "50000"),
		l("attendance.clock", "branch", ""), l("leave.request", "branch", ""), l("payslips.view", "own", ""),
		l("clients.view", "own", ""), l("projects.view", "own", ""), l("design.view", "own", ""), l("tasks.view", "own", ""), l("renewals.view", "branch", ""),
	}
}
