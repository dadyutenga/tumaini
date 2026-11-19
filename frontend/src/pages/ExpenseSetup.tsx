import { useState } from "react";
import { Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
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
import { formatDate, formatMoney, today } from "@/lib/formatters";
import type { MoneyAccount } from "@/types";

interface Category {
  id: string;
  name: string;
  ledger_code: string;
  ledger_name: string;
  is_active: boolean;
}
interface Recurring {
  id: string;
  description: string;
  category_name: string;
  amount: string;
  frequency: string;
  next_run: string | null;
  is_active: boolean;
}
interface Petty {
  id: string;
  name: string;
  balance: string;
  branch_name: string;
  last_reconciled: string | null;
  custodian_name: string | null;
}

function CategoriesTab() {
  const list = useGet<Category[]>("/expense-categories", { all: true });
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const create = useWrite("POST", "/expense-categories", {
    invalidate: ["/expense-categories"],
    success: "Category added",
    onSuccess: () => {
      setOpen(false);
      setName("");
    },
  });
  const toggle = useWrite<Category, unknown>("PUT", (c) => `/expense-categories/${c.id}`, {
    invalidate: ["/expense-categories"],
    success: "Category updated",
    toBody: (c) => ({ name: c.name, ledger_code: c.ledger_code, is_active: c.is_active }),
  });
  return (
    <>
      <div className="mb-4 flex justify-end">
        <Button onClick={() => setOpen(true)}>
          <Plus size={16} /> New category
        </Button>
      </div>
      <DataGrid
        rows={list.data}
        loading={list.isLoading}
        rowKey={(c) => c.id}
        columns={[
          { header: "Name", cell: (c) => <span className="font-medium">{c.name}</span> },
          { header: "Ledger account", cell: (c) => `${c.ledger_code} ${c.ledger_name}` },
          {
            header: "Status",
            cell: (c) => <StatusBadge value={c.is_active ? "active" : "inactive"} />,
          },
          {
            header: "",
            cell: (c) => (
              <Button
                size="sm"
                variant="ghost"
                onClick={() => toggle.mutate({ ...c, is_active: !c.is_active })}
              >
                {c.is_active ? "Deactivate" : "Activate"}
              </Button>
            ),
          },
        ]}
      />
      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title="New expense category"
        busy={create.isPending}
        onSubmit={() => create.mutate({ name })}
      >
        <Field label="Name">
          <Input required value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
      </FormDialog>
    </>
  );
}

function RecurringTab() {
  const [page, setPage] = useState(1);
  const list = useGetPage<Recurring>("/recurring-expenses", { page });
  const categories = useGet<Category[]>("/expense-categories");
  const blank = {
    category_id: "",
    amount: "",
    description: "",
    frequency: "monthly",
    start_date: today(),
  };
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState(blank);
  const create = useWrite("POST", "/recurring-expenses", {
    invalidate: ["/recurring-expenses"],
    success: "Recurring expense saved",
    onSuccess: () => {
      setOpen(false);
      setDraft(blank);
    },
  });
  const toggle = useWrite<Recurring, unknown>(
    "POST",
    (r) => `/recurring-expenses/${r.id}/${r.is_active ? "pause" : "resume"}`,
    { invalidate: ["/recurring-expenses"], success: "Updated", toBody: () => ({}) },
  );
  return (
    <>
      <div className="mb-4 flex justify-end">
        <Button onClick={() => setOpen(true)}>
          <Plus size={16} /> New recurring expense
        </Button>
      </div>
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(r) => r.id}
        empty="No recurring expenses"
        columns={[
          {
            header: "Description",
            cell: (r) => <span className="font-medium">{r.description}</span>,
          },
          { header: "Category", cell: (r) => r.category_name },
          { header: "Repeats", cell: (r) => r.frequency },
          { header: "Next run", cell: (r) => (r.next_run ? formatDate(r.next_run) : "-") },
          { header: "Amount", align: "right", cell: (r) => formatMoney(r.amount) },
          {
            header: "Status",
            cell: (r) => <StatusBadge value={r.is_active ? "active" : "paused"} />,
          },
          {
            header: "",
            cell: (r) => (
              <Button size="sm" variant="ghost" onClick={() => toggle.mutate(r)}>
                {r.is_active ? "Pause" : "Resume"}
              </Button>
            ),
          },
        ]}
      />
      <Pager meta={list.data?.meta} onPage={setPage} />
      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title="New recurring expense"
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
        <Field label="Description">
          <Input
            required
            value={draft.description}
            onChange={(e) => setDraft((d) => ({ ...d, description: e.target.value }))}
          />
        </Field>
        <div className="grid grid-cols-3 gap-3">
          <Field label="Amount">
            <Input
              required
              inputMode="decimal"
              value={draft.amount}
              onChange={(e) => setDraft((d) => ({ ...d, amount: e.target.value }))}
            />
          </Field>
          <Field label="Repeats">
            <NativeSelect
              value={draft.frequency}
              onChange={(e) => setDraft((d) => ({ ...d, frequency: e.target.value }))}
            >
              <option value="weekly">Weekly</option>
              <option value="monthly">Monthly</option>
              <option value="yearly">Yearly</option>
            </NativeSelect>
          </Field>
          <Field label="Starts">
            <Input
              required
              type="date"
              value={draft.start_date}
              onChange={(e) => setDraft((d) => ({ ...d, start_date: e.target.value }))}
            />
          </Field>
        </div>
      </FormDialog>
    </>
  );
}

