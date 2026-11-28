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
import {
  Field,
  FormDialog,
  NativeSelect,
  PageHeader,
  SectionCard,
  SegmentedChips,
  StatusBadge,
} from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { ClientSelect } from "@/features/services/shared";
import { label } from "@/features/services/helpers";
import { errorMessage, useGet, useWrite } from "@/hooks/useApi";
import { formatDate, formatMoney, today, toNumber } from "@/lib/formatters";
import type { CatalogItem } from "@/types/services";
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";
import type { Translations } from "@/features/i18n/i18n";

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

function greeting(t: Translations) {
  const h = new Date().getHours();
  if (h < 12) return t.dashboard.goodMorning;
  if (h < 17) return t.dashboard.goodAfternoon;
  return t.dashboard.goodEvening;
}

function trendInfo(pct?: string | null, invertGood = false) {
  if (pct === null || pct === undefined || pct === "") return null;
  const n = toNumber(pct);
  const sign = n > 0 ? "+" : "";
  const up = n >= 0;
  return {
    text: `${sign}${n.toFixed(1)}%`,
    up,
    good: invertGood ? n <= 0 : n >= 0,
  };
}

function DashboardError({ message, onRetry }: { message: string; onRetry: () => void }) {
  const { t } = useI18n();
  return (
    <div className="od-card items-center py-12 text-center">
      <div className="flex h-11 w-11 items-center justify-center rounded-full bg-[color-mix(in_srgb,var(--od-bad-mark)_12%,transparent)] text-[var(--od-bad)]">
        <AlertTriangle size={20} />
      </div>
      <h2 className="od-section-title mt-3">{t.dashboard.couldNotLoadDashboardData}</h2>
      <p className="od-caption mt-1 max-w-md">{message}</p>
      <Button className="mt-4" variant="outline" size="sm" onClick={onRetry}>
        <RefreshCw size={14} /> {t.dashboard.tryAgain}
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
}: {
  label: string;
  value: string;
  sub?: string;
  trend?: { text: string; up: boolean; good: boolean } | null;
  icon: React.ComponentType<{ size?: number; className?: string }>;
  to?: string;
}) {
  const inner = (
    <article className="od-card group h-full justify-between gap-3 transition-colors hover:border-[var(--od-ink-muted)]">
      <div className="flex items-center justify-between gap-2">
        <p className="od-kpi-label">{title}</p>
        <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-[var(--od-surface-subtle)] text-[var(--od-ink-secondary)]">
          <Icon size={15} />
        </span>
      </div>
      <p className="od-kpi-value truncate text-[var(--od-ink)]">{value}</p>
      <div className="flex items-center justify-between gap-2 pt-0.5">
        <div className="flex min-w-0 items-center gap-2">
          {trend && (
            <span className={`od-delta ${trend.good ? "od-delta-good" : "od-delta-bad"}`}>
              {trend.up ? <TrendingUp size={12} /> : <TrendingDown size={12} />}
              {trend.text}
            </span>
          )}
          {sub && <span className="od-caption truncate">{sub}</span>}
        </div>
        {to && (
          <ArrowUpRight
            size={14}
            className="shrink-0 text-[var(--od-ink-muted)] transition-transform group-hover:translate-x-0.5 group-hover:-translate-y-0.5 group-hover:text-[var(--od-ink)]"
          />
        )}
      </div>
    </article>
  );
  return to ? (
    <Link to={to} className="block h-full">
      {inner}
    </Link>
  ) : (
    inner
  );
}

