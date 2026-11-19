import { useState } from "react";
import { Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  DataGrid,
  Field,
  FormDialog,
  NativeSelect,
  PageHeader,
  Pager,
  StatusBadge,
} from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { useGet, useGetPage, useWrite } from "@/hooks/useApi";
import { isPendingApproval } from "@/lib/api";
import { formatDate, formatMoney, today } from "@/lib/formatters";
import type { MoneyAccount } from "@/types";

interface Category {
  id: string;
  name: string;
}
interface Expense {
  id: string;
  number?: string;
  status: string;
  amount?: string;
  description: string;
  date?: string;
  category_name?: string;
  payee?: string;
  submitted_by_name?: string;
}

export default function Expenses() {
  const { can } = useAuth();
  const categories = useGet<Category[]>("/expense-categories");
  const accounts = useGet<MoneyAccount[]>("/money-accounts", undefined, {
    enabled: can("accounts.view"),
  });
  const [status, setStatus] = useState("");
  const [page, setPage] = useState(1);
  const list = useGetPage<Expense>("/expenses", { status: status || undefined, page });
  const [open, setOpen] = useState(false);
  const [paying, setPaying] = useState<Expense | null>(null);
  const [account, setAccount] = useState("");
  const [draft, setDraft] = useState({
    category_id: "",
    amount: "",
    description: "",
    payee: "",
    date: today(),
  });

  const create = useWrite<typeof draft, Expense & { status: string }>("POST", "/expenses", {
    idempotent: true,
    success: "Expense submitted",
    invalidate: ["/expenses", "/dashboard"],
    onSuccess: (result) => {
      setOpen(false);
      if (isPendingApproval(result)) return;
      setDraft({ category_id: "", amount: "", description: "", payee: "", date: today() });
    },
  });
  const pay = useWrite<{ money_account_id: string }, unknown>(
    "POST",
    () => `/expenses/${paying?.id}/pay`,
    {
      idempotent: true,
      success: "Expense paid",
      invalidate: ["/money", "/expenses"],
      onSuccess: () => setPaying(null),
    },
  );
  const usable = (accounts.data ?? []).filter((a) => a.is_active);

  return (
    <>
      <PageHeader
        title="Expenses"
        description="Record spending. Amounts above your limit go to a manager for approval."
        actions={
          can("expenses.create") && (
            <Button onClick={() => setOpen(true)}>
              <Plus size={16} /> New expense
            </Button>
          )
        }
      />
      <NativeSelect
        aria-label="Filter by status"
        className="mb-4 max-w-[12rem]"
        value={status}
        onChange={(e) => {
          setStatus(e.target.value);
          setPage(1);
        }}
      >
        <option value="">All statuses</option>
        {["pending_approval", "approved", "paid", "rejected", "reversed"].map((v) => (
          <option key={v} value={v}>
            {v.replace("_", " ")}
          </option>
        ))}
      </NativeSelect>
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(e) => e.id}
        empty="No expenses found"
        columns={[
          { header: "Number", cell: (e) => e.number ?? "-" },
          { header: "Date", cell: (e) => (e.date ? formatDate(e.date) : "-") },
          { header: "Category", cell: (e) => e.category_name ?? "-" },
          {
            header: "Description",
            cell: (e) => (
              <>
                {e.description}
                {e.payee && <span className="text-muted-foreground"> · {e.payee}</span>}
              </>
            ),
          },
          { header: "By", cell: (e) => e.submitted_by_name ?? "-" },
          { header: "Amount", align: "right", cell: (e) => formatMoney(e.amount) },
          { header: "Status", cell: (e) => <StatusBadge value={e.status} /> },
          {
            header: "",
            cell: (e) =>
              can("expenses.pay") &&
              e.status === "approved" && (
                <Button size="sm" variant="outline" onClick={() => setPaying(e)}>
                  Pay
                </Button>
              ),
          },
        ]}
      />
      <Pager meta={list.data?.meta} onPage={setPage} />
      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title="New expense"
        busy={create.isPending}
        onSubmit={() => create.mutate(draft)}
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
        <Field label="Paid to">
          <Input
            value={draft.payee}
            onChange={(e) => setDraft((d) => ({ ...d, payee: e.target.value }))}
          />
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
    </>
  );
}
