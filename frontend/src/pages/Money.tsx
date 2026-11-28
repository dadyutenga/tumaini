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
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";
import type { Translations } from "@/features/i18n/i18n";
import { statusLabel } from "@/features/i18n/status";

interface Txn {
  id: string;
  date: string;
  account: string;
  in: string;
  out: string;
  description: string;
  type: string;
}
const types = (t: Translations) => [
  ["cash", t.common.cash],
  ["bank", t.common.bank],
  ["mobile_money", t.common.mobileMoney],
  ["petty_cash", t.finance.pettyCash],
];

function AccountIcon({ type }: { type: string }) {
  if (type === "bank") return <Building2 size={16} className="text-[var(--od-ink-secondary)]" />;
  if (type === "mobile_money")
    return <Smartphone size={16} className="text-[var(--od-ink-secondary)]" />;
  if (type === "petty_cash") return <Wallet size={16} className="text-[var(--od-ink-secondary)]" />;
  return <Banknote size={16} className="text-[var(--od-ink-secondary)]" />;
}

export default function Money() {
  const { t } = useI18n();
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
    success: t.finance.accountCreated,
    onSuccess: () => {
      setCreating(false);
      setAcct({ name: "", type: "cash", provider: "", account_number: "", opening_balance: "0" });
    },
  });
  const transfer = useWrite<unknown>("POST", "/money-transfers", {
    idempotent: true,
    invalidate: ["/money-accounts", "/transactions"],
    success: t.finance.transferRecorded,
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
      [
        t.common.date,
        t.common.account,
        t.common.type,
        t.common.description,
        t.finance.moneyIn,
        t.finance.moneyOut,
      ],
      ...rows.map((item) => [
        item.date,
        item.account,
        statusLabel(t, item.type),
        item.description,
        item.in,
        item.out,
      ]),
    ]);
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title={t.common.money}
        description={t.finance.cashBankAndMobileMoney}
        actions={
          canManage && (
            <>
              <Button size="sm" variant="outline" onClick={() => setTransferring(true)}>
                <ArrowRightLeft size={14} /> {t.finance.transfer}
              </Button>
              <Button size="sm" onClick={() => setCreating(true)}>
                <Plus size={14} /> {t.finance.newAccount}
              </Button>
            </>
          )
        }
      />

      <Tabs value={tab} onValueChange={setTab}>
        <TabsList>
          {showBalance && <TabsTrigger value="accounts">{t.common.accounts}</TabsTrigger>}
          {canTxns && <TabsTrigger value="transactions">{t.finance.transactions}</TabsTrigger>}
          {canPayments && <TabsTrigger value="payments">{t.common.payments}</TabsTrigger>}
        </TabsList>

        <TabsContent value="accounts" className="space-y-5">
          {showBalance && accounts.data && accounts.data.length > 0 && (
            <div className="grid gap-4 sm:grid-cols-3">
              <StatCard
                label={t.finance.totalBalanceAcrossAllAccounts}
                value={formatMoney(totalBalance)}
                className="sm:col-span-2"
              />
              <StatCard
                label={t.finance.activeAccounts}
                value={active.length}
                trend={fmt(t.finance.total, { count: accounts.data.length })}
              />
            </div>
          )}

          {accounts.isLoading ? (
            <PageLoader />
          ) : !accounts.data?.length ? (
            <div className="od-card">
              <EmptyState title={t.finance.noAccountsYet} />
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
                            {statusLabel(t, a.type)}
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
                      <p className="od-mono od-caption mt-1">
                        {fmt(t.finance.acc, { account_number: a.account_number })}
                      </p>
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
                        {t.finance.transactions}
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
                        <FileText size={13} /> {t.common.statementPdf}
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
                aria-label={t.common.account}
                className="w-44"
                value={txAccount}
                onChange={(e) => {
                  setTxAccount(e.target.value);
                  setTxPage(1);
                }}
              >
                <option value="">{t.finance.allAccounts}</option>
                {(accounts.data ?? []).map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.name}
                  </option>
                ))}
              </NativeSelect>
              <Input
                type="date"
                aria-label={t.common.fromDate}
                className="w-36"
                value={txFrom}
                onChange={(e) => {
                  setTxFrom(e.target.value);
                  setTxPage(1);
                }}
              />
              <Input
                type="date"
                aria-label={t.common.toDate}
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
                  aria-label={t.finance.searchTransactions}
                  className="pl-9"
                  placeholder={t.finance.descriptionOrReference}
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
                  {t.common.clear}
                </Button>
              )}
            </div>
            <Button
              variant="outline"
              size="sm"
              disabled={!txns.data?.data?.length}
              onClick={exportTransactionsCsv}
            >
              <Download size={14} /> {t.common.exportCsv}
            </Button>
          </FilterBar>

          <DataGrid
            rows={txns.data?.data}
            loading={txns.isLoading}
            rowKey={(item) => item.id}
            empty={t.finance.noMovementsMatchYourFilters}
            columns={[
              {
                header: t.common.date,
                cell: (item) => (
                  <span className="text-[var(--od-ink-secondary)]">{formatDate(item.date)}</span>
                ),
              },
              {
                header: t.common.account,
                cell: (item) => <span className="font-medium">{item.account}</span>,
              },
              { header: t.common.description, cell: (item) => item.description },
              {
                header: t.finance.moneyIn2,
                align: "right",
                cell: (item) =>
                  Number(item.in) ? (
                    <span className="font-semibold text-[var(--od-good)]">
                      +{formatMoney(item.in)}
                    </span>
                  ) : (
                    ""
                  ),
              },
              {
                header: t.finance.moneyOut2,
                align: "right",
                cell: (item) =>
                  Number(item.out) ? (
                    <span className="font-semibold text-[var(--od-bad)]">
                      −{formatMoney(item.out)}
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
        title={t.finance.newMoneyAccount}
        busy={create.isPending}
        onSubmit={() => create.mutate(acct)}
      >
        <Field label={t.common.name2}>
          <Input
            required
            value={acct.name}
            onChange={(e) => setAcct((a) => ({ ...a, name: e.target.value }))}
          />
        </Field>
        <Field label={t.common.type}>
          <NativeSelect
            value={acct.type}
            onChange={(e) => setAcct((a) => ({ ...a, type: e.target.value }))}
          >
            {types(t).map(([v, l]) => (
              <option key={v} value={v}>
                {l}
              </option>
            ))}
          </NativeSelect>
        </Field>
        {(acct.type === "bank" || acct.type === "mobile_money") && (
          <div className="grid grid-cols-2 gap-3">
            <Field label={t.finance.provider}>
              <Input
                value={acct.provider}
                onChange={(e) => setAcct((a) => ({ ...a, provider: e.target.value }))}
              />
            </Field>
            <Field label={t.finance.accountNumber}>
              <Input
                value={acct.account_number}
                onChange={(e) => setAcct((a) => ({ ...a, account_number: e.target.value }))}
              />
            </Field>
          </div>
        )}
        <Field label={t.finance.openingBalance}>
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
        title={t.finance.transferMoney}
        busy={transfer.isPending}
        onSubmit={() => transfer.mutate({ ...tr, date: today() })}
      >
        {(["from_account_id", "to_account_id"] as const).map((k) => (
          <Field key={k} label={k === "from_account_id" ? t.common.from : t.common.to}>
            <NativeSelect
              required
              value={tr[k]}
              onChange={(e) => setTr((item) => ({ ...item, [k]: e.target.value }))}
            >
              <option value="">{t.common.choose}</option>
              {active.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
            </NativeSelect>
          </Field>
        ))}
        <Field label={t.common.amount}>
          <Input
            required
            inputMode="decimal"
            value={tr.amount}
            onChange={(e) => setTr((item) => ({ ...item, amount: e.target.value }))}
          />
        </Field>
        <Field label={t.common.reference}>
          <Input
            value={tr.reference}
            onChange={(e) => setTr((item) => ({ ...item, reference: e.target.value }))}
          />
        </Field>
        <p className="od-caption">{t.finance.anyBankOrMobileMoney}</p>
      </FormDialog>
    </div>
  );
}
