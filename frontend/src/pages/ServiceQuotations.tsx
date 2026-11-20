import { useEffect, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { Plus } from "lucide-react";
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
import { ConfirmDialog, PageLoader } from "@/components/shared/Display";
import { useAuth } from "@/features/auth/auth";
import { LinesEditor, LinesTable } from "@/features/services/LinesEditor";
import { blankLine, linesBody, linesFrom, type LineDraft } from "@/features/services/lines";
import { ClientSelect, PdfButton } from "@/features/services/shared";
import { useGet, useWrite } from "@/hooks/useApi";
import { formatDate, formatMoney, toNumber } from "@/lib/formatters";
import type { CatalogItem, Quotation } from "@/types/services";

interface Draft {
  id?: string;
  client_id: string;
  valid_until: string;
  discount: string;
  notes: string;
  lines: LineDraft[];
}

const blank: Draft = {
  client_id: "",
  valid_until: "",
  discount: "0",
  notes: "",
  lines: [{ ...blankLine }],
};

function QuotationForm({ draft, onClose }: { draft: Draft; onClose: () => void }) {
  const [d, setD] = useState(draft);
  const body = () => ({
    client_id: d.client_id,
    valid_until: d.valid_until || undefined,
    discount: d.discount || "0",
    notes: d.notes || undefined,
    lines: linesBody(d.lines),
  });
  const create = useWrite("POST", "/service-quotations", {
    invalidate: ["/service-quotations"],
    success: "Quotation saved as a draft",
    onSuccess: onClose,
  });
  const edit = useWrite("PUT", `/service-quotations/${d.id}`, {
    invalidate: ["/service-quotations"],
    success: "Quotation updated",
    onSuccess: onClose,
  });
  return (
    <FormDialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={d.id ? "Edit quotation" : "New quotation"}
      busy={create.isPending || edit.isPending}
      onSubmit={() => (d.id ? edit.mutate(body()) : create.mutate(body()))}
      wide
    >
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Client *" className="sm:col-span-2">
          <ClientSelect
            required
            value={d.client_id}
            onChange={(v) => setD({ ...d, client_id: v })}
          />
        </Field>
        <Field label="Valid until">
          <Input
            type="date"
            value={d.valid_until}
            onChange={(e) => setD({ ...d, valid_until: e.target.value })}
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
      <Field label="Terms and notes">
        <Textarea
          rows={2}
          value={d.notes}
          onChange={(e) => setD({ ...d, notes: e.target.value })}
        />
      </Field>
    </FormDialog>
  );
}

function AcceptDialog({ quote, onClose }: { quote: Quotation; onClose: () => void }) {
  const navigate = useNavigate();
  const catalog = useGet<CatalogItem[]>("/service-catalog", { per_page: 100 });
  const [createProject, setCreateProject] = useState(true);
  const [serviceId, setServiceId] = useState("");
  const [name, setName] = useState("");
  const accept = useWrite<unknown, Quotation & { project_id: string | null }>(
    "POST",
    `/service-quotations/${quote.id}/accept`,
    {
      invalidate: [
        "/service-quotations",
        "/service-invoices",
        "/projects",
        "/clients",
        "/services",
      ],
      success: "Quotation accepted. The invoice is ready as a draft.",
      onSuccess: (r) => {
        onClose();
        if (r.project_id) navigate(`/projects/${r.project_id}`);
      },
    },
  );
  return (
    <FormDialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={`Accept ${quote.number}`}
      description="A draft invoice is created. You can also start the project now."
      submitLabel="Accept"
      busy={accept.isPending}
      onSubmit={() =>
        accept.mutate({
          create_project: createProject,
          service_id: createProject && serviceId ? serviceId : undefined,
          project_name: createProject && name ? name : undefined,
        })
      }
    >
      <label className="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          checked={createProject}
          onChange={(e) => setCreateProject(e.target.checked)}
        />
        Start a project for this client
      </label>
      {createProject && (
        <>
          <Field label="Service (gives the stages and documents)">
            <NativeSelect value={serviceId} onChange={(e) => setServiceId(e.target.value)}>
              <option value="">Custom project, no stages</option>
              {catalog.data
                ?.filter((s) => s.is_active)
                .map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
            </NativeSelect>
          </Field>
          <Field label="Project name">
            <Input value={name} placeholder="Optional" onChange={(e) => setName(e.target.value)} />
          </Field>
        </>
      )}
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
  onEdit: (q: Quotation) => void;
}) {
  const { can } = useAuth();
  const navigate = useNavigate();
  const quote = useGet<Quotation>(`/service-quotations/${id}`);
  const [accepting, setAccepting] = useState(false);
  const [rejecting, setRejecting] = useState(false);
  const refresh = ["/service-quotations"];
  const send = useWrite("POST", `/service-quotations/${id}/send`, {
    invalidate: refresh,
    success: "Marked as sent",
  });
  const reject = useWrite("POST", `/service-quotations/${id}/reject`, {
    invalidate: refresh,
    success: "Marked as rejected",
    onSuccess: () => setRejecting(false),
  });
  const q = quote.data;
  const manage = can("services.quotation.manage");
  const canDraft = can("services.quotation.manage") || can("services.quotation.draft");
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{q ? `${q.number} · ${q.client_name}` : "Quotation"}</DialogTitle>
          <DialogDescription className="sr-only">Quotation details</DialogDescription>
        </DialogHeader>
        {!q ? (
          <PageLoader />
        ) : (
          <div className="space-y-4">
            <div className="flex flex-wrap items-center gap-3 text-sm">
              <StatusBadge value={q.status} />
              {q.valid_until && (
                <span className="text-muted-foreground">
                  Valid until {formatDate(q.valid_until)}
                </span>
              )}
            </div>
            <LinesTable lines={q.lines ?? []} />
            <div className="text-right text-sm">
              {toNumber(q.discount) > 0 && <p>Discount {formatMoney(q.discount)}</p>}
              <p className="text-lg font-semibold">Total {formatMoney(q.total)}</p>
            </div>
            {q.notes && <p className="rounded-xl bg-muted/50 p-3 text-sm">{q.notes}</p>}
            <div className="flex flex-wrap justify-end gap-2">
              <PdfButton path={`/service-quotations/${id}/pdf`} filename={`${q.number}.pdf`} />
              {canDraft && q.status === "draft" && (
                <Button variant="outline" onClick={() => onEdit(q)}>
                  Edit
                </Button>
              )}
              {manage && q.status === "draft" && (
                <Button onClick={() => send.mutate(undefined)} disabled={send.isPending}>
                  Mark as sent
                </Button>
              )}
              {manage && (q.status === "draft" || q.status === "sent") && (
                <>
                  <Button variant="outline" onClick={() => setRejecting(true)}>
                    Rejected
                  </Button>
                  <Button onClick={() => setAccepting(true)}>Accepted</Button>
                </>
              )}
              {q.invoice_id && (
                <Button
                  variant="outline"
                  onClick={() => navigate(`/service-invoices?open=${q.invoice_id}`)}
                >
                  Open invoice
                </Button>
              )}
              {q.project_id && (
                <Button variant="outline" onClick={() => navigate(`/projects/${q.project_id}`)}>
                  Open project
                </Button>
              )}
            </div>
          </div>
        )}
        {accepting && q && <AcceptDialog quote={q} onClose={() => setAccepting(false)} />}
        <ConfirmDialog
          open={rejecting}
          onOpenChange={setRejecting}
          title="Mark this quotation as rejected?"
          description="The client said no. You can still create a new quotation for them."
          onConfirm={() => reject.mutate(undefined)}
        />
      </DialogContent>
    </Dialog>
  );
}

export default function ServiceQuotations() {
  const { canAny } = useAuth();
  const canCreate = canAny("services.quotation.manage", "services.quotation.draft");
  const [params, setParams] = useSearchParams();
  const [status, setStatus] = useState("");
  const [q, setQ] = useState("");
  const list = useGet<Quotation[]>("/service-quotations", { status, q, per_page: 100 });
  const [form, setForm] = useState<Draft | null>(null);
  const [openId, setOpenId] = useState<string | null>(null);

  // Arriving from a client page with ?client=: start a quotation for them.
  const preset = params.get("client");
  useEffect(() => {
    if (preset && canCreate) {
      setForm({ ...blank, client_id: preset });
      setParams({}, { replace: true });
    }
  }, [preset, canCreate, setParams]);

  async function edit(quote: Quotation) {
    setOpenId(null);
    setForm({
      id: quote.id,
      client_id: quote.client_id,
      valid_until: quote.valid_until ?? "",
      discount: quote.discount,
      notes: quote.notes ?? "",
      lines: linesFrom(quote.lines),
    });
  }

  return (
    <>
      <PageHeader
        title="Quotations"
        description="Price a job for a client. When they agree, accept it to get the invoice and project."
        actions={
          canCreate && (
            <Button onClick={() => setForm(blank)}>
              <Plus size={16} /> New quotation
            </Button>
          )
        }
      />
      <div className="mb-4 flex flex-wrap gap-3">
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
          <option value="sent">Sent</option>
          <option value="accepted">Accepted</option>
          <option value="rejected">Rejected</option>
        </NativeSelect>
      </div>
      <DataGrid
        rows={list.data}
        loading={list.isLoading}
        rowKey={(r) => r.id}
        empty="No quotations yet"
        onRowClick={(r) => setOpenId(r.id)}
        columns={[
          { header: "Number", cell: (r) => <span className="font-medium">{r.number}</span> },
          { header: "Client", cell: (r) => r.client_name },
          { header: "Date", cell: (r) => formatDate(r.created_at) },
          { header: "Valid until", cell: (r) => (r.valid_until ? formatDate(r.valid_until) : "-") },
          { header: "Status", cell: (r) => <StatusBadge value={r.status} /> },
          { header: "Total", align: "right", cell: (r) => formatMoney(r.total) },
        ]}
      />
      {form && <QuotationForm key={form.id ?? "new"} draft={form} onClose={() => setForm(null)} />}
      {openId && <Detail id={openId} onClose={() => setOpenId(null)} onEdit={edit} />}
    </>
  );
}
