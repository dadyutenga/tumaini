import { useEffect, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { Plus, Search } from "lucide-react";
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
import { ConfirmDialog, PageLoader } from "@/components/shared/Display";
import { useAuth } from "@/features/auth/auth";
import { LinesEditor, LinesTable } from "@/features/services/LinesEditor";
import { blankLine, linesBody, linesFrom, type LineDraft } from "@/features/services/lines";
import { ClientSelect, PdfButton } from "@/features/services/shared";
import { useGet, useWrite } from "@/hooks/useApi";
import { formatDate, formatMoney, toNumber } from "@/lib/formatters";
import type { CatalogItem, Quotation } from "@/types/services";
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";
import { useSearchBox } from "@/hooks/useSearchBox";

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
  const { t } = useI18n();
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
    success: t.services.quotationSavedAsADraft,
    onSuccess: onClose,
  });
  const edit = useWrite("PUT", `/service-quotations/${d.id}`, {
    invalidate: ["/service-quotations"],
    success: t.services.quotationUpdated,
    onSuccess: onClose,
  });
  return (
    <FormDialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={d.id ? t.services.editQuotation : t.services.newQuotation}
      busy={create.isPending || edit.isPending}
      onSubmit={() => (d.id ? edit.mutate(body()) : create.mutate(body()))}
      wide
    >
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label={t.common.client2} className="sm:col-span-2">
          <ClientSelect
            required
            value={d.client_id}
            onChange={(v) => setD({ ...d, client_id: v })}
          />
        </Field>
        <Field label={t.services.validUntil}>
          <Input
            type="date"
            value={d.valid_until}
            onChange={(e) => setD({ ...d, valid_until: e.target.value })}
          />
        </Field>
        <Field label={t.common.discount}>
          <Input
            inputMode="decimal"
            value={d.discount}
            onChange={(e) => setD({ ...d, discount: e.target.value })}
          />
        </Field>
      </div>
      <LinesEditor lines={d.lines} onChange={(lines) => setD({ ...d, lines })} />
      <Field label={t.services.termsAndNotes}>
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
  const { t } = useI18n();
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
      success: t.services.quotationAcceptedTheInvoiceIs,
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
      title={fmt(t.services.accept, { number: quote.number })}
      description={t.services.aDraftInvoiceIsCreated}
      submitLabel={t.services.accept2}
      busy={accept.isPending}
      onSubmit={() =>
        accept.mutate({
          create_project: createProject,
          service_id: createProject && serviceId ? serviceId : undefined,
          project_name: createProject && name ? name : undefined,
        })
      }
    >
      <label className="flex items-center gap-2 text-[13px] text-[var(--od-ink-secondary)]">
        <input
          type="checkbox"
          checked={createProject}
          onChange={(e) => setCreateProject(e.target.checked)}
        />
        {t.services.startAProjectForThis}
      </label>
      {createProject && (
        <>
          <Field label={t.services.serviceGivesTheStagesAnd}>
            <NativeSelect value={serviceId} onChange={(e) => setServiceId(e.target.value)}>
              <option value="">{t.services.customProjectNoStages}</option>
              {catalog.data
                ?.filter((s) => s.is_active)
                .map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
            </NativeSelect>
          </Field>
          <Field label={t.services.projectName}>
            <Input
              value={name}
              placeholder={t.common.optional}
              onChange={(e) => setName(e.target.value)}
            />
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
  const { t } = useI18n();
  const { can } = useAuth();
  const navigate = useNavigate();
  const quote = useGet<Quotation>(`/service-quotations/${id}`);
  const [accepting, setAccepting] = useState(false);
  const [rejecting, setRejecting] = useState(false);
  const refresh = ["/service-quotations"];
  const send = useWrite("POST", `/service-quotations/${id}/send`, {
    invalidate: refresh,
    success: t.services.markedAsSent,
  });
  const reject = useWrite("POST", `/service-quotations/${id}/reject`, {
    invalidate: refresh,
    success: t.services.markedAsRejected,
    onSuccess: () => setRejecting(false),
  });
  const q = quote.data;
  const manage = can("services.quotation.manage");
  const canDraft = can("services.quotation.manage") || can("services.quotation.draft");
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{q ? `${q.number} · ${q.client_name}` : t.services.quotation}</DialogTitle>
          <DialogDescription className="sr-only">{t.services.quotationDetails}</DialogDescription>
        </DialogHeader>
        {!q ? (
          <PageLoader />
        ) : (
          <div className="space-y-4">
            <div className="flex flex-wrap items-center gap-3 text-[13px]">
              <StatusBadge value={q.status} />
              {q.valid_until && (
                <span className="od-caption">
                  {fmt(t.services.validUntil2, { valid_until: formatDate(q.valid_until) })}
                </span>
              )}
            </div>
            <LinesTable lines={q.lines ?? []} />
            <div className="ml-auto w-full max-w-xs space-y-1 rounded-[var(--od-radius-md,12px)] bg-[var(--od-surface-subtle)] p-4 text-[13px]">
              {toNumber(q.discount) > 0 && (
                <p className="flex justify-between text-[var(--od-ink-secondary)]">
                  <span>{t.common.discount}</span>
                  <span className="od-tabular">{formatMoney(q.discount)}</span>
                </p>
              )}
              <p className="flex justify-between text-[15px] font-semibold text-[var(--od-ink)]">
                <span>{t.common.total}</span>
                <span className="od-tabular">{formatMoney(q.total)}</span>
              </p>
            </div>
            {q.notes && (
              <p className="rounded-[var(--od-radius-sm,8px)] bg-[var(--od-surface-subtle)] p-3 text-[13px] text-[var(--od-ink-secondary)]">
                {q.notes}
              </p>
            )}
            <div className="flex flex-wrap justify-end gap-2 pt-1">
              <PdfButton
                size="sm"
                path={`/service-quotations/${id}/pdf`}
                filename={`${q.number}.pdf`}
              />
              {canDraft && q.status === "draft" && (
                <Button size="sm" variant="outline" onClick={() => onEdit(q)}>
                  {t.common.edit}
                </Button>
              )}
              {manage && q.status === "draft" && (
                <Button size="sm" onClick={() => send.mutate(undefined)} disabled={send.isPending}>
                  {t.services.markAsSent}
                </Button>
              )}
              {manage && (q.status === "draft" || q.status === "sent") && (
                <>
                  <Button size="sm" variant="outline" onClick={() => setRejecting(true)}>
                    {t.common.rejected}
                  </Button>
                  <Button size="sm" onClick={() => setAccepting(true)}>
                    {t.services.accepted}
                  </Button>
                </>
              )}
              {q.invoice_id && (
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => navigate(`/service-invoices?open=${q.invoice_id}`)}
                >
                  {t.services.openInvoice}
                </Button>
              )}
              {q.project_id && (
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => navigate(`/projects/${q.project_id}`)}
                >
                  {t.services.openProject}
                </Button>
              )}
            </div>
          </div>
        )}
        {accepting && q && <AcceptDialog quote={q} onClose={() => setAccepting(false)} />}
        <ConfirmDialog
          open={rejecting}
          onOpenChange={setRejecting}
          title={t.services.markThisQuotationAsRejected}
          description={t.services.theClientSaidNoYou}
          onConfirm={() => reject.mutate(undefined)}
        />
      </DialogContent>
    </Dialog>
  );
}

