import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Plus, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { StatCard } from "@/components/shared/Display";
import {
  DataGrid,
  Field,
  FilterBar,
  FormDialog,
  PageHeader,
  SegmentedChips,
  StatusBadge,
} from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { clientBlank } from "@/features/services/helpers";
import { useDebounce } from "@/hooks/useDebounce";
import { useGet, useWrite } from "@/hooks/useApi";
import { formatMoney, toNumber } from "@/lib/formatters";
import type { Client } from "@/types/services";
import { useI18n } from "@/features/i18n/i18n";

/** The form fields shared by "new client" and "edit client". */
export function ClientFields({
  draft,
  setDraft,
}: {
  draft: typeof clientBlank;
  setDraft: (next: typeof clientBlank) => void;
}) {
  const { t } = useI18n();
  const set = (k: keyof typeof clientBlank) => (e: { target: { value: string } }) =>
    setDraft({ ...draft, [k]: e.target.value });
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <Field label={t.common.name} className="sm:col-span-2">
        <Input required value={draft.name} onChange={set("name")} />
      </Field>
      <Field label={t.common.phone}>
        <Input inputMode="tel" value={draft.phone} onChange={set("phone")} />
      </Field>
      <Field label={t.common.email}>
        <Input type="email" value={draft.email} onChange={set("email")} />
      </Field>
      <Field label={t.common.address} className="sm:col-span-2">
        <Input value={draft.address} onChange={set("address")} />
      </Field>
      <Field label={t.services.idNumber}>
        <Input value={draft.id_number} onChange={set("id_number")} />
      </Field>
      <Field label={t.services.whereDidTheyHearOf}>
        <Input
          value={draft.source}
          onChange={set("source")}
          placeholder={t.services.walkInReferralInstagram}
        />
      </Field>
      <Field label={t.services.whatDoTheyNeed} className="sm:col-span-2">
        <Textarea rows={2} value={draft.needs} onChange={set("needs")} />
      </Field>
      <Field label={t.common.notes} className="sm:col-span-2">
        <Textarea rows={2} value={draft.notes} onChange={set("notes")} />
      </Field>
    </div>
  );
}

export default function Clients() {
  const { t } = useI18n();
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
    success: t.services.clientAdded,
    onSuccess: (c) => {
      setOpen(false);
      setDraft(clientBlank);
      navigate(`/clients/${c.id}`);
    },
  });

  const rows = clients.data ?? [];
  const owed = rows.reduce((sum, c) => sum + Math.max(0, toNumber(c.balance)), 0);
  const leadsCount = rows.filter((c) => c.status === "lead").length;

  return (
    <div className="space-y-6">
      <PageHeader
        title={t.common.clientsAndLeads}
        description={t.services.serviceClientsPipelineLeadsAnd}
        actions={
          can("services.client.manage") && (
            <Button size="sm" onClick={() => setOpen(true)}>
              <Plus size={14} /> {t.common.newClient}
            </Button>
          )
        }
      />

      <div className="grid gap-4 sm:grid-cols-3">
        <StatCard label={t.services.matchingRecords} value={rows.length} />
        <StatCard label={t.services.leadsInView} value={leadsCount} />
        <StatCard
          label={t.services.clientsOweUs}
          value={formatMoney(owed)}
          tone={owed > 0 ? "bad" : "good"}
        />
      </div>

      <FilterBar className="justify-between">
        <div className="relative min-w-60 flex-1 sm:max-w-xs">
          <Search
            size={15}
            className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-[var(--od-ink-muted)]"
          />
          <Input
            aria-label={t.services.searchClients}
            className="pl-9"
            placeholder={t.common.searchNameOrPhone}
            value={q}
            onChange={(e) => setQ(e.target.value)}
          />
        </div>
        <SegmentedChips
          ariaLabel={t.services.clientStatus}
          value={status}
          onChange={setStatus}
          options={[
            { value: "", label: t.common.all },
            { value: "lead", label: t.services.leads },
            { value: "active", label: t.services.active },
            { value: "completed", label: t.common.completed },
          ]}
        />
      </FilterBar>

      <DataGrid
        rows={rows}
        loading={clients.isLoading}
        rowKey={(c) => c.id}
        empty={t.services.noClientsYetAddThe}
        onRowClick={(c) => navigate(`/clients/${c.id}`)}
        columns={[
          {
            header: t.common.name2,
            sortValue: (c) => c.name,
            cell: (c) => <span className="font-medium">{c.name}</span>,
          },
          {
            header: t.common.phone,
            cell: (c) => (
              <span className="od-mono text-xs text-[var(--od-ink-secondary)]">
                {c.phone ?? "—"}
              </span>
            ),
          },
          {
            header: t.common.status,
            cell: (c) => <StatusBadge value={c.is_active ? c.status : "inactive"} />,
          },
          {
            header: t.common.source,
            cell: (c) => <span className="text-[var(--od-ink-secondary)]">{c.source ?? "—"}</span>,
          },
          {
            header: t.common.projects,
            align: "right",
            sortValue: (c) => c.projects,
            cell: (c) => c.projects,
          },
          {
            header: t.common.owes,
            align: "right",
            sortValue: (c) => toNumber(c.balance),
            cell: (c) => (
              <span className={toNumber(c.balance) > 0 ? "font-semibold text-[var(--od-bad)]" : ""}>
                {formatMoney(c.balance)}
              </span>
            ),
          },
        ]}
      />

      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title={t.common.newClient}
        description={t.services.aNameIsEnoughTo}
        busy={create.isPending}
        onSubmit={() => create.mutate(draft)}
        wide
      >
        <ClientFields draft={draft} setDraft={setDraft} />
      </FormDialog>
    </div>
  );
}
