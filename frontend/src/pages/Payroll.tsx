import { useState } from "react";
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
import { useAuth } from "@/features/auth/auth";
import { useGetPage, useWrite } from "@/hooks/useApi";
import { formatDate, formatMoney } from "@/lib/formatters";

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
    success: "Payroll run created",
    onSuccess: (r) => {
      setOpen(false);
      navigate(`/payroll/${r.id}`);
    },
  });
  return (
    <>
      <PageHeader
        title="Payroll"
        description="Calculate, approve and pay salaries for a period."
        actions={
          can("hr.payroll.manage") && (
            <Button onClick={() => setOpen(true)}>
              <Plus size={16} /> New run
            </Button>
          )
        }
      />
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(r) => r.id}
        empty="No payroll runs yet"
        onRowClick={(r) => navigate(`/payroll/${r.id}`)}
        columns={[
          { header: "Number", cell: (r) => <span className="font-medium">{r.number}</span> },
          {
            header: "Period",
            cell: (r) => `${formatDate(r.period_start)} – ${formatDate(r.period_end)}`,
          },
          { header: "Staff", align: "right", cell: (r) => r.payslip_count },
          { header: "Gross", align: "right", cell: (r) => formatMoney(r.gross_total) },
          { header: "Net", align: "right", cell: (r) => formatMoney(r.net_total) },
          { header: "Paid", align: "right", cell: (r) => formatMoney(r.paid_total) },
          { header: "Status", cell: (r) => <StatusBadge value={r.status} /> },
        ]}
      />
      <Pager meta={list.data?.meta} onPage={setPage} />
      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title="New payroll run"
        busy={create.isPending}
        onSubmit={() => create.mutate(period)}
      >
        <div className="grid grid-cols-2 gap-3">
          <Field label="Period start">
            <Input
              required
              type="date"
              value={period.period_start}
              onChange={(e) => setPeriod((p) => ({ ...p, period_start: e.target.value }))}
            />
          </Field>
          <Field label="Period end">
            <Input
              required
              type="date"
              value={period.period_end}
              onChange={(e) => setPeriod((p) => ({ ...p, period_end: e.target.value }))}
            />
          </Field>
        </div>
      </FormDialog>
    </>
  );
}
