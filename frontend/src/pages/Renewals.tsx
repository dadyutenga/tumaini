import { useState } from "react";
import { MessageCircle, Plus } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  DataGrid,
  Field,
  FilterBar,
  FormDialog,
  PageHeader,
  SegmentedChips,
  StatusBadge,
} from "@/components/shared/kit";
import { ConfirmDialog } from "@/components/shared/Display";
import { useAuth } from "@/features/auth/auth";
import { ClientSelect } from "@/features/services/shared";
import { whatsappUrl } from "@/features/services/helpers";
import { useGet, useWrite } from "@/hooks/useApi";
import { ApiError } from "@/lib/api";
import { formatDate, formatMoney, toNumber } from "@/lib/formatters";
import type { Renewal } from "@/types/services";
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";

const blank = { client_id: "", item: "", due_date: "", amount: "", notes: "" };

export default function Renewals() {
  const { t } = useI18n();
  const { can } = useAuth();
  const manage = can("services.renewal.manage");
  const [status, setStatus] = useState("pending");
  const [within, setWithin] = useState("");
  const renewals = useGet<Renewal[]>("/renewals", { status, within_days: within, per_page: 100 });
  const [adding, setAdding] = useState(false);
  const [draft, setDraft] = useState(blank);
  const [renewing, setRenewing] = useState<Renewal | null>(null);
  const [next, setNext] = useState("");
  const [cancelling, setCancelling] = useState<Renewal | null>(null);
  const [waFallback, setWaFallback] = useState<Record<string, boolean>>({});

  const refresh = ["/renewals", "/services"];
  const add = useWrite("POST", "/renewals", {
    invalidate: refresh,
    success: t.services.renewalAddedRemindersGoOut,
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
      success: t.services.markedAsRenewed,
      toBody: (b) => ({ next_due_date: b.next_due_date }),
      onSuccess: () => {
        setRenewing(null);
        setNext("");
      },
    },
  );
  const cancel = useWrite<{ id: string }>("POST", (b) => `/renewals/${b.id}/cancel`, {
    invalidate: refresh,
    success: t.services.renewalCancelled,
    toBody: () => undefined,
  });
  const remind = useWrite<{ id: string }, { queued?: boolean; delivered?: boolean }>(
    "POST",
    (b) => `/renewals/${b.id}/remind`,
    {
      invalidate: refresh,
      toBody: () => ({ channel: "sms" }),
      onSuccess: (r) =>
        r?.queued || r?.delivered
          ? toast.success(t.services.renewalReminderQueued)
          : toast.warning(t.services.reminderCouldNotBeQueued),
      onError: (err, vars) => {
        if (err instanceof ApiError && err.status === 503) {
          setWaFallback((m) => ({ ...m, [vars.id]: true }));
        }
      },
    },
  );

  return (
    <div className="space-y-6">
      <PageHeader
        title={t.common.renewals}
        description={t.services.licencesAndAnnualFeesComing}
        actions={
          manage && (
            <Button size="sm" onClick={() => setAdding(true)}>
              <Plus size={14} /> {t.services.newRenewal}
            </Button>
          )
        }
      />

      <FilterBar className="justify-between">
        <SegmentedChips
          ariaLabel={t.services.renewalStatus}
          value={status}
          onChange={setStatus}
          options={[
            { value: "pending", label: t.common.pending },
            { value: "renewed", label: t.services.renewed },
            { value: "cancelled", label: t.common.cancelled },
            { value: "", label: t.common.all },
          ]}
        />
        <SegmentedChips
          ariaLabel={t.services.dueWithin}
          value={within}
          onChange={setWithin}
          options={[
            { value: "", label: t.services.anyDate },
            { value: "7", label: t.services.n7Days },
            { value: "30", label: t.services.n30Days },
            { value: "90", label: t.services.n90Days },
          ]}
        />
      </FilterBar>

      <DataGrid
        rows={renewals.data}
        loading={renewals.isLoading}
        rowKey={(r) => r.id}
        empty={t.services.nothingToRenew}
        columns={[
          {
            header: t.common.client,
            sortValue: (r) => r.client_name,
            cell: (r) => <span className="font-medium">{r.client_name}</span>,
          },
          { header: t.services.item, sortValue: (r) => r.item, cell: (r) => r.item },
          {
            header: t.common.due2,
            sortValue: (r) => r.due_date,
            cell: (r) => (
              <span className="text-[var(--od-ink-secondary)]">{formatDate(r.due_date)}</span>
            ),
          },
          {
            header: t.services.daysLeft,
            align: "right",
            sortValue: (r) => r.days_left,
            cell: (r) =>
              r.status !== "pending" ? (
                "—"
              ) : (
                <span
                  className={
                    r.days_left < 0
                      ? "font-semibold text-[var(--od-bad)]"
                      : r.days_left <= 7
                        ? "font-semibold text-[var(--od-warning)]"
                        : "text-[var(--od-ink-secondary)]"
                  }
                >
                  {r.days_left < 0 ? fmt(t.services.late, { value: -r.days_left }) : r.days_left}
                </span>
              ),
          },
          {
            header: t.services.fee,
            align: "right",
            sortValue: (r) => toNumber(r.amount),
            cell: (r) => (toNumber(r.amount) > 0 ? formatMoney(r.amount) : "—"),
          },
          { header: t.common.status, cell: (r) => <StatusBadge value={r.status} /> },
          {
            header: "",
            className: "text-right",
            cell: (r) =>
              manage && r.status === "pending" ? (
                <div className="flex justify-end gap-1.5">
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={remind.isPending}
                    onClick={() => remind.mutate({ id: r.id })}
                  >
                    {t.common.remind}
                  </Button>
                  {waFallback[r.id] && (
                    <Button size="sm" variant="outline" asChild>
                      <a
                        href={whatsappUrl(
                          r.client_phone ?? undefined,
                          `Hello ${r.client_name}, reminder: ${r.item} is due on ${formatDate(r.due_date)}.`,
                        )}
                        target="_blank"
                        rel="noreferrer"
                      >
                        <MessageCircle size={13} /> {t.common.openInWhatsapp}
                      </a>
                    </Button>
                  )}
                  <Button size="sm" variant="outline" onClick={() => setRenewing(r)}>
                    {t.services.renewed}
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    className="text-[var(--od-bad)] hover:text-[var(--od-bad)]"
                    onClick={() => setCancelling(r)}
                  >
                    {t.common.cancel}
                  </Button>
                </div>
              ) : null,
          },
        ]}
      />

      <FormDialog
        open={adding}
        onOpenChange={setAdding}
        title={t.services.newRenewal}
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
        <Field label={t.common.client2}>
          <ClientSelect
            required
            value={draft.client_id}
            onChange={(v) => setDraft({ ...draft, client_id: v })}
          />
        </Field>
        <Field label={t.services.whatIsRenewing}>
          <Input
            required
            placeholder={t.services.businessLicenceAnnualReturn}
            value={draft.item}
            onChange={(e) => setDraft({ ...draft, item: e.target.value })}
          />
        </Field>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label={t.services.dueDate}>
            <Input
              required
              type="date"
              value={draft.due_date}
              onChange={(e) => setDraft({ ...draft, due_date: e.target.value })}
            />
          </Field>
          <Field label={t.services.fee}>
            <Input
              inputMode="decimal"
              value={draft.amount}
              onChange={(e) => setDraft({ ...draft, amount: e.target.value })}
            />
          </Field>
        </div>
        <Field label={t.common.notes}>
          <Input
            value={draft.notes}
            onChange={(e) => setDraft({ ...draft, notes: e.target.value })}
          />
        </Field>
      </FormDialog>

      <FormDialog
        open={!!renewing}
        onOpenChange={(o) => !o && setRenewing(null)}
        title={fmt(t.services.renewed2, { item: renewing?.item ?? "" })}
        description={t.services.setTheNextDueDate}
        submitLabel={t.services.markRenewed}
        busy={renew.isPending}
        onSubmit={() =>
          renewing && renew.mutate({ id: renewing.id, next_due_date: next || undefined })
        }
      >
        <Field label={t.services.nextDueDateOptional}>
          <Input type="date" value={next} onChange={(e) => setNext(e.target.value)} />
        </Field>
      </FormDialog>

      <ConfirmDialog
        open={!!cancelling}
        onOpenChange={(o) => !o && setCancelling(null)}
        title={t.services.cancelThisRenewal}
        description={t.services.theClientWillNotBe}
        confirmLabel={t.services.cancelRenewal}
        destructive
        onConfirm={() => cancelling && cancel.mutate({ id: cancelling.id })}
      />
    </div>
  );
}
