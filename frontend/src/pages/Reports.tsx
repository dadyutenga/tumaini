import { useMemo, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { ArrowLeft, BarChart3, ChevronRight, Download, Printer } from "lucide-react";
import { toast } from "sonner";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { BarChartView, LineChartView } from "@/components/shared/Charts";
import { ChartCard, StatCard } from "@/components/shared/Display";
import { DataGrid, Field, NativeSelect, PageHeader, SegmentedChips } from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { errorMessage, useGet } from "@/hooks/useApi";
import { download } from "@/lib/api";
import { formatMoney, toNumber } from "@/lib/formatters";
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";
import type { Translations } from "@/features/i18n/i18n";

interface Catalogue {
  path: string;
  label: string;
  group: string;
  permission: ("reports.operations.view" | "reports.business.view")[];
  dated?: boolean;
  amountKeys: string[];
  labelKey?: string;
  chartType?: "bar" | "line";
}

const OPS: Catalogue["permission"] = ["reports.operations.view", "reports.business.view"];
const BIZ: Catalogue["permission"] = ["reports.business.view"];

const catalogueFor = (t: Translations): Catalogue[] => [
  {
    group: t.common.sales,
    label: t.reports.salesSummary,
    path: "/reports/sales/summary",
    permission: OPS,
    dated: true,
    amountKeys: ["net_sales", "gross_sales", "total"],
    chartType: "line",
  },
  {
    group: t.common.sales,
    label: t.reports.byProduct,
    path: "/reports/sales/by-product",
    permission: OPS,
    dated: true,
    amountKeys: ["net_sales", "gross_sales", "total", "qty"],
  },
  {
    group: t.common.sales,
    label: t.reports.byCategory,
    path: "/reports/sales/by-category",
    permission: OPS,
    dated: true,
    amountKeys: ["net_sales", "gross_sales", "total"],
  },
  {
    group: t.common.sales,
    label: t.reports.byCashier,
    path: "/reports/sales/by-cashier",
    permission: OPS,
    dated: true,
    amountKeys: ["net_sales", "gross_sales", "total"],
  },
  {
    group: t.common.sales,
    label: t.reports.byCustomer,
    path: "/reports/sales/by-customer",
    permission: OPS,
    dated: true,
    amountKeys: ["net_sales", "gross_sales", "total"],
  },
  {
    group: t.common.sales,
    label: t.reports.byPaymentMethod,
    path: "/reports/sales/by-payment-method",
    permission: OPS,
    dated: true,
    amountKeys: ["net", "amount_in"],
  },
  {
    group: t.reports.groupProfitLoss,
    label: t.reports.profitAndLoss,
    path: "/reports/profit-loss",
    permission: BIZ,
    dated: true,
    amountKeys: ["amount"],
    labelKey: "name",
  },
  {
    group: t.reports.cashFlowFinance,
    label: t.reports.cashFlow,
    path: "/reports/cash-flow",
    permission: BIZ,
    dated: true,
    amountKeys: ["closing", "money_in", "net"],
    chartType: "line",
  },
  {
    group: t.reports.cashFlowFinance,
    label: t.reports.balanceSheet,
    path: "/reports/balance-sheet",
    permission: BIZ,
    amountKeys: ["amount"],
    labelKey: "name",
  },
  {
    group: t.reports.cashFlowFinance,
    label: t.common.tax,
    path: "/reports/tax",
    permission: BIZ,
    dated: true,
    amountKeys: ["net_tax", "output_tax", "taxable_sales"],
    chartType: "line",
  },
  {
    group: t.reports.groupProjects,
    label: t.reports.projectProfit,
    path: "/reports/projects",
    permission: OPS,
    dated: true,
    amountKeys: ["profit", "total_price", "paid"],
    labelKey: "client_name",
  },
  {
    group: t.reports.groupProjects,
    label: t.common.designJobs,
    path: "/reports/design-jobs",
    permission: OPS,
    dated: true,
    amountKeys: ["profit", "income", "jobs"],
    labelKey: "designer",
  },
  {
    group: t.common.expenses,
    label: t.common.expenses,
    path: "/reports/expenses",
    permission: BIZ,
    dated: true,
    amountKeys: ["amount", "paid"],
  },
  {
    group: t.common.stock,
    label: t.reports.stockOnHand,
    path: "/reports/inventory/on-hand",
    permission: OPS,
    amountKeys: ["value", "qty_on_hand"],
    labelKey: "product",
  },
  {
    group: t.common.stock,
    label: t.reports.stockValuation,
    path: "/reports/inventory/valuation",
    permission: BIZ,
    amountKeys: ["value", "qty_on_hand"],
  },
  {
    group: t.common.stock,
    label: t.common.movements,
    path: "/reports/inventory/movements",
    permission: OPS,
    dated: true,
    amountKeys: ["value", "qty"],
    chartType: "line",
  },
  {
    group: t.common.stock,
    label: t.common.lowStock,
    path: "/reports/inventory/low-stock",
    permission: OPS,
    amountKeys: ["suggested_qty", "qty_on_hand"],
    labelKey: "product",
  },
  {
    group: t.common.stock,
    label: t.reports.deadStock,
    path: "/reports/inventory/dead-stock",
    permission: OPS,
    amountKeys: ["value", "qty_on_hand"],
    labelKey: "product",
  },
  {
    group: t.common.stock,
    label: t.reports.lossesAndAdjustments,
    path: "/reports/inventory/losses",
    permission: OPS,
    dated: true,
    amountKeys: ["value", "qty"],
    labelKey: "product",
  },
  {
    group: t.common.stock,
    label: t.reports.purchases,
    path: "/reports/purchases",
    permission: OPS,
    dated: true,
    amountKeys: ["total", "amount", "qty"],
  },
  {
    group: t.common.debts,
    label: t.reports.customerDebts,
    path: "/reports/customer-debt",
    permission: OPS,
    amountKeys: ["balance", "overdue"],
    labelKey: "customer",
  },
  {
    group: t.common.debts,
    label: t.reports.supplierDebts,
    path: "/reports/supplier-debt",
    permission: OPS,
    amountKeys: ["balance", "overdue"],
    labelKey: "supplier",
  },
  {
    group: t.reports.groupStaff,
    label: t.reports.payrollSummary,
    path: "/reports/payroll/summary",
    permission: BIZ,
    dated: true,
    amountKeys: ["net", "gross", "paid"],
    labelKey: "run",
  },
  {
    group: t.reports.groupStaff,
    label: t.common.deductions,
    path: "/reports/payroll/deductions",
    permission: BIZ,
    dated: true,
    amountKeys: ["amount"],
    labelKey: "label",
  },
  {
    group: t.reports.groupStaff,
    label: t.reports.staffAdvancesLoans,
    path: "/reports/payroll/loans",
    permission: BIZ,
    amountKeys: ["balance", "principal"],
    labelKey: "employee",
  },
  {
    group: t.reports.groupStaff,
    label: t.reports.staffPerformance,
    path: "/reports/staff-performance",
    permission: BIZ,
    dated: true,
    amountKeys: ["net_sales", "commission", "sales_count"],
    labelKey: "employee",
  },
  {
    group: t.reports.groupStaff,
    label: t.reports.locationComparison,
    path: "/reports/branches",
    permission: BIZ,
    dated: true,
    amountKeys: ["net_profit", "net_sales", "sales", "total"],
  },
];

interface Table {
  title: string;
  subtitle?: string;
  columns: { key: string; label: string }[];
  rows: Record<string, unknown>[];
  totals?: Record<string, unknown>;
  businesses?: {
    shop: { income: string; cost_of_goods: string; profit: string };
    services: { income: string; direct_costs: string; profit: string };
    shared: { other_income: string; operating_expenses: string };
    total: { income: string; expenses: string; net_profit: string };
  };
  previous?: {
    net_profit?: string;
    income?: string;
    expenses?: string;
    from?: string;
    to?: string;
  };
  change?: { net_profit?: string; net_profit_pct?: string | null };
}

const moneyKey =
  /(sales|amount|total|cost|profit|balance|value|price|tax|discount|returns|net|gross|due|paid|expense|current|overdue|d\d+_\d+|over_\d+)/i;
const countKey = /(count|invoices|qty|quantity|days|level|units)/i;

function cell(key: string, value: unknown) {
  if (value === null || value === undefined || value === "") return "-";
  const text = String(value);
  if (typeof value === "string" && /^-?\d+(\.\d+)?$/.test(text)) {
    if (countKey.test(key)) return String(toNumber(text));
    if (moneyKey.test(key) && !/margin|pct|percent/i.test(key)) return formatMoney(text);
  }
  return text;
}

export default function Reports() {
  const { t, locale } = useI18n();
  const { can, canAny } = useAuth();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const available = useMemo(
    () => catalogueFor(t).filter((r) => canAny(...r.permission)),
    [canAny, t],
  );
  const selectedPath = params.get("path") ?? "";
  const current = available.find((r) => r.path === selectedPath) ?? null;

  const month = new Date();
  const first = new Date(month.getFullYear(), month.getMonth(), 1).toLocaleDateString("en-CA");
  const [from, setFrom] = useState(first);
  const [to, setTo] = useState(new Date().toLocaleDateString("en-CA"));
  const [business, setBusiness] = useState<"all" | "shop" | "services">("all");
  const [compare, setCompare] = useState(true);

  const isPL = current?.path === "/reports/profit-loss";
  const query = current?.dated
    ? {
        from,
        to,
        ...(isPL ? { business, compare: compare ? "previous" : undefined } : {}),
      }
    : undefined;

  const report = useGet<Table>(current?.path ?? "", query, { enabled: !!current });
  const groups = [...new Set(available.map((r) => r.group))];

  const openReport = (p: string) => setParams({ path: p });
  const closeReport = () => setParams({});

  const save = (format: string) =>
    download(current!.path, { ...query, format }, `${current!.label}.${format}`).catch((e) =>
      toast.error(errorMessage(e)),
    );

  // The API names its columns in English; show our wording for the ones we know in other languages.
  const columns = useMemo(() => {
    const names = t.reports.columns as Record<string, string>;
    return (report.data?.columns ?? []).map((c) => ({
      ...c,
      label:
        locale !== "en" && Object.prototype.hasOwnProperty.call(names, c.key)
          ? names[c.key]
          : c.label,
    }));
  }, [report.data?.columns, t, locale]);
  const rows = useMemo(() => report.data?.rows ?? [], [report.data?.rows]);

  const chartConfig = useMemo(() => {
    if (!current || !report.data) {
      return { kind: "bar" as const, data: [] as { name: string; value: number }[], subtitle: "" };
    }
    // Profit & Loss: chart Business A, Business B, Operating expenses, and Net profit (plus previous when comparing).
    if (isPL && report.data.businesses) {
      const b = report.data.businesses;
      const items = [
        { name: t.reports.shopIncome, value: toNumber(b.shop.income) },
        { name: t.reports.servicesIncome, value: toNumber(b.services.income) },
        { name: t.reports.operatingExp, value: toNumber(b.shared.operating_expenses) },
        { name: t.common.netProfit, value: toNumber(b.total.net_profit) },
      ];
      if (compare && report.data.previous?.net_profit !== undefined) {
        items.push({
          name: t.reports.prevNetProfit,
          value: toNumber(report.data.previous.net_profit),
        });
      }
      const hasAny = items.some((i) => i.value !== 0);
      return {
        kind: "bar" as const,
        data: hasAny ? items : [],
        subtitle:
          compare && report.data.previous?.net_profit !== undefined
            ? fmt(t.reports.previousNetProfit, {
                net_profit: formatMoney(report.data.previous.net_profit),
              })
            : t.reports.shopServicesExpensesAndNet,
      };
    }

    if (columns.length < 2 || rows.length === 0) {
      return { kind: "bar" as const, data: [] as { name: string; value: number }[], subtitle: "" };
    }

    const colKeys = new Set(columns.map((c) => c.key));
    const labelCol =
      current.labelKey && colKeys.has(current.labelKey) ? current.labelKey : columns[0].key;

    // Pick the first explicit amount key still present in columns (respecting backend stripCost).
    let amountCol = columns.find((c) => current.amountKeys.includes(c.key) && c.key !== labelCol);
    if (!amountCol) {
      // Fallback: prefer money-like numeric columns over id/count columns.
      const numericCols = columns.filter(
        (c) =>
          c.key !== labelCol &&
          !/(_id|^id$)/i.test(c.key) &&
          rows.some((r) => {
            const v = r[c.key];
            return typeof v === "number" || (typeof v === "string" && /^-?\d+(\.\d+)?$/.test(v));
          }),
      );
      amountCol =
        numericCols.find((c) => moneyKey.test(c.key) && !countKey.test(c.key)) ?? numericCols[0];
    }
    if (!amountCol) {
      return { kind: "bar" as const, data: [] as { name: string; value: number }[], subtitle: "" };
    }

    const looksLikeDates =
      current.chartType === "line" ||
      /^(date|day|month|period)/i.test(labelCol) ||
      rows.every((r) => /^\d{4}-\d{2}/.test(String(r[labelCol] ?? "")));

    const mapped = rows.map((r) => {
      const rawLabel = String(r[labelCol] ?? "-");
      return {
        name: looksLikeDates ? rawLabel.slice(0, 10) : rawLabel.slice(0, 22),
        value: toNumber(r[amountCol.key]),
      };
    });

    if (mapped.every((pt) => pt.value === 0)) {
      return { kind: "bar" as const, data: [] as { name: string; value: number }[], subtitle: "" };
    }

    if (looksLikeDates) {
      return {
        kind: "line" as const,
        data: mapped,
        subtitle: amountCol.label,
      };
    }

    const top10 = [...mapped].sort((a, b) => Math.abs(b.value) - Math.abs(a.value)).slice(0, 10);
    return {
      kind: "bar" as const,
      data: top10,
      subtitle:
        rows.length > 10
          ? fmt(t.reports.top10By, { label: amountCol.label })
          : fmt(t.reports.by, { label: amountCol.label }),
    };
  }, [columns, compare, current, isPL, report.data, rows, t]);

  if (available.length === 0) {
    return <PageHeader title={t.common.reports} description={t.reports.youDoNotHaveAccess} />;
  }

  // SRS Section 10: Reports Home with grouped cards.
  if (!current) {
    return (
      <div className="space-y-6">
        <PageHeader title={t.common.reports} description={t.reports.chooseAReportCategoryTo} />
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {groups.map((g) => {
            const items = available.filter((r) => r.group === g);
            return (
              <section key={g} className="od-card p-5">
                <div className="mb-3 flex items-center justify-between gap-2">
                  <h2 className="od-card-title flex items-center gap-2 text-[var(--od-ink)]">
                    <span className="flex h-7 w-7 items-center justify-center rounded-lg border border-[var(--od-border)] bg-[var(--od-surface-subtle)] text-[var(--od-ink-secondary)]">
                      <BarChart3 size={14} />
                    </span>
                    {g}
                  </h2>
                  <Badge variant="secondary" className="od-tabular text-[11px]">
                    {items.length}
                  </Badge>
                </div>
                <ul className="space-y-0.5">
                  {items.map((r) => (
                    <li key={r.path}>
                      <button
                        type="button"
                        onClick={() => openReport(r.path)}
                        className="group flex w-full items-center justify-between rounded-lg px-3 py-2 text-left text-sm text-[var(--od-ink-secondary)] transition hover:bg-[var(--od-surface-subtle)] hover:text-[var(--od-ink)]"
                      >
                        <span>{r.label}</span>
                        <ChevronRight
                          size={14}
                          className="text-[var(--od-ink-muted)] opacity-0 transition group-hover:opacity-100"
                        />
                      </button>
                    </li>
                  ))}
                </ul>
              </section>
            );
          })}
        </div>
      </div>
    );
  }

  const netPct = report.data?.change?.net_profit_pct
    ? toNumber(report.data.change.net_profit_pct)
    : null;

  return (
    <div className="space-y-6">
      <PageHeader
        title={current.label}
        description={report.data?.subtitle ?? t.reports.filterReviewPrintOrExport}
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <Button variant="outline" size="sm" onClick={closeReport}>
              <ArrowLeft size={14} /> {t.reports.allReports}
            </Button>
            <Button variant="outline" size="sm" onClick={() => window.print()}>
              <Printer size={14} /> {t.common.print}
            </Button>
            {can("reports.export") &&
              ["csv", "xlsx", "pdf"].map((f) => (
                <Button key={f} variant="outline" size="sm" onClick={() => save(f)}>
                  <Download size={14} /> {f.toUpperCase()}
                </Button>
              ))}
          </div>
        }
      />

      <div className="od-card flex flex-wrap items-end gap-3 p-4">
        <Field label={t.reports.report} className="min-w-[14rem]">
          <NativeSelect value={current.path} onChange={(e) => openReport(e.target.value)}>
            {groups.map((g) => (
              <optgroup key={g} label={g}>
                {available
                  .filter((r) => r.group === g)
                  .map((r) => (
                    <option key={r.path} value={r.path}>
                      {r.label}
                    </option>
                  ))}
              </optgroup>
            ))}
          </NativeSelect>
        </Field>
        {current.dated && (
          <>
            <Field label={t.common.from}>
              <Input type="date" value={from} onChange={(e) => setFrom(e.target.value)} />
            </Field>
            <Field label={t.common.to}>
              <Input type="date" value={to} onChange={(e) => setTo(e.target.value)} />
            </Field>
          </>
        )}
        {isPL && (
          <>
            <div className="flex flex-col gap-1.5">
              <span className="text-xs font-medium text-[var(--od-ink-secondary)]">
                {t.common.business}
              </span>
              <SegmentedChips
                ariaLabel={t.reports.filterBusiness}
                value={business}
                onChange={(v) => setBusiness(v as typeof business)}
                options={[
                  { value: "all", label: t.reports.both },
                  { value: "shop", label: t.reports.shopA },
                  { value: "services", label: t.reports.servicesB },
                ]}
              />
            </div>
            <label className="flex h-9 cursor-pointer items-center gap-2 rounded-lg border border-[var(--od-border)] bg-[var(--od-surface-subtle)] px-3 text-sm text-[var(--od-ink)]">
              <input
                type="checkbox"
                checked={compare}
                onChange={(e) => setCompare(e.target.checked)}
              />
              {t.reports.comparePreviousPeriod}
            </label>
          </>
        )}
      </div>

      {/* SRS Section 10: Business A vs Business B vs Total P&L summary cards */}
      {isPL && report.data?.businesses && (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
          <StatCard
            label={t.reports.shopBusinessA}
            value={formatMoney(report.data.businesses.shop.profit)}
            trend={fmt(t.reports.salesCogs, {
              income: formatMoney(report.data.businesses.shop.income),
              cost_of_goods: formatMoney(report.data.businesses.shop.cost_of_goods),
            })}
            tone={toNumber(report.data.businesses.shop.profit) >= 0 ? "good" : "bad"}
          />
          <StatCard
            label={t.reports.servicesBusinessB}
            value={formatMoney(report.data.businesses.services.profit)}
            trend={fmt(t.reports.incomeCosts, {
              income: formatMoney(report.data.businesses.services.income),
              direct_costs: formatMoney(report.data.businesses.services.direct_costs),
            })}
            tone={toNumber(report.data.businesses.services.profit) >= 0 ? "good" : "bad"}
          />
          <StatCard
            label={t.reports.operatingExpenses}
            value={formatMoney(report.data.businesses.shared.operating_expenses)}
            trend={fmt(t.reports.otherIncome, {
              other_income: formatMoney(report.data.businesses.shared.other_income),
            })}
          />
          <StatCard
            label={t.common.netProfit}
            value={formatMoney(report.data.businesses.total.net_profit)}
            trend={
              report.data.previous
                ? fmt(t.reports.previous, {
                    net_profit: formatMoney(report.data.previous.net_profit),
                    value:
                      netPct !== null ? ` (${netPct > 0 ? "+" : ""}${netPct.toFixed(1)}%)` : "",
                  })
                : undefined
            }
            tone={
              netPct !== null
                ? netPct >= 0
                  ? "good"
                  : "bad"
                : toNumber(report.data.businesses.total.net_profit) >= 0
                  ? "good"
                  : "bad"
            }
          />
        </div>
      )}

      {!report.isLoading &&
        (chartConfig.data.length > 0 ? (
          <ChartCard
            title={fmt(t.reports.chart, { label: current.label })}
            description={chartConfig.subtitle}
          >
            {chartConfig.kind === "line" ? (
              <LineChartView data={chartConfig.data} label={current.label} />
            ) : (
              <BarChartView data={chartConfig.data} label={current.label} />
            )}
          </ChartCard>
        ) : (
          <div className="od-card p-4 text-sm text-[var(--od-ink-muted)]">
            {t.reports.noAmountsToChartFor}
          </div>
        ))}

      <DataGrid
        rows={
          rows.length && report.data?.totals
            ? [...rows, { ...report.data.totals, __total: true }]
            : rows
        }
        loading={report.isLoading}
        rowKey={(r) => String(r.id ?? r.product_id ?? JSON.stringify(r))}
        onRowClick={
          current.path === "/reports/projects"
            ? (r) => {
                if (!r.__total && typeof r.id === "string") navigate(`/projects/${r.id}`);
              }
            : undefined
        }
        empty={t.reports.noDataForThisReport}
        columns={columns.map((c, i) => ({
          header: c.label,
          align: i === 0 ? undefined : ("right" as const),
          cell: (r: Record<string, unknown>) => {
            const value =
              i === 0 && r.__total ? (r.label ?? t.common.total) : cell(c.key, r[c.key]);
            return r.__total ? (
              <strong className="od-tabular font-semibold text-[var(--od-ink)]">
                {value as string}
              </strong>
            ) : (
              <span className={i === 0 ? "text-[var(--od-ink)]" : "od-tabular"}>
                {value as string}
              </span>
            );
          },
        }))}
      />
    </div>
  );
}
