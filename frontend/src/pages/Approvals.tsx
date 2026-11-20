import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  DataGrid,
  FormDialog,
  Field,
  NativeSelect,
  PageHeader,
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
  service_price_change: "services.price.approve",
  expense: "finance.expense.approve",
  payment_void: "finance.payment.void",
  transaction_correction: "finance.transaction.correct",
};

const ANY_APPROVAL_PERMS = Object.values(APPROVAL_PERMISSION);

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
    <>
      <PageHeader
        title="Approvals"
        description="Requests that went over someone's limit."
        actions={
          <NativeSelect
            aria-label="Filter by status"
            className="w-40"
            value={status}
            onChange={(e) => setStatus(e.target.value)}
          >
            <option value="pending">Pending</option>
            <option value="approved">Approved</option>
            <option value="rejected">Rejected</option>
            <option value="">All</option>
          </NativeSelect>
        }
      />
      <DataGrid
        rows={data}
        loading={isLoading}
        rowKey={(a) => a.id}
        empty="Nothing waiting for approval"
        columns={[
          { header: "Requested", cell: (a) => formatDateTime(a.created_at) },
          {
            header: "Kind",
            cell: (a) => <span className="capitalize">{a.kind.replaceAll("_", " ")}</span>,
          },
          { header: "Summary", cell: (a) => a.summary },
          {
            header: "Amount",
            align: "right",
            cell: (a) => (toNumber(a.amount) ? formatMoney(a.amount) : "-"),
          },
          { header: "Status", cell: (a) => <StatusBadge value={a.status} /> },
          {
            header: "",
            cell: (a) => {
              const isOwn =
                a.requested_by === user?.id ||
                (Boolean(user?.membership_id) && a.requested_by === user?.membership_id);
              return a.status === "pending" && canDecideKind(a.kind) && !isOwn ? (
                <div className="flex gap-2">
                  <Button size="sm" onClick={() => setDeciding({ approval: a, action: "approve" })}>
                    Approve
                  </Button>
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => setDeciding({ approval: a, action: "reject" })}
                  >
                    Reject
                  </Button>
                </div>
              ) : a.status === "pending" && isOwn ? (
                <span className="text-xs text-muted-foreground">Your request</span>
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
    </>
  );
}
