import { useState } from "react";
import { Check, Eye, Paperclip, Plus, Upload, X } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  DataGrid,
  Field,
  FilterBar,
  FormDialog,
  NativeSelect,
  PageHeader,
  Pager,
  SegmentedChips,
  StatusBadge,
} from "@/components/shared/kit";
import { FilePreviewDialog } from "@/components/shared/FilePreview";
import { useAuth } from "@/features/auth/auth";
import { BusinessSelect, useBusinessChoice } from "@/features/business/BusinessSelect";
import { errorMessage, useGet, useGetPage, useWrite } from "@/hooks/useApi";
import { isPendingApproval, uploadFile } from "@/lib/api";
import { formatDate, formatMoney, today } from "@/lib/formatters";
import type { Branch, MoneyAccount } from "@/types";
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";
import type { Translations } from "@/features/i18n/i18n";

interface Category {
  id: string;
  name: string;
}
interface Expense {
  id: string;
  number?: string;
  branch_id?: string;
  location_name?: string;
  status: string;
  amount?: string;
  description: string;
  date?: string;
  category_name?: string;
  submitted_by?: string;
  requested_by?: string;
  recurring_id?: string | null;
  submitted_by_name?: string;
  receipt_file_id?: string | null;
}

const statusOptions = (t: Translations) => [
  { value: "", label: t.common.all },
  { value: "submitted", label: t.finance.waitingApproval },
  { value: "approved", label: t.common.approved },
  { value: "paid", label: t.common.paid },
  { value: "rejected", label: t.common.rejected },
  { value: "cancelled", label: t.common.cancelled },
];