function PettyTab() {
  const list = useGet<Petty[]>("/petty-cash");
  const accounts = useGet<MoneyAccount[]>("/money-accounts");
  const [topUp, setTopUp] = useState<Petty | null>(null);
  const [recon, setRecon] = useState<Petty | null>(null);
  const [from, setFrom] = useState("");
  const [amount, setAmount] = useState("");
  const [actual, setActual] = useState("");
  const done = { idempotent: true, invalidate: ["/petty-cash", "/money"] };
  const top = useWrite<unknown, unknown>("POST", () => `/petty-cash/${topUp?.id}/top-up`, {
    ...done,
    success: "Petty cash topped up",
    onSuccess: () => {
      setTopUp(null);
      setAmount("");
    },
  });
  const reconcile = useWrite<unknown, unknown>("POST", () => `/petty-cash/${recon?.id}/reconcile`, {
    ...done,
    success: "Count recorded",
    onSuccess: () => {
      setRecon(null);
      setActual("");
    },
  });
  return (
    <>
      <DataGrid
        rows={list.data}
        loading={list.isLoading}
        rowKey={(p) => p.id}
        empty="No petty cash accounts. Add one in Money with the type petty cash."
        columns={[
          { header: "Account", cell: (p) => <span className="font-medium">{p.name}</span> },
          { header: "Branch", cell: (p) => p.branch_name },
          { header: "Custodian", cell: (p) => p.custodian_name ?? "-" },
          {
            header: "Last counted",
            cell: (p) => (p.last_reconciled ? formatDate(p.last_reconciled) : "Never"),
          },
          { header: "Balance", align: "right", cell: (p) => formatMoney(p.balance) },
          {
            header: "",
            cell: (p) => (
              <div className="flex gap-2">
                <Button size="sm" onClick={() => setTopUp(p)}>
                  Top up
                </Button>
                <Button size="sm" variant="outline" onClick={() => setRecon(p)}>
                  Count
                </Button>
              </div>
            ),
          },
        ]}
      />
      <FormDialog
        open={topUp !== null}
        onOpenChange={(o) => !o && setTopUp(null)}
        title={`Top up ${topUp?.name ?? ""}`}
        busy={top.isPending}
        onSubmit={() => top.mutate({ from_account_id: from, amount })}
      >
        <Field label="Take from">
          <NativeSelect required value={from} onChange={(e) => setFrom(e.target.value)}>
            <option value="">Choose…</option>
            {accounts.data
              ?.filter((a) => a.is_active && a.id !== topUp?.id)
              .map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
          </NativeSelect>
        </Field>
        <Field label="Amount">
          <Input
            required
            inputMode="decimal"
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
          />
        </Field>
      </FormDialog>
      <FormDialog
        open={recon !== null}
        onOpenChange={(o) => !o && setRecon(null)}
        title={`Count ${recon?.name ?? ""}`}
        submitLabel="Record count"
        busy={reconcile.isPending}
        onSubmit={() => reconcile.mutate({ actual_balance: actual })}
      >
        <p className="text-sm text-muted-foreground">
          The system expects {formatMoney(recon?.balance)}. Any difference is posted as an over or
          short.
        </p>
        <Field label="Cash actually in the box">
          <Input
            required
            inputMode="decimal"
            value={actual}
            onChange={(e) => setActual(e.target.value)}
          />
        </Field>
      </FormDialog>
    </>
  );
}

export default function ExpenseSetup() {
  const { can } = useAuth();
  return (
    <>
      <PageHeader title="Expense setup" description="Categories, repeating bills and petty cash." />
      <Tabs defaultValue={can("expense_categories.manage") ? "categories" : "recurring"}>
        <TabsList className="mb-4">
          {can("expense_categories.manage") && (
            <TabsTrigger value="categories">Categories</TabsTrigger>
          )}
          {can("recurring_expenses.manage") && (
            <TabsTrigger value="recurring">Recurring</TabsTrigger>
          )}
          {can("petty_cash.manage") && <TabsTrigger value="petty">Petty cash</TabsTrigger>}
        </TabsList>
        {can("expense_categories.manage") && (
          <TabsContent value="categories">
            <CategoriesTab />
          </TabsContent>
        )}
        {can("recurring_expenses.manage") && (
          <TabsContent value="recurring">
            <RecurringTab />
          </TabsContent>
        )}
        {can("petty_cash.manage") && (
          <TabsContent value="petty">
            <PettyTab />
          </TabsContent>
        )}
      </Tabs>
    </>
  );
}
