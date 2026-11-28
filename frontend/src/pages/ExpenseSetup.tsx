import { useState } from "react";
import { Plus } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  DataGrid,
  Field,
  FilterBar,
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
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";

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
  const { t } = useI18n();
  const list = useGet<Category[]>("/expense-categories", { all: true });
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [isProjectCost, setIsProjectCost] = useState(false);
  const create = useWrite("POST", "/expense-categories", {
    invalidate: ["/expense-categories"],
    success: t.finance.categoryAdded,
    onSuccess: () => {
      setOpen(false);
      setName("");
      setIsProjectCost(false);
    },
  });
  const toggle = useWrite<Category, unknown>("PUT", (c) => `/expense-categories/${c.id}`, {
    invalidate: ["/expense-categories"],
    success: t.finance.categoryUpdated,
    toBody: (c) => ({
      name: c.name,
      is_project_cost: Boolean(c.is_project_cost),
      is_active: c.is_active,
    }),
  });
  return (
    <div className="space-y-4">
      <FilterBar className="justify-end">
        <Button onClick={() => setOpen(true)}>
          <Plus size={16} /> {t.common.newCategory}
        </Button>
      </FilterBar>
      <DataGrid
        rows={list.data}
        loading={list.isLoading}
        rowKey={(c) => c.id}
        columns={[
          {
            header: t.common.name2,
            cell: (c) => <span className="font-medium text-[var(--od-ink)]">{c.name}</span>,
          },
          {
            header: t.finance.projectCost,
            cell: (c) =>
              c.is_project_cost ? (
                <Badge variant="secondary">{t.finance.directCost}</Badge>
              ) : (
                <span className="text-[var(--od-ink-muted)]">{t.finance.no}</span>
              ),
          },
          {
            header: t.common.status,
            cell: (c) => <StatusBadge value={c.is_active ? "active" : "inactive"} />,
          },
          {
            header: "",
            cell: (c) => (
              <div className="flex justify-end">
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => toggle.mutate({ ...c, is_active: !c.is_active })}
                >
                  {c.is_active ? t.common.deactivate : t.common.activate}
                </Button>
              </div>
            ),
          },
        ]}
      />
      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title={t.finance.newExpenseCategory}
        busy={create.isPending}
        onSubmit={() => create.mutate({ name, is_project_cost: isProjectCost })}
      >
        <Field label={t.common.name2}>
          <Input required value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        <label className="flex cursor-pointer items-center gap-2.5 rounded-xl border border-[var(--od-border)] bg-[var(--od-surface-subtle)] px-3.5 py-2.5 text-sm text-[var(--od-ink)]">
          <input
            type="checkbox"
            checked={isProjectCost}
            onChange={(e) => setIsProjectCost(e.target.checked)}
          />
          {t.finance.countsAsADirectProject}
        </label>
      </FormDialog>
    </div>
  );
}

function RecurringTab() {
  const { t } = useI18n();
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
    success: t.finance.recurringExpenseSaved,
    onSuccess: () => {
      setOpen(false);
      setDraft(blank);
    },
  });
  const toggle = useWrite<Recurring, unknown>(
    "POST",
    (r) => `/recurring-expenses/${r.id}/${r.is_active ? "pause" : "resume"}`,
    { invalidate: ["/recurring-expenses"], success: t.finance.updated, toBody: () => ({}) },
  );
  return (
    <div className="space-y-4">
      <FilterBar className="justify-end">
        <Button onClick={() => setOpen(true)}>
          <Plus size={16} /> {t.finance.newRecurringExpense}
        </Button>
      </FilterBar>
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(r) => r.id}
        empty={t.finance.noRecurringExpenses}
        columns={[
          {
            header: t.common.description,
            cell: (r) => <span className="font-medium text-[var(--od-ink)]">{r.description}</span>,
          },
          { header: t.common.category, cell: (r) => r.category_name },
          {
            header: t.finance.schedule,
            cell: (r) => (
              <Badge variant="secondary">
                {r.day_of_month
                  ? fmt(t.finance.monthlyDay, { day_of_month: r.day_of_month })
                  : t.common.monthly}
              </Badge>
            ),
          },
          { header: t.finance.nextRun, cell: (r) => (r.next_run ? formatDate(r.next_run) : "-") },
          {
            header: t.common.amount,
            align: "right",
            cell: (r) => (
              <span className="od-tabular font-medium text-[var(--od-ink)]">
                {formatMoney(r.amount)}
              </span>
            ),
          },
          {
            header: t.common.status,
            cell: (r) => <StatusBadge value={r.is_active ? "active" : "paused"} />,
          },
          {
            header: "",
            cell: (r) => (
              <div className="flex justify-end">
                <Button size="sm" variant="ghost" onClick={() => toggle.mutate(r)}>
                  {r.is_active ? t.finance.pause : t.common.resume}
                </Button>
              </div>
            ),
          },
        ]}
      />
      <Pager meta={list.data?.meta} onPage={setPage} />
      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title={t.finance.newMonthlyRecurringExpense}
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
        <Field label={t.common.description}>
          <Input
            required
            value={draft.description}
            onChange={(e) => setDraft((d) => ({ ...d, description: e.target.value }))}
          />
        </Field>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
          <Field label={t.common.amount}>
            <Input
              required
              inputMode="decimal"
              className="od-tabular"
              value={draft.amount}
              onChange={(e) => setDraft((d) => ({ ...d, amount: e.target.value }))}
            />
          </Field>
          <Field label={t.finance.dayOfMonth128}>
            <Input
              required
              type="number"
              min={1}
              max={28}
              className="od-tabular"
              value={draft.day_of_month}
              onChange={(e) => setDraft((d) => ({ ...d, day_of_month: e.target.value }))}
            />
          </Field>
          <Field label={t.finance.firstRunDate}>
            <Input
              required
              type="date"
              value={draft.next_run_on}
              onChange={(e) => setDraft((d) => ({ ...d, next_run_on: e.target.value }))}
            />
          </Field>
        </div>
      </FormDialog>
    </div>
  );
}