export default function ServiceQuotations() {
  const { t } = useI18n();
  const { canAny } = useAuth();
  const canCreate = canAny("services.quotation.manage", "services.quotation.draft");
  const [params, setParams] = useSearchParams();
  const [status, setStatus] = useState("");
  const [q, setQ] = useSearchBox();
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
    <div className="space-y-6">
      <PageHeader
        title={t.services.quotations}
        description={t.services.priceAJobForA}
        actions={
          canCreate && (
            <Button size="sm" onClick={() => setForm(blank)}>
              <Plus size={14} /> {t.services.newQuotation}
            </Button>
          )
        }
      />

      <FilterBar className="justify-between">
        <div className="relative min-w-60 flex-1 sm:max-w-xs">
          <Search
            size={15}
            className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-[var(--od-ink-muted)]"
          />
          <Input
            aria-label={t.common.search}
            className="pl-9"
            placeholder={t.services.searchNumberOrClient}
            value={q}
            onChange={(e) => setQ(e.target.value)}
          />
        </div>
        <SegmentedChips
          ariaLabel={t.services.quotationStatus}
          value={status}
          onChange={setStatus}
          options={[
            { value: "", label: t.common.all },
            { value: "draft", label: t.common.draft },
            { value: "sent", label: t.services.sent },
            { value: "accepted", label: t.services.accepted },
            { value: "rejected", label: t.common.rejected },
          ]}
        />
      </FilterBar>

      <DataGrid
        rows={list.data}
        loading={list.isLoading}
        rowKey={(r) => r.id}
        empty={t.services.noQuotationsYet}
        onRowClick={(r) => setOpenId(r.id)}
        columns={[
          {
            header: t.common.number,
            sortValue: (r) => r.number,
            cell: (r) => <span className="od-mono font-medium">{r.number}</span>,
          },
          { header: t.common.client, sortValue: (r) => r.client_name, cell: (r) => r.client_name },
          {
            header: t.common.date,
            sortValue: (r) => r.created_at,
            cell: (r) => (
              <span className="text-[var(--od-ink-secondary)]">{formatDate(r.created_at)}</span>
            ),
          },
          {
            header: t.services.validUntil,
            cell: (r) => (
              <span className="text-[var(--od-ink-secondary)]">
                {r.valid_until ? formatDate(r.valid_until) : "—"}
              </span>
            ),
          },
          { header: t.common.status, cell: (r) => <StatusBadge value={r.status} /> },
          {
            header: t.common.total,
            align: "right",
            sortValue: (r) => toNumber(r.total),
            cell: (r) => <span className="font-medium">{formatMoney(r.total)}</span>,
          },
        ]}
      />

      {form && <QuotationForm key={form.id ?? "new"} draft={form} onClose={() => setForm(null)} />}
      {openId && <Detail id={openId} onClose={() => setOpenId(null)} onEdit={edit} />}
    </div>
  );
}
