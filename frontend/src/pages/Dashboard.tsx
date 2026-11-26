import { useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import {
  AlertTriangle,
  ArrowUpRight,
  Briefcase,
  Check,
  Clock,
  CreditCard,
  Receipt,
  RefreshCw,
  ShoppingBag,
  ShoppingCart,
  TrendingDown,
  TrendingUp,
  Wallet,
  X,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { IncomeExpenseChart } from "@/components/shared/Charts";
import { ChartCard, PageLoader } from "@/components/shared/Display";
import { Field, FormDialog, NativeSelect, PageHeader, StatusBadge } from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { ClientSelect } from "@/features/services/shared";
import { label } from "@/features/services/helpers";
import { errorMessage, useGet, useWrite } from "@/hooks/useApi";
import { formatDate, formatMoney, today, toNumber } from "@/lib/formatters";
import type { CatalogItem } from "@/types/services";

interface DashboardPayload {
  sales?: { total: string; count: number; previous_total: string; change_pct?: string | null };
  service_income?: { total: string; previous_total: string; change_pct?: string | null };
  expenses?: { total: string; previous_total: string; change_pct?: string | null };
  profit?: {
    gross_profit: string;
    net_profit: string;
    previous_net_profit?: string;
    change_pct?: string | null;
  };
  income_vs_expenses?: { date: string; income: string; expenses: string }[];
  cash?: {
    total: string;
    accounts: { id: string; name: string; type: string; balance: string }[];
    includes_bank_and_mobile: boolean;
  };
  stock?: {
    low_stock_count: number;
    value?: string;
    items?: { id: string; name: string; qty_on_hand: string; reorder_level: string }[];
  };
  outstanding?: {
    customers_owe?: string;
    clients_owe?: string;
    we_owe_suppliers?: string;
    we_owe_contractors?: string;
    salaries_due?: string;
  };
  projects_by_stage?: { stage: string; count: number; late: number }[];
  overdue_tasks?: {
    id: string;
    title: string;
    due_date: string;
    assignee_name?: string | null;
    project_number?: string | null;
  }[];
  renewals_due?: {
    id: string;
    item: string;
    due_date: string;
    days_left: number;
    client_name: string;
  }[];
  pending_approvals?: {
    count: number;
    latest: {
      id: string;
      kind: string;
      summary: string;
      amount?: string | null;
      created_at: string;
    }[];
  };
}

interface StaffMePayload {
  sales_today?: { count: number; total: string; on_credit: string };
  commission_month?: string;
  attendance?: { clocked_in: boolean; today?: { first_in?: string | null } } | null;
  my_jobs?: {
    id: string;
    kind: "project" | "design_job";
    title: string;
    client_name: string;
    due_date: string | null;
    status: string;
  }[];
  my_tasks?: {
    id: string;
    title: string;
    due_date: string | null;
    status: string;
    overdue: boolean;
    project_number?: string | null;
  }[];
  low_stock?: { id: string; name: string; qty_on_hand: string; reorder_level: string }[];
}

function greeting() {
  const h = new Date().getHours();
  if (h < 12) return "Good morning";
  if (h < 17) return "Good afternoon";
  return "Good evening";
}

function trendInfo(pct?: string | null) {
  if (pct === null || pct === undefined || pct === "") return null;
  const n = toNumber(pct);
  const sign = n > 0 ? "+" : "";
  return {
    text: `${sign}${n.toFixed(1)}%`,
    positive: n >= 0,
  };
}

function DashboardError({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <div className="flex flex-col items-center justify-center rounded-2xl border border-destructive/30 bg-card p-10 text-center shadow-sm">
      <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-destructive/10 text-destructive">
        <AlertTriangle size={22} />
      </div>
      <h2 className="mt-4 text-base font-semibold">Could not load dashboard data</h2>
      <p className="mt-1 max-w-md text-sm text-muted-foreground">{message}</p>
      <Button className="mt-4" variant="outline" onClick={onRetry}>
        <RefreshCw size={15} /> Try again
      </Button>
    </div>
  );
}

function KpiCard({
  label: title,
  value,
  sub,
  trend,
  icon: Icon,
  to,
  accent = "primary",
}: {
  label: string;
  value: string;
  sub?: string;
  trend?: { text: string; positive: boolean } | null;
  icon: React.ComponentType<{ size?: number; className?: string }>;
  to?: string;
  accent?: "primary" | "amber" | "emerald";
}) {
  const iconTone =
    accent === "amber"
      ? "bg-amber-500/12 text-amber-600 dark:text-amber-400 ring-amber-500/20"
      : "bg-primary/12 text-primary ring-primary/20";

  const inner = (
    <div className="group relative overflow-hidden rounded-2xl border bg-card p-5 shadow-xs transition duration-200 hover:-translate-y-0.5 hover:border-primary/45 hover:shadow-md">
      <div className="pointer-events-none absolute inset-x-0 top-0 h-1 bg-gradient-to-r from-primary/70 via-primary/30 to-transparent opacity-75 transition group-hover:opacity-100" />
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            {title}
          </p>
          <p className="mt-2 truncate text-2xl font-bold tracking-tight tabular-nums">{value}</p>
        </div>
        <div
          className={`flex h-10 w-10 shrink-0 items-center justify-center rounded-xl ring-1 ${iconTone}`}
        >
          <Icon size={18} />
        </div>
      </div>
      <div className="mt-3 flex items-center justify-between gap-2">
        <div className="flex min-w-0 items-center gap-2">
          {trend && (
            <span
              className={`inline-flex items-center gap-0.5 rounded-full px-2 py-0.5 text-[11px] font-semibold ${
                trend.positive ? "bg-primary/12 text-primary" : "bg-destructive/12 text-destructive"
              }`}
            >
              {trend.positive ? <TrendingUp size={11} /> : <TrendingDown size={11} />}
              {trend.text}
            </span>
          )}
          {sub && <p className="truncate text-xs text-muted-foreground">{sub}</p>}
        </div>
        {to && (
          <ArrowUpRight
            size={15}
            className="shrink-0 text-muted-foreground/60 transition group-hover:translate-x-0.5 group-hover:-translate-y-0.5 group-hover:text-primary"
          />
        )}
      </div>
    </div>
  );
  return to ? <Link to={to}>{inner}</Link> : inner;
}

/** Quick Staff New Job modal (SRS Section 2A: How Staff enter jobs). */
function QuickJobDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
}) {
  const navigate = useNavigate();
  const catalog = useGet<CatalogItem[]>("/service-catalog", { per_page: 100 }, { enabled: open });
  const [d, setD] = useState({
    client_id: "",
    service_id: "",
    title: "",
    details: "",
    deadline: "",
  });
  const selected = (catalog.data ?? []).find((c) => c.id === d.service_id);
  const isDesign = selected?.category === "design";

  const createProject = useWrite<unknown, { id: string }>("POST", "/projects", {
    invalidate: ["/projects", "/dashboard/me", "/services"],
    success: "Job registered",
    onSuccess: (res) => {
      onOpenChange(false);
      navigate(`/projects/${res.id}`);
    },
  });
  const createDesign = useWrite<unknown, { id: string }>("POST", "/design-jobs", {
    invalidate: ["/design-jobs", "/dashboard/me", "/services"],
    success: "Design job registered",
    onSuccess: (res) => {
      onOpenChange(false);
      navigate(`/design-jobs/${res.id}`);
    },
  });

  function submit() {
    if (isDesign) {
      createDesign.mutate({
        client_id: d.client_id,
        service_id: d.service_id || undefined,
        job_type: "other",
        title: d.title || selected?.name || "Design job",
        brief: d.details,
        deadline: d.deadline || undefined,
      });
    } else {
      createProject.mutate({
        client_id: d.client_id,
        service_id: d.service_id || undefined,
        name: d.title || selected?.name || "Service job",
        notes: d.details,
        start_date: today(),
      });
    }
  }

  return (
    <FormDialog
      open={open}
      onOpenChange={onOpenChange}
      title="New job"
      description="Register a service or design job quickly. Price comes from the Service Catalog."
      busy={createProject.isPending || createDesign.isPending}
      onSubmit={submit}
    >
      <Field label="Client *">
        <ClientSelect required value={d.client_id} onChange={(v) => setD({ ...d, client_id: v })} />
      </Field>
      <Field label="Service / job type">
        <NativeSelect
          value={d.service_id}
          onChange={(e) => {
            const item = (catalog.data ?? []).find((c) => c.id === e.target.value);
            setD({ ...d, service_id: e.target.value, title: d.title || item?.name || "" });
          }}
        >
          <option value="">Choose from Service Catalog…</option>
          {(catalog.data ?? []).map((s) => (
            <option key={s.id} value={s.id}>
              {s.name} ({label(s.category)}) · {formatMoney(s.standard_price)}
            </option>
          ))}
        </NativeSelect>
      </Field>
      {selected && (
        <p className="rounded-lg bg-muted px-3 py-2 text-xs text-muted-foreground">
          Catalog price:{" "}
          <strong className="text-foreground">{formatMoney(selected.standard_price)}</strong>{" "}
          (fixed; ask a Manager to change price)
        </p>
      )}
      <Field label="Job title *">
        <Input
          required
          placeholder="e.g. Company registration or Logo design"
          value={d.title}
          onChange={(e) => setD({ ...d, title: e.target.value })}
        />
      </Field>
      <Field label="Details">
        <Textarea
          rows={3}
          placeholder="What does the client want?"
          value={d.details}
          onChange={(e) => setD({ ...d, details: e.target.value })}
        />
      </Field>
      <Field label="Deadline">
        <Input
          type="date"
          value={d.deadline}
          onChange={(e) => setD({ ...d, deadline: e.target.value })}
        />
      </Field>
    </FormDialog>
  );
}

