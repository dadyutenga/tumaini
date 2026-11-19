import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import {
  ArrowLeft,
  Download,
  FileText,
  MessageCircle,
  Pencil,
  Plus,
  RotateCcw,
  Trash2,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
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

interface ManualLineDraft {
  kind: "allowance" | "overtime" | "commission" | "bonus" | "deduction";
  label: string;
  amount: string;
}

export default function PayrollRun() {
  const { id } = useParams();
  const { can } = useAuth();
  const run = useGet<Detail>(`/payroll-runs/${id}`);
  const accounts = useGet<MoneyAccount[]>("/money-accounts", undefined, {
    enabled: can("accounts.view"),
  });
  const [paying, setPaying] = useState(false);
  const [account, setAccount] = useState("");

  const [editingSlip, setEditingSlip] = useState<Slip | null>(null);
  const [manualLines, setManualLines] = useState<ManualLineDraft[]>([
    { kind: "bonus", label: "", amount: "" },
  ]);
  const [lineReason, setLineReason] = useState("");

  const [reversing, setReversing] = useState(false);
  const [reverseReason, setReverseReason] = useState("");

  const refresh = ["/payroll-runs", "/money-accounts", "/transactions", "/dashboard", "/me/payslips"];
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
  const putLines = useWrite<{ lines: ManualLineDraft[]; reason: string }, unknown>(
    "PUT",
    () => `/payroll-runs/${id}/payslips/${editingSlip?.id}/lines`,
    {
      invalidate: refresh,
      success: "Payslip lines updated",
      onSuccess: () => {
        setEditingSlip(null);
        setManualLines([{ kind: "bonus", label: "", amount: "" }]);
        setLineReason("");
      },
    },
  );
  const reverse = useWrite<{ reason: string }, unknown>("POST", `/payroll-runs/${id}/reverse`, {
    idempotent: true,
    invalidate: refresh,
    success: "Payroll run reversed",
    onSuccess: () => {
      setReversing(false);
      setReverseReason("");
    },
  });

  if (run.isLoading || !run.data) return <PageLoader />;
  const r = run.data;
  const save = (path: string, query: Record<string, string>, name: string) =>
    download(path, query, name).catch((e) => toast.error(errorMessage(e)));

  const canEditLines = ["draft", "calculated"].includes(r.status) && can("payroll.prepare");

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
            {["approved", "paid"].includes(r.status) && can("payroll.reverse") && (
              <Button
                variant="outline"
                className="text-destructive hover:text-destructive"
                onClick={() => setReversing(true)}
              >
                <RotateCcw size={15} /> Reverse payroll
              </Button>
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
            cell: (s) => (
              <div className="flex justify-end gap-1">
                {canEditLines && (
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() => {
                      setEditingSlip(s);
                      setManualLines([{ kind: "bonus", label: "", amount: "" }]);
                      setLineReason("");
                    }}
                  >
                    <Pencil size={13} /> Edit lines
                  </Button>
                )}
                {can("payslips.view") && (
                  <>
                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={() => save(`/payslips/${s.id}`, { format: "pdf" }, `${s.number}.pdf`)}
                    >
                      <FileText size={14} /> PDF
                    </Button>
                    <Button size="sm" variant="ghost" asChild>
                      <a
                        href={`https://wa.me/?text=${encodeURIComponent(
                          `Payslip ${s.number} (${formatDate(r.period_start)} – ${formatDate(r.period_end)})\nEmployee: ${s.name}\nGross: ${formatMoney(s.gross)}\nDeductions: ${formatMoney(s.total_deductions)}\nNet Pay: ${formatMoney(s.net)}`,
                        )}`}
                        target="_blank"
                        rel="noreferrer"
                      >
                        <MessageCircle size={14} /> Share
                      </a>
                    </Button>
                  </>
                )}
              </div>
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

      <FormDialog
        open={editingSlip !== null}
        onOpenChange={(o) => !o && setEditingSlip(null)}
        title={`Edit manual lines — ${editingSlip?.name ?? ""}`}
        submitLabel="Save lines"
        busy={putLines.isPending}
        onSubmit={() =>
          putLines.mutate({
            lines: manualLines.filter((l) => l.label.trim() && Number(l.amount) > 0),
            reason: lineReason,
          })
        }
      >
        <p className="text-xs text-muted-foreground">
          Replaces manual earning/deduction lines on this payslip and recalculates net pay. Leave rows empty to remove manual lines.
        </p>
        <div className="space-y-2">
          {manualLines.map((line, idx) => (
            <div key={idx} className="grid grid-cols-[7.5rem_1fr_6.5rem_2rem] items-center gap-2">
              <NativeSelect
                value={line.kind}
                onChange={(e) =>
                  setManualLines((prev) =>
                    prev.map((l, i) =>
                      i === idx ? { ...l, kind: e.target.value as ManualLineDraft["kind"] } : l,
                    ),
                  )
                }
              >
                <option value="bonus">Bonus</option>
                <option value="allowance">Allowance</option>
                <option value="overtime">Overtime</option>
                <option value="commission">Commission</option>
                <option value="deduction">Deduction</option>
              </NativeSelect>
              <Input
                placeholder="Description"
                value={line.label}
                onChange={(e) =>
                  setManualLines((prev) =>
                    prev.map((l, i) => (i === idx ? { ...l, label: e.target.value } : l)),
                  )
                }
              />
              <Input
                inputMode="decimal"
                placeholder="Amount"
                value={line.amount}
                onChange={(e) =>
                  setManualLines((prev) =>
                    prev.map((l, i) => (i === idx ? { ...l, amount: e.target.value } : l)),
                  )
                }
              />
              <Button
                type="button"
                size="icon"
                variant="ghost"
                className="h-8 w-8 text-destructive"
                onClick={() => setManualLines((prev) => prev.filter((_, i) => i !== idx))}
              >
                <Trash2 size={14} />
              </Button>
            </div>
          ))}
          <Button
            type="button"
            size="sm"
            variant="outline"
            onClick={() =>
              setManualLines((prev) => [...prev, { kind: "bonus", label: "", amount: "" }])
            }
          >
            <Plus size={13} /> Add line
          </Button>
        </div>
        <Field label="Reason (required)">
          <Input
            required
            value={lineReason}
            onChange={(e) => setLineReason(e.target.value)}
            placeholder="e.g. Special sales bonus approved by owner"
          />
        </Field>
      </FormDialog>

      <FormDialog
        open={reversing}
        onOpenChange={setReversing}
        title={`Reverse payroll ${r.number}`}
        submitLabel="Confirm reversal"
        busy={reverse.isPending}
        onSubmit={() => reverse.mutate({ reason: reverseReason })}
      >
        <p className="text-sm text-muted-foreground">
          Reversing a payroll run posts mirror journal entries and voids associated salary payments.
        </p>
        <Field label="Reason for reversal (required)">
          <Textarea
            required
            rows={3}
            value={reverseReason}
            onChange={(e) => setReverseReason(e.target.value)}
            placeholder="Explain why this payroll run is being reversed…"
          />
        </Field>
      </FormDialog>
    </>
  );
}
