import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  DataGrid,
  Field,
  FilterBar,
  FormDialog,
  PageHeader,
  SegmentedChips,
  StatusBadge,
} from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { useGet, useWrite } from "@/hooks/useApi";
import { formatDateTime, formatMoney, toNumber } from "@/lib/formatters";
import type { Approval } from "@/types";
import { useI18n } from "@/features/i18n/i18n";
import type { Translations } from "@/features/i18n/i18n";

const APPROVAL_PERMISSION: Record<string, string> = {
  discount: "shop.discount.approve",
  sale_discount: "shop.discount.approve",
  refund: "shop.return.approve",
  sale_return: "shop.return.approve",
  sale_void: "shop.sale.void",
  product_price_change: "shop.product.price_approve",
  job_price_change: "services.price.approve",
  service_price_change: "services.price.approve",
  price_change: "services.price.approve",
  expense: "finance.expense.approve",
  payment_void: "finance.payment.void",
  transaction_correction: "finance.transaction.correct",
  day_reopen: "finance.day.reopen",
};

const ANY_APPROVAL_PERMS = Object.values(APPROVAL_PERMISSION);

const statusOptions = (t: Translations) => [
  { value: "pending", label: t.common.pending },
  { value: "approved", label: t.common.approved },
  { value: "rejected", label: t.common.rejected },
  { value: "", label: t.common.all },
];

export default function Approvals() {
  const { t } = useI18n();
  const { can, canAny, user } = useAuth();
  const canDecideKind = (kind: string) => {
    const specific = APPROVAL_PERMISSION[kind];
    return specific ? can(specific) : canAny(...ANY_APPROVAL_PERMS);
  };
  const [status, setStatus] = useState("pending");
  const { data, isLoading } = useGet<Approval[]>("/approvals", { status });
  const [deciding, setDeciding] = useState<{
    approval: Approval;
    action: "approve" | "reject";
  } | null>(null);
  const [note, setNote] = useState("");
  const decide = useWrite<{ id: string; action: string; note: string }, unknown>(
    "POST",
    (b) => `/approvals/${b.id}/${b.action}`,
    {
      idempotent: true,
      toBody: (b) => ({ note: b.note }),
      invalidate: ["/approvals", "/expenses", "/sales", "/stock", "/money"],
      success: t.approvals.decisionRecorded,
      onSuccess: () => {
        setDeciding(null);
        setNote("");
      },
    },
  );
  return (
    <div className="space-y-6">
      <PageHeader
        title={t.common.approvals}
        description={t.approvals.requestsThatWentOverSomeones}
      />

      <FilterBar>
        <SegmentedChips
          ariaLabel={t.common.filterByStatus}
          value={status}
          onChange={setStatus}
          options={statusOptions(t)}
        />
      </FilterBar>

      <DataGrid
        rows={data}
        loading={isLoading}
        rowKey={(a) => a.id}
        empty={t.approvals.nothingWaitingForApproval}
        columns={[
          {
            header: t.common.requested,
            cell: (a) => (
              <span className="od-tabular text-xs text-[var(--od-ink-secondary)]">
                {formatDateTime(a.created_at)}
              </span>
            ),
          },
          {
            header: t.common.kind,
            cell: (a) => (
              <Badge variant="secondary" className="capitalize">
                {a.kind.replaceAll("_", " ")}
              </Badge>
            ),
          },
          {
            header: t.approvals.summary,
            cell: (a) => <span className="font-medium text-[var(--od-ink)]">{a.summary}</span>,
          },
          {
            header: t.common.amount,
            align: "right",
            cell: (a) =>
              toNumber(a.amount) ? (
                <span className="od-tabular font-semibold text-[var(--od-ink)]">
                  {formatMoney(a.amount)}
                </span>
              ) : (
                <span className="text-[var(--od-ink-muted)]">-</span>
              ),
          },
          { header: t.common.status, cell: (a) => <StatusBadge value={a.status} /> },
          {
            header: "",
            cell: (a) => {
              const isOwn =
                a.requested_by === user?.id ||
                (Boolean(user?.membership_id) && a.requested_by === user?.membership_id);
              return a.status === "pending" && canDecideKind(a.kind) && !isOwn ? (
                <div className="flex justify-end gap-1.5">
                  <Button size="sm" onClick={() => setDeciding({ approval: a, action: "approve" })}>
                    {t.common.approve}
                  </Button>
                  <Button
                    size="sm"
                    variant="outline"
                    className="text-[var(--od-bad)] hover:text-[var(--od-bad)]"
                    onClick={() => setDeciding({ approval: a, action: "reject" })}
                  >
                    {t.common.reject}
                  </Button>
                </div>
              ) : a.status === "pending" && isOwn ? (
                <div className="flex justify-end">
                  <span className="text-xs text-[var(--od-ink-muted)]">
                    {t.approvals.yourRequest}
                  </span>
                </div>
              ) : null;
            },
          },
        ]}
      />
      <FormDialog
        open={deciding !== null}
        onOpenChange={(o) => !o && setDeciding(null)}
        title={
          deciding?.action === "approve" ? t.approvals.approveRequest : t.approvals.rejectRequest
        }
        description={deciding?.approval.summary}
        submitLabel={deciding?.action === "approve" ? t.common.approve : t.common.reject}
        busy={decide.isPending}
        onSubmit={() =>
          deciding && decide.mutate({ id: deciding.approval.id, action: deciding.action, note })
        }
      >
        <Field label={t.common.noteOptional}>
          <Input value={note} onChange={(e) => setNote(e.target.value)} />
        </Field>
      </FormDialog>
    </div>
  );
}
