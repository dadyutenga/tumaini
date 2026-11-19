import { useMemo, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { ArrowLeft, BarChart3, Download, Printer } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { BarChartView } from "@/components/shared/Charts";
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
  permission: string;
  dated?: boolean;
}

const catalogue: Catalogue[] = [
  { group: "Sales", label: "Sales summary", path: "/reports/sales/summary", permission: "reports.sales", dated: true },
  { group: "Sales", label: "By product", path: "/reports/sales/by-product", permission: "reports.sales", dated: true },
  { group: "Sales", label: "By category", path: "/reports/sales/by-category", permission: "reports.sales", dated: true },
  { group: "Sales", label: "By cashier", path: "/reports/sales/by-cashier", permission: "reports.sales", dated: true },
  { group: "Sales", label: "By customer", path: "/reports/sales/by-customer", permission: "reports.sales", dated: true },
  { group: "Sales", label: "By payment method", path: "/reports/sales/by-payment-method", permission: "reports.sales", dated: true },
  { group: "Sales", label: "Till sessions", path: "/reports/tills", permission: "reports.sales", dated: true },
  { group: "Profit & Loss", label: "Profit and loss", path: "/reports/profit-loss", permission: "reports.financial", dated: true },
  { group: "Cash Flow & Finance", label: "Cash flow", path: "/reports/cash-flow", permission: "reports.financial", dated: true },
  { group: "Cash Flow & Finance", label: "Balance sheet", path: "/reports/balance-sheet", permission: "reports.financial" },
  { group: "Cash Flow & Finance", label: "Tax", path: "/reports/tax", permission: "reports.financial", dated: true },
  { group: "Projects & Design", label: "Project profit", path: "/reports/projects", permission: "reports.services", dated: true },
  { group: "Projects & Design", label: "Design jobs", path: "/reports/design-jobs", permission: "reports.services", dated: true },
  { group: "Expenses", label: "Expenses", path: "/reports/expenses", permission: "reports.expenses", dated: true },
  { group: "Stock", label: "Stock on hand", path: "/reports/inventory/on-hand", permission: "reports.inventory" },
  { group: "Stock", label: "Stock valuation", path: "/reports/inventory/valuation", permission: "reports.inventory" },
  { group: "Stock", label: "Movements", path: "/reports/inventory/movements", permission: "reports.inventory", dated: true },
  { group: "Stock", label: "Low stock", path: "/reports/inventory/low-stock", permission: "reports.inventory" },
  { group: "Stock", label: "Dead stock", path: "/reports/inventory/dead-stock", permission: "reports.inventory" },
  { group: "Stock", label: "Losses and adjustments", path: "/reports/inventory/losses", permission: "reports.inventory", dated: true },
  { group: "Stock", label: "Purchases", path: "/reports/purchases", permission: "reports.purchases", dated: true },
  { group: "Debts", label: "Customer debts", path: "/reports/customer-debt", permission: "reports.debts" },
  { group: "Debts", label: "Supplier debts", path: "/reports/supplier-debt", permission: "reports.debts" },
  { group: "Staff & Payroll", label: "Payroll summary", path: "/reports/payroll/summary", permission: "reports.payroll", dated: true },
  { group: "Staff & Payroll", label: "By department", path: "/reports/payroll/by-department", permission: "reports.payroll", dated: true },
  { group: "Staff & Payroll", label: "Deductions", path: "/reports/payroll/deductions", permission: "reports.payroll", dated: true },
  { group: "Staff & Payroll", label: "Staff loans", path: "/reports/payroll/loans", permission: "reports.payroll" },
  { group: "Staff & Payroll", label: "Staff performance", path: "/reports/staff-performance", permission: "reports.staff", dated: true },
  { group: "Staff & Payroll", label: "Branch comparison", path: "/reports/branches", permission: "reports.branches", dated: true },
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
  previous?: { net_profit?: string; income?: string; expenses?: string; from?: string; to?: string };
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
  const { can } = useAuth();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const available = useMemo(() => catalogue.filter((r) => can(r.permission)), [can]);
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

  const columns = report.data?.columns ?? [];
  const rows = report.data?.rows ?? [];

  // Derive a chart automatically from the first label column + first numeric column.
  const chartSeries = useMemo(() => {
    if (columns.length < 2 || rows.length === 0) return [];
    const labelCol = columns[0].key;
    const numCol = columns.slice(1).find((c) =>
      rows.some((r) => {
        const v = r[c.key];
        return typeof v === "number" || (typeof v === "string" && /^-?\d+(\.\d+)?$/.test(v));
      }),
    );
    if (!numCol) return [];
    return rows.slice(0, 10).map((r) => ({
      name: String(r[labelCol] ?? "-").slice(0, 18),
      value: toNumber(r[numCol.key]),
    }));
  }, [columns, rows]);

  return (
    <div className="space-y-5">
      <PageHeader
        title={current.label}
        description={report.data?.subtitle ?? "Filter, review, print or export to CSV, Excel or PDF."}
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
          {["csv", "xlsx", "pdf"].map((f) => (
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
            <p className="text-xs uppercase tracking-wide text-muted-foreground">Shop (Business A)</p>
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
            <p className="text-xs uppercase tracking-wide text-muted-foreground">Operating expenses</p>
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

      {chartSeries.length > 0 && (
        <ChartCard title={`${current.label} chart`} description="Top 10 entries">
          <BarChartView data={chartSeries} label={current.label} />
        </ChartCard>
      )}

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