/** Quick Staff New Job modal (SRS Section 2A: How Staff enter jobs). */
function QuickJobDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
}) {
  const { t } = useI18n();
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
    success: t.dashboard.jobRegistered,
    onSuccess: (res) => {
      onOpenChange(false);
      navigate(`/projects/${res.id}`);
    },
  });
  const createDesign = useWrite<unknown, { id: string }>("POST", "/design-jobs", {
    invalidate: ["/design-jobs", "/dashboard/me", "/services"],
    success: t.dashboard.designJobRegistered,
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
      title={t.common.newJob}
      description={t.dashboard.registerAServiceOrDesign}
      busy={createProject.isPending || createDesign.isPending}
      onSubmit={submit}
    >
      <Field label={t.common.client2}>
        <ClientSelect required value={d.client_id} onChange={(v) => setD({ ...d, client_id: v })} />
      </Field>
      <Field label={t.dashboard.serviceJobType}>
        <NativeSelect
          value={d.service_id}
          onChange={(e) => {
            const item = (catalog.data ?? []).find((c) => c.id === e.target.value);
            setD({ ...d, service_id: e.target.value, title: d.title || item?.name || "" });
          }}
        >
          <option value="">{t.dashboard.chooseFromServiceCatalog}</option>
          {(catalog.data ?? []).map((s) => (
            <option key={s.id} value={s.id}>
              {s.name} ({label(s.category)}) · {formatMoney(s.standard_price)}
            </option>
          ))}
        </NativeSelect>
      </Field>
      {selected && (
        <p className="rounded-[var(--od-radius-sm,8px)] bg-[var(--od-surface-subtle)] px-3 py-2 text-xs text-[var(--od-ink-secondary)]">
          {t.dashboard.catalogPrice}{" "}
          <strong className="od-tabular text-[var(--od-ink)]">
            {formatMoney(selected.standard_price)}
          </strong>{" "}
          {t.dashboard.fixedAskAManagerTo}
        </p>
      )}
      <Field label={t.dashboard.jobTitle}>
        <Input
          required
          placeholder={t.dashboard.eGCompanyRegistrationOr}
          value={d.title}
          onChange={(e) => setD({ ...d, title: e.target.value })}
        />
      </Field>
      <Field label={t.common.details}>
        <Textarea
          rows={3}
          placeholder={t.dashboard.whatDoesTheClientWant}
          value={d.details}
          onChange={(e) => setD({ ...d, details: e.target.value })}
        />
      </Field>
      <Field label={t.common.deadline}>
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
  const { t } = useI18n();
  const { user, can } = useAuth();
  const navigate = useNavigate();
  const [newJobOpen, setNewJobOpen] = useState(false);
  const meDash = useGet<StaffMePayload>("/dashboard/me");
  const clockIn = useWrite("POST", "/attendance/clock-in", {
    invalidate: ["/dashboard/me", "/attendance"],
    success: t.common.clockedIn,
  });
  const clockOut = useWrite("POST", "/attendance/clock-out", {
    invalidate: ["/dashboard/me", "/attendance"],
    success: t.common.clockedOut,
  });
  const completeTask = useWrite<{ id: string }>("PUT", (b) => `/tasks/${b.id}`, {
    invalidate: ["/dashboard/me", "/tasks"],
    success: t.dashboard.taskMarkedDone,
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
        title={`${greeting(t)}, ${user?.name.split(" ")[0] ?? ""}`}
        description={t.dashboard.yourSalesAssignedJobsTasks}
        actions={
          d?.attendance !== null && (
            <Button
              variant={clockedIn ? "outline" : "default"}
              disabled={clockIn.isPending || clockOut.isPending}
              onClick={() => (clockedIn ? clockOut.mutate({}) : clockIn.mutate({}))}
            >
              <Clock size={15} /> {clockedIn ? t.common.clockOut : t.common.clockIn}
            </Button>
          )
        }
      />

      {/* 3 Speed-First Action Buttons */}
      <div className="grid gap-3 sm:grid-cols-3">
        {can("shop.sale.create") && (
          <Button
            size="lg"
            className="h-14 justify-start gap-3 rounded-[var(--od-radius,16px)] px-5 text-sm font-semibold"
            onClick={() => navigate("/pos")}
          >
            <ShoppingCart size={18} />
            <span>{t.common.newSalePos}</span>
            <ArrowUpRight size={15} className="ml-auto opacity-70" />
          </Button>
        )}
        {(can("services.project.manage") || can("services.design.manage")) && (
          <Button
            size="lg"
            variant="outline"
            className="h-14 justify-start gap-3 rounded-[var(--od-radius,16px)] px-5 text-sm font-semibold"
            onClick={() => setNewJobOpen(true)}
          >
            <Briefcase size={18} />
            <span>{t.common.newJob}</span>
            <ArrowUpRight size={15} className="ml-auto opacity-50" />
          </Button>
        )}
        {(can("shop.customer.view_all") ||
          can("shop.customer.manage") ||
          can("services.invoice.manage") ||
          can("finance.payment.receive")) && (
          <Button
            size="lg"
            variant="outline"
            className="h-14 justify-start gap-3 rounded-[var(--od-radius,16px)] px-5 text-sm font-semibold"
            onClick={() =>
              navigate(can("services.invoice.manage") ? "/service-invoices" : "/customers")
            }
          >
            <CreditCard size={18} />
            <span>{t.common.receivePayment}</span>
            <ArrowUpRight size={15} className="ml-auto opacity-50" />
          </Button>
        )}
      </div>

      {/* 4 Staff Dashboard Panels */}
      <div className="grid gap-5 lg:grid-cols-2">
        <SectionCard
          title={t.dashboard.mySalesToday}
          subtitle={t.dashboard.recordedUnderYourAccount}
        >
          <div className="grid grid-cols-2 gap-4 pt-1">
            <div className="rounded-[var(--od-radius-md,12px)] bg-[var(--od-surface-subtle)] p-4">
              <p className="od-kpi-label">{t.dashboard.salesCount}</p>
              <p className="od-kpi-value mt-1.5">{d?.sales_today?.count ?? 0}</p>
            </div>
            <div className="rounded-[var(--od-radius-md,12px)] bg-[var(--od-surface-subtle)] p-4">
              <p className="od-kpi-label">{t.dashboard.totalSold}</p>
              <p className="od-kpi-value mt-1.5">{formatMoney(d?.sales_today?.total)}</p>
            </div>
            <div className="col-span-2 flex items-center justify-between border-t border-[var(--od-border)] pt-3">
              <span className="od-caption">{t.dashboard.myCommissionThisMonth}</span>
              <span className="od-tabular text-[15px] font-semibold text-[var(--od-ink)]">
                {formatMoney(d?.commission_month ?? "0")}
              </span>
            </div>
          </div>
        </SectionCard>

        <SectionCard
          title={t.dashboard.myJobs}
          actions={
            <Link
              to="/projects"
              className="text-xs font-medium text-[var(--od-ink-secondary)] underline underline-offset-4 hover:text-[var(--od-ink)]"
            >
              {t.dashboard.allJobs}
            </Link>
          }
        >
          {(d?.my_jobs ?? []).length === 0 ? (
            <p className="od-caption py-4">{t.dashboard.noOpenJobsAssignedTo}</p>
          ) : (
            <ul className="divide-y divide-[var(--od-border)] text-[13px]">
              {(d?.my_jobs ?? []).map((j) => (
                <li key={`${j.kind}-${j.id}`}>
                  <Link
                    to={j.kind === "design_job" ? `/design-jobs/${j.id}` : `/projects/${j.id}`}
                    className="flex items-center justify-between gap-2 py-2.5 transition-colors hover:text-[var(--od-info)]"
                  >
                    <div className="min-w-0">
                      <p className="truncate font-medium">{j.title}</p>
                      <p className="od-caption">
                        {j.client_name}
                        {j.due_date
                          ? ` ${fmt(t.dashboard.due, { due_date: formatDate(j.due_date) })}`
                          : ""}
                      </p>
                    </div>
                    <StatusBadge value={j.status} />
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </SectionCard>

        <SectionCard
          title={t.dashboard.myTasks}
          actions={
            <Link
              to="/service-tasks"
              className="text-xs font-medium text-[var(--od-ink-secondary)] underline underline-offset-4 hover:text-[var(--od-ink)]"
            >
              {t.dashboard.tasksCalendar}
            </Link>
          }
        >
          {(d?.my_tasks ?? []).length === 0 ? (
            <p className="od-caption py-4">{t.dashboard.noPendingTasks}</p>
          ) : (
            <ul className="divide-y divide-[var(--od-border)] text-[13px]">
              {(d?.my_tasks ?? []).map((item) => (
                <li key={item.id} className="flex items-center justify-between gap-2 py-2.5">
                  <div className="min-w-0">
                    <p className="truncate font-medium">{item.title}</p>
                    <p
                      className={
                        item.overdue ? "text-xs font-medium text-[var(--od-bad)]" : "od-caption"
                      }
                    >
                      {item.due_date
                        ? fmt(t.common.due, { due_date: formatDate(item.due_date) })
                        : t.dashboard.noDeadline}
                      {item.project_number ? ` · ${item.project_number}` : ""}
                    </p>
                  </div>
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => completeTask.mutate({ id: item.id })}
                  >
                    <Check size={13} /> {t.common.done}
                  </Button>
                </li>
              ))}
            </ul>
          )}
        </SectionCard>

        <SectionCard
          title={t.dashboard.productsRunningLow}
          actions={
            <Link
              to="/products"
              className="text-xs font-medium text-[var(--od-ink-secondary)] underline underline-offset-4 hover:text-[var(--od-ink)]"
            >
              {t.common.catalog}
            </Link>
          }
        >
          {(d?.low_stock ?? []).length === 0 ? (
            <p className="od-caption py-4">{t.dashboard.allStockedProductsLookGood}</p>
          ) : (
            <ul className="divide-y divide-[var(--od-border)] text-[13px]">
              {(d?.low_stock ?? []).map((p) => (
                <li key={p.id} className="flex items-center justify-between py-2.5">
                  <span className="truncate font-medium">{p.name}</span>
                  <span className="od-tabular shrink-0 text-xs font-semibold text-[var(--od-warning)]">
                    {fmt(t.dashboard.left, { qty_on_hand: toNumber(p.qty_on_hand) })}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </SectionCard>
      </div>

      <QuickJobDialog open={newJobOpen} onOpenChange={setNewJobOpen} />
    </div>
  );
}

/** SRS Section 5: Unified Admin & Manager Dashboard. */
export default function Dashboard() {
  const { t } = useI18n();
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
    success: t.common.approved,
    toBody: () => ({}),
  });
  const reject = useWrite<{ id: string }>("POST", (b) => `/approvals/${b.id}/reject`, {
    invalidate: ["/dashboard", "/approvals"],
    success: t.common.rejected,
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
        title={`${greeting(t)}, ${user?.name.split(" ")[0] ?? ""}`}
        description={t.dashboard.businessPerformanceAcrossShopAnd}
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <SegmentedChips
              ariaLabel={t.common.period}
              value={period}
              onChange={setPeriod}
              options={[
                { value: "today", label: t.common.today },
                { value: "week", label: t.dashboard.week },
                { value: "month", label: t.common.month },
                { value: "custom", label: t.common.custom },
              ]}
            />
            <SegmentedChips
              ariaLabel={t.common.business}
              value={business}
              onChange={setBusiness}
              options={[
                { value: "all", label: t.common.all },
                { value: "shop", label: t.common.shop },
                { value: "services", label: t.common.services },
              ]}
            />
            {can("shop.sale.create") && (
              <Button size="sm" onClick={() => navigate("/pos")}>
                <ShoppingCart size={14} /> {t.dashboard.openPos}
              </Button>
            )}
          </div>
        }
      />

      {period === "custom" && (
        <div className="flex flex-wrap items-center gap-2.5 rounded-[var(--od-radius-md,12px)] border border-[var(--od-border-strong)] bg-[var(--od-surface-subtle)] px-4 py-2.5">
          <span className="text-xs font-medium text-[var(--od-ink-secondary)]">
            {t.dashboard.dateRange}
          </span>
          <Input
            type="date"
            aria-label={t.common.startDate}
            className="h-8 w-36 text-xs"
            value={from}
            onChange={(e) => setFrom(e.target.value)}
          />
          <span className="text-xs text-[var(--od-ink-muted)]">{t.dashboard.to}</span>
          <Input
            type="date"
            aria-label={t.dashboard.endDate}
            className="h-8 w-36 text-xs"
            value={to}
            onChange={(e) => setTo(e.target.value)}
          />
        </div>
      )}

      {/* Row 1: Four KPI Total Cards */}
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {showSalesKpi && (
          <KpiCard
            label={t.dashboard.shopSales}
            value={formatMoney(d?.sales?.total ?? "0")}
            sub={fmt(t.dashboard.sales, { count: d?.sales?.count ?? 0 })}
            trend={trendInfo(d?.sales?.change_pct)}
            icon={ShoppingBag}
            to="/reports?path=/reports/sales/summary"
          />
        )}
        {showServicesKpi && (
          <KpiCard
            label={t.dashboard.serviceIncome}
            value={formatMoney(d?.service_income?.total ?? "0")}
            sub={t.dashboard.servicesRevenue}
            trend={trendInfo(d?.service_income?.change_pct)}
            icon={Briefcase}
            to="/reports?path=/reports/projects"
          />
        )}
        {showExpensesKpi && (
          <KpiCard
            label={t.common.expenses}
            value={formatMoney(d?.expenses?.total ?? "0")}
            sub={t.dashboard.approvedPaid}
            trend={trendInfo(d?.expenses?.change_pct, true)}
            icon={Receipt}
            to="/reports?path=/reports/expenses"
          />
        )}
        {showProfitKpi && (
          <KpiCard
            label={t.common.netProfit}
            value={formatMoney(d?.profit?.net_profit ?? "0")}
            sub={fmt(t.dashboard.gross, {
              gross_profit: formatMoney(d?.profit?.gross_profit ?? "0"),
            })}
            trend={trendInfo(d?.profit?.change_pct)}
            icon={TrendingUp}
            to="/reports?path=/reports/profit-loss"
          />
        )}
      </div>

      {/* Row 2: Income vs Expenses Line Chart + Cash Balances */}
      <div className="grid gap-5 lg:grid-cols-3">
        <div className={d?.cash ? "lg:col-span-2" : "lg:col-span-3"}>
          <ChartCard
            title={t.dashboard.incomeVsExpensesByDay}
            description={t.dashboard.clickAnyDayOnThe}
          >
            {chartData.length > 0 ? (
              <IncomeExpenseChart data={chartData} onDayClick={() => navigate("/money")} />
            ) : (
              <div className="flex h-64 items-center justify-center text-[13px] text-[var(--od-ink-muted)]">
                {t.dashboard.noIncomeOrExpenseTransactions}
              </div>
            )}
          </ChartCard>
        </div>
        {d?.cash && (
          <SectionCard
            title={t.dashboard.cashBalances}
            subtitle={t.dashboard.activeMoneyAccounts}
            actions={
              <Link
                to="/money"
                className="inline-flex items-center gap-1 text-xs font-medium text-[var(--od-ink-secondary)] hover:text-[var(--od-ink)]"
              >
                <Wallet size={13} /> {t.common.accounts}
              </Link>
            }
          >
            <p className="od-kpi-value pt-1">{formatMoney(d.cash.total)}</p>
            <ul className="divide-y divide-[var(--od-border)] text-[13px]">
              {d.cash.accounts.length === 0 ? (
                <li className="py-3 text-xs text-[var(--od-ink-muted)]">
                  {t.dashboard.noMoneyAccountsYet}
                </li>
              ) : (
                d.cash.accounts.slice(0, 6).map((a) => (
                  <li key={a.id} className="flex items-center justify-between gap-2 py-2.5">
                    <div className="min-w-0">
                      <p className="truncate font-medium text-[var(--od-ink)]">{a.name}</p>
                      <p className="od-caption">{label(a.type)}</p>
                    </div>
                    <span className="od-tabular shrink-0 font-semibold">
                      {formatMoney(a.balance)}
                    </span>
                  </li>
                ))
              )}
            </ul>
          </SectionCard>
        )}
      </div>

      {/* Row 3: Projects by Stage + Debts */}
      <div className="grid gap-5 lg:grid-cols-2">
        {d?.projects_by_stage && (
          <SectionCard
            title={t.common.projectsByStage}
            subtitle={t.dashboard.activeServiceWorkDistribution}
            actions={
              <Link
                to="/projects"
                className="text-xs font-medium text-[var(--od-ink-secondary)] underline underline-offset-4 hover:text-[var(--od-ink)]"
              >
                {t.common.openBoard}
              </Link>
            }
          >
            {d.projects_by_stage.length === 0 ? (
              <p className="od-caption py-4">{t.dashboard.noActiveProjects}</p>
            ) : (
              <ul className="space-y-3.5 pt-1 text-[13px]">
                {d.projects_by_stage.map((st) => (
                  <li key={st.stage}>
                    <Link to="/projects" className="group block">
                      <div className="flex items-baseline justify-between text-xs">
                        <span className="font-medium text-[var(--od-ink)] group-hover:underline">
                          {st.stage}
                        </span>
                        <span className="od-tabular text-[var(--od-ink-secondary)]">
                          <strong className="text-[var(--od-ink)]">{st.count}</strong>
                          {st.late > 0 && (
                            <span className="ml-1.5 font-medium text-[var(--od-bad)]">
                              {fmt(t.dashboard.late, { late: st.late })}
                            </span>
                          )}
                        </span>
                      </div>
                      <div className="mt-1.5 h-2 overflow-hidden rounded-full bg-[var(--od-neutral-soft)]">
                        <div
                          className="h-full rounded-full transition-all"
                          style={{
                            width: `${Math.round((st.count / maxStage) * 100)}%`,
                            backgroundColor: "var(--od-series-1)",
                          }}
                        />
                      </div>
                    </Link>
                  </li>
                ))}
              </ul>
            )}
          </SectionCard>
        )}

        {d?.outstanding && (
          <SectionCard
            title={t.dashboard.debtsPayables}
            subtitle={t.dashboard.receivablesAndOutstandingObligations}
            actions={
              <Link
                to="/debts"
                className="text-xs font-medium text-[var(--od-ink-secondary)] underline underline-offset-4 hover:text-[var(--od-ink)]"
              >
                {t.dashboard.viewDebts}
              </Link>
            }
          >
            <div className="grid gap-3 pt-1 sm:grid-cols-2">
              <div className="rounded-[var(--od-radius-md,12px)] border border-[var(--od-border)] bg-[var(--od-surface-subtle)] p-4">
                <p className="od-kpi-label">{t.common.owedToUs}</p>
                <p className="od-tabular mt-1.5 text-[20px] font-semibold tracking-tight text-[var(--od-ink)]">
                  {formatMoney(owedToUs)}
                </p>
                <div className="mt-3 space-y-1.5 text-xs text-[var(--od-ink-secondary)]">
                  <div className="flex justify-between">
                    <span>{t.common.shopCustomers}</span>
                    <span className="od-tabular font-medium text-[var(--od-ink)]">
                      {formatMoney(d.outstanding.customers_owe ?? "0")}
                    </span>
                  </div>
                  <div className="flex justify-between">
                    <span>{t.common.serviceClients}</span>
                    <span className="od-tabular font-medium text-[var(--od-ink)]">
                      {formatMoney(d.outstanding.clients_owe ?? "0")}
                    </span>
                  </div>
                </div>
              </div>
              <div className="rounded-[var(--od-radius-md,12px)] border border-[var(--od-border)] bg-[var(--od-surface-subtle)] p-4">
                <p className="od-kpi-label">{t.common.weOwe}</p>
                <p className="od-tabular mt-1.5 text-[20px] font-semibold tracking-tight text-[var(--od-bad)]">
                  {formatMoney(weOwe)}
                </p>
                <div className="mt-3 space-y-1.5 text-xs text-[var(--od-ink-secondary)]">
                  <div className="flex justify-between">
                    <span>{t.common.suppliers}</span>
                    <span className="od-tabular font-medium text-[var(--od-ink)]">
                      {formatMoney(d.outstanding.we_owe_suppliers ?? "0")}
                    </span>
                  </div>
                  <div className="flex justify-between">
                    <span>{t.common.contractors}</span>
                    <span className="od-tabular font-medium text-[var(--od-ink)]">
                      {formatMoney(d.outstanding.we_owe_contractors ?? "0")}
                    </span>
                  </div>
                </div>
              </div>
            </div>
            {d.outstanding.salaries_due !== undefined && d.outstanding.salaries_due !== null && (
              <Link
                to="/payroll"
                className="flex items-center justify-between rounded-[var(--od-radius-md,12px)] border border-[var(--od-border-strong)] px-4 py-2.5 text-[13px] transition-colors hover:bg-[var(--od-surface-subtle)]"
              >
                <span className="font-medium text-[var(--od-ink-secondary)]">
                  {t.dashboard.salariesDue}
                </span>
                <span className="od-tabular font-semibold text-[var(--od-ink)]">
                  {formatMoney(d.outstanding.salaries_due)}
                </span>
              </Link>
            )}
          </SectionCard>
        )}
      </div>

      {/* Row 4: Overdue Tasks, Low Stock, Renewals Due, Approvals Waiting */}
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        {d?.overdue_tasks && (
          <SectionCard
            title={fmt(t.dashboard.overdueTasks, { count: d.overdue_tasks.length })}
            actions={
              <Link
                to="/service-tasks"
                className="text-xs font-medium text-[var(--od-ink-secondary)] hover:text-[var(--od-ink)]"
              >
                {t.common.all}
              </Link>
            }
          >
            {d.overdue_tasks.length === 0 ? (
              <p className="od-caption py-2">{t.dashboard.noOverdueTasks}</p>
            ) : (
              <ul className="divide-y divide-[var(--od-border)] text-xs">
                {d.overdue_tasks.map((item) => (
                  <li key={item.id} className="py-2.5">
                    <p className="font-medium text-[var(--od-ink)]">{item.title}</p>
                    <p className="mt-0.5 font-medium text-[var(--od-bad)]">
                      {fmt(t.dashboard.due2, {
                        due_date: formatDate(item.due_date),
                        value: item.assignee_name ? ` · ${item.assignee_name}` : "",
                      })}
                    </p>
                  </li>
                ))}
              </ul>
            )}
          </SectionCard>
        )}

        {d?.stock && (
          <SectionCard
            title={fmt(t.dashboard.lowStock, { low_stock_count: d.stock.low_stock_count })}
            actions={
              <Link
                to="/products"
                className="text-xs font-medium text-[var(--od-ink-secondary)] hover:text-[var(--od-ink)]"
              >
                {t.common.products}
              </Link>
            }
          >
            {d.stock.value !== undefined && d.stock.value !== null && (
              <Link
                to={
                  can("reports.business.view")
                    ? "/reports?path=/reports/inventory/valuation"
                    : "/products"
                }
                className="flex items-center justify-between rounded-[var(--od-radius-sm,8px)] bg-[var(--od-surface-subtle)] px-3 py-2 text-xs transition-colors hover:bg-[var(--od-border)]"
              >
                <span className="font-medium text-[var(--od-ink-secondary)]">
                  {t.dashboard.stockValue}
                </span>
                <span className="od-tabular font-semibold text-[var(--od-ink)]">
                  {formatMoney(d.stock.value)}
                </span>
              </Link>
            )}
            {(d.stock.items ?? []).length === 0 ? (
              <p className="od-caption py-2">{t.dashboard.stockLevelsAreHealthy}</p>
            ) : (
              <ul className="divide-y divide-[var(--od-border)] text-xs">
                {(d.stock.items ?? []).map((p) => (
                  <li key={p.id} className="flex items-center justify-between gap-2 py-2.5">
                    <span className="truncate font-medium text-[var(--od-ink)]">{p.name}</span>
                    <span className="od-tabular shrink-0 font-semibold text-[var(--od-warning)]">
                      {fmt(t.dashboard.left, { qty_on_hand: toNumber(p.qty_on_hand) })}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </SectionCard>
        )}

        {d?.renewals_due && (
          <SectionCard
            title={fmt(t.dashboard.renewalsDue, { count: d.renewals_due.length })}
            actions={
              <Link
                to="/renewals"
                className="text-xs font-medium text-[var(--od-ink-secondary)] hover:text-[var(--od-ink)]"
              >
                {t.common.all}
              </Link>
            }
          >
            {d.renewals_due.length === 0 ? (
              <p className="od-caption py-2">{t.dashboard.noneDueInTheNext}</p>
            ) : (
              <ul className="divide-y divide-[var(--od-border)] text-xs">
                {d.renewals_due.map((r) => (
                  <li key={r.id} className="py-2.5">
                    <p className="font-medium text-[var(--od-ink)]">{r.item}</p>
                    <p className="mt-0.5 text-[var(--od-ink-muted)]">
                      {r.client_name} ·{" "}
                      <span
                        className={
                          r.days_left <= 7 ? "font-semibold text-[var(--od-bad)]" : undefined
                        }
                      >
                        {r.days_left < 0
                          ? fmt(t.common.dLate, { value: -r.days_left })
                          : fmt(t.dashboard.dLeft, { days_left: r.days_left })}
                      </span>
                    </p>
                  </li>
                ))}
              </ul>
            )}
          </SectionCard>
        )}

        {d?.pending_approvals && (
          <SectionCard
            title={fmt(t.dashboard.approvals, { count: d.pending_approvals.count })}
            actions={
              <Link
                to="/approvals"
                className="text-xs font-medium text-[var(--od-ink-secondary)] hover:text-[var(--od-ink)]"
              >
                {t.common.all}
              </Link>
            }
          >
            {d.pending_approvals.latest.length === 0 ? (
              <p className="od-caption py-2">{t.dashboard.nothingWaitingForApproval}</p>
            ) : (
              <ul className="divide-y divide-[var(--od-border)] text-xs">
                {d.pending_approvals.latest.map((a) => (
                  <li key={a.id} className="space-y-2 py-2.5">
                    <div className="flex items-center justify-between gap-2">
                      <span className="truncate font-medium text-[var(--od-ink)]">
                        {a.summary || label(a.kind)}
                      </span>
                      {a.amount && (
                        <span className="od-tabular shrink-0 font-semibold">
                          {formatMoney(a.amount)}
                        </span>
                      )}
                    </div>
                    {canDecideApprovals && (
                      <div className="flex gap-1.5">
                        <Button
                          size="sm"
                          variant="outline"
                          className="h-6 px-2 text-[11px] text-[var(--od-good)]"
                          disabled={approve.isPending}
                          onClick={() => approve.mutate({ id: a.id })}
                        >
                          <Check size={12} /> {t.common.approve}
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          className="h-6 px-2 text-[11px] text-[var(--od-bad)]"
                          disabled={reject.isPending}
                          onClick={() => reject.mutate({ id: a.id })}
                        >
                          <X size={12} /> {t.common.reject}
                        </Button>
                      </div>
                    )}
                  </li>
                ))}
              </ul>
            )}
          </SectionCard>
        )}
      </div>
    </div>
  );
}