export default function Expenses() {
  const { t } = useI18n();
  const { can, canAny, user, branchId } = useAuth();
  const isAllLocations = !branchId || branchId === "all";
  const categories = useGet<Category[]>("/expense-categories");
  const branches = useGet<Branch[]>("/branches", undefined, { enabled: isAllLocations });
  const accounts = useGet<MoneyAccount[]>("/money-accounts", undefined, {
    enabled: canAny("finance.account.view", "finance.expense.enter", "finance.expense.pay"),
  });
  const [status, setStatus] = useState("");
  const [locationId, setLocationId] = useState("");
  const [page, setPage] = useState(1);
  const list = useGetPage<Expense>("/expenses", {
    status: status || undefined,
    location_id: isAllLocations && locationId ? locationId : undefined,
    page,
  });
  const [open, setOpen] = useState(false);
  const [paying, setPaying] = useState<Expense | null>(null);
  const [account, setAccount] = useState("");
  const [preview, setPreview] = useState<{ id: string; name: string } | null>(null);
  const [rejecting, setRejecting] = useState<Expense | null>(null);
  const [rejectNote, setRejectNote] = useState("");
  const [uploading, setUploading] = useState(false);
  const [receiptName, setReceiptName] = useState("");

  const blankDraft = {
    category_id: "",
    amount: "",
    description: "",
    date: today(),
    receipt_file_id: "",
    business_unit: undefined as string | undefined,
  };
  const [draft, setDraft] = useState(blankDraft);
  const business = useBusinessChoice(draft.business_unit);

  const create = useWrite<Record<string, unknown>, Expense & { status: string }>(
    "POST",
    "/expenses",
    {
      idempotent: true,
      success: t.finance.expenseSubmitted,
      invalidate: ["/expenses", "/dashboard", "/approvals"],
      onSuccess: (result) => {
        setOpen(false);
        setReceiptName("");
        setDraft(blankDraft);
        if (isPendingApproval(result)) return;
      },
    },
  );

  const pay = useWrite<{ money_account_id: string }, unknown>(
    "POST",
    () => `/expenses/${paying?.id}/pay`,
    {
      idempotent: true,
      success: t.finance.expensePaid,
      invalidate: ["/money-accounts", "/transactions", "/expenses", "/dashboard"],
      onSuccess: () => setPaying(null),
    },
  );

  const approve = useWrite<{ id: string }, unknown>(
    "POST",
    (vars) => `/expenses/${vars.id}/approve`,
    {
      toBody: () => ({}),
      success: t.finance.expenseApproved,
      invalidate: ["/expenses", "/approvals", "/dashboard"],
    },
  );

  const reject = useWrite<{ note: string }, unknown>(
    "POST",
    () => `/expenses/${rejecting?.id}/reject`,
    {
      success: t.finance.expenseRejected,
      invalidate: ["/expenses", "/approvals", "/dashboard"],
      onSuccess: () => {
        setRejecting(null);
        setRejectNote("");
      },
    },
  );

  const cancel = useWrite<{ id: string }, unknown>(
    "POST",
    (vars) => `/expenses/${vars.id}/cancel`,
    {
      toBody: () => ({}),
      success: t.finance.expenseCancelled,
      invalidate: ["/expenses", "/approvals", "/dashboard"],
    },
  );

  async function onReceiptPicked(file: File | undefined) {
    if (!file) return;
    setUploading(true);
    try {
      const id = await uploadFile(file);
      setDraft((d) => ({ ...d, receipt_file_id: id }));
      setReceiptName(file.name);
      toast.success(t.finance.receiptUploaded);
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setUploading(false);
    }
  }

  const usable = (accounts.data ?? []).filter((a) => a.is_active);
  const activeLocations = (branches.data ?? []).filter((b) => b.is_active);

  return (
    <div className="space-y-6">
      <PageHeader
        title={t.common.expenses}
        description={t.finance.recordSpendingWithReceiptPhotos}
        actions={
          canAny("finance.expense.submit", "finance.expense.enter") && (
            <Button onClick={() => setOpen(true)}>
              <Plus size={16} /> {t.common.newExpense}
            </Button>
          )
        }
      />

      <FilterBar>
        <SegmentedChips
          ariaLabel={t.common.filterByStatus}
          value={status}
          onChange={(v) => {
            setStatus(v);
            setPage(1);
          }}
          options={statusOptions(t)}
        />
        {isAllLocations && activeLocations.length > 1 && (
          <NativeSelect
            aria-label={t.finance.filterByLocation}
            className="w-48"
            value={locationId}
            onChange={(e) => {
              setLocationId(e.target.value);
              setPage(1);
            }}
          >
            <option value="">{t.common.allLocations}</option>
            {activeLocations.map((b) => (
              <option key={b.id} value={b.id}>
                {b.name}
              </option>
            ))}
          </NativeSelect>
        )}
      </FilterBar>

      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(e) => e.id}
        empty={t.finance.noExpensesFound}
        columns={[
          {
            header: t.common.number,
            cell: (e) => (
              <span className="od-mono font-medium text-[var(--od-ink)]">{e.number ?? "-"}</span>
            ),
          },
          { header: t.common.date, cell: (e) => (e.date ? formatDate(e.date) : "-") },
          { header: t.common.location, cell: (e) => e.location_name ?? "-" },
          { header: t.common.category, cell: (e) => e.category_name ?? "-" },
          {
            header: t.common.description,
            cell: (e) => (
              <div className="flex flex-wrap items-center gap-1.5">
                <span className="text-[var(--od-ink)]">{e.description}</span>
                {e.receipt_file_id && (
                  <button
                    type="button"
                    title={t.finance.previewReceiptPhoto}
                    className="inline-flex items-center gap-1 rounded-full border border-[var(--od-border-strong)] bg-[var(--od-surface-subtle)] px-2 py-0.5 text-[11px] font-medium text-[var(--od-ink-secondary)] transition hover:text-[var(--od-ink)]"
                    onClick={() =>
                      setPreview({
                        id: e.receipt_file_id!,
                        name: fmt(t.common.receipt, { number: e.number ?? e.id }),
                      })
                    }
                  >
                    <Eye size={12} /> {t.common.receipt2}
                  </button>
                )}
              </div>
            ),
          },
          { header: t.common.by, cell: (e) => e.submitted_by_name ?? "-" },
          {
            header: t.common.amount,
            align: "right",
            cell: (e) => (
              <span className="od-tabular font-medium text-[var(--od-ink)]">
                {formatMoney(e.amount)}
              </span>
            ),
          },
          { header: t.common.status, cell: (e) => <StatusBadge value={e.status} /> },
          {
            header: "",
            cell: (e) => {
              const isOwn =
                (Boolean(user?.id) && e.submitted_by === user?.id) ||
                (Boolean(user?.membership_id) && e.requested_by === user?.membership_id);
              const canDecide =
                can("finance.expense.approve") &&
                e.status === "submitted" &&
                (!isOwn || Boolean(e.recurring_id));
              const canCancel =
                (e.status === "submitted" || e.status === "approved") &&
                (isOwn || can("finance.expense.approve"));
              return (
                <div className="flex justify-end gap-1.5">
                  {canDecide && (
                    <>
                      <Button
                        size="sm"
                        variant="outline"
                        className="text-[var(--od-good)] hover:text-[var(--od-good)]"
                        disabled={approve.isPending}
                        onClick={() => approve.mutate({ id: e.id })}
                      >
                        <Check size={14} /> {t.common.approve}
                      </Button>
                      <Button
                        size="sm"
                        variant="outline"
                        className="text-[var(--od-bad)] hover:text-[var(--od-bad)]"
                        onClick={() => {
                          setRejecting(e);
                          setRejectNote("");
                        }}
                      >
                        <X size={14} /> {t.common.reject}
                      </Button>
                    </>
                  )}
                  {can("finance.expense.pay") && e.status === "approved" && (
                    <Button size="sm" variant="outline" onClick={() => setPaying(e)}>
                      {t.common.pay}
                    </Button>
                  )}
                  {canCancel && (
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={cancel.isPending}
                      onClick={() => cancel.mutate({ id: e.id })}
                    >
                      {t.common.cancel}
                    </Button>
                  )}
                </div>
              );
            },
          },
        ]}
      />
      <Pager meta={list.data?.meta} onPage={setPage} />

      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title={t.common.newExpense}
        busy={create.isPending || uploading}
        onSubmit={() => {
          const payload: Record<string, unknown> = {
            category_id: draft.category_id,
            amount: draft.amount,
            description: draft.description,
            date: draft.date,
          };
          if (draft.receipt_file_id) payload.receipt_file_id = draft.receipt_file_id;
          if (business) payload.business_unit = business;
          create.mutate(payload);
        }}
      >
        <Field label={t.common.category}>
          <NativeSelect
            required
            value={draft.category_id}
            onChange={(e) => setDraft((d) => ({ ...d, category_id: e.target.value }))}
          >
            <option value="">{t.common.choose}</option>
            {categories.data?.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <BusinessSelect
          value={draft.business_unit}
          onChange={(business_unit) => setDraft((d) => ({ ...d, business_unit }))}
        />
        <div className="grid grid-cols-2 gap-3">
          <Field label={t.common.amount}>
            <Input
              required
              inputMode="decimal"
              className="od-tabular"
              value={draft.amount}
              onChange={(e) => setDraft((d) => ({ ...d, amount: e.target.value }))}
            />
          </Field>
          <Field label={t.common.date}>
            <Input
              required
              type="date"
              value={draft.date}
              onChange={(e) => setDraft((d) => ({ ...d, date: e.target.value }))}
            />
          </Field>
        </div>
        <Field label={t.common.description}>
          <Input
            required
            value={draft.description}
            onChange={(e) => setDraft((d) => ({ ...d, description: e.target.value }))}
          />
        </Field>
        <Field label={t.finance.receiptPhotoOrPdfOptional}>
          {draft.receipt_file_id ? (
            <div className="flex items-center justify-between rounded-xl border border-[var(--od-border-strong)] bg-[var(--od-surface-subtle)] px-3 py-2 text-xs">
              <span className="flex items-center gap-1.5 truncate text-[var(--od-ink)]">
                <Paperclip size={13} className="shrink-0 text-[var(--od-ink-muted)]" />
                <span className="truncate">{receiptName || t.finance.receiptAttached}</span>
              </span>
              <div className="flex items-center gap-1">
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  className="h-6 px-2 text-xs"
                  onClick={() =>
                    setPreview({
                      id: draft.receipt_file_id,
                      name: receiptName || t.common.receipt2,
                    })
                  }
                >
                  {t.common.preview}
                </Button>
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  className="h-6 px-1.5 text-xs text-[var(--od-bad)]"
                  onClick={() => {
                    setDraft((d) => ({ ...d, receipt_file_id: "" }));
                    setReceiptName("");
                  }}
                >
                  <X size={13} />
                </Button>
              </div>
            </div>
          ) : (
            <label className="flex cursor-pointer items-center justify-center gap-2 rounded-xl border border-dashed border-[var(--od-border-strong)] bg-[var(--od-surface-subtle)]/50 px-3 py-3.5 text-xs text-[var(--od-ink-secondary)] transition hover:border-[var(--od-ink)] hover:text-[var(--od-ink)]">
              <Upload size={14} />
              <span>{uploading ? t.common.uploading : t.finance.uploadReceiptPhotoOrPdf}</span>
              <input
                type="file"
                accept="image/*,application/pdf"
                className="hidden"
                disabled={uploading}
                onChange={(e) => onReceiptPicked(e.target.files?.[0])}
              />
            </label>
          )}
        </Field>
      </FormDialog>

      <FormDialog
        open={paying !== null}
        onOpenChange={(o) => !o && setPaying(null)}
        title={t.finance.payExpense}
        submitLabel={t.common.pay}
        busy={pay.isPending}
        onSubmit={() => pay.mutate({ money_account_id: account })}
      >
        {paying && (
          <div className="rounded-xl border border-[var(--od-border)] bg-[var(--od-surface-subtle)] px-3.5 py-2.5 text-sm">
            <div className="flex items-center justify-between gap-2">
              <span className="font-medium text-[var(--od-ink)]">{paying.description}</span>
              <span className="od-tabular font-semibold text-[var(--od-ink)]">
                {formatMoney(paying.amount)}
              </span>
            </div>
            {paying.number && (
              <p className="od-mono mt-0.5 text-xs text-[var(--od-ink-muted)]">{paying.number}</p>
            )}
          </div>
        )}
        <Field label={t.common.payFrom}>
          <NativeSelect required value={account} onChange={(e) => setAccount(e.target.value)}>
            <option value="">{t.common.choose}</option>
            {usable.map((a) => (
              <option key={a.id} value={a.id}>
                {a.name}
              </option>
            ))}
          </NativeSelect>
        </Field>
      </FormDialog>

      <FormDialog
        open={rejecting !== null}
        onOpenChange={(o) => !o && setRejecting(null)}
        title={fmt(t.finance.rejectExpense, { number: rejecting?.number ?? "" })}
        submitLabel={t.finance.rejectExpense2}
        busy={reject.isPending}
        onSubmit={() => reject.mutate({ note: rejectNote })}
      >
        <Field label={t.finance.reasonForRejection}>
          <Textarea
            rows={3}
            required
            value={rejectNote}
            onChange={(e) => setRejectNote(e.target.value)}
            placeholder={t.finance.explainWhyThisExpenseIs}
          />
        </Field>
      </FormDialog>

      <FilePreviewDialog
        fileId={preview?.id ?? null}
        fileName={preview?.name}
        onClose={() => setPreview(null)}
      />
    </div>
  );
}