function PettyTab() {
  const { t } = useI18n();
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
    success: t.finance.pettyCashToppedUp,
    onSuccess: () => {
      setTopUp(null);
      setAmount("");
    },
  });
  const reconcile = useWrite<unknown, unknown>("POST", () => `/petty-cash/${recon?.id}/reconcile`, {
    ...done,
    success: t.finance.countRecorded,
    onSuccess: () => {
      setRecon(null);
      setActual("");
    },
  });
  return (
    <div className="space-y-4">
      <DataGrid
        rows={list.data}
        loading={list.isLoading}
        rowKey={(p) => p.id}
        empty={t.finance.noPettyCashAccountsAdd}
        columns={[
          {
            header: t.common.account,
            cell: (p) => <span className="font-medium text-[var(--od-ink)]">{p.name}</span>,
          },
          { header: t.common.location, cell: (p) => p.branch_name },
          { header: t.finance.custodian, cell: (p) => p.custodian_name ?? "-" },
          {
            header: t.finance.lastCounted,
            cell: (p) => (p.last_reconciled ? formatDate(p.last_reconciled) : t.finance.never),
          },
          {
            header: t.common.balance,
            align: "right",
            cell: (p) => (
              <span className="od-tabular font-semibold text-[var(--od-ink)]">
                {formatMoney(p.balance)}
              </span>
            ),
          },
          {
            header: "",
            cell: (p) => (
              <div className="flex justify-end gap-2">
                {can("finance.account.manage") && (
                  <Button size="sm" onClick={() => setTopUp(p)}>
                    {t.finance.topUp}
                  </Button>
                )}
                {canAny("finance.account.manage", "finance.day.close") && (
                  <Button size="sm" variant="outline" onClick={() => setRecon(p)}>
                    {t.finance.count}
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
        title={fmt(t.finance.topUp2, { name: topUp?.name ?? "" })}
        busy={top.isPending}
        onSubmit={() => top.mutate({ from_account_id: from, amount })}
      >
        <Field label={t.finance.takeFrom}>
          <NativeSelect required value={from} onChange={(e) => setFrom(e.target.value)}>
            <option value="">{t.common.choose}</option>
            {accounts.data
              ?.filter((a) => a.is_active && a.id !== topUp?.id)
              .map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
          </NativeSelect>
        </Field>
        <Field label={t.common.amount}>
          <Input
            required
            inputMode="decimal"
            className="od-tabular"
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
          />
        </Field>
      </FormDialog>
      <FormDialog
        open={recon !== null}
        onOpenChange={(o) => !o && setRecon(null)}
        title={fmt(t.finance.count2, { name: recon?.name ?? "" })}
        submitLabel={t.finance.recordCount}
        busy={reconcile.isPending}
        onSubmit={() => reconcile.mutate({ actual_balance: actual })}
      >
        <div className="rounded-xl border border-[var(--od-border)] bg-[var(--od-surface-subtle)] px-3.5 py-2.5 text-sm text-[var(--od-ink-secondary)]">
          {t.finance.theSystemExpects}{" "}
          <strong className="od-tabular font-semibold text-[var(--od-ink)]">
            {formatMoney(recon?.balance)}
          </strong>
          {t.finance.anyDifferenceIsPostedAs}
        </div>
        <Field label={t.finance.cashActuallyInTheBox}>
          <Input
            required
            inputMode="decimal"
            className="od-tabular"
            value={actual}
            onChange={(e) => setActual(e.target.value)}
          />
        </Field>
      </FormDialog>
    </div>
  );
}

export default function ExpenseSetup() {
  const { t } = useI18n();
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
    <div className="space-y-6">
      <PageHeader
        title={t.common.expenseSetup}
        description={t.finance.categoriesRepeatingBillsAndPetty}
      />
      <Tabs defaultValue={firstTab}>
        <TabsList className="mb-4">
          {canCategories && <TabsTrigger value="categories">{t.common.categories}</TabsTrigger>}
          {canRecurring && <TabsTrigger value="recurring">{t.finance.recurring}</TabsTrigger>}
          {canPetty && <TabsTrigger value="petty">{t.finance.pettyCash}</TabsTrigger>}
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
    </div>
  );
}
