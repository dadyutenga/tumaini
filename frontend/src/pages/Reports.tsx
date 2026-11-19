import { useMemo, useState } from "react";
import { Download } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
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
  {
    group: "Sales",
    label: "Sales summary",
    path: "/reports/sales/summary",
    permission: "reports.sales",
    dated: true,
  },
  {
    group: "Sales",
    label: "By product",
    path: "/reports/sales/by-product",
    permission: "reports.sales",
    dated: true,
  },
  {
    group: "Sales",
    label: "By category",
    path: "/reports/sales/by-category",
    permission: "reports.sales",
    dated: true,
  },
  {
    group: "Sales",
    label: "By cashier",
    path: "/reports/sales/by-cashier",
    permission: "reports.sales",
    dated: true,
  },
  {
    group: "Sales",
    label: "By customer",
    path: "/reports/sales/by-customer",
    permission: "reports.sales",
    dated: true,
  },
  {
    group: "Sales",
    label: "By payment method",
    path: "/reports/sales/by-payment-method",
    permission: "reports.sales",
    dated: true,
  },
  {
    group: "Sales",
    label: "Till sessions",
    path: "/reports/tills",
    permission: "reports.sales",
    dated: true,
  },
  {
    group: "Finance",
    label: "Profit and loss",
    path: "/reports/profit-loss",
    permission: "reports.financial",
    dated: true,
  },
  {
    group: "Finance",
    label: "Cash flow",
    path: "/reports/cash-flow",
    permission: "reports.financial",
    dated: true,
  },
  {
    group: "Finance",
    label: "Balance sheet",
    path: "/reports/balance-sheet",
    permission: "reports.financial",
  },
  {
    group: "Finance",
    label: "Tax",
    path: "/reports/tax",
    permission: "reports.financial",
    dated: true,
  },
  {
    group: "Expenses",
    label: "Expenses",
    path: "/reports/expenses",
    permission: "reports.expenses",
    dated: true,
  },
  {
    group: "Stock",
    label: "Stock on hand",
    path: "/reports/inventory/on-hand",
    permission: "reports.inventory",
  },
  {
    group: "Stock",
    label: "Stock valuation",
    path: "/reports/inventory/valuation",
    permission: "reports.inventory",
  },
  {
    group: "Stock",
    label: "Movements",
    path: "/reports/inventory/movements",
    permission: "reports.inventory",
    dated: true,
  },
  {
    group: "Stock",
    label: "Low stock",
    path: "/reports/inventory/low-stock",
    permission: "reports.inventory",
  },
  {
    group: "Stock",
    label: "Dead stock",
    path: "/reports/inventory/dead-stock",
    permission: "reports.inventory",
  },
  {
    group: "Stock",
    label: "Losses and adjustments",
    path: "/reports/inventory/losses",
    permission: "reports.inventory",
    dated: true,
  },
  {
    group: "Purchasing",
    label: "Purchases",
    path: "/reports/purchases",
    permission: "reports.purchases",
    dated: true,
  },
  {
    group: "Debts",
    label: "Customer debts",
    path: "/reports/customer-debt",
    permission: "reports.debts",
  },
  {
    group: "Debts",
    label: "Supplier debts",
    path: "/reports/supplier-debt",
    permission: "reports.debts",
  },
  {
    group: "Payroll",
    label: "Payroll summary",
    path: "/reports/payroll/summary",
    permission: "reports.payroll",
    dated: true,
  },
  {
    group: "Payroll",
    label: "By department",
    path: "/reports/payroll/by-department",
    permission: "reports.payroll",
    dated: true,
  },
  {
    group: "Payroll",
    label: "Deductions",
    path: "/reports/payroll/deductions",
    permission: "reports.payroll",
    dated: true,
  },
  {
    group: "Payroll",
    label: "Staff loans",
    path: "/reports/payroll/loans",
    permission: "reports.payroll",
  },
  {
    group: "Team",
    label: "Staff performance",
    path: "/reports/staff-performance",
    permission: "reports.staff",
    dated: true,
  },
  {
    group: "Team",
    label: "Branch comparison",
    path: "/reports/branches",
    permission: "reports.branches",
    dated: true,
  },
];

interface Table {
  title: string;
  subtitle?: string;
  columns: { key: string; label: string }[];
  rows: Record<string, unknown>[];
  totals?: Record<string, unknown>;
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
  const available = useMemo(() => catalogue.filter((r) => can(r.permission)), [can]);
  const [path, setPath] = useState("");
  const current = available.find((r) => r.path === path) ?? available[0];
  const month = new Date();
  const first = new Date(month.getFullYear(), month.getMonth(), 1).toLocaleDateString("en-CA");
  const [from, setFrom] = useState(first);
  const [to, setTo] = useState(new Date().toLocaleDateString("en-CA"));
  const query = current?.dated ? { from, to } : undefined;
  const report = useGet<Table>(current?.path ?? "", query, { enabled: !!current });
  const groups = [...new Set(available.map((r) => r.group))];

  const save = (format: string) =>
    download(current!.path, { ...query, format }, `${current!.label}.${format}`).catch((e) =>
      toast.error(errorMessage(e)),
    );

  if (!current) {
    return <PageHeader title="Reports" description="You do not have access to any reports." />;
  }
  const columns = report.data?.columns ?? [];
  const rows = report.data?.rows ?? [];

  return (
    <>
      <PageHeader title="Reports" description="Choose a report, set the dates, and export it." />
      <div className="mb-4 flex flex-wrap items-end gap-3">
        <Field label="Report" className="min-w-[14rem]">
          <NativeSelect value={current.path} onChange={(e) => setPath(e.target.value)}>
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
        <div className="ml-auto flex gap-2">
          {["csv", "xlsx", "pdf"].map((f) => (
            <Button key={f} variant="outline" size="sm" onClick={() => save(f)}>
              <Download size={14} /> {f.toUpperCase()}
            </Button>
          ))}
        </div>
      </div>
      {report.data?.subtitle && (
        <p className="mb-2 text-sm text-muted-foreground">{report.data.subtitle}</p>
      )}
      <DataGrid
        rows={
          rows.length && report.data?.totals
            ? [...rows, { ...report.data.totals, __total: true }]
            : rows
        }
        loading={report.isLoading}
        rowKey={(r) => String(r.id ?? r.product_id ?? JSON.stringify(r))}
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
    </>
  );
}
