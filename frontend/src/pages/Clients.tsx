import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  DataGrid,
  Field,
  FormDialog,
  NativeSelect,
  PageHeader,
  StatusBadge,
} from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { clientBlank } from "@/features/services/helpers";
import { useDebounce } from "@/hooks/useDebounce";
import { useGet, useWrite } from "@/hooks/useApi";
import { formatMoney, toNumber } from "@/lib/formatters";
import type { Client } from "@/types/services";

/** The form fields shared by "new client" and "edit client". */
export function ClientFields({
  draft,
  setDraft,
}: {
  draft: typeof clientBlank;
  setDraft: (next: typeof clientBlank) => void;
}) {
  const set = (k: keyof typeof clientBlank) => (e: { target: { value: string } }) =>
    setDraft({ ...draft, [k]: e.target.value });
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <Field label="Name *" className="sm:col-span-2">
        <Input required value={draft.name} onChange={set("name")} />
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
      <Field label="ID number">
        <Input value={draft.id_number} onChange={set("id_number")} />
      </Field>
      <Field label="Where did they hear of us?">
        <Input
          value={draft.source}
          onChange={set("source")}
          placeholder="Walk-in, referral, Instagram…"
        />
      </Field>
      <Field label="What do they need?" className="sm:col-span-2">
        <Textarea rows={2} value={draft.needs} onChange={set("needs")} />
      </Field>
      <Field label="Notes" className="sm:col-span-2">
        <Textarea rows={2} value={draft.notes} onChange={set("notes")} />
      </Field>
    </div>
  );
}

export default function Clients() {
  const { can } = useAuth();
  const navigate = useNavigate();
  const [q, setQ] = useState("");
  const [status, setStatus] = useState("");
  const search = useDebounce(q, 300);
  const clients = useGet<Client[]>("/clients", { q: search, status, per_page: 100 });
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState(clientBlank);
  const create = useWrite<unknown, Client>("POST", "/clients", {
    invalidate: ["/clients", "/services"],
    success: "Client added",
    onSuccess: (c) => {
      setOpen(false);
      setDraft(clientBlank);
      navigate(`/clients/${c.id}`);
    },
  });
  const owed = (clients.data ?? []).reduce((sum, c) => sum + Math.max(0, toNumber(c.balance)), 0);

  return (
    <>
      <PageHeader
        title="Clients and leads"
        description={clients.data ? `Clients owe you ${formatMoney(owed)} in total.` : undefined}
        actions={
          can("clients.manage") && (
            <Button onClick={() => setOpen(true)}>
              <Plus size={16} /> New client
            </Button>
          )
        }
      />
      <div className="mb-4 flex flex-wrap gap-3">
        <Input
          aria-label="Search clients"
          className="max-w-xs"
          placeholder="Search name or phone…"
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
          <option value="lead">Leads</option>
          <option value="active">Active</option>
          <option value="completed">Completed</option>
        </NativeSelect>
      </div>
      <DataGrid
        rows={clients.data}
        loading={clients.isLoading}
        rowKey={(c) => c.id}
        empty="No clients yet. Add the first one."
        onRowClick={(c) => navigate(`/clients/${c.id}`)}
        columns={[
          { header: "Name", cell: (c) => <span className="font-medium">{c.name}</span> },
          { header: "Phone", cell: (c) => c.phone ?? "-" },
          {
            header: "Status",
            cell: (c) => <StatusBadge value={c.is_active ? c.status : "inactive"} />,
          },
          { header: "Source", cell: (c) => c.source ?? "-" },
          { header: "Projects", align: "right", cell: (c) => c.projects },
          {
            header: "Owes",
            align: "right",
            cell: (c) => (
              <span className={toNumber(c.balance) > 0 ? "font-medium text-destructive" : ""}>
                {formatMoney(c.balance)}
              </span>
            ),
          },
        ]}
      />
      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title="New client"
        description="A name is enough to start. A lead becomes an active client when their first project starts."
        busy={create.isPending}
        onSubmit={() => create.mutate(draft)}
        wide
      >
        <ClientFields draft={draft} setDraft={setDraft} />
      </FormDialog>
    </>
  );
}
