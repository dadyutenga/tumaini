import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { Download } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { DataGrid, FormDialog, PageHeader, StatusBadge } from "@/components/shared/kit";
import { ConfirmDialog, PageLoader } from "@/components/shared/Display";
import { SendOutboundButton } from "@/components/shared/SendOutbound";
import { useAuth } from "@/features/auth/auth";
import { NewProjectDialog } from "@/features/services/NewProjectDialog";
import { PdfButton, Tile } from "@/features/services/shared";
import { label } from "@/features/services/helpers";
import { errorMessage, useGet, useWrite } from "@/hooks/useApi";
import { download } from "@/lib/api";
import { formatDate, formatMoney, toNumber } from "@/lib/formatters";
import { ClientFields } from "@/pages/Clients";
import { clientBlank } from "@/features/services/helpers";
import type { ClientDetail as ClientDetailType } from "@/types/services";

export default function ClientDetail() {
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
    success: "Client updated",
    onSuccess: () => setEditing(false),
  });
  const convert = useWrite("POST", `/clients/${id}/convert`, {
    invalidate: refresh,
    success: "Lead converted to a client",
  });
  const deactivate = useWrite("POST", `/clients/${id}/deactivate`, {
    invalidate: refresh,
    success: "Client deactivated",
  });

  if (client.isLoading) return <PageLoader />;
  const c = client.data;
  if (!c)
    return (
      <p className="text-destructive">
        Client not found.{" "}
        <Link className="underline" to="/clients">
          Back
        </Link>
      </p>
    );

  return (
    <>
      <PageHeader
        title={c.name}
        description={[c.phone, c.email, c.address].filter(Boolean).join(" · ") || undefined}
        actions={
          <>
            <StatusBadge value={c.is_active ? c.status : "inactive"} />
            {can("services.client.manage") && (
              <Button
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
                Edit
              </Button>
            )}
            {can("services.client.manage") && c.status === "lead" && (
              <Button variant="outline" onClick={() => convert.mutate(undefined)}>
                Convert to client
              </Button>
            )}
            {canAny("services.quotation.manage", "services.quotation.draft") && (
              <Button
                variant="outline"
                onClick={() => navigate(`/service-quotations?client=${c.id}`)}
              >
                Create quotation
              </Button>
            )}
            {canAny("services.invoice.manage", "services.invoice.draft", "finance.debt.view") && (
              <>
                <PdfButton path={`/clients/${id}/statement`} filename={`statement-${c.name}.pdf`}>
                  Statement
                </PdfButton>
                <SendOutboundButton
                  label="Send statement"
                  title="Send client statement"
                  path={`/clients/${id}/statement/send`}
                  successMessage="Statement queued"
                  phone={c.phone}
                  whatsappText={`Statement for ${c.name}: current balance ${formatMoney(c.balance)}.`}
                />
              </>
            )}
            {can("services.project.manage") && (
              <Button onClick={() => setStarting(true)}>New project</Button>
            )}
          </>
        }
      />
      {c.needs && (
        <p className="mb-4 rounded-2xl border bg-card p-4 text-sm">
          <span className="text-muted-foreground">Needs: </span>
          {c.needs}
        </p>
      )}
      <div className="mb-6 grid gap-4 sm:grid-cols-3">
        <Tile
          label="Owes us"
          value={formatMoney(c.balance)}
          tone={toNumber(c.balance) > 0 ? "bad" : undefined}
        />
        <Tile label="Deposits held" value={formatMoney(c.deposit_held)} />
        <Tile label="Projects" value={c.projects} />
      </div>
      <Tabs defaultValue="projects">
        <TabsList className="mb-4 flex-wrap">
          <TabsTrigger value="projects">Projects</TabsTrigger>
          <TabsTrigger value="invoices">Invoices</TabsTrigger>
          <TabsTrigger value="payments">Payments</TabsTrigger>
          <TabsTrigger value="documents">Documents</TabsTrigger>
          <TabsTrigger value="communication">Communication</TabsTrigger>
        </TabsList>
        <TabsContent value="projects">
          <DataGrid
            rows={c.projects_list}
            rowKey={(p) => p.id}
            empty="No projects yet"
            onRowClick={(p) => navigate(`/projects/${p.id}`)}
            columns={[
              { header: "Number", cell: (p) => p.number },
              { header: "Project", cell: (p) => <span className="font-medium">{p.name}</span> },
              { header: "Status", cell: (p) => <StatusBadge value={p.status} /> },
              { header: "Price", align: "right", cell: (p) => formatMoney(p.total_price) },
              { header: "Paid", align: "right", cell: (p) => formatMoney(p.paid) },
            ]}
          />
        </TabsContent>
        <TabsContent value="invoices">
          <DataGrid
            rows={c.invoices}
            rowKey={(i) => i.id}
            empty="No invoices yet"
            onRowClick={(i) => navigate(`/service-invoices?open=${i.id}`)}
            columns={[
              { header: "Invoice", cell: (i) => i.number },
              { header: "Status", cell: (i) => <StatusBadge value={i.status} /> },
              { header: "Due", cell: (i) => (i.due_date ? formatDate(i.due_date) : "-") },
              { header: "Total", align: "right", cell: (i) => formatMoney(i.total) },
              { header: "Paid", align: "right", cell: (i) => formatMoney(i.paid_total) },
              { header: "Balance", align: "right", cell: (i) => formatMoney(i.balance) },
            ]}
          />
        </TabsContent>
        <TabsContent value="payments">
          <DataGrid
            rows={c.payments}
            rowKey={(p) => p.id}
            empty="No payments yet"
            columns={[
              { header: "Receipt", cell: (p) => p.number },
              { header: "Date", cell: (p) => formatDate(p.date) },
              { header: "Method", cell: (p) => label(p.method) },
              { header: "Status", cell: (p) => <StatusBadge value={p.status} /> },
              { header: "Amount", align: "right", cell: (p) => formatMoney(p.amount) },
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
                    <Download size={14} /> Receipt PDF
                  </Button>
                ),
              },
            ]}
          />
        </TabsContent>
        <TabsContent value="documents">
          <DataGrid
            rows={c.documents}
            rowKey={(d) => d.id}
            empty="No documents yet"
            columns={[
              { header: "Document", cell: (d) => d.name },
              { header: "Status", cell: (d) => <StatusBadge value={d.status} /> },
              {
                header: "Received",
                cell: (d) => (d.received_at ? formatDate(d.received_at) : "-"),
              },
            ]}
          />
          <p className="mt-2 text-sm text-muted-foreground">
            Upload and mark documents in the{" "}
            <Link className="underline" to={`/client-documents?client=${c.id}`}>
              document library
            </Link>
            .
          </p>
        </TabsContent>
        <TabsContent value="communication">
          <DataGrid
            rows={c.communication}
            rowKey={(m) => m.id}
            empty="No calls or messages recorded"
            columns={[
              { header: "Date", cell: (m) => formatDate(m.noted_on) },
              { header: "Type", cell: (m) => label(m.kind) },
              { header: "Project", cell: (m) => m.project_number ?? "-" },
              { header: "Note", cell: (m) => m.body, className: "whitespace-normal" },
            ]}
          />
        </TabsContent>
      </Tabs>
      {can("services.client.manage") && c.is_active && (
        <div className="mt-8">
          <Button
            variant="ghost"
            className="text-destructive"
            onClick={() => setDeactivating(true)}
          >
            Deactivate client
          </Button>
        </div>
      )}
      <FormDialog
        open={editing}
        onOpenChange={setEditing}
        title="Edit client"
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
        title="Deactivate this client?"
        description="They will no longer appear when you pick a client. A client who still owes money cannot be deactivated."
        onConfirm={() => deactivate.mutate(undefined)}
      />
    </>
  );
}
