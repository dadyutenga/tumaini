import { useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
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
import { LinesEditor, LinesTable } from "@/features/services/LinesEditor";
import { blankLine, linesBody, linesFrom, type LineDraft } from "@/features/services/lines";
import { ClientSelect, PaymentDialog, PdfButton, ReasonDialog } from "@/features/services/shared";
import { label } from "@/features/services/helpers";
import { useGet, useWrite } from "@/hooks/useApi";
import { formatDate, formatMoney, toNumber } from "@/lib/formatters";
import type { InvoiceDetail, InvoiceRow, ProjectRow } from "@/types/services";

interface Draft {
  id?: string;
  client_id: string;
  project_id: string;
  due_date: string;
  discount: string;
  notes: string;
  lines: LineDraft[];
  installments: { due_date: string; amount: string }[];
}

const blank: Draft = {
  client_id: "",
  project_id: "",
  due_date: "",
  discount: "0",
  notes: "",
  lines: [{ ...blankLine }],
  installments: [],
};

function InvoiceForm({ draft, onClose }: { draft: Draft; onClose: () => void }) {
  const [d, setD] = useState(draft);
  const projects = useGet<ProjectRow[]>(
    "/projects",
    { client_id: d.client_id, per_page: 100 },
    { enabled: !!d.client_id },
  );
  const body = () => ({
    client_id: d.client_id,
    project_id: d.project_id || undefined,
    due_date: d.due_date || undefined,
    discount: d.discount || "0",
    notes: d.notes || undefined,
    lines: linesBody(d.lines),
    installments: d.installments.filter((i) => i.due_date && i.amount),
  });
  const create = useWrite("POST", "/service-invoices", {
    invalidate: ["/service-invoices"],
    success: "Invoice saved as a draft",
    onSuccess: onClose,
  });
  const edit = useWrite("PUT", `/service-invoices/${d.id}`, {
    invalidate: ["/service-invoices"],
    success: "Invoice updated",
    onSuccess: onClose,
  });
  const planned = d.installments.reduce((s, i) => s + toNumber(i.amount), 0);
  return (
    <FormDialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={d.id ? "Edit invoice" : "New invoice"}
      description="Saved as a draft. Nothing is booked until you issue it."
      busy={create.isPending || edit.isPending}
      onSubmit={() => (d.id ? edit.mutate(body()) : create.mutate(body()))}
      wide
    >
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Client *">
          <ClientSelect
            required
            value={d.client_id}
            onChange={(v) => setD({ ...d, client_id: v, project_id: "" })}
          />
        </Field>
        <Field label="Project (optional)">
          <NativeSelect
            value={d.project_id}
            onChange={(e) => setD({ ...d, project_id: e.target.value })}
          >
            <option value="">No project</option>
            {projects.data?.map((p) => (
              <option key={p.id} value={p.id}>
                {p.number} · {p.name}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field label="Due date">
          <Input
            type="date"
            value={d.due_date}
            onChange={(e) => setD({ ...d, due_date: e.target.value })}
          />
        </Field>
        <Field label="Discount">
          <Input
            inputMode="decimal"
            value={d.discount}
            onChange={(e) => setD({ ...d, discount: e.target.value })}
          />
        </Field>
      </div>
      <LinesEditor lines={d.lines} onChange={(lines) => setD({ ...d, lines })} />
      <Field label="Installment plan (optional)">
        <div className="space-y-2">
          {d.installments.map((inst, i) => (
            <div key={i} className="grid grid-cols-[1fr_1fr_auto] gap-2">
              <Input
                type="date"
                aria-label="Installment due date"
                required
                value={inst.due_date}
                onChange={(e) =>
                  setD({
                    ...d,
                    installments: d.installments.map((x, j) =>
                      j === i ? { ...x, due_date: e.target.value } : x,
                    ),
                  })
                }
              />
              <Input
                aria-label="Installment amount"
                inputMode="decimal"
                placeholder="Amount"
                required
                value={inst.amount}
                onChange={(e) =>
                  setD({
                    ...d,
                    installments: d.installments.map((x, j) =>
                      j === i ? { ...x, amount: e.target.value } : x,
                    ),
                  })
                }
              />
              <Button
                type="button"
                variant="ghost"
                size="icon"
                aria-label="Remove installment"
                onClick={() =>
                  setD({ ...d, installments: d.installments.filter((_, j) => j !== i) })
                }
              >
                <Trash2 size={14} />
              </Button>
            </div>
          ))}
          <div className="flex items-center justify-between">
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() =>
                setD({ ...d, installments: [...d.installments, { due_date: "", amount: "" }] })
              }
            >
              <Plus size={14} /> Add installment
            </Button>
            {d.installments.length > 0 && (
              <p className="text-sm text-muted-foreground">Planned {formatMoney(planned)}</p>
            )}
          </div>
        </div>
      </Field>
      <Field label="Notes">
        <Textarea
          rows={2}
          value={d.notes}
          onChange={(e) => setD({ ...d, notes: e.target.value })}
        />
      </Field>
    </FormDialog>
  );
}

function Detail({
  id,
  onClose,
  onEdit,
}: {
  id: string;
  onClose: () => void;
  onEdit: (i: InvoiceDetail) => void;
}) {
  const { can } = useAuth();
  const navigate = useNavigate();
  const invoice = useGet<InvoiceDetail>(`/service-invoices/${id}`);
  const [paying, setPaying] = useState(false);
  const [voiding, setVoiding] = useState(false);
  const refresh = ["/service-invoices", "/service-payments", "/projects", "/clients", "/services"];
  const issue = useWrite("POST", `/service-invoices/${id}/issue`, {
    invalidate: refresh,
    success: "Invoice issued. Deposits already paid were applied.",
  });
  const voidInvoice = useWrite<{ reason: string }>("POST", `/service-invoices/${id}/void`, {
    invalidate: refresh,
    success: "Invoice voided",
    onSuccess: () => setVoiding(false),
  });
  const i = invoice.data;
  const manage = can("services.invoice.manage");
  const canDraft = manage || can("services.invoice.draft");
  const canReceive = manage || can("finance.payment.receive");
  const open = i && (i.status === "issued" || i.status === "partial");
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{i ? `${i.number} · ${i.client_name}` : "Invoice"}</DialogTitle>
          <DialogDescription className="sr-only">Invoice details</DialogDescription>
        </DialogHeader>
        {!i ? (
          <PageLoader />
        ) : (
          <div className="space-y-4">
            <div className="flex flex-wrap items-center gap-3 text-sm">
              <StatusBadge value={i.status} />
              {i.due_date && (
                <span className="text-muted-foreground">Due {formatDate(i.due_date)}</span>
              )}
              {i.project_number && (
                <span className="text-muted-foreground">Project {i.project_number}</span>
              )}
            </div>
            <LinesTable lines={i.lines} />
            <div className="ml-auto w-60 space-y-1 text-sm">
              <p className="flex justify-between">
                <span>Subtotal</span> <span>{formatMoney(i.subtotal)}</span>
              </p>
              {toNumber(i.discount) > 0 && (
                <p className="flex justify-between">
                  <span>Discount</span> <span>{formatMoney(i.discount)}</span>
                </p>
              )}
              <p className="flex justify-between font-semibold">
                <span>Total</span> <span>{formatMoney(i.total)}</span>
              </p>
              <p className="flex justify-between">
                <span>Paid</span> <span>{formatMoney(i.paid_total)}</span>
              </p>
              <p className="flex justify-between font-semibold">
                <span>Balance</span>
                <span className={toNumber(i.balance) > 0 ? "text-destructive" : ""}>
                  {formatMoney(i.balance)}
                </span>
              </p>
            </div>
            {i.installments.length > 0 && (
              <div>
                <h3 className="mb-1 text-sm font-semibold">Installments</h3>
                <DataGrid
                  rows={i.installments}
                  rowKey={(x) => x.id ?? x.due_date}
                  columns={[
                    { header: "Due", cell: (x) => formatDate(x.due_date) },
                    { header: "Amount", align: "right", cell: (x) => formatMoney(x.amount) },
                    { header: "Paid", align: "right", cell: (x) => formatMoney(x.paid ?? 0) },
                    {
                      header: "Status",
                      cell: (x) => (x.status ? <StatusBadge value={x.status} /> : "-"),
                    },
                  ]}
                />
              </div>
            )}
            {i.payments.length > 0 && (
              <div>
                <h3 className="mb-1 text-sm font-semibold">Payments</h3>
                <DataGrid
                  rows={i.payments}
                  rowKey={(x) => x.id}
                  columns={[
                    { header: "Receipt", cell: (x) => x.number },
                    { header: "Date", cell: (x) => formatDate(x.date) },
                    { header: "Method", cell: (x) => label(x.method) },
                    { header: "Amount", align: "right", cell: (x) => formatMoney(x.amount) },
                  ]}
                />
              </div>
            )}
            {i.status === "void" && i.void_reason && (
              <p className="rounded-xl bg-red-500/10 p-3 text-sm text-red-700 dark:text-red-400">
                Voided: {i.void_reason}
              </p>
            )}
            <div className="flex flex-wrap justify-end gap-2">
              <PdfButton path={`/service-invoices/${id}/pdf`} filename={`${i.number}.pdf`} />
              {i.project_id && (
                <Button variant="outline" onClick={() => navigate(`/projects/${i.project_id}`)}>
                  Open project
                </Button>
              )}
              {canDraft && i.status === "draft" && (
                <Button variant="outline" onClick={() => onEdit(i)}>
                  Edit
                </Button>
              )}
              {manage && i.status === "draft" && (
                <Button onClick={() => issue.mutate(undefined)} disabled={issue.isPending}>
                  Issue invoice
                </Button>
              )}
              {canReceive && open && (
                <Button onClick={() => setPaying(true)}>Record payment</Button>
              )}
              {manage && (i.status === "draft" || (open && toNumber(i.paid_total) === 0)) && (
                <Button
                  variant="ghost"
                  className="text-destructive"
                  onClick={() => setVoiding(true)}
                >
                  Void
                </Button>
              )}
            </div>
          </div>
        )}
        <PaymentDialog
          open={paying}
          onOpenChange={setPaying}
          title="Record payment"
          hint="You cannot take more than the balance."
          path={`/service-invoices/${id}/payments`}
          defaultAmount={i?.balance}
          invalidate={refresh}
        />
        <ReasonDialog
          open={voiding}
          onOpenChange={setVoiding}
          title="Void this invoice?"
          description="An issued invoice reverses the amount the client owed. This cannot be undone."
          submitLabel="Void invoice"
          busy={voidInvoice.isPending}
          onSubmit={(reason) => voidInvoice.mutate({ reason })}
        />
      </DialogContent>
    </Dialog>
  );
}

export default function ServiceInvoices() {
  const { canAny } = useAuth();
  const [params, setParams] = useSearchParams();
  const [status, setStatus] = useState("");
  const [overdue, setOverdue] = useState(false);
  const [q, setQ] = useState("");
  const list = useGet<InvoiceRow[]>("/service-invoices", {
    status,
    q,
    overdue: overdue || undefined,
    per_page: 100,
  });
  const [form, setForm] = useState<Draft | null>(null);
  const openId = params.get("open");
  const setOpenId = (id: string | null) => setParams(id ? { open: id } : {}, { replace: true });

  function edit(inv: InvoiceDetail) {
    setOpenId(null);
    setForm({
      id: inv.id,
      client_id: inv.client_id,
      project_id: inv.project_id ?? "",
      due_date: inv.due_date ?? "",
      discount: inv.discount,
      notes: inv.notes ?? "",
      lines: linesFrom(inv.lines),
      installments: inv.installments.map((x) => ({ due_date: x.due_date, amount: x.amount })),
    });
  }

  const total = (list.data ?? []).reduce(
    (s, i) => s + (i.status === "void" ? 0 : toNumber(i.balance)),
    0,
  );

  return (
    <>
      <PageHeader
        title="Invoices"
        description={
          list.data ? `${formatMoney(total)} still to collect on these invoices.` : undefined
        }
        actions={
          canAny("services.invoice.manage", "services.invoice.draft") && (
            <Button onClick={() => setForm(blank)}>
              <Plus size={16} /> New invoice
            </Button>
          )
        }
      />
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <Input
          aria-label="Search"
          className="max-w-xs"
          placeholder="Search number or client…"
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
        <NativeSelect
          aria-label="Status"
          className="w-40"
          value={status}
          onChange={(e) => setStatus(e.target.value)}
        >
          <option value="">All</option>
          <option value="draft">Draft</option>
          <option value="issued">Issued</option>
          <option value="partial">Part paid</option>
          <option value="paid">Paid</option>
          <option value="void">Void</option>
        </NativeSelect>
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={overdue} onChange={(e) => setOverdue(e.target.checked)} />
          Overdue only
        </label>
      </div>
      <DataGrid
        rows={list.data}
        loading={list.isLoading}
        rowKey={(r) => r.id}
        empty="No invoices yet"
        onRowClick={(r) => setOpenId(r.id)}
        columns={[
          { header: "Number", cell: (r) => <span className="font-medium">{r.number}</span> },
          { header: "Client", cell: (r) => r.client_name },
          { header: "Project", cell: (r) => r.project_number ?? "-" },
          { header: "Due", cell: (r) => (r.due_date ? formatDate(r.due_date) : "-") },
          { header: "Status", cell: (r) => <StatusBadge value={r.status} /> },
          { header: "Total", align: "right", cell: (r) => formatMoney(r.total) },
          { header: "Paid", align: "right", cell: (r) => formatMoney(r.paid_total) },
          { header: "Balance", align: "right", cell: (r) => formatMoney(r.balance) },
        ]}
      />
      {form && <InvoiceForm key={form.id ?? "new"} draft={form} onClose={() => setForm(null)} />}
      {openId && <Detail id={openId} onClose={() => setOpenId(null)} onEdit={edit} />}
    </>
  );
}
