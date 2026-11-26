import { useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { AlertCircle, Plus, Search, Trash2 } from "lucide-react";
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
  FilterBar,
  FormDialog,
  NativeSelect,
  PageHeader,
  SegmentedChips,
  StatusBadge,
} from "@/components/shared/kit";
import { PageLoader, StatCard } from "@/components/shared/Display";
import { useAuth } from "@/features/auth/auth";
import { LinesEditor, LinesTable } from "@/features/services/LinesEditor";
import { blankLine, linesBody, linesFrom, type LineDraft } from "@/features/services/lines";
import { ClientSelect, PaymentDialog, PdfButton, ReasonDialog } from "@/features/services/shared";
import { label } from "@/features/services/helpers";
import { useGet, useWrite } from "@/hooks/useApi";
import { formatDate, formatMoney, toNumber } from "@/lib/formatters";
import { cn } from "@/lib/utils";
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
              <p className="od-caption od-tabular">Planned {formatMoney(planned)}</p>
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
            <div className="flex flex-wrap items-center gap-3 text-[13px]">
              <StatusBadge value={i.status} />
              {i.due_date && <span className="od-caption">Due {formatDate(i.due_date)}</span>}
              {i.project_number && (
                <span className="od-mono od-caption">Project {i.project_number}</span>
              )}
            </div>
            <LinesTable lines={i.lines} />
            <div className="ml-auto w-full max-w-xs space-y-1.5 rounded-[var(--od-radius-md,12px)] bg-[var(--od-surface-subtle)] p-4 text-[13px]">
              <p className="flex justify-between text-[var(--od-ink-secondary)]">
                <span>Subtotal</span>
                <span className="od-tabular">{formatMoney(i.subtotal)}</span>
              </p>
              {toNumber(i.discount) > 0 && (
                <p className="flex justify-between text-[var(--od-ink-secondary)]">
                  <span>Discount</span>
                  <span className="od-tabular">{formatMoney(i.discount)}</span>
                </p>
              )}
              <p className="flex justify-between border-t border-[var(--od-border)] pt-1.5 font-semibold">
                <span>Total</span>
                <span className="od-tabular">{formatMoney(i.total)}</span>
              </p>
              <p className="flex justify-between text-[var(--od-ink-secondary)]">
                <span>Paid</span>
                <span className="od-tabular">{formatMoney(i.paid_total)}</span>
              </p>
              <p className="flex justify-between font-semibold">
                <span>Balance</span>
                <span
                  className={cn("od-tabular", toNumber(i.balance) > 0 && "text-[var(--od-bad)]")}
                >
                  {formatMoney(i.balance)}
                </span>
              </p>
            </div>
            {i.installments.length > 0 && (
              <div className="space-y-2">
                <h3 className="od-card-title">Installments</h3>
                <DataGrid
                  rows={i.installments}
                  rowKey={(x) => x.id ?? x.due_date}
                  columns={[
                    { header: "Due", cell: (x) => formatDate(x.due_date) },
                    { header: "Amount", align: "right", cell: (x) => formatMoney(x.amount) },
                    { header: "Paid", align: "right", cell: (x) => formatMoney(x.paid ?? 0) },
                    {
                      header: "Status",
                      cell: (x) => (x.status ? <StatusBadge value={x.status} /> : "—"),
                    },
                  ]}
                />
              </div>
            )}
            {i.payments.length > 0 && (
              <div className="space-y-2">
                <h3 className="od-card-title">Payments</h3>
                <DataGrid
                  rows={i.payments}
                  rowKey={(x) => x.id}
                  columns={[
                    {
                      header: "Receipt",
                      cell: (x) => <span className="od-mono font-medium">{x.number}</span>,
                    },
                    { header: "Date", cell: (x) => formatDate(x.date) },
                    { header: "Method", cell: (x) => label(x.method) },
                    { header: "Amount", align: "right", cell: (x) => formatMoney(x.amount) },
                  ]}
                />
              </div>
            )}
            {i.status === "void" && i.void_reason && (
              <p className="rounded-[var(--od-radius-sm,8px)] bg-[color-mix(in_srgb,var(--od-bad-mark)_10%,transparent)] p-3 text-[13px] font-medium text-[var(--od-bad)]">
                Voided: {i.void_reason}
              </p>
            )}
            <div className="flex flex-wrap justify-end gap-2 pt-1">
              <PdfButton
                size="sm"
                path={`/service-invoices/${id}/pdf`}
                filename={`${i.number}.pdf`}
              />
              {i.project_id && (
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => navigate(`/projects/${i.project_id}`)}
                >
                  Open project
                </Button>
              )}
              {canDraft && i.status === "draft" && (
                <Button size="sm" variant="outline" onClick={() => onEdit(i)}>
                  Edit
                </Button>
              )}
              {manage && i.status === "draft" && (
                <Button
                  size="sm"
                  onClick={() => issue.mutate(undefined)}
                  disabled={issue.isPending}
                >
                  Issue invoice
                </Button>
              )}
              {canReceive && open && (
                <Button size="sm" onClick={() => setPaying(true)}>
                  Record payment
                </Button>
              )}
              {manage && (i.status === "draft" || (open && toNumber(i.paid_total) === 0)) && (
                <Button
                  size="sm"
                  variant="ghost"
                  className="text-[var(--od-bad)] hover:text-[var(--od-bad)]"
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

  const rows = list.data ?? [];
  const totalBilled = rows.reduce((s, i) => s + (i.status === "void" ? 0 : toNumber(i.total)), 0);
  const totalBalance = rows.reduce(
    (s, i) => s + (i.status === "void" ? 0 : toNumber(i.balance)),
    0,
  );

  return (
    <div className="space-y-6">
      <PageHeader
        title="Invoices"
        description="Client billing, installment schedules and payment collection."
        actions={
          canAny("services.invoice.manage", "services.invoice.draft") && (
            <Button size="sm" onClick={() => setForm(blank)}>
              <Plus size={14} /> New invoice
            </Button>
          )
        }
      />

      <div className="grid gap-4 sm:grid-cols-3">
        <StatCard label="Invoices in view" value={rows.length} />
        <StatCard label="Total billed" value={formatMoney(totalBilled)} />
        <StatCard
          label="Still to collect"
          value={formatMoney(totalBalance)}
          tone={totalBalance > 0 ? "bad" : "good"}
        />
      </div>

      <FilterBar className="justify-between">
        <div className="flex flex-1 flex-wrap items-center gap-2.5">
          <div className="relative min-w-60 flex-1 sm:max-w-xs">
            <Search
              size={15}
              className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-[var(--od-ink-muted)]"
            />
            <Input
              aria-label="Search"
              className="pl-9"
              placeholder="Search number or client…"
              value={q}
              onChange={(e) => setQ(e.target.value)}
            />
          </div>
          <button
            type="button"
            onClick={() => setOverdue((v) => !v)}
            className={cn(
              "inline-flex h-9 cursor-pointer items-center gap-1.5 rounded-[var(--od-app-radius,10px)] border px-3 text-[13px] font-medium transition-colors",
              overdue
                ? "border-transparent bg-[color-mix(in_srgb,var(--od-bad-mark)_12%,transparent)] text-[var(--od-bad)]"
                : "border-[var(--od-border-strong)] bg-[var(--od-surface)] text-[var(--od-ink-secondary)] hover:text-[var(--od-ink)]",
            )}
          >
            <AlertCircle size={14} /> Overdue only
          </button>
        </div>
        <SegmentedChips
          ariaLabel="Invoice status"
          value={status}
          onChange={setStatus}
          options={[
            { value: "", label: "All" },
            { value: "draft", label: "Draft" },
            { value: "issued", label: "Issued" },
            { value: "partial", label: "Part paid" },
            { value: "paid", label: "Paid" },
            { value: "void", label: "Void" },
          ]}
        />
      </FilterBar>

      <DataGrid
        rows={rows}
        loading={list.isLoading}
        rowKey={(r) => r.id}
        empty="No invoices yet"
        onRowClick={(r) => setOpenId(r.id)}
        columns={[
          {
            header: "Number",
            sortValue: (r) => r.number,
            cell: (r) => <span className="od-mono font-medium">{r.number}</span>,
          },
          { header: "Client", sortValue: (r) => r.client_name, cell: (r) => r.client_name },
          {
            header: "Project",
            cell: (r) => (
              <span className="od-mono text-xs text-[var(--od-ink-secondary)]">
                {r.project_number ?? "—"}
              </span>
            ),
          },
          {
            header: "Due",
            sortValue: (r) => r.due_date ?? "",
            cell: (r) => (r.due_date ? formatDate(r.due_date) : "—"),
          },
          { header: "Status", cell: (r) => <StatusBadge value={r.status} /> },
          {
            header: "Total",
            align: "right",
            sortValue: (r) => toNumber(r.total),
            cell: (r) => formatMoney(r.total),
          },
          {
            header: "Paid",
            align: "right",
            sortValue: (r) => toNumber(r.paid_total),
            cell: (r) => formatMoney(r.paid_total),
          },
          {
            header: "Balance",
            align: "right",
            sortValue: (r) => toNumber(r.balance),
            cell: (r) => (
              <span
                className={
                  toNumber(r.balance) > 0 && r.status !== "void"
                    ? "font-semibold text-[var(--od-bad)]"
                    : undefined
                }
              >
                {formatMoney(r.balance)}
              </span>
            ),
          },
        ]}
      />

      {form && <InvoiceForm key={form.id ?? "new"} draft={form} onClose={() => setForm(null)} />}
      {openId && <Detail id={openId} onClose={() => setOpenId(null)} onEdit={edit} />}
    </div>
  );
}
