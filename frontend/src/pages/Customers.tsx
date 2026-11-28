import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Plus, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { StatCard } from "@/components/shared/Display";
import {
  DataGrid,
  Field,
  FilterBar,
  FormDialog,
  PageHeader,
  StatusBadge,
} from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { useDebounce } from "@/hooks/useDebounce";
import { useGet, useWrite } from "@/hooks/useApi";
import { formatMoney, toNumber } from "@/lib/formatters";
import type { Customer } from "@/types";
import { useI18n } from "@/features/i18n/i18n";
import { useSearchBox } from "@/hooks/useSearchBox";

interface Debt {
  customer_id: string;
  name: string;
  phone: string | null;
  balance: string;
  bucket_0_30: string;
  bucket_31_60: string;
  bucket_61_90: string;
  bucket_over_90: string;
  unpaid_sales: number;
}

function DebtsTab() {
  const { t } = useI18n();
  const navigate = useNavigate();
  const debts = useGet<Debt[]>("/customers/debts");
  const rows = debts.data ?? [];
  return (
    <DataGrid
      rows={rows}
      loading={debts.isLoading}
      rowKey={(d) => d.customer_id}
      empty={t.customers.nobodyOwesYouAnything}
      onRowClick={(d) => navigate(`/customers/${d.customer_id}`)}
      columns={[
        {
          header: t.common.customer,
          sortValue: (d) => d.name,
          cell: (d) => <span className="font-medium">{d.name}</span>,
        },
        {
          header: t.common.n030Days,
          align: "right",
          sortValue: (d) => toNumber(d.bucket_0_30),
          cell: (d) => formatMoney(d.bucket_0_30),
        },
        {
          header: "31–60",
          align: "right",
          sortValue: (d) => toNumber(d.bucket_31_60),
          cell: (d) => formatMoney(d.bucket_31_60),
        },
        {
          header: "61–90",
          align: "right",
          sortValue: (d) => toNumber(d.bucket_61_90),
          cell: (d) => formatMoney(d.bucket_61_90),
        },
        {
          header: t.customers.over90,
          align: "right",
          sortValue: (d) => toNumber(d.bucket_over_90),
          cell: (d) => (
            <span
              className={toNumber(d.bucket_over_90) > 0 ? "font-semibold text-[var(--od-bad)]" : ""}
            >
              {formatMoney(d.bucket_over_90)}
            </span>
          ),
        },
        {
          header: t.common.owes,
          align: "right",
          sortValue: (d) => toNumber(d.balance),
          cell: (d) => <span className="font-semibold">{formatMoney(d.balance)}</span>,
        },
      ]}
      totals={
        rows.length > 0
          ? [
              t.common.total,
              formatMoney(rows.reduce((s, d) => s + toNumber(d.bucket_0_30), 0)),
              formatMoney(rows.reduce((s, d) => s + toNumber(d.bucket_31_60), 0)),
              formatMoney(rows.reduce((s, d) => s + toNumber(d.bucket_61_90), 0)),
              formatMoney(rows.reduce((s, d) => s + toNumber(d.bucket_over_90), 0)),
              formatMoney(rows.reduce((s, d) => s + toNumber(d.balance), 0)),
            ]
          : undefined
      }
    />
  );
}

const blank = {
  name: "",
  phone: "",
  email: "",
  address: "",
  tax_number: "",
  credit_limit: "0",
  payment_terms_days: "0",
  notes: "",
};

