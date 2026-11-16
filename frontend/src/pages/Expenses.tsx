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
  StatusBadge,
} from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { useGet, useWrite } from "@/hooks/useApi";
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
}

export default function Expenses() {
  const { can } = useAuth();
  const categories = useGet<Category[]>("/expense-categories");
  const accounts = useGet<MoneyAccount[]>("/money-accounts", undefined, {
    enabled: can("accounts.view"),
  });
  const [mine, setMine] = useState<Expense[]>([]);
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
    onSuccess: (result) => {
      setOpen(false);
      if (isPendingApproval(result)) return;
      setMine((m) => [
        {
          ...result,
          description: draft.description,
          amount: result.amount ?? draft.amount,
          date: draft.date,
        },
        ...m,
      ]);
      setDraft({ category_id: "", amount: "", description: "", payee: "", date: today() });
    },
  });
  const pay = useWrite<{ money_account_id: string }, unknown>(
    "POST",
    () => `/expenses/${paying?.id}/pay`,
    {
      idempotent: true,
      success: "Expense paid",
      invalidate: ["/money"],
      onSuccess: () => {
        setMine((m) => m.map((e) => (e.id === paying?.id ? { ...e, status: "paid" } : e)));
        setPaying(null);
      },
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
      <p className="mb-3 text-sm text-muted-foreground">
        Expenses you submit in this session appear below. The API has no expense list yet, so
        earlier ones are not shown; pending ones are in Approvals.
      </p>
      <DataGrid
        rows={mine}
        rowKey={(e) => e.id}
        empty="Nothing submitted in this session"
        columns={[
          { header: "Number", cell: (e) => e.number ?? "-" },
          { header: "Date", cell: (e) => (e.date ? formatDate(e.date) : "-") },
          { header: "Description", cell: (e) => e.description },
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
