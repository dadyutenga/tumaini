import { useMemo } from "react";
import { Link } from "react-router-dom";
import { BarChartView } from "@/components/shared/Charts";
import { ChartCard, StatCard } from "@/components/shared/Display";
import { PageHeader, StatusBadge } from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { useGet, useGetPage } from "@/hooks/useApi";
import { formatMoney, toNumber, today } from "@/lib/formatters";
import type { Customer, SaleRow } from "@/types";

function monthStart() {
  return `${today().slice(0, 8)}01`;
}

export default function Dashboard() {
  const { can, user } = useAuth();
  const sales = useGetPage<SaleRow>("/sales", { per_page: 100 });
  const pl = useGet<{ income: string; expenses: string; net_profit: string }>(
    "/reports/profit-loss",
    { from: monthStart(), to: today() },
    { enabled: can("reports.financial") },
  );
  const low = useGet<unknown[]>("/stock/low", undefined, { enabled: can("stock.view") });
  const pending = useGet<unknown[]>(
    "/approvals",
    { status: "pending" },
    { enabled: can("approvals.view") },
  );
  const customers = useGet<Customer[]>("/customers", undefined, { enabled: can("customers.view") });

  const stats = useMemo(() => {
    const rows = (sales.data?.data ?? []).filter((s) => s.status !== "voided");
    const todayRows = rows.filter((s) => s.business_date === today());
    const byDay = new Map<string, number>();
    for (let i = 6; i >= 0; i--) {
      const d = new Date();
      d.setDate(d.getDate() - i);
      byDay.set(d.toISOString().slice(0, 10), 0);
    }
    for (const s of rows)
      if (byDay.has(s.business_date))
        byDay.set(s.business_date, (byDay.get(s.business_date) ?? 0) + toNumber(s.total));
    return {
      todayTotal: todayRows.reduce((sum, s) => sum + toNumber(s.total), 0),
      todayCount: todayRows.length,
      chart: [...byDay].map(([name, value]) => ({ name: name.slice(5), value })),
    };
  }, [sales.data]);
  const receivable = (customers.data ?? []).reduce(
    (sum, c) => sum + Math.max(0, toNumber(c.balance)),
    0,
  );

  return (
    <>
      <PageHeader
        title={`Hello, ${user?.name.split(" ")[0] ?? ""}`}
        description="Here is how the active branch is doing."
      />
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {can("sales.view") && (
          <StatCard
            label="Sales today"
            value={formatMoney(stats.todayTotal)}
            trend={`${stats.todayCount} sales`}
          />
        )}
        {pl.data && (
          <StatCard
            label="Profit this month"
            value={formatMoney(pl.data.net_profit)}
            trend={`Income ${formatMoney(pl.data.income)}`}
          />
        )}
        {low.data && <StatCard label="Low stock items" value={low.data.length} />}
        {pending.data && <StatCard label="Waiting for approval" value={pending.data.length} />}
        {customers.data && <StatCard label="Customers owe you" value={formatMoney(receivable)} />}
      </div>
      {can("sales.view") && (
        <div className="mt-6 grid gap-6 lg:grid-cols-2">
          <ChartCard title="Sales, last 7 days" description="Daily totals for this branch">
            <BarChartView data={stats.chart} label="Sales over the last seven days" />
          </ChartCard>
          <section className="rounded-2xl border bg-card p-6">
            <h2 className="font-semibold">Recent sales</h2>
            <ul className="mt-4 divide-y">
              {sales.data?.data.slice(0, 7).map((s) => (
                <li key={s.id}>
                  <Link
                    to={`/sales/${s.id}`}
                    className="flex items-center justify-between gap-3 py-2 hover:text-primary"
                  >
                    <span className="font-medium">{s.number}</span>
                    <StatusBadge value={s.status} />
                    <span className="tabular-nums">{formatMoney(s.total)}</span>
                  </Link>
                </li>
              ))}
              {sales.data?.data.length === 0 && (
                <li className="py-6 text-center text-sm text-muted-foreground">
                  No sales yet. Open the till and make the first one.
                </li>
              )}
            </ul>
          </section>
        </div>
      )}
    </>
  );
}
