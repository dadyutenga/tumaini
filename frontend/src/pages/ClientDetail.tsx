import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { Download } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  DataGrid,
  FormDialog,
  PageHeader,
  SectionCard,
  StatusBadge,
} from "@/components/shared/kit";
import { ConfirmDialog, PageLoader } from "@/components/shared/Display";
import { SendOutboundButton } from "@/components/shared/SendOutbound";
import { useAuth } from "@/features/auth/auth";
import { NewProjectDialog } from "@/features/services/NewProjectDialog";
import { PdfButton, Tile } from "@/features/services/shared";
import { clientBlank, label } from "@/features/services/helpers";
import { errorMessage, useGet, useWrite } from "@/hooks/useApi";
import { download } from "@/lib/api";
import { formatDate, formatMoney, toNumber } from "@/lib/formatters";
import { ClientFields } from "@/pages/Clients";
import type { ClientDetail as ClientDetailType } from "@/types/services";
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";

export default function ClientDetail() {
  const { t } = useI18n();
  const { id = "" } = useParams();
  const { can, canAny } = useAuth();
  const navigate = useNavigate();
  const client = useGet<ClientDetailType>(`/clients/${id}`);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(clientBlank);
  const [starting, setStarting] = useState(false);
  const [deactivating, setDeactivating] = useState(false);

  const refresh = ["/clients"];
  const save = useWrite<typeof clientBlank>("PUT", `/clients/${id}`, {
    invalidate: refresh,
    success: t.services.clientUpdated,
    onSuccess: () => setEditing(false),
  });
  const convert = useWrite("POST", `/clients/${id}/convert`, {
    invalidate: refresh,
    success: t.services.leadConvertedToAClient,
  });
  const deactivate = useWrite("POST", `/clients/${id}/deactivate`, {
    invalidate: refresh,
    success: t.services.clientDeactivated,
  });

  if (client.isLoading) return <PageLoader />;
  const c = client.data;
  if (!c)
    return (
      <div className="od-card items-center py-10 text-center">
        <p className="text-[14px] font-semibold text-[var(--od-bad)]">
          {t.services.clientNotFound}
        </p>
        <Button asChild variant="outline" size="sm" className="mt-3">
          <Link to="/clients">{t.services.backToClients}</Link>
        </Button>
      </div>
    );

  return (
    <div className="space-y-6">
      <PageHeader
        backTo="/clients"
        backLabel={t.common.clientsAndLeads}
        title={c.name}
        badge={<StatusBadge value={c.is_active ? c.status : "inactive"} />}
        description={[c.phone, c.email, c.address].filter(Boolean).join(" · ") || undefined}
        actions={
          <>
            {can("services.client.manage") && (
              <Button
                size="sm"
                variant="outline"
                onClick={() => {
                  setDraft({
                    name: c.name,
                    phone: c.phone ?? "",
                    email: c.email ?? "",
                    address: c.address ?? "",
                    id_number: c.id_number ?? "",
                    source: c.source ?? "",
                    needs: c.needs ?? "",
                    notes: c.notes ?? "",
                    status: c.status,
                  });
                  setEditing(true);
                }}
              >
                {t.common.edit}
              </Button>
            )}
            {can("services.client.manage") && c.status === "lead" && (
              <Button size="sm" variant="outline" onClick={() => convert.mutate(undefined)}>
                {t.services.convertToClient}
              </Button>
            )}
            {canAny("services.quotation.manage", "services.quotation.draft") && (
              <Button
                size="sm"
                variant="outline"
                onClick={() => navigate(`/service-quotations?client=${c.id}`)}
              >
                {t.services.createQuotation}
              </Button>
            )}
            {canAny("services.invoice.manage", "services.invoice.draft", "finance.debt.view") && (
              <>
                <PdfButton
                  size="sm"
                  path={`/clients/${id}/statement`}
                  filename={`statement-${c.name}.pdf`}
                >
                  {t.common.statement}
                </PdfButton>
                <SendOutboundButton
                  size="sm"
                  label={t.common.sendStatement}
                  title={t.services.sendClientStatement}
                  path={`/clients/${id}/statement/send`}
                  successMessage={t.common.statementQueued}
                  phone={c.phone}
                  whatsappText={`Statement for ${c.name}: current balance ${formatMoney(c.balance)}.`}
                />
              </>
            )}
            {can("services.project.manage") && (
              <Button size="sm" onClick={() => setStarting(true)}>
                {t.common.newProject}
              </Button>
            )}
          </>
        }
      />

      {c.needs && (
        <SectionCard title={t.services.clientBriefNeeds}>
          <p className="text-[13px] leading-relaxed text-[var(--od-ink-secondary)]">{c.needs}</p>
        </SectionCard>
      )}

      <div className="grid gap-4 sm:grid-cols-3">
        <Tile
          label={t.services.owesUs}
          value={formatMoney(c.balance)}
          tone={toNumber(c.balance) > 0 ? "bad" : undefined}
        />
        <Tile label={t.services.depositsHeld} value={formatMoney(c.deposit_held)} />
        <Tile label={t.common.projects} value={c.projects} />
      </div>

      <Tabs defaultValue="projects">
        <TabsList>
          <TabsTrigger value="projects">{t.common.projects}</TabsTrigger>
          <TabsTrigger value="invoices">{t.services.invoices}</TabsTrigger>
          <TabsTrigger value="payments">{t.common.payments}</TabsTrigger>
          <TabsTrigger value="documents">{t.common.documents}</TabsTrigger>
          <TabsTrigger value="communication">{t.services.communication}</TabsTrigger>
        </TabsList>

        <TabsContent value="projects">
          <DataGrid
            rows={c.projects_list}
            rowKey={(p) => p.id}
            empty={t.services.noProjectsYet}
            onRowClick={(p) => navigate(`/projects/${p.id}`)}
            columns={[
              {
                header: t.common.number,
                cell: (p) => <span className="od-mono font-medium">{p.number}</span>,
              },
              {
                header: t.common.project,
                cell: (p) => <span className="font-medium">{p.name}</span>,
              },
              { header: t.common.status, cell: (p) => <StatusBadge value={p.status} /> },
              { header: t.common.price, align: "right", cell: (p) => formatMoney(p.total_price) },
              { header: t.common.paid, align: "right", cell: (p) => formatMoney(p.paid) },
            ]}
          />
        </TabsContent>

        <TabsContent value="invoices">
          <DataGrid
            rows={c.invoices}
            rowKey={(i) => i.id}
            empty={t.services.noInvoicesYet}
            onRowClick={(i) => navigate(`/service-invoices?open=${i.id}`)}
            columns={[
              {
                header: t.common.invoice,
                cell: (i) => <span className="od-mono font-medium">{i.number}</span>,
              },
              { header: t.common.status, cell: (i) => <StatusBadge value={i.status} /> },
              { header: t.common.due2, cell: (i) => (i.due_date ? formatDate(i.due_date) : "—") },
              { header: t.common.total, align: "right", cell: (i) => formatMoney(i.total) },
              { header: t.common.paid, align: "right", cell: (i) => formatMoney(i.paid_total) },
              {
                header: t.common.balance,
                align: "right",
                cell: (i) => (
                  <span
                    className={
                      toNumber(i.balance) > 0 ? "font-semibold text-[var(--od-bad)]" : undefined
                    }
                  >
                    {formatMoney(i.balance)}
                  </span>
                ),
              },
            ]}
          />
        </TabsContent>

        <TabsContent value="payments">
          <DataGrid
            rows={c.payments}
            rowKey={(p) => p.id}
            empty={t.common.noPaymentsYet}
            columns={[
              {
                header: t.common.receipt2,
                cell: (p) => <span className="od-mono font-medium">{p.number}</span>,
              },
              { header: t.common.date, cell: (p) => formatDate(p.date) },
              { header: t.common.method, cell: (p) => label(p.method) },
              { header: t.common.status, cell: (p) => <StatusBadge value={p.status} /> },
              {
                header: t.common.amount,
                align: "right",
                cell: (p) => <span className="font-medium">{formatMoney(p.amount)}</span>,
              },
              {
                header: "",
                className: "text-right",
                cell: (p) => (
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() =>
                      download(
                        `/service-payments/${p.id}/receipt`,
                        { format: "pdf" },
                        `receipt-${p.number}.pdf`,
                      ).catch((e) => toast.error(errorMessage(e)))
                    }
                  >
                    <Download size={13} /> {t.common.receiptPdf}
                  </Button>
                ),
              },
            ]}
          />
        </TabsContent>

        <TabsContent value="documents" className="space-y-3">
          <DataGrid
            rows={c.documents}
            rowKey={(d) => d.id}
            empty={t.services.noDocumentsYet}
            columns={[
              {
                header: t.common.document,
                cell: (d) => <span className="font-medium">{d.name}</span>,
              },
              { header: t.common.status, cell: (d) => <StatusBadge value={d.status} /> },
              {
                header: t.common.received,
                cell: (d) => (d.received_at ? formatDate(d.received_at) : "—"),
              },
            ]}
          />
          <p className="od-caption">
            {t.services.uploadAndMarkDocumentsIn}{" "}
            <Link
              className="font-medium text-[var(--od-ink)] underline underline-offset-4"
              to={`/client-documents?client=${c.id}`}
            >
              {t.services.documentLibrary}
            </Link>
            .
          </p>
        </TabsContent>

        <TabsContent value="communication">
          <DataGrid
            rows={c.communication}
            rowKey={(m) => m.id}
            empty={t.services.noCallsOrMessagesRecorded}
            columns={[
              { header: t.common.date, cell: (m) => formatDate(m.noted_on) },
              { header: t.common.type, cell: (m) => label(m.kind) },
              {
                header: t.common.project,
                cell: (m) => <span className="od-mono text-xs">{m.project_number ?? "—"}</span>,
              },
              { header: t.common.note, cell: (m) => m.body, className: "whitespace-normal" },
            ]}
          />
        </TabsContent>
      </Tabs>

      {can("services.client.manage") && c.is_active && (
        <div className="border-t border-[var(--od-border)] pt-5">
          <Button
            size="sm"
            variant="ghost"
            className="text-[var(--od-bad)] hover:text-[var(--od-bad)]"
            onClick={() => setDeactivating(true)}
          >
            {t.services.deactivateClient}
          </Button>
        </div>
      )}

      <FormDialog
        open={editing}
        onOpenChange={setEditing}
        title={t.services.editClient}
        busy={save.isPending}
        onSubmit={() => save.mutate(draft)}
        wide
      >
        <ClientFields draft={draft} setDraft={setDraft} />
      </FormDialog>
      <NewProjectDialog open={starting} onOpenChange={setStarting} clientId={c.id} />
      <ConfirmDialog
        open={deactivating}
        onOpenChange={setDeactivating}
        title={t.services.deactivateThisClient}
        description={t.services.theyWillNoLongerAppear}
        confirmLabel={t.common.deactivate}
        destructive
        onConfirm={() => deactivate.mutate(undefined)}
      />
    </div>
  );
}
