import { useState } from "react";
import {
  ArrowRightLeft,
  Banknote,
  Building2,
  Download,
  FileText,
  Plus,
  Search,
  Smartphone,
  Wallet,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { EmptyState, PageLoader, StatCard } from "@/components/shared/Display";
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
import { PaymentsTab } from "@/pages/MoneyExtras";
import { errorMessage, useGet, useGetPage, useWrite } from "@/hooks/useApi";
import { download } from "@/lib/api";
import { downloadCsv } from "@/lib/csv";
import { formatDate, formatMoney, today } from "@/lib/formatters";
import type { MoneyAccount } from "@/types";

interface Txn {
  id: string;
  date: string;
  account: string;
  in: string;
  out: string;
  description: string;
  type: string;
}
const TYPES = [
  ["cash", "Cash"],
  ["bank", "Bank"],
  ["mobile_money", "Mobile money"],
  ["petty_cash", "Petty cash"],
];

function AccountIcon({ type }: { type: string }) {
  if (type === "bank") return <Building2 size={16} className="text-[var(--od-ink-secondary)]" />;
  if (type === "mobile_money")
    return <Smartphone size={16} className="text-[var(--od-ink-secondary)]" />;
  if (type === "petty_cash") return <Wallet size={16} className="text-[var(--od-ink-secondary)]" />;
  return <Banknote size={16} className="text-[var(--od-ink-secondary)]" />;
}

export default function Money() {
  const { can, canAny } = useAuth();
  const canTxns = can("finance.transaction.view");
  const canPayments = canAny("finance.payment.view_all", "finance.payment.receive");
  const showBalance = can("finance.account.view");
  const canManage = can("finance.account.manage");
  const [tab, setTab] = useState(showBalance ? "accounts" : "transactions");
  const accounts = useGet<MoneyAccount[]>("/money-accounts", undefined, {
    enabled: canAny(
      "finance.account.view",
      "finance.payment.receive",
      "shop.sale.create",
      "finance.expense.enter",
    ),
  });

  const [txAccount, setTxAccount] = useState("");
  const [txFrom, setTxFrom] = useState("");
  const [txTo, setTxTo] = useState("");
  const [txSearch, setTxSearch] = useState("");
  const [txPage, setTxPage] = useState(1);

  const txns = useGetPage<Txn>(
    "/transactions",
    {
      account_id: txAccount || undefined,
      from: txFrom || undefined,
      to: txTo || undefined,
      q: txSearch.trim() || undefined,
      page: txPage,
    },
    { enabled: canTxns },
  );

  const [creating, setCreating] = useState(false);
  const [transferring, setTransferring] = useState(false);

  const [acct, setAcct] = useState({
    name: "",
    type: "cash",
    provider: "",
    account_number: "",
    opening_balance: "0",
  });
  const [tr, setTr] = useState({
    from_account_id: "",
    to_account_id: "",
    amount: "",
    reference: "",
    note: "",
  });

  const create = useWrite<unknown>("POST", "/money-accounts", {
    invalidate: ["/money-accounts"],
    success: "Account created",
    onSuccess: () => {
      setCreating(false);
      setAcct({ name: "", type: "cash", provider: "", account_number: "", opening_balance: "0" });
    },
  });
  const transfer = useWrite<unknown>("POST", "/money-transfers", {
    idempotent: true,
    invalidate: ["/money-accounts", "/transactions"],
    success: "Transfer recorded",
    onSuccess: () => {
      setTransferring(false);
      setTr({
        from_account_id: "",
        to_account_id: "",
        amount: "",
        reference: "",
        note: "",
      });
    },
  });

  const active = accounts.data?.filter((a) => a.is_active) ?? [];
  const totalBalance = (accounts.data ?? []).reduce((sum, a) => sum + Number(a.balance ?? 0), 0);

  function openAccountTransactions(id: string) {
    if (!canTxns) return;
    setTxAccount(id);
    setTxPage(1);
    setTab("transactions");
  }

  function exportTransactionsCsv() {
    const rows = txns.data?.data ?? [];
    downloadCsv("transactions.csv", [
      ["Date", "Account", "Type", "Description", "Money In", "Money Out"],
      ...rows.map((t) => [t.date, t.account, t.type, t.description, t.in, t.out]),
    ]);
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Money"
        description="Cash, bank and mobile money accounts and every movement between them."
        actions={
          canManage && (
            <>
              <Button size="sm" variant="outline" onClick={() => setTransferring(true)}>
                <ArrowRightLeft size={14} /> Transfer
              </Button>
              <Button size="sm" onClick={() => setCreating(true)}>
                <Plus size={14} /> New account
              </Button>
            </>
          )
        }
      />

      <Tabs value={tab} onValueChange={setTab}>
        <TabsList>
          {showBalance && <TabsTrigger value="accounts">Accounts</TabsTrigger>}
          {canTxns && <TabsTrigger value="transactions">Transactions</TabsTrigger>}
          {canPayments && <TabsTrigger value="payments">Payments</TabsTrigger>}
        </TabsList>

        <TabsContent value="accounts" className="space-y-5">
          {showBalance && accounts.data && accounts.data.length > 0 && (
            <div className="grid gap-4 sm:grid-cols-3">
              <StatCard
                label="Total balance across all accounts"
                value={formatMoney(totalBalance)}
                className="sm:col-span-2"
              />
              <StatCard
                label="Active accounts"
                value={active.length}
                trend={`${accounts.data.length} total`}
              />
            </div>
          )}

          {accounts.isLoading ? (
            <PageLoader />
          ) : !accounts.data?.length ? (
            <div className="od-card">
              <EmptyState title="No accounts yet" />
            </div>
          ) : (
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
              {accounts.data.map((a) => (
                <article
                  key={a.id}
                  className="od-card justify-between gap-4 transition-colors hover:border-[var(--od-ink-muted)]"
                >
                  <button
                    type="button"
                    onClick={() => openAccountTransactions(a.id)}
                    className="cursor-pointer text-left"
                  >
                    <div className="flex items-start justify-between gap-2">
                      <div className="flex items-center gap-2.5">
                        <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-[var(--od-surface-subtle)]">
                          <AccountIcon type={a.type} />
                        </div>
                        <div className="min-w-0">
                          <p className="truncate text-[14px] font-semibold text-[var(--od-ink)]">
                            {a.name}
                          </p>
                          <p className="od-caption capitalize">
                            {a.type.replaceAll("_", " ")}
                            {a.provider ? ` · ${a.provider}` : ""}
                          </p>
                        </div>
                      </div>
                      <StatusBadge value={a.is_active ? "active" : "inactive"} />
                    </div>
                    {showBalance && (
                      <p className="od-kpi-value mt-4 text-[22px]">{formatMoney(a.balance)}</p>
                    )}
                    {a.account_number && (
                      <p className="od-mono od-caption mt-1">Acc: {a.account_number}</p>
                    )}
                  </button>

                  <div className="flex flex-wrap items-center gap-1.5 border-t border-[var(--od-border)] pt-3">
                    {canTxns && (
                      <Button
                        size="sm"
                        variant="ghost"
                        className="h-7 px-2 text-xs"
                        onClick={() => openAccountTransactions(a.id)}
                      >
                        Transactions
                      </Button>
                    )}
                    {showBalance && (
                      <Button
                        size="sm"
                        variant="ghost"
                        className="h-7 px-2 text-xs"
                        onClick={() =>
                          download(
                            `/money-accounts/${a.id}/statement`,
                            { format: "pdf" },
                            `statement-${a.name.replaceAll(/\s+/g, "-").toLowerCase()}.pdf`,
                          ).catch((e) => toast.error(errorMessage(e)))
                        }
                      >
                        <FileText size={13} /> Statement PDF
                      </Button>
                    )}
                  </div>
                </article>
              ))}
            </div>
          )}
        </TabsContent>

        <TabsContent value="transactions" className="space-y-4">
          <FilterBar className="justify-between">
            <div className="flex flex-1 flex-wrap items-center gap-2">
              <NativeSelect
                aria-label="Account"
                className="w-44"
                value={txAccount}
                onChange={(e) => {
                  setTxAccount(e.target.value);
                  setTxPage(1);
                }}
              >
                <option value="">All accounts</option>
                {(accounts.data ?? []).map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.name}
                  </option>
                ))}
              </NativeSelect>
              <Input
                type="date"
                aria-label="From date"
                className="w-36"
                value={txFrom}
                onChange={(e) => {
                  setTxFrom(e.target.value);
                  setTxPage(1);
                }}
              />
              <Input
                type="date"
                aria-label="To date"
                className="w-36"
                value={txTo}
                onChange={(e) => {
                  setTxTo(e.target.value);
                  setTxPage(1);
                }}
              />
              <div className="relative min-w-48 flex-1 sm:max-w-xs">
                <Search
                  size={15}
                  className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-[var(--od-ink-muted)]"
                />
                <Input
                  aria-label="Search transactions"
                  className="pl-9"
                  placeholder="Description or reference…"
                  value={txSearch}
                  onChange={(e) => {
                    setTxSearch(e.target.value);
                    setTxPage(1);
                  }}
                />
              </div>
              {(txAccount || txFrom || txTo || txSearch) && (
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => {
                    setTxAccount("");
                    setTxFrom("");
                    setTxTo("");
                    setTxSearch("");
                    setTxPage(1);
                  }}
                >
                  Clear
                </Button>
              )}
            </div>
            <Button
              variant="outline"
              size="sm"
              disabled={!txns.data?.data?.length}
              onClick={exportTransactionsCsv}
            >
              <Download size={14} /> Export CSV
            </Button>
          </FilterBar>

          <DataGrid
            rows={txns.data?.data}
            loading={txns.isLoading}
            rowKey={(t) => t.id}
            empty="No movements match your filters"
            columns={[
              {
                header: "Date",
                cell: (t) => (
                  <span className="text-[var(--od-ink-secondary)]">{formatDate(t.date)}</span>
                ),
              },
              { header: "Account", cell: (t) => <span className="font-medium">{t.account}</span> },
              { header: "Description", cell: (t) => t.description },
              {
                header: "Money in",
                align: "right",
                cell: (t) =>
                  Number(t.in) ? (
                    <span className="font-semibold text-[var(--od-good)]">
                      +{formatMoney(t.in)}
                    </span>
                  ) : (
                    ""
                  ),
              },
              {
                header: "Money out",
                align: "right",
                cell: (t) =>
                  Number(t.out) ? (
                    <span className="font-semibold text-[var(--od-bad)]">
                      −{formatMoney(t.out)}
                    </span>
                  ) : (
                    ""
                  ),
              },
            ]}
          />
          <Pager meta={txns.data?.meta} onPage={setTxPage} />
        </TabsContent>

        {canPayments && (
          <TabsContent value="payments">
            <PaymentsTab />
          </TabsContent>
        )}
      </Tabs>

      <FormDialog
        open={creating}
        onOpenChange={setCreating}
        title="New money account"
        busy={create.isPending}
        onSubmit={() => create.mutate(acct)}
      >
        <Field label="Name">
          <Input
            required
            value={acct.name}
            onChange={(e) => setAcct((a) => ({ ...a, name: e.target.value }))}
          />
        </Field>
        <Field label="Type">
          <NativeSelect
            value={acct.type}
            onChange={(e) => setAcct((a) => ({ ...a, type: e.target.value }))}
          >
            {TYPES.map(([v, l]) => (
              <option key={v} value={v}>
                {l}
              </option>
            ))}
          </NativeSelect>
        </Field>
        {(acct.type === "bank" || acct.type === "mobile_money") && (
          <div className="grid grid-cols-2 gap-3">
            <Field label="Provider">
              <Input
                value={acct.provider}
                onChange={(e) => setAcct((a) => ({ ...a, provider: e.target.value }))}
              />
            </Field>
            <Field label="Account number">
              <Input
                value={acct.account_number}
                onChange={(e) => setAcct((a) => ({ ...a, account_number: e.target.value }))}
              />
            </Field>
          </div>
        )}
        <Field label="Opening balance">
          <Input
            inputMode="decimal"
            value={acct.opening_balance}
            onChange={(e) => setAcct((a) => ({ ...a, opening_balance: e.target.value }))}
          />
        </Field>
      </FormDialog>

      <FormDialog
        open={transferring}
        onOpenChange={setTransferring}
        title="Transfer money"
        busy={transfer.isPending}
        onSubmit={() => transfer.mutate({ ...tr, date: today() })}
      >
        {(["from_account_id", "to_account_id"] as const).map((k) => (
          <Field key={k} label={k === "from_account_id" ? "From" : "To"}>
            <NativeSelect
              required
              value={tr[k]}
              onChange={(e) => setTr((t) => ({ ...t, [k]: e.target.value }))}
            >
              <option value="">Choose…</option>
              {active.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
            </NativeSelect>
          </Field>
        ))}
        <Field label="Amount">
          <Input
            required
            inputMode="decimal"
            value={tr.amount}
            onChange={(e) => setTr((t) => ({ ...t, amount: e.target.value }))}
          />
        </Field>
        <Field label="Reference">
          <Input
            value={tr.reference}
            onChange={(e) => setTr((t) => ({ ...t, reference: e.target.value }))}
          />
        </Field>
        <p className="od-caption">
          Any bank or mobile-money transfer charge should be recorded separately as an expense.
        </p>
      </FormDialog>
    </div>
  );
}
