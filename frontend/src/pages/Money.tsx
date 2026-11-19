import { useState } from "react";
import {
  ArrowRightLeft,
  Banknote,
  Building2,
  Download,
  FileText,
  Plus,
  Smartphone,
  Wallet,
} from "lucide-react";
import { toast } from "sonner";
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
import { IncomeButton, PaymentsTab, PeriodLock } from "@/pages/MoneyExtras";
import { errorMessage, useGet, useGetPage, useWrite } from "@/hooks/useApi";
import { download } from "@/lib/api";
import { downloadCsv } from "@/lib/csv";
import { formatDate, formatMoney, today } from "@/lib/formatters";
import type { MoneyAccount } from "@/types";

interface Txn {
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
  if (type === "bank") return <Building2 size={18} className="text-primary" />;
  if (type === "mobile_money") return <Smartphone size={18} className="text-primary" />;
  if (type === "petty_cash") return <Wallet size={18} className="text-primary" />;
  return <Banknote size={18} className="text-primary" />;
}

export default function Money() {
  const { can } = useAuth();
  const [tab, setTab] = useState("accounts");
  const accounts = useGet<MoneyAccount[]>("/money-accounts");

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
    { enabled: can("transactions.view") },
  );

  const [creating, setCreating] = useState(false);
  const [transferring, setTransferring] = useState(false);
  const [reconciling, setReconciling] = useState<MoneyAccount | null>(null);
  const [recActual, setRecActual] = useState("");
  const [recNote, setRecNote] = useState("");

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
    fee: "0",
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
        fee: "0",
        reference: "",
        note: "",
      });
    },
  });
  const reconcile = useWrite<{ actual_balance: string; note: string }, unknown>(
    "POST",
    () => `/money-accounts/${reconciling?.id}/reconcile`,
    {
      idempotent: true,
      invalidate: ["/money-accounts", "/transactions"],
      success: "Account reconciled",
      onSuccess: () => {
        setReconciling(null);
        setRecActual("");
        setRecNote("");
      },
    },
  );

  const showBalance = can("accounts.view_balance");
  const active = accounts.data?.filter((a) => a.is_active) ?? [];
  const totalBalance = (accounts.data ?? []).reduce((sum, a) => sum + Number(a.balance ?? 0), 0);

  function openAccountTransactions(id: string) {
    if (!can("transactions.view")) return;
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
    <>
      <PageHeader
        title="Money"
        description="Cash, bank and mobile money accounts and every movement between them."
        actions={
          <>
            {can("transfers_money.create") && (
              <Button variant="outline" onClick={() => setTransferring(true)}>
                <ArrowRightLeft size={16} /> Transfer
              </Button>
            )}
            <IncomeButton accounts={active} />
            {can("accounts.manage") && (
              <Button onClick={() => setCreating(true)}>
                <Plus size={16} /> New account
              </Button>
            )}
          </>
        }
      />
      <PeriodLock />
      <Tabs value={tab} onValueChange={setTab}>
        <TabsList>
          <TabsTrigger value="accounts">Accounts</TabsTrigger>
          {can("transactions.view") && <TabsTrigger value="transactions">Transactions</TabsTrigger>}
          {can("payments.view") && <TabsTrigger value="payments">Payments</TabsTrigger>}
        </TabsList>

        <TabsContent value="accounts" className="space-y-4">
          {showBalance && accounts.data && accounts.data.length > 0 && (
            <div className="rounded-xl border bg-card p-4 shadow-sm">
              <p className="text-xs uppercase tracking-wider text-muted-foreground">
                Total balance across all accounts
              </p>
              <p className="mt-1 text-2xl font-bold tabular-nums">{formatMoney(totalBalance)}</p>
            </div>
          )}

          {accounts.isLoading ? (
            <p className="py-10 text-center text-sm text-muted-foreground">Loading accounts…</p>
          ) : !accounts.data?.length ? (
            <p className="py-10 text-center text-sm text-muted-foreground">No accounts yet</p>
          ) : (
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
              {accounts.data.map((a) => (
                <div
                  key={a.id}
                  className="flex flex-col justify-between rounded-xl border bg-card p-4 shadow-sm transition hover:border-primary/60"
                >
                  <button
                    type="button"
                    onClick={() => openAccountTransactions(a.id)}
                    className="text-left"
                  >
                    <div className="flex items-start justify-between gap-2">
                      <div className="flex items-center gap-2.5">
                        <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-primary/10">
                          <AccountIcon type={a.type} />
                        </div>
                        <div>
                          <p className="font-semibold leading-tight">{a.name}</p>
                          <p className="text-xs capitalize text-muted-foreground">
                            {a.type.replaceAll("_", " ")}
                            {a.provider ? ` · ${a.provider}` : ""}
                          </p>
                        </div>
                      </div>
                      <StatusBadge value={a.is_active ? "active" : "inactive"} />
                    </div>
                    {showBalance && (
                      <p className="mt-4 text-xl font-bold tabular-nums">
                        {formatMoney(a.balance)}
                      </p>
                    )}
                    {a.account_number && (
                      <p className="mt-1 text-xs text-muted-foreground">
                        Acc: {a.account_number}
                      </p>
                    )}
                  </button>

                  <div className="mt-4 flex flex-wrap items-center gap-2 border-t pt-3">
                    {can("transactions.view") && (
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
                    {can("accounts.reconcile") && a.is_active && (
                      <Button
                        size="sm"
                        variant="outline"
                        className="ml-auto h-7 px-2 text-xs"
                        onClick={() => {
                          setReconciling(a);
                          setRecActual(String(a.balance ?? "0"));
                          setRecNote("");
                        }}
                      >
                        Reconcile
                      </Button>
                    )}
                  </div>
                </div>
              ))}
            </div>
          )}
        </TabsContent>

        <TabsContent value="transactions" className="space-y-4">
          <div className="flex flex-wrap items-end gap-3 rounded-xl border bg-card p-3">
            <div className="min-w-[11rem] flex-1">
              <label className="mb-1 block text-xs text-muted-foreground">Account</label>
              <NativeSelect
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
            </div>
            <div>
              <label className="mb-1 block text-xs text-muted-foreground">From</label>
              <Input
                type="date"
                value={txFrom}
                onChange={(e) => {
                  setTxFrom(e.target.value);
                  setTxPage(1);
                }}
              />
            </div>
            <div>
              <label className="mb-1 block text-xs text-muted-foreground">To</label>
              <Input
                type="date"
                value={txTo}
                onChange={(e) => {
                  setTxTo(e.target.value);
                  setTxPage(1);
                }}
              />
            </div>
            <div className="min-w-[12rem] flex-1">
              <label className="mb-1 block text-xs text-muted-foreground">Search</label>
              <Input
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
            <Button
              variant="outline"
              size="sm"
              disabled={!txns.data?.data?.length}
              onClick={exportTransactionsCsv}
            >
              <Download size={14} /> Export CSV
            </Button>
          </div>

          <DataGrid
            rows={txns.data?.data}
            loading={txns.isLoading}
            rowKey={(t) => `${t.date}-${t.account}-${t.type}-${t.description}-${t.in}-${t.out}`}
            empty="No movements match your filters"
            columns={[
              { header: "Date", cell: (t) => formatDate(t.date) },
              { header: "Account", cell: (t) => t.account },
              { header: "Description", cell: (t) => t.description },
              {
                header: "Money in",
                align: "right",
                cell: (t) =>
                  Number(t.in) ? (
                    <span className="font-medium text-emerald-700">{formatMoney(t.in)}</span>
                  ) : (
                    ""
                  ),
              },
              {
                header: "Money out",
                align: "right",
                cell: (t) =>
                  Number(t.out) ? (
                    <span className="font-medium text-rose-700">{formatMoney(t.out)}</span>
                  ) : (
                    ""
                  ),
              },
            ]}
          />
          <Pager meta={txns.data?.meta} onPage={setTxPage} />
        </TabsContent>

        {can("payments.view") && (
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
        <div className="grid grid-cols-2 gap-3">
          <Field label="Amount">
            <Input
              required
              inputMode="decimal"
              value={tr.amount}
              onChange={(e) => setTr((t) => ({ ...t, amount: e.target.value }))}
            />
          </Field>
          <Field label="Fee">
            <Input
              inputMode="decimal"
              value={tr.fee}
              onChange={(e) => setTr((t) => ({ ...t, fee: e.target.value }))}
            />
          </Field>
        </div>
        <Field label="Reference">
          <Input
            value={tr.reference}
            onChange={(e) => setTr((t) => ({ ...t, reference: e.target.value }))}
          />
        </Field>
      </FormDialog>

      <FormDialog
        open={reconciling !== null}
        onOpenChange={(o) => !o && setReconciling(null)}
        title={`Reconcile ${reconciling?.name ?? ""}`}
        submitLabel="Post adjustment"
        busy={reconcile.isPending}
        onSubmit={() => reconcile.mutate({ actual_balance: recActual, note: recNote })}
      >
        {reconciling && showBalance && (
          <p className="text-sm text-muted-foreground">
            Current system balance: <strong>{formatMoney(reconciling.balance)}</strong>
          </p>
        )}
        <Field label="Actual balance">
          <Input
            required
            inputMode="decimal"
            value={recActual}
            onChange={(e) => setRecActual(e.target.value)}
          />
        </Field>
        <Field label="Note / statement reference">
          <Input
            value={recNote}
            onChange={(e) => setRecNote(e.target.value)}
            placeholder="e.g. Monthly bank statement"
          />
        </Field>
      </FormDialog>
    </>
  );
}
