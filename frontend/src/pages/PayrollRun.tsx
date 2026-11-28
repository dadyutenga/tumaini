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
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";

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
  const { t } = useI18n();
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
    success: t.payroll.payrollCalculated,
  });
  const approve = useWrite("POST", `/payroll-runs/${id}/approve`, {
    idempotent: true,
    invalidate: refresh,
    success: t.payroll.payrollApproved,
  });
  const pay = useWrite<{ money_account_id: string }, unknown>("POST", `/payroll-runs/${id}/pay`, {
    idempotent: true,
    invalidate: refresh,
    success: t.payroll.salariesPaid,
    onSuccess: () => setPaying(false),
  });
  const putLines = useWrite<{ lines: ManualLineDraft[]; reason: string }, unknown>(
    "PUT",
    () => `/payroll-runs/${id}/payslips/${editingSlip?.id}/lines`,
    {
      invalidate: refresh,
      success: t.payroll.payslipLinesUpdated,
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
    success: t.payroll.payrollRunReversed,
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
        backLabel={t.common.payroll}
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
                {hasSlips ? t.payroll.recalculate : t.payroll.calculate}
              </Button>
            )}
            {["draft", "calculated", "pending_approval"].includes(r.status) &&
              hasSlips &&
              canApprove && (
                <Button onClick={() => approve.mutate({})} disabled={approve.isPending}>
                  {t.common.approve}
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
                  <Download size={15} /> {t.payroll.paymentList}
                </Button>
                <Button onClick={() => setPaying(true)}>{t.payroll.paySalaries}</Button>
              </>
            )}
            {["draft", "approved"].includes(r.status) && (canApprove || canManage) && (
              <Button
                variant="outline"
                className="text-[var(--od-bad)] hover:text-[var(--od-bad)]"
                onClick={() => setReversing(true)}
              >
                <RotateCcw size={15} />{" "}
                {r.status === "approved" ? t.payroll.reopenToDraft : t.payroll.cancelDraft}
              </Button>
            )}
          </div>
        }
      />

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard
          label={t.payroll.staffOnRun}
          value={String(r.payslips?.length ?? r.payslip_count ?? 0)}
        />
        <StatCard label={t.payroll.grossPay} value={formatMoney(r.gross_total)} />
        <StatCard label={t.common.deductions} value={formatMoney(r.deduction_total)} />
        <StatCard
          label={t.payroll.netPayable}
          value={formatMoney(r.net_total)}
          trend={fmt(t.payroll.paid, { paid_total: formatMoney(r.paid_total) })}
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
        empty={t.payroll.calculateTheRunToGenerate}
        columns={[
          {
            header: t.common.employee,
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
            header: t.common.basePay,
            align: "right",
            cell: (s) => <span className="od-tabular">{formatMoney(s.base_pay)}</span>,
          },
          {
            header: t.common.gross,
            align: "right",
            cell: (s) => <span className="od-tabular">{formatMoney(s.gross)}</span>,
          },
          {
            header: t.common.deductions,
            align: "right",
            cell: (s) => <span className="od-tabular">{formatMoney(s.total_deductions)}</span>,
          },
          {
            header: t.common.net,
            align: "right",
            cell: (s) => (
              <span className="od-tabular font-semibold text-[var(--od-ink)]">
                {formatMoney(s.net)}
              </span>
            ),
          },
          {
            header: t.common.paid,
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
                    <Pencil size={13} /> {t.payroll.editLines}
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
                        <MessageCircle size={14} /> {t.payroll.share}
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
        title={t.payroll.paySalaries}
        submitLabel={fmt(t.payroll.pay, { net_total: formatMoney(r.net_total) })}
        busy={pay.isPending}
        onSubmit={() => pay.mutate({ money_account_id: account })}
      >
        <div className="rounded-xl border border-[var(--od-border)] bg-[var(--od-surface-subtle)] px-3.5 py-2.5 text-sm text-[var(--od-ink-secondary)]">
          {t.payroll.totalNetSalariesToDisburse}{" "}
          <strong className="od-tabular font-semibold text-[var(--od-ink)]">
            {formatMoney(r.net_total)}
          </strong>
        </div>
        <Field label={t.common.payFrom}>
          <NativeSelect required value={account} onChange={(e) => setAccount(e.target.value)}>
            <option value="">{t.common.choose}</option>
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
        title={fmt(t.payroll.editManualLines, { name: editingSlip?.name ?? "" })}
        submitLabel={t.payroll.saveLines}
        busy={putLines.isPending}
        onSubmit={() =>
          putLines.mutate({
            lines: manualLines.filter((l) => l.label.trim() && Number(l.amount) > 0),
            reason: lineReason,
          })
        }
      >
        <p className="od-caption">{t.payroll.replacesManualEarningDeductionLines}</p>
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
                <option value="bonus">{t.common.bonus}</option>
                <option value="allowance">{t.payroll.allowance}</option>
                <option value="overtime">{t.common.overtime}</option>
                <option value="commission">{t.payroll.commission}</option>
                <option value="deduction">{t.common.deduction}</option>
              </NativeSelect>
              <Input
                placeholder={t.common.description}
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
                placeholder={t.common.amount}
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
            <Plus size={13} /> {t.common.addLine}
          </Button>
        </div>
        <Field label={t.payroll.reasonRequired}>
          <Input
            required
            value={lineReason}
            onChange={(e) => setLineReason(e.target.value)}
            placeholder={t.payroll.eGSpecialSalesBonus}
          />
        </Field>
      </FormDialog>

      <FormDialog
        open={reversing}
        onOpenChange={setReversing}
        title={
          r.status === "approved"
            ? fmt(t.payroll.reopenPayroll, { number: r.number })
            : fmt(t.payroll.cancelDraft2, { number: r.number })
        }
        submitLabel={r.status === "approved" ? t.payroll.reopenToDraft : t.payroll.cancelDraft}
        busy={reverse.isPending}
        onSubmit={() => reverse.mutate({ reason: reverseReason })}
      >
        <div className="rounded-xl border border-[var(--od-border)] bg-[var(--od-surface-subtle)] px-3.5 py-2.5 text-sm text-[var(--od-ink-secondary)]">
          {r.status === "approved"
            ? t.payroll.reopeningMovesThisUnpaidPayroll
            : t.payroll.cancellingReleasesAnyLinkedPay}
        </div>
        <Field label={t.common.reason}>
          <Textarea
            rows={3}
            value={reverseReason}
            onChange={(e) => setReverseReason(e.target.value)}
            placeholder={t.payroll.optionalNoteExplainingWhy}
          />
        </Field>
      </FormDialog>
    </div>
  );
}
