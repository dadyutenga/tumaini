import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  DataGrid,
  Field,
  FormDialog,
  PageHeader,
  Pager,
  StatusBadge,
} from "@/components/shared/kit";
import { StatCard } from "@/components/shared/Display";
import { useAuth } from "@/features/auth/auth";
import { useGetPage, useWrite } from "@/hooks/useApi";
import { formatDate, formatMoney } from "@/lib/formatters";
import { useI18n } from "@/features/i18n/i18n";

export interface Run {
  id: string;
  number: string;
  period_start: string;
  period_end: string;
  status: string;
  gross_total: string;
  deduction_total: string;
  net_total: string;
  paid_total: string;
  payslip_count: number;
}

export default function Payroll() {
  const { t } = useI18n();
  const { can } = useAuth();
  const navigate = useNavigate();
  const [page, setPage] = useState(1);
  const list = useGetPage<Run>("/payroll-runs", { page });
  const [open, setOpen] = useState(false);
  const now = new Date();
  const iso = (d: Date) => d.toISOString().slice(0, 10);
  const [period, setPeriod] = useState({
    period_start: iso(new Date(now.getFullYear(), now.getMonth(), 2)),
    period_end: iso(new Date(now.getFullYear(), now.getMonth() + 1, 1)),
  });
  const create = useWrite<typeof period, { id: string }>("POST", "/payroll-runs", {
    invalidate: ["/payroll-runs"],
    success: t.payroll.payrollRunCreated,
    onSuccess: (r) => {
      setOpen(false);
      navigate(`/payroll/${r.id}`);
    },
  });

  const rows = useMemo(() => list.data?.data ?? [], [list.data?.data]);
  const totals = useMemo(() => {
    const gross = rows.reduce((s, r) => s + Number(r.gross_total || 0), 0);
    const net = rows.reduce((s, r) => s + Number(r.net_total || 0), 0);
    const paid = rows.reduce((s, r) => s + Number(r.paid_total || 0), 0);
    return { gross, net, paid };
  }, [rows]);

  return (
    <div className="space-y-6">
      <PageHeader
        title={t.common.payroll}
        description={t.payroll.calculateApproveAndPaySalaries}
        actions={
          can("hr.payroll.manage") && (
            <Button onClick={() => setOpen(true)}>
              <Plus size={16} /> {t.payroll.newRun}
            </Button>
          )
        }
      />

      {rows.length > 0 && (
        <div className="grid gap-4 sm:grid-cols-3">
          <StatCard label={t.payroll.grossThisPage} value={formatMoney(totals.gross)} />
          <StatCard label={t.payroll.netPayable} value={formatMoney(totals.net)} />
          <StatCard
            label={t.common.paid}
            value={formatMoney(totals.paid)}
            tone={totals.paid >= totals.net && totals.net > 0 ? "good" : "neutral"}
          />
        </div>
      )}

      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(r) => r.id}
        empty={t.payroll.noPayrollRunsYet}
        onRowClick={(r) => navigate(`/payroll/${r.id}`)}
        columns={[
          {
            header: t.common.number,
            cell: (r) => (
              <span className="od-mono font-medium text-[var(--od-ink)]">{r.number}</span>
            ),
          },
          {
            header: t.common.period,
            cell: (r) => `${formatDate(r.period_start)} – ${formatDate(r.period_end)}`,
          },
          {
            header: t.common.staff,
            align: "right",
            cell: (r) => <span className="od-tabular">{r.payslip_count}</span>,
          },
          {
            header: t.common.gross,
            align: "right",
            cell: (r) => <span className="od-tabular">{formatMoney(r.gross_total)}</span>,
          },
          {
            header: t.common.net,
            align: "right",
            cell: (r) => (
              <span className="od-tabular font-semibold text-[var(--od-ink)]">
                {formatMoney(r.net_total)}
              </span>
            ),
          },
          {
            header: t.common.paid,
            align: "right",
            cell: (r) => <span className="od-tabular">{formatMoney(r.paid_total)}</span>,
          },
          { header: t.common.status, cell: (r) => <StatusBadge value={r.status} /> },
        ]}
      />
      <Pager meta={list.data?.meta} onPage={setPage} />
      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title={t.payroll.newPayrollRun}
        busy={create.isPending}
        onSubmit={() => create.mutate(period)}
      >
        <div className="grid grid-cols-2 gap-3">
          <Field label={t.payroll.periodStart}>
            <Input
              required
              type="date"
              value={period.period_start}
              onChange={(e) => setPeriod((p) => ({ ...p, period_start: e.target.value }))}
            />
          </Field>
          <Field label={t.payroll.periodEnd}>
            <Input
              required
              type="date"
              value={period.period_end}
              onChange={(e) => setPeriod((p) => ({ ...p, period_end: e.target.value }))}
            />
          </Field>
        </div>
      </FormDialog>
    </div>
  );
}
