import { useState } from "react";
import { Plus } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  DataGrid,
  Field,
  FormDialog,
  NativeSelect,
  PageHeader,
  StatusBadge,
} from "@/components/shared/kit";
import { ConfirmDialog } from "@/components/shared/Display";
import { useAuth } from "@/features/auth/auth";
import { ClientSelect } from "@/features/services/shared";
import { useGet, useWrite } from "@/hooks/useApi";
import { formatDate, formatMoney, toNumber } from "@/lib/formatters";
import type { Renewal } from "@/types/services";

const blank = { client_id: "", item: "", due_date: "", amount: "", notes: "" };

export default function Renewals() {
  const { can } = useAuth();
  const manage = can("renewals.manage");
  const [status, setStatus] = useState("pending");
  const [within, setWithin] = useState("");
  const renewals = useGet<Renewal[]>("/renewals", { status, within_days: within, per_page: 100 });
  const [adding, setAdding] = useState(false);
  const [draft, setDraft] = useState(blank);
  const [renewing, setRenewing] = useState<Renewal | null>(null);
  const [next, setNext] = useState("");
  const [cancelling, setCancelling] = useState<Renewal | null>(null);

  const refresh = ["/renewals", "/services"];
  const add = useWrite("POST", "/renewals", {
    invalidate: refresh,
    success: "Renewal added. Reminders go out at 30 and 7 days.",
    onSuccess: () => {
      setAdding(false);
      setDraft(blank);
    },
  });
  const renew = useWrite<{ id: string; next_due_date?: string }>(
    "POST",
    (b) => `/renewals/${b.id}/renew`,
    {
      invalidate: refresh,
      success: "Marked as renewed",
      toBody: (b) => ({ next_due_date: b.next_due_date }),
      onSuccess: () => {
        setRenewing(null);
        setNext("");
      },
    },
  );
  const cancel = useWrite<{ id: string }>("POST", (b) => `/renewals/${b.id}/cancel`, {
    invalidate: refresh,
    success: "Renewal cancelled",
    toBody: () => undefined,
  });
  const remind = useWrite<{ id: string }, { delivered?: boolean }>(
    "POST",
    (b) => `/renewals/${b.id}/remind`,
    {
      invalidate: refresh,
      toBody: () => undefined,
      onSuccess: (r) =>
        r?.delivered === false
          ? toast.warning("Reminder recorded. No SMS provider is set up, so it was not sent.")
          : toast.success("Reminder sent to the client"),
    },
  );

  return (
    <>
      <PageHeader
        title="Renewals"
        description="Licences and annual fees coming due. Clients are reminded 30 and 7 days before."
        actions={
          manage && (
            <Button onClick={() => setAdding(true)}>
              <Plus size={16} /> New renewal
            </Button>
          )
        }
      />
      <div className="mb-4 flex flex-wrap gap-3">
        <NativeSelect
          aria-label="Status"
          className="w-40"
          value={status}
          onChange={(e) => setStatus(e.target.value)}
        >
          <option value="pending">Pending</option>
          <option value="renewed">Renewed</option>
          <option value="cancelled">Cancelled</option>
          <option value="">All</option>
        </NativeSelect>
        <NativeSelect
          aria-label="Due within"
          className="w-44"
          value={within}
          onChange={(e) => setWithin(e.target.value)}
        >
          <option value="">Any date</option>
          <option value="7">Next 7 days</option>
          <option value="30">Next 30 days</option>
          <option value="90">Next 90 days</option>
        </NativeSelect>
      </div>
      <DataGrid
        rows={renewals.data}
        loading={renewals.isLoading}
        rowKey={(r) => r.id}
        empty="Nothing to renew"
        columns={[
          { header: "Client", cell: (r) => <span className="font-medium">{r.client_name}</span> },
          { header: "Item", cell: (r) => r.item },
          { header: "Due", cell: (r) => formatDate(r.due_date) },
          {
            header: "Days left",
            align: "right",
            cell: (r) =>
              r.status !== "pending" ? (
                "-"
              ) : (
                <span
                  className={
                    r.days_left < 0
                      ? "font-medium text-destructive"
                      : r.days_left <= 7
                        ? "font-medium text-amber-600"
                        : ""
                  }
                >
                  {r.days_left < 0 ? `${-r.days_left} late` : r.days_left}
                </span>
              ),
          },
          {
            header: "Fee",
            align: "right",
            cell: (r) => (toNumber(r.amount) > 0 ? formatMoney(r.amount) : "-"),
          },
          { header: "Status", cell: (r) => <StatusBadge value={r.status} /> },
          {
            header: "",
            className: "text-right",
            cell: (r) =>
              manage && r.status === "pending" ? (
                <div className="flex justify-end gap-2">
                  <Button size="sm" variant="outline" onClick={() => remind.mutate({ id: r.id })}>
                    Remind
                  </Button>
                  <Button size="sm" variant="outline" onClick={() => setRenewing(r)}>
                    Renewed
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    className="text-destructive"
                    onClick={() => setCancelling(r)}
                  >
                    Cancel
                  </Button>
                </div>
              ) : null,
          },
        ]}
      />
      <FormDialog
        open={adding}
        onOpenChange={setAdding}
        title="New renewal"
        busy={add.isPending}
        onSubmit={() =>
          add.mutate({
            client_id: draft.client_id,
            item: draft.item,
            due_date: draft.due_date,
            amount: draft.amount || undefined,
            notes: draft.notes || undefined,
          })
        }
      >
        <Field label="Client *">
          <ClientSelect
            required
            value={draft.client_id}
            onChange={(v) => setDraft({ ...draft, client_id: v })}
          />
        </Field>
        <Field label="What is renewing? *">
          <Input
            required
            placeholder="Business licence, annual return…"
            value={draft.item}
            onChange={(e) => setDraft({ ...draft, item: e.target.value })}
          />
        </Field>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Due date *">
            <Input
              required
              type="date"
              value={draft.due_date}
              onChange={(e) => setDraft({ ...draft, due_date: e.target.value })}
            />
          </Field>
          <Field label="Fee">
            <Input
              inputMode="decimal"
              value={draft.amount}
              onChange={(e) => setDraft({ ...draft, amount: e.target.value })}
            />
          </Field>
        </div>
        <Field label="Notes">
          <Input
            value={draft.notes}
            onChange={(e) => setDraft({ ...draft, notes: e.target.value })}
          />
        </Field>
      </FormDialog>
      <FormDialog
        open={!!renewing}
        onOpenChange={(o) => !o && setRenewing(null)}
        title={`Renewed: ${renewing?.item ?? ""}`}
        description="Set the next due date to schedule the following renewal, for example next year."
        submitLabel="Mark renewed"
        busy={renew.isPending}
        onSubmit={() =>
          renewing && renew.mutate({ id: renewing.id, next_due_date: next || undefined })
        }
      >
        <Field label="Next due date (optional)">
          <Input type="date" value={next} onChange={(e) => setNext(e.target.value)} />
        </Field>
      </FormDialog>
      <ConfirmDialog
        open={!!cancelling}
        onOpenChange={(o) => !o && setCancelling(null)}
        title="Cancel this renewal?"
        description="The client will not be reminded. Use this if they no longer need it."
        onConfirm={() => cancelling && cancel.mutate({ id: cancelling.id })}
      />
    </>
  );
}