/** SRS Section 2A: Staff Home Screen. */
function StaffHome() {
  const { user, can } = useAuth();
  const navigate = useNavigate();
  const [newJobOpen, setNewJobOpen] = useState(false);
  const meDash = useGet<StaffMePayload>("/dashboard/me");
  const clockIn = useWrite("POST", "/attendance/clock-in", {
    invalidate: ["/dashboard/me", "/attendance"],
    success: "Clocked in",
  });
  const clockOut = useWrite("POST", "/attendance/clock-out", {
    invalidate: ["/dashboard/me", "/attendance"],
    success: "Clocked out",
  });
  const completeTask = useWrite<{ id: string }>("PUT", (b) => `/tasks/${b.id}`, {
    invalidate: ["/dashboard/me", "/tasks"],
    success: "Task marked done",
    toBody: () => ({ status: "done" }),
  });

  if (meDash.isLoading) return <PageLoader />;
  if (meDash.isError) {
    return (
      <DashboardError message={errorMessage(meDash.error)} onRetry={() => void meDash.refetch()} />
    );
  }
  const d = meDash.data;
  const clockedIn = Boolean(d?.attendance?.clocked_in);

  return (
    <div className="space-y-6">
      <PageHeader
        title={`${greeting()}, ${user?.name.split(" ")[0] ?? ""}`}
        description="Your sales, assigned jobs, tasks and stock alerts for today."
        actions={
          d?.attendance !== null && (
            <Button
              variant={clockedIn ? "outline" : "default"}
              disabled={clockIn.isPending || clockOut.isPending}
              onClick={() => (clockedIn ? clockOut.mutate({}) : clockIn.mutate({}))}
            >
              <Clock size={16} /> {clockedIn ? "Clock out" : "Clock in"}
            </Button>
          )
        }
      />

      {/* 3 Big Speed-First Action Buttons */}
      <div className="grid gap-3 sm:grid-cols-3">
        {can("shop.sale.create") && (
          <Button
            size="lg"
            className="h-14 rounded-2xl text-base shadow-sm transition hover:-translate-y-0.5 hover:shadow-md"
            onClick={() => navigate("/pos")}
          >
            <ShoppingCart size={20} /> NEW SALE
          </Button>
        )}
        {(can("services.project.manage") || can("services.design.manage")) && (
          <Button
            size="lg"
            variant="outline"
            className="h-14 rounded-2xl bg-card text-base shadow-xs transition hover:-translate-y-0.5 hover:border-primary/50 hover:shadow-sm"
            onClick={() => setNewJobOpen(true)}
          >
            <Briefcase size={20} className="text-primary" /> NEW JOB
          </Button>
        )}
        {(can("shop.customer.view_all") ||
          can("shop.customer.manage") ||
          can("services.invoice.manage") ||
          can("finance.payment.receive")) && (
          <Button
            size="lg"
            variant="outline"
            className="h-14 rounded-2xl bg-card text-base shadow-xs transition hover:-translate-y-0.5 hover:border-primary/50 hover:shadow-sm"
            onClick={() =>
              navigate(can("services.invoice.manage") ? "/service-invoices" : "/customers")
            }
          >
            <CreditCard size={20} className="text-primary" /> RECEIVE PAYMENT
          </Button>
        )}
      </div>

      {/* 4 Staff Dashboard Panels */}
      <div className="grid gap-6 lg:grid-cols-2">
        <section className="rounded-2xl border bg-card p-5 shadow-xs">
          <h2 className="font-semibold">My sales today</h2>
          <div className="mt-4 grid grid-cols-2 gap-4">
            <div>
              <p className="text-xs text-muted-foreground">Sales count</p>
              <p className="text-2xl font-bold">{d?.sales_today?.count ?? 0}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Total sold</p>
              <p className="text-2xl font-bold tabular-nums">
                {formatMoney(d?.sales_today?.total)}
              </p>
            </div>
            <div className="col-span-2 border-t pt-3">
              <p className="text-xs text-muted-foreground">My commission this month</p>
              <p className="text-lg font-semibold tabular-nums">
                {formatMoney(d?.commission_month ?? "0")}
              </p>
            </div>
          </div>
        </section>

        <section className="rounded-2xl border bg-card p-5">
          <div className="flex items-center justify-between">
            <h2 className="font-semibold">My jobs</h2>
            <Link to="/projects" className="text-xs text-primary underline">
              All jobs
            </Link>
          </div>
          {(d?.my_jobs ?? []).length === 0 ? (
            <p className="mt-4 text-sm text-muted-foreground">No open jobs assigned to you.</p>
          ) : (
            <ul className="mt-3 divide-y text-sm">
              {(d?.my_jobs ?? []).map((j) => (
                <li key={`${j.kind}-${j.id}`}>
                  <Link
                    to={j.kind === "design_job" ? `/design-jobs/${j.id}` : `/projects/${j.id}`}
                    className="flex items-center justify-between gap-2 py-2.5 hover:text-primary"
                  >
                    <div className="min-w-0">
                      <p className="truncate font-medium">{j.title}</p>
                      <p className="text-xs text-muted-foreground">
                        {j.client_name}
                        {j.due_date ? ` · Due ${formatDate(j.due_date)}` : ""}
                      </p>
                    </div>
                    <StatusBadge value={j.status} />
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </section>

        <section className="rounded-2xl border bg-card p-5">
          <div className="flex items-center justify-between">
            <h2 className="font-semibold">My tasks</h2>
            <Link to="/service-tasks" className="text-xs text-primary underline">
              Tasks & calendar
            </Link>
          </div>
          {(d?.my_tasks ?? []).length === 0 ? (
            <p className="mt-4 text-sm text-muted-foreground">No pending tasks.</p>
          ) : (
            <ul className="mt-3 divide-y text-sm">
              {(d?.my_tasks ?? []).map((t) => (
                <li key={t.id} className="flex items-center justify-between gap-2 py-2.5">
                  <div className="min-w-0">
                    <p className="truncate font-medium">{t.title}</p>
                    <p
                      className={
                        t.overdue ? "text-xs text-destructive" : "text-xs text-muted-foreground"
                      }
                    >
                      {t.due_date ? `Due ${formatDate(t.due_date)}` : "No deadline"}
                      {t.project_number ? ` · ${t.project_number}` : ""}
                    </p>
                  </div>
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => completeTask.mutate({ id: t.id })}
                  >
                    <Check size={14} /> Done
                  </Button>
                </li>
              ))}
            </ul>
          )}
        </section>

        <section className="rounded-2xl border bg-card p-5">
          <div className="flex items-center justify-between">
            <h2 className="font-semibold">Products running low</h2>
            <Link to="/products" className="text-xs text-primary underline">
              Catalog
            </Link>
          </div>
          {(d?.low_stock ?? []).length === 0 ? (
            <p className="mt-4 text-sm text-muted-foreground">All stocked products look good.</p>
          ) : (
            <ul className="mt-3 divide-y text-sm">
              {(d?.low_stock ?? []).map((p) => (
                <li key={p.id} className="flex items-center justify-between py-2">
                  <span className="truncate font-medium">{p.name}</span>
                  <span className="shrink-0 font-medium text-amber-600">
                    {toNumber(p.qty_on_hand)} left
                  </span>
                </li>
              ))}
            </ul>
          )}
        </section>
      </div>

      <QuickJobDialog open={newJobOpen} onOpenChange={setNewJobOpen} />
    </div>
  );
}

/** SRS Section 5: Unified Admin & Manager Dashboard. */
export default function Dashboard() {
  const { me, can, canAny, user } = useAuth();
  const navigate = useNavigate();
  const isStaffHome =
    !me?.is_owner && !canAny("dashboard.business.view", "dashboard.operations.view");
  const canDecideApprovals = canAny(
    "shop.discount.approve",
    "shop.return.approve",
    "shop.sale.void",
    "shop.product.price_approve",
    "services.price.approve",
    "finance.expense.approve",
    "finance.payment.void",
    "finance.transaction.correct",
  );

  const [period, setPeriod] = useState<"today" | "week" | "month" | "custom">("month");
  const [business, setBusiness] = useState<"all" | "shop" | "services">("all");
  const [from, setFrom] = useState(() => `${today().slice(0, 8)}01`);
  const [to, setTo] = useState(() => today());

  const query = period === "custom" ? { period, business, from, to } : { period, business };

  const dash = useGet<DashboardPayload>("/dashboard", query, { enabled: !isStaffHome });
  const approve = useWrite<{ id: string }>("POST", (b) => `/approvals/${b.id}/approve`, {
    idempotent: true,
    invalidate: ["/dashboard", "/approvals"],
    success: "Approved",
    toBody: () => ({}),
  });
  const reject = useWrite<{ id: string }>("POST", (b) => `/approvals/${b.id}/reject`, {
    invalidate: ["/dashboard", "/approvals"],
    success: "Rejected",
    toBody: () => ({ note: "Rejected from dashboard" }),
  });

  if (isStaffHome) return <StaffHome />;
  if (dash.isLoading) return <PageLoader />;
  if (dash.isError) {
    return (
      <DashboardError message={errorMessage(dash.error)} onRetry={() => void dash.refetch()} />
    );
  }
  const d = dash.data;

  const canBusinessDash = Boolean(me?.is_owner) || can("dashboard.business.view");
  const showSalesKpi = Boolean(d?.sales) || business !== "services";
  const showServicesKpi = Boolean(d?.service_income) || business !== "shop";
  const showExpensesKpi = Boolean(d?.expenses) || canBusinessDash;
  const showProfitKpi = Boolean(d?.profit) || Boolean(me?.is_owner) || can("reports.profit.view");

  const owedToUs = toNumber(d?.outstanding?.customers_owe) + toNumber(d?.outstanding?.clients_owe);
  const weOwe =
    toNumber(d?.outstanding?.we_owe_suppliers) + toNumber(d?.outstanding?.we_owe_contractors);

  const chartData = (d?.income_vs_expenses ?? []).map((pt) => ({
    date: pt.date,
    name: pt.date.slice(5),
    income: toNumber(pt.income),
    expenses: toNumber(pt.expenses),
  }));
  const maxStage = Math.max(1, ...(d?.projects_by_stage ?? []).map((s) => s.count));

  return (
    <div className="space-y-6">
      <PageHeader
        title={`${greeting()}, ${user?.name.split(" ")[0] ?? ""}`}
        description="Business overview across Shop (Business A) and Services (Business B)."
        actions={
          <div className="flex flex-wrap items-center gap-2">
            {can("shop.sale.create") && (
              <Button size="sm" onClick={() => navigate("/pos")}>
                <ShoppingCart size={15} /> Open POS
              </Button>
            )}
            <NativeSelect
              aria-label="Period"
              className="w-36"
              value={period}
              onChange={(e) => setPeriod(e.target.value as typeof period)}
            >
              <option value="today">Today</option>
              <option value="week">This week</option>
              <option value="month">This month</option>
              <option value="custom">Custom dates</option>
            </NativeSelect>
            {period === "custom" && (
              <>
                <Input
                  type="date"
                  className="w-36"
                  value={from}
                  onChange={(e) => setFrom(e.target.value)}
                />
                <Input
                  type="date"
                  className="w-36"
                  value={to}
                  onChange={(e) => setTo(e.target.value)}
                />
              </>
            )}
            <NativeSelect
              aria-label="Business"
              className="w-44"
              value={business}
              onChange={(e) => setBusiness(e.target.value as typeof business)}
            >
              <option value="all">Both businesses</option>
              <option value="shop">Shop (Business A)</option>
              <option value="services">Services (Business B)</option>
            </NativeSelect>
          </div>
        }
      />

      {/* Row 1: Four KPI Total Cards */}
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {showSalesKpi && (
          <KpiCard
            label="Shop sales"
            value={formatMoney(d?.sales?.total ?? "0")}
            sub={`${d?.sales?.count ?? 0} sales`}
            trend={trendInfo(d?.sales?.change_pct)}
            icon={ShoppingBag}
            to="/reports?path=/reports/sales/summary"
          />
        )}
        {showServicesKpi && (
          <KpiCard
            label="Service income"
            value={formatMoney(d?.service_income?.total ?? "0")}
            sub="Services revenue"
            trend={trendInfo(d?.service_income?.change_pct)}
            icon={Briefcase}
            to="/reports?path=/reports/projects"
          />
        )}
        {showExpensesKpi && (
          <KpiCard
            label="Expenses"
            value={formatMoney(d?.expenses?.total ?? "0")}
            sub="Approved & paid"
            trend={trendInfo(d?.expenses?.change_pct)}
            icon={Receipt}
            accent="amber"
            to="/reports?path=/reports/expenses"
          />
        )}
        {showProfitKpi && (
          <KpiCard
            label="Net profit"
            value={formatMoney(d?.profit?.net_profit ?? "0")}
            sub={`Gross ${formatMoney(d?.profit?.gross_profit ?? "0")}`}
            trend={trendInfo(d?.profit?.change_pct)}
            icon={TrendingUp}
            to="/reports?path=/reports/profit-loss"
          />
        )}
      </div>

      {/* Row 2: Income vs Expenses Line Chart + Cash Balances */}
      <div className="grid gap-6 lg:grid-cols-3">
        <div className={d?.cash ? "lg:col-span-2" : "lg:col-span-3"}>
          <ChartCard
            title="Income vs expenses by day"
            description="Click a day to inspect transactions"
          >
            {chartData.length > 0 ? (
              <IncomeExpenseChart data={chartData} onDayClick={() => navigate("/money")} />
            ) : (
              <div className="flex h-56 items-center justify-center text-sm text-muted-foreground">
                No income or expense transactions recorded for this period yet.
              </div>
            )}
          </ChartCard>
        </div>
        {d?.cash && (
          <section className="rounded-2xl border bg-card p-5 shadow-xs">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2">
                <span className="flex h-8 w-8 items-center justify-center rounded-lg bg-primary/12 text-primary">
                  <Wallet size={16} />
                </span>
                <h2 className="font-semibold">Cash balances</h2>
              </div>
              <Link to="/money" className="text-xs font-medium text-primary hover:underline">
                Accounts
              </Link>
            </div>
            <p className="mt-3 text-2xl font-bold tracking-tight tabular-nums">
              {formatMoney(d.cash.total)}
            </p>
            <ul className="mt-4 divide-y text-sm">
              {d.cash.accounts.length === 0 ? (
                <li className="py-3 text-xs text-muted-foreground">No money accounts yet.</li>
              ) : (
                d.cash.accounts.slice(0, 6).map((a) => (
                  <li key={a.id} className="flex items-center justify-between py-2.5">
                    <div>
                      <p className="font-medium">{a.name}</p>
                      <p className="text-xs text-muted-foreground">{label(a.type)}</p>
                    </div>
                    <span className="font-semibold tabular-nums">{formatMoney(a.balance)}</span>
                  </li>
                ))
              )}
            </ul>
          </section>
        )}
      </div>

      {/* Row 3: Projects by Stage + Debts */}
      <div className="grid gap-6 lg:grid-cols-2">
        {d?.projects_by_stage && (
          <section className="rounded-2xl border bg-card p-5">
            <div className="flex items-center justify-between">
              <h2 className="font-semibold">Projects by stage</h2>
              <Link to="/projects" className="text-xs text-primary underline">
                Open board
              </Link>
            </div>
            {d.projects_by_stage.length === 0 ? (
              <p className="mt-4 text-sm text-muted-foreground">No active projects.</p>
            ) : (
              <ul className="mt-4 space-y-3 text-sm">
                {d.projects_by_stage.map((st) => (
                  <li key={st.stage}>
                    <Link to="/projects" className="block group">
                      <div className="flex justify-between text-xs">
                        <span className="font-medium group-hover:text-primary">{st.stage}</span>
                        <span>
                          {st.count}
                          {st.late > 0 && (
                            <span className="ml-1.5 text-destructive">({st.late} late/stuck)</span>
                          )}
                        </span>
                      </div>
                      <div className="mt-1 h-2 overflow-hidden rounded-full bg-muted">
                        <div
                          className="h-full rounded-full bg-primary"
                          style={{ width: `${Math.round((st.count / maxStage) * 100)}%` }}
                        />
                      </div>
                    </Link>
                  </li>
                ))}
              </ul>
            )}
          </section>
        )}

        {d?.outstanding && (
          <section className="rounded-2xl border bg-card p-5">
            <div className="flex items-center justify-between">
              <h2 className="font-semibold">Debts</h2>
              <Link to="/customers" className="text-xs text-primary underline">
                View debts
              </Link>
            </div>
            <div className="mt-4 grid gap-4 sm:grid-cols-2">
              <div className="rounded-xl border p-4">
                <p className="text-xs uppercase tracking-wide text-muted-foreground">Owed to us</p>
                <p className="mt-1 text-xl font-bold tabular-nums">{formatMoney(owedToUs)}</p>
                <div className="mt-2 space-y-1 text-xs text-muted-foreground">
                  <div className="flex justify-between">
                    <span>Shop customers</span>
                    <span>{formatMoney(d.outstanding.customers_owe ?? "0")}</span>
                  </div>
                  <div className="flex justify-between">
                    <span>Service clients</span>
                    <span>{formatMoney(d.outstanding.clients_owe ?? "0")}</span>
                  </div>
                </div>
              </div>
              <div className="rounded-xl border p-4">
                <p className="text-xs uppercase tracking-wide text-muted-foreground">We owe</p>
                <p className="mt-1 text-xl font-bold tabular-nums text-destructive">
                  {formatMoney(weOwe)}
                </p>
                <div className="mt-2 space-y-1 text-xs text-muted-foreground">
                  <div className="flex justify-between">
                    <span>Suppliers</span>
                    <span>{formatMoney(d.outstanding.we_owe_suppliers ?? "0")}</span>
                  </div>
                  <div className="flex justify-between">
                    <span>Contractors</span>
                    <span>{formatMoney(d.outstanding.we_owe_contractors ?? "0")}</span>
                  </div>
                </div>
              </div>
            </div>
            {d.outstanding.salaries_due !== undefined && d.outstanding.salaries_due !== null && (
              <Link
                to="/payroll"
                className="mt-3 flex items-center justify-between rounded-xl border bg-muted/25 px-4 py-2.5 text-sm transition hover:border-primary/40 hover:text-primary"
              >
                <span className="font-medium">Salaries due</span>
                <span className="font-semibold tabular-nums">
                  {formatMoney(d.outstanding.salaries_due)}
                </span>
              </Link>
            )}
          </section>
        )}
      </div>

      {/* Row 4: Overdue Tasks, Low Stock, Renewals Due, Approvals Waiting */}
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        {d?.overdue_tasks && (
          <section className="rounded-2xl border bg-card p-4">
            <div className="flex items-center justify-between">
              <h2 className="text-sm font-semibold">Overdue tasks ({d.overdue_tasks.length})</h2>
              <Link to="/service-tasks" className="text-xs text-primary underline">
                All
              </Link>
            </div>
            {d.overdue_tasks.length === 0 ? (
              <p className="mt-3 text-xs text-muted-foreground">No overdue tasks.</p>
            ) : (
              <ul className="mt-3 divide-y text-xs">
                {d.overdue_tasks.map((t) => (
                  <li key={t.id} className="py-2">
                    <p className="font-medium">{t.title}</p>
                    <p className="text-destructive">
                      Due {formatDate(t.due_date)}
                      {t.assignee_name ? ` · ${t.assignee_name}` : ""}
                    </p>
                  </li>
                ))}
              </ul>
            )}
          </section>
        )}

        {d?.stock && (
          <section className="rounded-2xl border bg-card p-4">
            <div className="flex items-center justify-between">
              <h2 className="text-sm font-semibold">Low stock ({d.stock.low_stock_count})</h2>
              <Link to="/products" className="text-xs text-primary underline">
                Products
              </Link>
            </div>
            {d.stock.value !== undefined && d.stock.value !== null && (
              <Link
                to={
                  can("reports.business.view")
                    ? "/reports?path=/reports/inventory/valuation"
                    : "/products"
                }
                className="mt-2.5 flex items-center justify-between rounded-xl border bg-muted/25 px-3 py-2 text-xs transition hover:border-primary/40 hover:text-primary"
              >
                <span className="font-medium text-muted-foreground">Stock value</span>
                <span className="font-bold tabular-nums text-foreground">
                  {formatMoney(d.stock.value)}
                </span>
              </Link>
            )}
            {(d.stock.items ?? []).length === 0 ? (
              <p className="mt-3 text-xs text-muted-foreground">Stock levels are healthy.</p>
            ) : (
              <ul className="mt-3 divide-y text-xs">
                {(d.stock.items ?? []).map((p) => (
                  <li key={p.id} className="flex items-center justify-between py-2">
                    <span className="truncate font-medium">{p.name}</span>
                    <span className="shrink-0 font-medium text-amber-600">
                      {toNumber(p.qty_on_hand)} left
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </section>
        )}

        {d?.renewals_due && (
          <section className="rounded-2xl border bg-card p-4">
            <div className="flex items-center justify-between">
              <h2 className="text-sm font-semibold">Renewals due ({d.renewals_due.length})</h2>
              <Link to="/renewals" className="text-xs text-primary underline">
                All
              </Link>
            </div>
            {d.renewals_due.length === 0 ? (
              <p className="mt-3 text-xs text-muted-foreground">None due in the next 30 days.</p>
            ) : (
              <ul className="mt-3 divide-y text-xs">
                {d.renewals_due.map((r) => (
                  <li key={r.id} className="py-2">
                    <p className="font-medium">{r.item}</p>
                    <p className="text-muted-foreground">
                      {r.client_name} ·{" "}
                      <span className={r.days_left <= 7 ? "font-medium text-destructive" : ""}>
                        {r.days_left < 0 ? `${-r.days_left}d late` : `${r.days_left}d left`}
                      </span>
                    </p>
                  </li>
                ))}
              </ul>
            )}
          </section>
        )}

        {d?.pending_approvals && (
          <section className="rounded-2xl border bg-card p-4">
            <div className="flex items-center justify-between">
              <h2 className="text-sm font-semibold">Approvals ({d.pending_approvals.count})</h2>
              <Link to="/approvals" className="text-xs text-primary underline">
                All
              </Link>
            </div>
            {d.pending_approvals.latest.length === 0 ? (
              <p className="mt-3 text-xs text-muted-foreground">Nothing waiting for approval.</p>
            ) : (
              <ul className="mt-3 divide-y text-xs">
                {d.pending_approvals.latest.map((a) => (
                  <li key={a.id} className="space-y-1.5 py-2">
                    <div className="flex items-center justify-between gap-2">
                      <span className="truncate font-medium">{a.summary || label(a.kind)}</span>
                      {a.amount && (
                        <span className="shrink-0 tabular-nums">{formatMoney(a.amount)}</span>
                      )}
                    </div>
                    {canDecideApprovals && (
                      <div className="flex gap-1.5">
                        <Button
                          size="sm"
                          variant="outline"
                          className="h-6 px-2 text-xs text-emerald-700"
                          disabled={approve.isPending}
                          onClick={() => approve.mutate({ id: a.id })}
                        >
                          <Check size={12} /> Approve
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          className="h-6 px-2 text-xs text-destructive"
                          disabled={reject.isPending}
                          onClick={() => reject.mutate({ id: a.id })}
                        >
                          <X size={12} /> Reject
                        </Button>
                      </div>
                    )}
                  </li>
                ))}
              </ul>
            )}
          </section>
        )}
      </div>
    </div>
  );
}
