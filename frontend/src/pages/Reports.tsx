import { useMemo, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { ArrowLeft, BarChart3, Download, Printer } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { BarChartView, LineChartView } from "@/components/shared/Charts";
import { ChartCard } from "@/components/shared/Display";
import { DataGrid, Field, NativeSelect, PageHeader } from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { errorMessage, useGet } from "@/hooks/useApi";
import { download } from "@/lib/api";
import { formatMoney, toNumber } from "@/lib/formatters";

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

const catalogue: Catalogue[] = [
  {
    group: "Sales",
    label: "Sales summary",
    path: "/reports/sales/summary",
    permission: OPS,
    dated: true,
    amountKeys: ["net_sales", "gross_sales", "total"],
    chartType: "line",
  },
  {
    group: "Sales",
    label: "By product",
    path: "/reports/sales/by-product",
    permission: OPS,
    dated: true,
    amountKeys: ["net_sales", "gross_sales", "total", "qty"],
  },
  {
    group: "Sales",
    label: "By category",
    path: "/reports/sales/by-category",
    permission: OPS,
    dated: true,
    amountKeys: ["net_sales", "gross_sales", "total"],
  },
  {
    group: "Sales",
    label: "By cashier",
    path: "/reports/sales/by-cashier",
    permission: OPS,
    dated: true,
    amountKeys: ["net_sales", "gross_sales", "total"],
  },
  {
    group: "Sales",
    label: "By customer",
    path: "/reports/sales/by-customer",
    permission: OPS,
    dated: true,
    amountKeys: ["net_sales", "gross_sales", "total"],
  },
  {
    group: "Sales",
    label: "By payment method",
    path: "/reports/sales/by-payment-method",
    permission: OPS,
    dated: true,
    amountKeys: ["net", "amount_in"],
  },
  {
    group: "Profit & Loss",
    label: "Profit and loss",
    path: "/reports/profit-loss",
    permission: BIZ,
    dated: true,
    amountKeys: ["amount"],
    labelKey: "name",
  },
  {
    group: "Cash Flow & Finance",
    label: "Cash flow",
    path: "/reports/cash-flow",
    permission: BIZ,
    dated: true,
    amountKeys: ["closing", "money_in", "net"],
    chartType: "line",
  },
  {
    group: "Cash Flow & Finance",
    label: "Balance sheet",
    path: "/reports/balance-sheet",
    permission: BIZ,
    amountKeys: ["amount"],
    labelKey: "name",
  },
  {
    group: "Cash Flow & Finance",
    label: "Tax",
    path: "/reports/tax",
    permission: BIZ,
    dated: true,
    amountKeys: ["net_tax", "output_tax", "taxable_sales"],
    chartType: "line",
  },
  {
    group: "Projects & Design",
    label: "Project profit",
    path: "/reports/projects",
    permission: OPS,
    dated: true,
    amountKeys: ["profit", "total_price", "paid"],
    labelKey: "client_name",
  },
  {
    group: "Projects & Design",
    label: "Design jobs",
    path: "/reports/design-jobs",
    permission: OPS,
    dated: true,
    amountKeys: ["profit", "income", "jobs"],
    labelKey: "designer",
  },
  {
    group: "Expenses",
    label: "Expenses",
    path: "/reports/expenses",
    permission: BIZ,
    dated: true,
    amountKeys: ["amount", "paid"],
  },
  {
    group: "Stock",
    label: "Stock on hand",
    path: "/reports/inventory/on-hand",
    permission: OPS,
    amountKeys: ["value", "qty_on_hand"],
    labelKey: "product",
  },
  {
    group: "Stock",
    label: "Stock valuation",
    path: "/reports/inventory/valuation",
    permission: BIZ,
    amountKeys: ["value", "qty_on_hand"],
  },
  {
    group: "Stock",
    label: "Movements",
    path: "/reports/inventory/movements",
    permission: OPS,
    dated: true,
    amountKeys: ["value", "qty"],
    chartType: "line",
  },
  {
    group: "Stock",
    label: "Low stock",
    path: "/reports/inventory/low-stock",
    permission: OPS,
    amountKeys: ["suggested_qty", "qty_on_hand"],
    labelKey: "product",
  },
  {
    group: "Stock",
    label: "Dead stock",
    path: "/reports/inventory/dead-stock",
    permission: OPS,
    amountKeys: ["value", "qty_on_hand"],
    labelKey: "product",
  },
  {
    group: "Stock",
    label: "Losses and adjustments",
    path: "/reports/inventory/losses",
    permission: OPS,
    dated: true,
    amountKeys: ["value", "qty"],
    labelKey: "product",
  },
  {
    group: "Stock",
    label: "Purchases",
    path: "/reports/purchases",
    permission: OPS,
    dated: true,
    amountKeys: ["total", "amount", "qty"],
  },
  {
    group: "Debts",
    label: "Customer debts",
    path: "/reports/customer-debt",
    permission: OPS,
    amountKeys: ["balance", "overdue"],
    labelKey: "customer",
  },
  {
    group: "Debts",
    label: "Supplier debts",
    path: "/reports/supplier-debt",
    permission: OPS,
    amountKeys: ["balance", "overdue"],
    labelKey: "supplier",
  },
  {
    group: "Staff & Payroll",
    label: "Payroll summary",
    path: "/reports/payroll/summary",
    permission: BIZ,
    dated: true,
    amountKeys: ["net", "gross", "paid"],
    labelKey: "run",
  },
  {
    group: "Staff & Payroll",
    label: "Deductions",
    path: "/reports/payroll/deductions",
    permission: BIZ,
    dated: true,
    amountKeys: ["amount"],
    labelKey: "label",
  },
  {
    group: "Staff & Payroll",
    label: "Staff advances & loans",
    path: "/reports/payroll/loans",
    permission: BIZ,
    amountKeys: ["balance", "principal"],
    labelKey: "employee",
  },
  {
    group: "Staff & Payroll",
    label: "Staff performance",
    path: "/reports/staff-performance",
    permission: BIZ,
    dated: true,
    amountKeys: ["net_sales", "commission", "sales_count"],
    labelKey: "employee",
  },
  {
    group: "Staff & Payroll",
    label: "Location comparison",
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
  const { can, canAny } = useAuth();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const available = useMemo(() => catalogue.filter((r) => canAny(...r.permission)), [canAny]);
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

  const columns = useMemo(() => report.data?.columns ?? [], [report.data?.columns]);
  const rows = useMemo(() => report.data?.rows ?? [], [report.data?.rows]);

  const chartConfig = useMemo(() => {
    if (!current || !report.data) {
      return { kind: "bar" as const, data: [] as { name: string; value: number }[], subtitle: "" };
    }
    // Profit & Loss: chart Business A, Business B, Operating expenses, and Net profit (plus previous when comparing).
    if (isPL && report.data.businesses) {
      const b = report.data.businesses;
      const items = [
        { name: "Shop income", value: toNumber(b.shop.income) },
        { name: "Services income", value: toNumber(b.services.income) },
        { name: "Operating exp.", value: toNumber(b.shared.operating_expenses) },
        { name: "Net profit", value: toNumber(b.total.net_profit) },
      ];
      if (compare && report.data.previous?.net_profit !== undefined) {
        items.push({
          name: "Prev net profit",
          value: toNumber(report.data.previous.net_profit),
        });
      }
      const hasAny = items.some((i) => i.value !== 0);
      return {
        kind: "bar" as const,
        data: hasAny ? items : [],
        subtitle:
          compare && report.data.previous?.net_profit !== undefined
            ? `Previous net profit: ${formatMoney(report.data.previous.net_profit)}`
            : "Shop, services, expenses and net profit",
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
      subtitle: rows.length > 10 ? `Top 10 by ${amountCol.label}` : `By ${amountCol.label}`,
    };
  }, [columns, compare, current, isPL, report.data, rows]);

  if (available.length === 0) {
    return <PageHeader title="Reports" description="You do not have access to any reports." />;
  }

  // SRS Section 10: Reports Home with grouped cards.
  if (!current) {
    return (
      <>
        <PageHeader
          title="Reports"
          description="Choose a report category to analyze sales, profit & loss, projects, stock, debts and staff."
        />
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {groups.map((g) => (
            <section key={g} className="rounded-2xl border bg-card p-5">
              <h2 className="mb-3 flex items-center gap-2 font-semibold">
                <BarChart3 size={16} className="text-primary" /> {g}
              </h2>
              <ul className="space-y-1">
                {available
                  .filter((r) => r.group === g)
                  .map((r) => (
                    <li key={r.path}>
                      <button
                        type="button"
                        onClick={() => openReport(r.path)}
                        className="w-full rounded-lg px-3 py-2 text-left text-sm transition hover:bg-muted hover:text-primary"
                      >
                        {r.label}
                      </button>
                    </li>
                  ))}
              </ul>
            </section>
          ))}
        </div>
      </>
    );
  }

  return (
    <div className="space-y-5">
      <PageHeader
        title={current.label}
        description={
          report.data?.subtitle ?? "Filter, review, print or export to CSV, Excel or PDF."
        }
        actions={
          <Button variant="outline" size="sm" onClick={closeReport}>
            <ArrowLeft size={14} /> All reports
          </Button>
        }
      />

      <div className="flex flex-wrap items-end gap-3">
        <Field label="Report" className="min-w-[14rem]">
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
            <Field label="From">
              <Input type="date" value={from} onChange={(e) => setFrom(e.target.value)} />
            </Field>
            <Field label="To">
              <Input type="date" value={to} onChange={(e) => setTo(e.target.value)} />
            </Field>
          </>
        )}
        {isPL && (
          <>
            <Field label="Business">
              <NativeSelect
                value={business}
                onChange={(e) => setBusiness(e.target.value as typeof business)}
              >
                <option value="all">Both businesses</option>
                <option value="shop">Shop (Business A)</option>
                <option value="services">Services (Business B)</option>
              </NativeSelect>
            </Field>
            <label className="flex h-9 items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={compare}
                onChange={(e) => setCompare(e.target.checked)}
              />
              Compare previous period
            </label>
          </>
        )}
        <div className="ml-auto flex gap-2">
          <Button variant="outline" size="sm" onClick={() => window.print()}>
            <Printer size={14} /> Print
          </Button>
          {can("reports.export") &&
            ["csv", "xlsx", "pdf"].map((f) => (
              <Button key={f} variant="outline" size="sm" onClick={() => save(f)}>
                <Download size={14} /> {f.toUpperCase()}
              </Button>
            ))}
        </div>
      </div>

      {/* SRS Section 10: Business A vs Business B vs Total P&L summary cards */}
      {isPL && report.data?.businesses && (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
          <div className="rounded-2xl border bg-card p-4">
            <p className="text-xs uppercase tracking-wide text-muted-foreground">
              Shop (Business A)
            </p>
            <p className="mt-1 text-xl font-bold tabular-nums">
              {formatMoney(report.data.businesses.shop.profit)}
            </p>
            <p className="mt-1 text-xs text-muted-foreground">
              Sales {formatMoney(report.data.businesses.shop.income)} · COGS{" "}
              {formatMoney(report.data.businesses.shop.cost_of_goods)}
            </p>
          </div>
          <div className="rounded-2xl border bg-card p-4">
            <p className="text-xs uppercase tracking-wide text-muted-foreground">
              Services (Business B)
            </p>
            <p className="mt-1 text-xl font-bold tabular-nums">
              {formatMoney(report.data.businesses.services.profit)}
            </p>
            <p className="mt-1 text-xs text-muted-foreground">
              Income {formatMoney(report.data.businesses.services.income)} · Costs{" "}
              {formatMoney(report.data.businesses.services.direct_costs)}
            </p>
          </div>
          <div className="rounded-2xl border bg-card p-4">
            <p className="text-xs uppercase tracking-wide text-muted-foreground">
              Operating expenses
            </p>
            <p className="mt-1 text-xl font-bold tabular-nums">
              {formatMoney(report.data.businesses.shared.operating_expenses)}
            </p>
            <p className="mt-1 text-xs text-muted-foreground">
              Other income {formatMoney(report.data.businesses.shared.other_income)}
            </p>
          </div>
          <div className="rounded-2xl border bg-card p-4">
            <p className="text-xs uppercase tracking-wide text-muted-foreground">Net profit</p>
            <p className="mt-1 text-xl font-bold tabular-nums">
              {formatMoney(report.data.businesses.total.net_profit)}
            </p>
            {report.data.previous && (
              <p className="mt-1 text-xs text-muted-foreground">
                Previous: {formatMoney(report.data.previous.net_profit)}
                {report.data.change?.net_profit_pct
                  ? ` (${toNumber(report.data.change.net_profit_pct) > 0 ? "+" : ""}${toNumber(report.data.change.net_profit_pct).toFixed(1)}%)`
                  : ""}
              </p>
            )}
          </div>
        </div>
      )}

      {!report.isLoading &&
        (chartConfig.data.length > 0 ? (
          <ChartCard title={`${current.label} chart`} description={chartConfig.subtitle}>
            {chartConfig.kind === "line" ? (
              <LineChartView data={chartConfig.data} label={current.label} />
            ) : (
              <BarChartView data={chartConfig.data} label={current.label} />
            )}
          </ChartCard>
        ) : (
          <p className="text-sm text-muted-foreground">No amounts to chart for this period.</p>
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
        empty="No data for this report and period"
        columns={columns.map((c, i) => ({
          header: c.label,
          align: i === 0 ? undefined : ("right" as const),
          cell: (r: Record<string, unknown>) => {
            const value = i === 0 && r.__total ? (r.label ?? "Total") : cell(c.key, r[c.key]);
            return r.__total ? <strong>{value as string}</strong> : (value as string);
          },
        }))}
      />
    </div>
  );
}
