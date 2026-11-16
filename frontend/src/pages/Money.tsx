import { useState } from "react";
import { ArrowRightLeft, Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
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

export default function Money() {
  const { can } = useAuth();
  const accounts = useGet<MoneyAccount[]>("/money-accounts");
  const txns = useGet<Txn[]>("/transactions", undefined, { enabled: can("transactions.view") });
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
    fee: "0",
    reference: "",
    note: "",
  });

  const create = useWrite<unknown>("POST", "/money-accounts", {
    invalidate: ["/money-accounts"],
    success: "Account created",
    onSuccess: () => setCreating(false),
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
  const showBalance = can("accounts.view_balance");
  const active = accounts.data?.filter((a) => a.is_active) ?? [];

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
            {can("accounts.manage") && (
              <Button onClick={() => setCreating(true)}>
                <Plus size={16} /> New account
              </Button>
            )}
          </>
        }
      />
      <Tabs defaultValue="accounts">
        <TabsList>
          <TabsTrigger value="accounts">Accounts</TabsTrigger>
          {can("transactions.view") && <TabsTrigger value="transactions">Transactions</TabsTrigger>}
        </TabsList>
        <TabsContent value="accounts">
          <DataGrid
            rows={accounts.data}
            loading={accounts.isLoading}
            rowKey={(a) => a.id}
            columns={[
              { header: "Account", cell: (a) => <span className="font-medium">{a.name}</span> },
              {
                header: "Type",
                cell: (a) => <span className="capitalize">{a.type.replaceAll("_", " ")}</span>,
              },
              { header: "Provider", cell: (a) => a.provider ?? "-" },
              {
                header: "Status",
                cell: (a) => <StatusBadge value={a.is_active ? "active" : "inactive"} />,
              },
              ...(showBalance
                ? [
                    {
                      header: "Balance",
                      align: "right" as const,
                      cell: (a: MoneyAccount) => formatMoney(a.balance),
                    },
                  ]
                : []),
            ]}
          />
        </TabsContent>
        <TabsContent value="transactions">
          <DataGrid
            rows={txns.data ? [...txns.data].reverse() : undefined}
            loading={txns.isLoading}
            rowKey={(t) => `${t.date}-${t.account}-${t.description}-${t.in}-${t.out}`}
            empty="No movements yet"
            columns={[
              { header: "Date", cell: (t) => formatDate(t.date) },
              { header: "Account", cell: (t) => t.account },
              { header: "Description", cell: (t) => t.description },
              {
                header: "Money in",
                align: "right",
                cell: (t) => (Number(t.in) ? formatMoney(t.in) : ""),
              },
              {
                header: "Money out",
                align: "right",
                cell: (t) => (Number(t.out) ? formatMoney(t.out) : ""),
              },
            ]}
          />
        </TabsContent>
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
    </>
  );
}