export default function Customers() {
  const { t } = useI18n();
  const { can } = useAuth();
  const navigate = useNavigate();
  const [q, setQ] = useSearchBox();
  const search = useDebounce(q, 300);
  const { data, isLoading } = useGet<Customer[]>("/customers", { q: search });
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState(blank);
  const create = useWrite<unknown, Customer>("POST", "/customers", {
    invalidate: ["/customers"],
    success: t.common.customerAdded,
    onSuccess: (c) => {
      setOpen(false);
      setDraft(blank);
      navigate(`/customers/${c.id}`);
    },
  });
  const set = (k: keyof typeof blank) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setDraft((d) => ({ ...d, [k]: e.target.value }));

  const list = (data ?? []).filter((c) => !c.is_walk_in);
  const owed = list.reduce((sum, c) => sum + Math.max(0, toNumber(c.balance)), 0);
  const withBalance = list.filter((c) => toNumber(c.balance) > 0).length;

  return (
    <div className="space-y-6">
      <PageHeader
        title={t.common.customers}
        description={t.customers.shopCustomersCreditLimitsAnd}
        actions={
          can("shop.customer.manage") && (
            <Button size="sm" onClick={() => setOpen(true)}>
              <Plus size={14} /> {t.customers.newCustomer}
            </Button>
          )
        }
      />

      <div className="grid gap-4 sm:grid-cols-3">
        <StatCard label={t.customers.totalCustomers} value={list.length} />
        <StatCard
          label={t.customers.withOpenBalance}
          value={withBalance}
          tone={withBalance > 0 ? "bad" : "neutral"}
        />
        <StatCard
          label={t.customers.totalOwedToUs}
          value={formatMoney(owed)}
          tone={owed > 0 ? "bad" : "good"}
        />
      </div>

      <Tabs defaultValue="all">
        <TabsList>
          <TabsTrigger value="all">{t.customers.allCustomers}</TabsTrigger>
          {can("finance.debt.view") && (
            <TabsTrigger value="debts">{t.customers.whoOwes}</TabsTrigger>
          )}
        </TabsList>

        <TabsContent value="all" className="space-y-4">
          <FilterBar>
            <div className="relative min-w-60 flex-1 sm:max-w-xs">
              <Search
                size={15}
                className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-[var(--od-ink-muted)]"
              />
              <Input
                aria-label={t.customers.searchCustomers}
                className="pl-9"
                placeholder={t.common.searchNameOrPhone}
                value={q}
                onChange={(e) => setQ(e.target.value)}
              />
            </div>
          </FilterBar>
          <DataGrid
            rows={list}
            loading={isLoading}
            rowKey={(c) => c.id}
            empty={t.customers.noCustomersYet}
            onRowClick={(c) => navigate(`/customers/${c.id}`)}
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
                header: t.customers.creditLimit,
                align: "right",
                sortValue: (c) => toNumber(c.credit_limit),
                cell: (c) => formatMoney(c.credit_limit),
              },
              {
                header: t.common.owes,
                align: "right",
                sortValue: (c) => toNumber(c.balance),
                cell: (c) => (
                  <span
                    className={toNumber(c.balance) > 0 ? "font-semibold text-[var(--od-bad)]" : ""}
                  >
                    {formatMoney(c.balance)}
                  </span>
                ),
              },
              {
                header: t.common.status,
                cell: (c) => <StatusBadge value={c.is_active ? "active" : "inactive"} />,
              },
            ]}
          />
        </TabsContent>

        {can("finance.debt.view") && (
          <TabsContent value="debts">
            <DebtsTab />
          </TabsContent>
        )}
      </Tabs>

      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title={t.customers.newCustomer}
        busy={create.isPending}
        onSubmit={() =>
          create.mutate({ ...draft, payment_terms_days: Number(draft.payment_terms_days) || 0 })
        }
        wide
      >
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label={t.common.name2} className="sm:col-span-2">
            <Input required value={draft.name} onChange={set("name")} />
          </Field>
          <Field label={t.common.phone}>
            <Input value={draft.phone} onChange={set("phone")} />
          </Field>
          <Field label={t.common.email}>
            <Input type="email" value={draft.email} onChange={set("email")} />
          </Field>
          <Field label={t.common.address} className="sm:col-span-2">
            <Input value={draft.address} onChange={set("address")} />
          </Field>
          {can("finance.debt.view") && (
            <Field label={t.customers.creditLimit}>
              <Input
                inputMode="decimal"
                value={draft.credit_limit}
                onChange={set("credit_limit")}
              />
            </Field>
          )}
          <Field label={t.common.paymentTermsDays}>
            <Input
              inputMode="numeric"
              value={draft.payment_terms_days}
              onChange={set("payment_terms_days")}
            />
          </Field>
        </div>
      </FormDialog>
    </div>
  );
}
