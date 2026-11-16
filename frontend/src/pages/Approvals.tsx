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

export default function Approvals() {
  const { can, user } = useAuth();
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
            cell: (a) =>
              a.status === "pending" && can("approvals.decide") && a.requested_by !== user?.id ? (
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
              ) : a.status === "pending" && a.requested_by === user?.id ? (
                <span className="text-xs text-muted-foreground">Your request</span>
              ) : null,
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
