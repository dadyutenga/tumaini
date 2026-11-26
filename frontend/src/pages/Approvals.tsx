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

const STATUS_OPTIONS = [
  { value: "pending", label: "Pending" },
  { value: "approved", label: "Approved" },
  { value: "rejected", label: "Rejected" },
  { value: "", label: "All" },
];

export default function Approvals() {
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
      success: "Decision recorded",
      onSuccess: () => {
        setDeciding(null);
        setNote("");
      },
    },
  );
  return (
    <div className="space-y-6">
      <PageHeader
        title="Approvals"
        description="Requests that went over someone's authority limit."
      />

      <FilterBar>
        <SegmentedChips
          ariaLabel="Filter by status"
          value={status}
          onChange={setStatus}
          options={STATUS_OPTIONS}
        />
      </FilterBar>

      <DataGrid
        rows={data}
        loading={isLoading}
        rowKey={(a) => a.id}
        empty="Nothing waiting for approval"
        columns={[
          {
            header: "Requested",
            cell: (a) => (
              <span className="od-tabular text-xs text-[var(--od-ink-secondary)]">
                {formatDateTime(a.created_at)}
              </span>
            ),
          },
          {
            header: "Kind",
            cell: (a) => (
              <Badge variant="secondary" className="capitalize">
                {a.kind.replaceAll("_", " ")}
              </Badge>
            ),
          },
          {
            header: "Summary",
            cell: (a) => <span className="font-medium text-[var(--od-ink)]">{a.summary}</span>,
          },
          {
            header: "Amount",
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
          { header: "Status", cell: (a) => <StatusBadge value={a.status} /> },
          {
            header: "",
            cell: (a) => {
              const isOwn =
                a.requested_by === user?.id ||
                (Boolean(user?.membership_id) && a.requested_by === user?.membership_id);
              return a.status === "pending" && canDecideKind(a.kind) && !isOwn ? (
                <div className="flex justify-end gap-1.5">
                  <Button size="sm" onClick={() => setDeciding({ approval: a, action: "approve" })}>
                    Approve
                  </Button>
                  <Button
                    size="sm"
                    variant="outline"
                    className="text-[var(--od-bad)] hover:text-[var(--od-bad)]"
                    onClick={() => setDeciding({ approval: a, action: "reject" })}
                  >
                    Reject
                  </Button>
                </div>
              ) : a.status === "pending" && isOwn ? (
                <div className="flex justify-end">
                  <span className="text-xs text-[var(--od-ink-muted)]">Your request</span>
                </div>
              ) : null;
            },
          },
        ]}
      />
      <FormDialog
        open={deciding !== null}
        onOpenChange={(o) => !o && setDeciding(null)}
        title={deciding?.action === "approve" ? "Approve request" : "Reject request"}
        description={deciding?.approval.summary}
        submitLabel={deciding?.action === "approve" ? "Approve" : "Reject"}
        busy={decide.isPending}
        onSubmit={() =>
          deciding && decide.mutate({ id: deciding.approval.id, action: deciding.action, note })
        }
      >
        <Field label="Note (optional)">
          <Input value={note} onChange={(e) => setNote(e.target.value)} />
        </Field>
      </FormDialog>
    </div>
  );
}
