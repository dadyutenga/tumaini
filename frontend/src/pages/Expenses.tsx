import { useState } from "react";
import { Check, Eye, Paperclip, Plus, Upload, X } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  DataGrid,
  Field,
  FormDialog,
  NativeSelect,
  PageHeader,
  Pager,
  StatusBadge,
} from "@/components/shared/kit";
import { FilePreviewDialog } from "@/components/shared/FilePreview";
import { useAuth } from "@/features/auth/auth";
import { errorMessage, useGet, useGetPage, useWrite } from "@/hooks/useApi";
import { isPendingApproval, uploadFile } from "@/lib/api";
import { formatDate, formatMoney, today } from "@/lib/formatters";
import type { Branch, MoneyAccount } from "@/types";

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

const STATUS_OPTIONS = [
  ["submitted", "Waiting approval"],
  ["approved", "Approved"],
  ["paid", "Paid"],
  ["rejected", "Rejected"],
  ["cancelled", "Cancelled"],
];

export default function Expenses() {
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
  };
  const [draft, setDraft] = useState(blankDraft);

  const create = useWrite<Record<string, unknown>, Expense & { status: string }>(
    "POST",
    "/expenses",
    {
      idempotent: true,
      success: "Expense submitted",
      invalidate: ["/expenses", "/dashboard", "/approvals"],
      onSuccess: (result) => {
        setOpen(false);
        setReceiptName("");
        setDraft({
          category_id: "",
          amount: "",
          description: "",
          date: today(),
          receipt_file_id: "",
        });
        if (isPendingApproval(result)) return;
      },
    },
  );

  const pay = useWrite<{ money_account_id: string }, unknown>(
    "POST",
    () => `/expenses/${paying?.id}/pay`,
    {
      idempotent: true,
      success: "Expense paid",
      invalidate: ["/money-accounts", "/transactions", "/expenses", "/dashboard"],
      onSuccess: () => setPaying(null),
    },
  );

  const approve = useWrite<{ id: string }, unknown>(
    "POST",
    (vars) => `/expenses/${vars.id}/approve`,
    {
      toBody: () => ({}),
      success: "Expense approved",
      invalidate: ["/expenses", "/approvals", "/dashboard"],
    },
  );

  const reject = useWrite<{ note: string }, unknown>(
    "POST",
    () => `/expenses/${rejecting?.id}/reject`,
    {
      success: "Expense rejected",
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
      success: "Expense cancelled",
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
      toast.success("Receipt uploaded");
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setUploading(false);
    }
  }

  const usable = (accounts.data ?? []).filter((a) => a.is_active);
  const activeLocations = (branches.data ?? []).filter((b) => b.is_active);

  return (
    <>
      <PageHeader
        title="Expenses"
        description="Record spending with receipt photos. Submitted expenses are approved before payment."
        actions={
          canAny("finance.expense.submit", "finance.expense.enter") && (
            <Button onClick={() => setOpen(true)}>
              <Plus size={16} /> New expense
            </Button>
          )
        }
      />
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <NativeSelect
          aria-label="Filter by status"
          className="max-w-[13rem]"
          value={status}
          onChange={(e) => {
            setStatus(e.target.value);
            setPage(1);
          }}
        >
          <option value="">All statuses</option>
          {STATUS_OPTIONS.map(([v, label]) => (
            <option key={v} value={v}>
              {label}
            </option>
          ))}
        </NativeSelect>
        {isAllLocations && activeLocations.length > 1 && (
          <NativeSelect
            aria-label="Filter by location"
            className="max-w-[14rem]"
            value={locationId}
            onChange={(e) => {
              setLocationId(e.target.value);
              setPage(1);
            }}
          >
            <option value="">All locations</option>
            {activeLocations.map((b) => (
              <option key={b.id} value={b.id}>
                {b.name}
              </option>
            ))}
          </NativeSelect>
        )}
      </div>
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(e) => e.id}
        empty="No expenses found"
        columns={[
          { header: "Number", cell: (e) => e.number ?? "-" },
          { header: "Date", cell: (e) => (e.date ? formatDate(e.date) : "-") },
          { header: "Location", cell: (e) => e.location_name ?? "-" },
          { header: "Category", cell: (e) => e.category_name ?? "-" },
          {
            header: "Description",
            cell: (e) => (
              <div className="flex items-center gap-1.5">
                <span>{e.description}</span>
                {e.receipt_file_id && (
                  <button
                    type="button"
                    title="Preview receipt photo"
                    className="inline-flex items-center gap-0.5 rounded px-1.5 py-0.5 text-xs text-primary hover:bg-primary/10"
                    onClick={() =>
                      setPreview({
                        id: e.receipt_file_id!,
                        name: `Receipt ${e.number ?? e.id}`,
                      })
                    }
                  >
                    <Eye size={13} /> Receipt
                  </button>
                )}
              </div>
            ),
          },
          { header: "By", cell: (e) => e.submitted_by_name ?? "-" },
          { header: "Amount", align: "right", cell: (e) => formatMoney(e.amount) },
          { header: "Status", cell: (e) => <StatusBadge value={e.status} /> },
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
                        className="text-emerald-700 hover:text-emerald-800"
                        disabled={approve.isPending}
                        onClick={() => approve.mutate({ id: e.id })}
                      >
                        <Check size={14} /> Approve
                      </Button>
                      <Button
                        size="sm"
                        variant="outline"
                        className="text-rose-700 hover:text-rose-800"
                        onClick={() => {
                          setRejecting(e);
                          setRejectNote("");
                        }}
                      >
                        <X size={14} /> Reject
                      </Button>
                    </>
                  )}
                  {can("finance.expense.pay") && e.status === "approved" && (
                    <Button size="sm" variant="outline" onClick={() => setPaying(e)}>
                      Pay
                    </Button>
                  )}
                  {canCancel && (
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={cancel.isPending}
                      onClick={() => cancel.mutate({ id: e.id })}
                    >
                      Cancel
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
        title="New expense"
        busy={create.isPending || uploading}
        onSubmit={() => {
          const payload: Record<string, unknown> = {
            category_id: draft.category_id,
            amount: draft.amount,
            description: draft.description,
            date: draft.date,
          };
          if (draft.receipt_file_id) payload.receipt_file_id = draft.receipt_file_id;
          create.mutate(payload);
        }}
      >
        <Field label="Category">
          <NativeSelect
            required
            value={draft.category_id}
            onChange={(e) => setDraft((d) => ({ ...d, category_id: e.target.value }))}
          >
            <option value="">Choose…</option>
            {categories.data?.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label="Amount">
            <Input
              required
              inputMode="decimal"
              value={draft.amount}
              onChange={(e) => setDraft((d) => ({ ...d, amount: e.target.value }))}
            />
          </Field>
          <Field label="Date">
            <Input
              required
              type="date"
              value={draft.date}
              onChange={(e) => setDraft((d) => ({ ...d, date: e.target.value }))}
            />
          </Field>
        </div>
        <Field label="Description">
          <Input
            required
            value={draft.description}
            onChange={(e) => setDraft((d) => ({ ...d, description: e.target.value }))}
          />
        </Field>
        <Field label="Receipt photo or PDF (optional)">
          {draft.receipt_file_id ? (
            <div className="flex items-center justify-between rounded-md border bg-muted/40 px-3 py-2 text-xs">
              <span className="flex items-center gap-1.5 truncate">
                <Paperclip size={13} />
                <span className="truncate">{receiptName || "Receipt attached"}</span>
              </span>
              <div className="flex items-center gap-1">
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  className="h-6 px-2 text-xs"
                  onClick={() =>
                    setPreview({ id: draft.receipt_file_id, name: receiptName || "Receipt" })
                  }
                >
                  Preview
                </Button>
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  className="h-6 px-1.5 text-xs text-destructive"
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
            <label className="flex cursor-pointer items-center justify-center gap-2 rounded-md border border-dashed px-3 py-3 text-xs text-muted-foreground transition hover:border-primary hover:text-foreground">
              <Upload size={14} />
              <span>{uploading ? "Uploading…" : "Upload receipt photo or PDF"}</span>
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
        title="Pay expense"
        submitLabel="Pay"
        busy={pay.isPending}
        onSubmit={() => pay.mutate({ money_account_id: account })}
      >
        <Field label="Pay from">
          <NativeSelect required value={account} onChange={(e) => setAccount(e.target.value)}>
            <option value="">Choose…</option>
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
        title={`Reject expense ${rejecting?.number ?? ""}`}
        submitLabel="Reject expense"
        busy={reject.isPending}
        onSubmit={() => reject.mutate({ note: rejectNote })}
      >
        <Field label="Reason for rejection">
          <Textarea
            rows={3}
            required
            value={rejectNote}
            onChange={(e) => setRejectNote(e.target.value)}
            placeholder="Explain why this expense is rejected…"
          />
        </Field>
      </FormDialog>

      <FilePreviewDialog
        fileId={preview?.id ?? null}
        fileName={preview?.name}
        onClose={() => setPreview(null)}
      />
    </>
  );
}
