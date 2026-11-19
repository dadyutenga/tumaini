import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { ArrowLeft, Download, FileText } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import {
  DataGrid,
  Field,
  FormDialog,
  NativeSelect,
  PageHeader,
  StatusBadge,
} from "@/components/shared/kit";
import { PageLoader } from "@/components/shared/Display";
import { useAuth } from "@/features/auth/auth";
import { errorMessage, useGet, useWrite } from "@/hooks/useApi";
import { download } from "@/lib/api";
import { formatDate, formatMoney } from "@/lib/formatters";
import type { MoneyAccount } from "@/types";
import type { Run } from "@/pages/Payroll";

interface Slip {
  id: string;
  number: string;
  name: string;
  employee_no: string;
  base_pay: string;
  gross: string;
  total_deductions: string;
  net: string;
  paid_total: string;
  balance: string;
}
type Detail = Run & { payslips?: Slip[] };

export default function PayrollRun() {
  const { id } = useParams();
  const { can } = useAuth();
  const run = useGet<Detail>(`/payroll-runs/${id}`);
  const accounts = useGet<MoneyAccount[]>("/money-accounts", undefined, {
    enabled: can("accounts.view"),
  });
  const [paying, setPaying] = useState(false);
  const [account, setAccount] = useState("");
  const refresh = ["/payroll-runs", "/money", "/dashboard", "/me/payslips"];
  const calculate = useWrite("POST", `/payroll-runs/${id}/calculate`, {
    invalidate: refresh,
    success: "Payroll calculated",
  });
  const submit = useWrite("POST", `/payroll-runs/${id}/submit`, {
    invalidate: refresh,
    success: "Submitted for approval",
  });
  const approve = useWrite("POST", `/payroll-runs/${id}/approve`, {
    idempotent: true,
    invalidate: refresh,
    success: "Payroll approved",
  });
  const pay = useWrite<{ money_account_id: string }, unknown>("POST", `/payroll-runs/${id}/pay`, {
    idempotent: true,
    invalidate: refresh,
    success: "Salaries paid",
    onSuccess: () => setPaying(false),
  });

  if (run.isLoading || !run.data) return <PageLoader />;
  const r = run.data;
  const save = (path: string, query: Record<string, string>, name: string) =>
    download(path, query, name).catch((e) => toast.error(errorMessage(e)));

  return (
    <>
      <Link
        to="/payroll"
        className="mb-3 inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft size={14} /> Payroll
      </Link>
      <PageHeader
        title={r.number}
        description={`${formatDate(r.period_start)} – ${formatDate(r.period_end)} · net ${formatMoney(r.net_total)}`}
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <StatusBadge value={r.status} />
            {["draft", "calculated"].includes(r.status) && can("payroll.prepare") && (
              <Button
                variant="outline"
                onClick={() => calculate.mutate({})}
                disabled={calculate.isPending}
              >
                {r.status === "draft" ? "Calculate" : "Recalculate"}
              </Button>
            )}
            {r.status === "calculated" && can("payroll.prepare") && (
              <Button onClick={() => submit.mutate({})} disabled={submit.isPending}>
                Submit
              </Button>
            )}
            {["calculated", "pending_approval"].includes(r.status) && can("payroll.approve") && (
              <Button onClick={() => approve.mutate({})} disabled={approve.isPending}>
                Approve
              </Button>
            )}
            {r.status === "approved" && can("payroll.pay") && (
              <>
                <Button
                  variant="outline"
                  onClick={() =>
                    save(
                      `/payroll-runs/${r.id}/payment-list`,
                      { format: "csv" },
                      `${r.number}-payments.csv`,
                    )
                  }
                >
                  <Download size={16} /> Payment list
                </Button>
                <Button onClick={() => setPaying(true)}>Pay salaries</Button>
              </>
            )}
          </div>
        }
      />
      <DataGrid
        rows={r.payslips}
        rowKey={(s) => s.id}
        empty="Calculate the run to generate payslips"
        columns={[
          { header: "Employee", cell: (s) => <span className="font-medium">{s.name}</span> },
          { header: "Base pay", align: "right", cell: (s) => formatMoney(s.base_pay) },
          { header: "Gross", align: "right", cell: (s) => formatMoney(s.gross) },
          { header: "Deductions", align: "right", cell: (s) => formatMoney(s.total_deductions) },
          { header: "Net", align: "right", cell: (s) => formatMoney(s.net) },
          { header: "Paid", align: "right", cell: (s) => formatMoney(s.paid_total) },
          {
            header: "",
            cell: (s) =>
              can("payslips.view") && (
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => save(`/payslips/${s.id}`, { format: "pdf" }, `${s.number}.pdf`)}
                >
                  <FileText size={14} /> PDF
                </Button>
              ),
          },
        ]}
      />
      <FormDialog
        open={paying}
        onOpenChange={setPaying}
        title="Pay salaries"
        submitLabel={`Pay ${formatMoney(r.net_total)}`}
        busy={pay.isPending}
        onSubmit={() => pay.mutate({ money_account_id: account })}
      >
        <Field label="Pay from">
          <NativeSelect required value={account} onChange={(e) => setAccount(e.target.value)}>
            <option value="">Choose…</option>
            {accounts.data
              ?.filter((a) => a.is_active)
              .map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
          </NativeSelect>
        </Field>
      </FormDialog>
    </>
  );
}
