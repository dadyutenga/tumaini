import { useState } from "react";
import { Download, Plus } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { DataGrid, Field, FormDialog, PageHeader, StatusBadge } from "@/components/shared/kit";
import { PageLoader } from "@/components/shared/Display";
import { useAuth } from "@/features/auth/auth";
import { PaymentDialog } from "@/features/services/shared";
import { errorMessage, useGet, useWrite } from "@/hooks/useApi";
import { download } from "@/lib/api";
import { formatDate, formatMoney, toNumber } from "@/lib/formatters";
import type { Contractor, ContractorStatement } from "@/types/services";

const blank = { name: "", trade: "", phone: "", email: "", address: "", notes: "" };

function Statement({ id, onClose }: { id: string; onClose: () => void }) {
  const { can } = useAuth();
  const statement = useGet<ContractorStatement>(`/contractors/${id}/statement`);
  const [paying, setPaying] = useState(false);
  const s = statement.data;
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{s ? `${s.contractor.name} · statement` : "Statement"}</DialogTitle>
          <DialogDescription>
            {s
              ? `${s.contractor.trade ?? "Contractor"}${s.contractor.phone ? ` · ${s.contractor.phone}` : ""}`
              : ""}
          </DialogDescription>
        </DialogHeader>
        {!s ? (
          <PageLoader />
        ) : (
          <div className="space-y-4">
            <DataGrid
              rows={s.lines}
              rowKey={(l) => `${l.number}-${l.date}-${l.owed_amount}-${l.paid_amount}`}
              empty="No work or payments yet"
              columns={[
                { header: "Date", cell: (l) => formatDate(l.date) },
                { header: "Reference", cell: (l) => l.number },
                { header: "Description", cell: (l) => l.description },
                {
                  header: "Work",
                  align: "right",
                  cell: (l) => (toNumber(l.owed_amount) ? formatMoney(l.owed_amount) : "-"),
                },
                {
                  header: "Paid",
                  align: "right",
                  cell: (l) => (toNumber(l.paid_amount) ? formatMoney(l.paid_amount) : "-"),
                },
                { header: "Balance", align: "right", cell: (l) => formatMoney(l.balance) },
              ]}
            />
            <div className="flex flex-wrap items-center justify-between gap-2">
              <p className="text-sm">
                We owe <strong>{formatMoney(s.owed)}</strong>
              </p>
              <div className="flex gap-2">
                <Button
                  variant="outline"
                  onClick={() =>
                    download(
                      `/contractors/${id}/statement`,
                      { format: "pdf" },
                      `contractor-statement-${s.contractor.name}.pdf`,
                    ).catch((e) => toast.error(errorMessage(e)))
                  }
                >
                  <Download size={14} /> Statement PDF
                </Button>
                {can("contractors.manage") && toNumber(s.owed) > 0 && (
                  <Button onClick={() => setPaying(true)}>Pay contractor</Button>
                )}
              </div>
            </div>
          </div>
        )}
        <PaymentDialog
          open={paying}
          onOpenChange={setPaying}
          title="Pay contractor"
          hint="Pays the oldest work first. You cannot pay more than is owed."
          path={`/contractors/${id}/payments`}
          defaultAmount={s?.owed}
          invalidate={["/contractors", "/service-costs", "/services", "/money"]}
          success="Contractor paid"
        />
      </DialogContent>
    </Dialog>
  );
}

