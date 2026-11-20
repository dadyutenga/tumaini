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
  is_project_cost?: boolean;
  is_active: boolean;
}
interface Recurring {
  id: string;
  description: string;
  category_name: string;
  amount: string;
  day_of_month?: number;
  frequency?: string;
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
  const [isProjectCost, setIsProjectCost] = useState(false);
  const create = useWrite("POST", "/expense-categories", {
    invalidate: ["/expense-categories"],
    success: "Category added",
    onSuccess: () => {
      setOpen(false);
      setName("");
      setIsProjectCost(false);
    },
  });
  const toggle = useWrite<Category, unknown>("PUT", (c) => `/expense-categories/${c.id}`, {
    invalidate: ["/expense-categories"],
    success: "Category updated",
    toBody: (c) => ({
      name: c.name,
      is_project_cost: Boolean(c.is_project_cost),
      is_active: c.is_active,
    }),
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
          { header: "Project cost", cell: (c) => (c.is_project_cost ? "Yes" : "No") },
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
        onSubmit={() => create.mutate({ name, is_project_cost: isProjectCost })}
      >
        <Field label="Name">
          <Input required value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={isProjectCost}
            onChange={(e) => setIsProjectCost(e.target.checked)}
          />
          Counts as a direct project cost
        </label>
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
    day_of_month: String(Math.min(28, new Date().getDate())),
    next_run_on: today(),
  };
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState(blank);
  const create = useWrite<Record<string, unknown>, unknown>("POST", "/recurring-expenses", {
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
          {
            header: "Schedule",
            cell: (r) => (r.day_of_month ? `Monthly (day ${r.day_of_month})` : "Monthly"),
          },
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
        title="New monthly recurring expense"
        busy={create.isPending}
        onSubmit={() =>
          create.mutate({
            category_id: draft.category_id,
            amount: draft.amount,
            description: draft.description,
            day_of_month: Math.min(28, Math.max(1, Number(draft.day_of_month) || 1)),
            next_run_on: draft.next_run_on,
          })
        }
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
          <Field label="Day of month (1–28)">
            <Input
              required
              type="number"
              min={1}
              max={28}
              value={draft.day_of_month}
              onChange={(e) => setDraft((d) => ({ ...d, day_of_month: e.target.value }))}
            />
          </Field>
          <Field label="First run date">
            <Input
              required
              type="date"
              value={draft.next_run_on}
              onChange={(e) => setDraft((d) => ({ ...d, next_run_on: e.target.value }))}
            />
          </Field>
        </div>
      </FormDialog>
    </>
  );
}

function PettyTab() {
  const { can, canAny } = useAuth();
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
          { header: "Location", cell: (p) => p.branch_name },
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
                {can("finance.account.manage") && (
                  <Button size="sm" onClick={() => setTopUp(p)}>
                    Top up
                  </Button>
                )}
                {canAny("finance.account.manage", "finance.day.close") && (
                  <Button size="sm" variant="outline" onClick={() => setRecon(p)}>
                    Count
                  </Button>
                )}
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
  const { canAny } = useAuth();
  const canCategories = canAny("finance.expense.enter", "settings.manage");
  const canRecurring = canAny(
    "finance.expense.enter",
    "finance.expense.approve",
    "finance.expense.pay",
  );
  const canPetty = canAny("finance.account.view", "finance.account.manage", "finance.expense.pay");
  const firstTab = canCategories ? "categories" : canRecurring ? "recurring" : "petty";
  return (
    <>
      <PageHeader title="Expense setup" description="Categories, repeating bills and petty cash." />
      <Tabs defaultValue={firstTab}>
        <TabsList className="mb-4">
          {canCategories && <TabsTrigger value="categories">Categories</TabsTrigger>}
          {canRecurring && <TabsTrigger value="recurring">Recurring</TabsTrigger>}
          {canPetty && <TabsTrigger value="petty">Petty cash</TabsTrigger>}
        </TabsList>
        {canCategories && (
          <TabsContent value="categories">
            <CategoriesTab />
          </TabsContent>
        )}
        {canRecurring && (
          <TabsContent value="recurring">
            <RecurringTab />
          </TabsContent>
        )}
        {canPetty && (
          <TabsContent value="petty">
            <PettyTab />
          </TabsContent>
        )}
      </Tabs>
    </>
  );
}
