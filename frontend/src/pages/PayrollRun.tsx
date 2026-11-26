import { useState } from "react";
import { useParams } from "react-router-dom";
import { Download, FileText, MessageCircle, Pencil, Plus, RotateCcw, Trash2 } from "lucide-react";
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
import { PageLoader, StatCard } from "@/components/shared/Display";
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
  const { can, canAny } = useAuth();
  const run = useGet<Detail>(`/payroll-runs/${id}`);
  const accounts = useGet<MoneyAccount[]>("/money-accounts", undefined, {
    enabled: canAny("finance.account.view", "hr.payroll.approve"),
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

  const refresh = [
    "/payroll-runs",
    "/money-accounts",
    "/transactions",
    "/dashboard",
    "/me/payslips",
  ];
  const calculate = useWrite("POST", `/payroll-runs/${id}/calculate`, {
    invalidate: refresh,
    success: "Payroll calculated",
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

  const hasSlips = (r.payslips?.length ?? 0) > 0;
  const canManage = can("hr.payroll.manage");
  const canApprove = can("hr.payroll.approve");
  const canViewSlips = canAny("hr.payroll.view", "hr.payroll.manage", "hr.payroll.approve");
  const canEditLines = ["draft", "calculated"].includes(r.status) && canManage;

  return (
    <div className="space-y-6">
      <PageHeader
        backTo="/payroll"
        backLabel="Payroll"
        title={r.number}
        description={`${formatDate(r.period_start)} – ${formatDate(r.period_end)}`}
        badge={<StatusBadge value={r.status} />}
        actions={
          <div className="flex flex-wrap items-center gap-2">
            {["draft", "calculated"].includes(r.status) && canManage && (
              <Button
                variant="outline"
                onClick={() => calculate.mutate({})}
                disabled={calculate.isPending}
              >
                {hasSlips ? "Recalculate" : "Calculate"}
              </Button>
            )}
            {["draft", "calculated", "pending_approval"].includes(r.status) &&
              hasSlips &&
              canApprove && (
                <Button onClick={() => approve.mutate({})} disabled={approve.isPending}>
                  Approve
                </Button>
              )}
            {r.status === "approved" && canApprove && (
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
                  <Download size={15} /> Payment list
                </Button>
                <Button onClick={() => setPaying(true)}>Pay salaries</Button>
              </>
            )}
            {["draft", "approved"].includes(r.status) && (canApprove || canManage) && (
              <Button
                variant="outline"
                className="text-[var(--od-bad)] hover:text-[var(--od-bad)]"
                onClick={() => setReversing(true)}
              >
                <RotateCcw size={15} />{" "}
                {r.status === "approved" ? "Reopen to draft" : "Cancel draft"}
              </Button>
            )}
          </div>
        }
      />

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard label="Staff on run" value={String(r.payslips?.length ?? r.payslip_count ?? 0)} />
        <StatCard label="Gross pay" value={formatMoney(r.gross_total)} />
        <StatCard label="Deductions" value={formatMoney(r.deduction_total)} />
        <StatCard
          label="Net payable"
          value={formatMoney(r.net_total)}
          trend={`Paid ${formatMoney(r.paid_total)}`}
          tone={
            Number(r.paid_total) >= Number(r.net_total) && Number(r.net_total) > 0
              ? "good"
              : "neutral"
          }
        />
      </div>

      <DataGrid
        rows={r.payslips}
        rowKey={(s) => s.id}
        empty="Calculate the run to generate payslips"
        columns={[
          {
            header: "Employee",
            cell: (s) => (
              <div>
                <p className="font-medium text-[var(--od-ink)]">{s.name}</p>
                {s.employee_no && (
                  <p className="od-mono text-xs text-[var(--od-ink-muted)]">{s.employee_no}</p>
                )}
              </div>
            ),
          },
          {
            header: "Base pay",
            align: "right",
            cell: (s) => <span className="od-tabular">{formatMoney(s.base_pay)}</span>,
          },
          {
            header: "Gross",
            align: "right",
            cell: (s) => <span className="od-tabular">{formatMoney(s.gross)}</span>,
          },
          {
            header: "Deductions",
            align: "right",
            cell: (s) => <span className="od-tabular">{formatMoney(s.total_deductions)}</span>,
          },
          {
            header: "Net",
            align: "right",
            cell: (s) => (
              <span className="od-tabular font-semibold text-[var(--od-ink)]">
                {formatMoney(s.net)}
              </span>
            ),
          },
          {
            header: "Paid",
            align: "right",
            cell: (s) => <span className="od-tabular">{formatMoney(s.paid_total)}</span>,
          },
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
                {canViewSlips && (
                  <>
                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={() =>
                        save(
                          `/payslips/${s.id}`,
                          { format: "pdf" },
                          `${s.number || s.employee_no}.pdf`,
                        )
                      }
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
        <div className="rounded-xl border border-[var(--od-border)] bg-[var(--od-surface-subtle)] px-3.5 py-2.5 text-sm text-[var(--od-ink-secondary)]">
          Total net salaries to disburse:{" "}
          <strong className="od-tabular font-semibold text-[var(--od-ink)]">
            {formatMoney(r.net_total)}
          </strong>
        </div>
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
        <p className="od-caption">
          Replaces manual earning/deduction lines on this payslip and recalculates net pay. Leave
          rows empty to remove manual lines.
        </p>
        <div className="space-y-2">
          {manualLines.map((line, idx) => (
            <div
              key={idx}
              className="grid grid-cols-1 items-center gap-2 sm:grid-cols-[7.5rem_1fr_6.5rem_2rem]"
            >
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
                className="od-tabular"
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
                className="h-8 w-8 text-[var(--od-bad)] hover:text-[var(--od-bad)]"
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
        title={r.status === "approved" ? `Reopen payroll ${r.number}` : `Cancel draft ${r.number}`}
        submitLabel={r.status === "approved" ? "Reopen to draft" : "Cancel draft"}
        busy={reverse.isPending}
        onSubmit={() => reverse.mutate({ reason: reverseReason })}
      >
        <div className="rounded-xl border border-[var(--od-border)] bg-[var(--od-surface-subtle)] px-3.5 py-2.5 text-sm text-[var(--od-ink-secondary)]">
          {r.status === "approved"
            ? "Reopening moves this unpaid payroll run back to draft so it can be edited and recalculated."
            : "Cancelling releases any linked pay adjustments and accrued commissions back for future runs."}
        </div>
        <Field label="Reason">
          <Textarea
            rows={3}
            value={reverseReason}
            onChange={(e) => setReverseReason(e.target.value)}
            placeholder="Optional note explaining why…"
          />
        </Field>
      </FormDialog>
    </div>
  );
}