export default function Contractors() {
  const { can } = useAuth();
  const manage = can("contractors.manage");
  const [q, setQ] = useState("");
  const list = useGet<Contractor[]>("/contractors", { q, all: manage || undefined, per_page: 100 });
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState<Contractor | null>(null);
  const [draft, setDraft] = useState(blank);
  const [viewing, setViewing] = useState<string | null>(null);
  const add = useWrite("POST", "/contractors", {
    invalidate: ["/contractors"],
    success: "Contractor added",
    onSuccess: () => {
      setOpen(false);
      setDraft(blank);
    },
  });
  const save = useWrite("PUT", () => `/contractors/${editing?.id}`, {
    invalidate: ["/contractors"],
    success: "Contractor updated",
    onSuccess: () => {
      setEditing(null);
      setDraft(blank);
    },
  });
  const toggle = useWrite<{ id: string; active: boolean }>(
    "POST",
    (b) => `/contractors/${b.id}/${b.active ? "activate" : "deactivate"}`,
    { invalidate: ["/contractors"], success: "Contractor updated", toBody: () => undefined },
  );
  const owed = (list.data ?? []).reduce((s, c) => s + toNumber(c.owed), 0);
  const set = (k: keyof typeof blank) => (e: { target: { value: string } }) =>
    setDraft({ ...draft, [k]: e.target.value });

  return (
    <>
      <PageHeader
        title="Contractors"
        description={list.data ? `You owe contractors ${formatMoney(owed)} in total.` : undefined}
        actions={
          manage && (
            <Button onClick={() => setOpen(true)}>
              <Plus size={16} /> New contractor
            </Button>
          )
        }
      />
      <Input
        aria-label="Search contractors"
        className="mb-4 max-w-xs"
        placeholder="Search name or trade…"
        value={q}
        onChange={(e) => setQ(e.target.value)}
      />
      <DataGrid
        rows={list.data}
        loading={list.isLoading}
        rowKey={(c) => c.id}
        empty="No contractors yet. Add the printers and tradespeople you use."
        onRowClick={(c) => setViewing(c.id)}
        columns={[
          { header: "Name", cell: (c) => <span className="font-medium">{c.name}</span> },
          { header: "Trade", cell: (c) => c.trade ?? "-" },
          { header: "Phone", cell: (c) => c.phone ?? "-" },
          {
            header: "Status",
            cell: (c) => <StatusBadge value={c.is_active ? "active" : "inactive"} />,
          },
          {
            header: "We owe",
            align: "right",
            cell: (c) => (
              <span className={toNumber(c.owed) > 0 ? "font-medium text-destructive" : ""}>
                {formatMoney(c.owed)}
              </span>
            ),
          },
          {
            header: "",
            className: "text-right",
            cell: (c) =>
              manage ? (
                <div className="flex justify-end gap-1" onClick={(e) => e.stopPropagation()}>
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() => {
                      setDraft({
                        name: c.name,
                        trade: c.trade ?? "",
                        phone: c.phone ?? "",
                        email: c.email ?? "",
                        address: c.address ?? "",
                        notes: c.notes ?? "",
                      });
                      setEditing(c);
                    }}
                  >
                    Edit
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() => toggle.mutate({ id: c.id, active: !c.is_active })}
                  >
                    {c.is_active ? "Deactivate" : "Activate"}
                  </Button>
                </div>
              ) : null,
          },
        ]}
      />
      <FormDialog
        open={open || editing !== null}
        onOpenChange={(o) => {
          if (!o) {
            setOpen(false);
            setEditing(null);
            setDraft(blank);
          }
        }}
        title={editing ? "Edit contractor" : "New contractor"}
        busy={add.isPending || save.isPending}
        onSubmit={() => (editing ? save.mutate(draft) : add.mutate(draft))}
      >
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Name *">
            <Input required value={draft.name} onChange={set("name")} />
          </Field>
          <Field label="Trade">
            <Input
              placeholder="Printing, painting, carpentry…"
              value={draft.trade}
              onChange={set("trade")}
            />
          </Field>
          <Field label="Phone">
            <Input inputMode="tel" value={draft.phone} onChange={set("phone")} />
          </Field>
          <Field label="Email">
            <Input type="email" value={draft.email} onChange={set("email")} />
          </Field>
          <Field label="Address" className="sm:col-span-2">
            <Input value={draft.address} onChange={set("address")} />
          </Field>
        </div>
      </FormDialog>
      {viewing && <Statement id={viewing} onClose={() => setViewing(null)} />}
    </>
  );
}
