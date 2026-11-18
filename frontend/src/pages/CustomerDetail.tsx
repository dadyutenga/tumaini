import { useState } from "react";
import { Link, useParams } from "react-router-dom";
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
import { PageLoader } from "@/components/shared/Display";
import { useAuth } from "@/features/auth/auth";
import { useGet, useWrite } from "@/hooks/useApi";
import { formatDate, formatMoney, toNumber } from "@/lib/formatters";
import { today } from "@/lib/formatters";
import type { Customer, MoneyAccount } from "@/types";

interface Line {
  date: string;
  number: string;
  kind: string;
  amount: string;
  paid: string;
  balance: string;
}
interface Statement {
  lines: Line[];
  closing_balance: string;
}

export default function CustomerDetail() {
  const { id = "" } = useParams();
  const { can } = useAuth();
  const customer = useGet<Customer>(`/customers/${id}`);
  const statement = useGet<Statement>(`/customers/${id}/statement`, undefined, {
    enabled: can("customers.statement"),
  });
  const accounts = useGet<MoneyAccount[]>("/money-accounts", undefined, {
    enabled: can("accounts.view"),
  });
  const [paying, setPaying] = useState(false);
  const [limiting, setLimiting] = useState(false);
  const [writingOff, setWritingOff] = useState(false);
  const [off, setOff] = useState({ amount: "", reason: "" });
  const writeOff = useWrite<typeof off, unknown>("POST", `/customers/${id}/write-off`, {
    idempotent: true,
    invalidate: ["/customers", "/reports", "/money"],
    success: "Debt written off",
    onSuccess: () => {
      setWritingOff(false);
      setOff({ amount: "", reason: "" });
    },
  });
  const [pay, setPay] = useState({
    amount: "",
    method: "cash",
    money_account_id: "",
    reference: "",
  });
  const [limit, setLimit] = useState({ credit_limit: "", payment_terms_days: "" });

  const receive = useWrite<unknown>("POST", `/customers/${id}/payments`, {
    idempotent: true,
    invalidate: ["/customers", "/money"],
    success: "Payment received",
    onSuccess: () => {
      setPaying(false);
      setPay({ amount: "", method: "cash", money_account_id: "", reference: "" });
    },
  });
  const setCredit = useWrite<unknown>("PUT", `/customers/${id}/credit-limit`, {
    invalidate: ["/customers"],
    success: "Credit limit updated",
    onSuccess: () => setLimiting(false),
  });

  if (customer.isLoading) return <PageLoader />;
  const c = customer.data;
  if (!c)
    return (
      <p className="text-destructive">
        Customer not found.{" "}
        <Link className="underline" to="/customers">
          Back
        </Link>
      </p>
    );
  const typed = (accounts.data ?? []).filter((a) => a.is_active && a.type === pay.method);

  return (
    <>
      <PageHeader
        title={c.name}
        description={[c.phone, c.email].filter(Boolean).join(" · ") || undefined}
        actions={
          <>
            <StatusBadge value={c.is_active ? "active" : "inactive"} />
            {can("customers.credit_limit") && (
              <Button
                variant="outline"
                onClick={() => {
                  setLimit({
                    credit_limit: c.credit_limit,
                    payment_terms_days: String(c.payment_terms_days),
                  });
                  setLimiting(true);
                }}
              >
                Credit limit
              </Button>
            )}
            {can("customers.write_off") && toNumber(c.balance) > 0 && (
              <Button variant="outline" onClick={() => setWritingOff(true)}>
                Write off
              </Button>
            )}
            {can("payments.receive") && (
              <Button onClick={() => setPaying(true)} disabled={toNumber(c.balance) <= 0}>
                Receive payment
              </Button>
            )}
          </>
        }
      />
      <div className="mb-6 grid gap-4 sm:grid-cols-3">
        {(
          [
            ["Owes", c.balance],
            ["Credit limit", c.credit_limit],
          ] as const
        ).map(([l, v]) => (
          <section key={l} className="rounded-2xl border bg-card p-5">
            <p className="text-sm text-muted-foreground">{l}</p>
            <p className="mt-2 text-2xl font-semibold">{formatMoney(v)}</p>
          </section>
        ))}
        <section className="rounded-2xl border bg-card p-5">
          <p className="text-sm text-muted-foreground">Payment terms</p>
          <p className="mt-2 text-2xl font-semibold">{c.payment_terms_days} days</p>
        </section>
      </div>
      {can("customers.statement") && (
        <>
          <h2 className="mb-3 text-lg font-semibold">Statement</h2>
          <DataGrid
            rows={statement.data?.lines}
            loading={statement.isLoading}
            rowKey={(l) => `${l.date}-${l.number}-${l.kind}`}
            empty="No activity yet"
            columns={[
              { header: "Date", cell: (l) => formatDate(l.date) },
              { header: "Document", cell: (l) => l.number },
              {
                header: "Type",
                cell: (l) => <span className="capitalize">{l.kind.replaceAll("_", " ")}</span>,
              },
              { header: "Amount", align: "right", cell: (l) => formatMoney(l.amount) },
              { header: "Paid", align: "right", cell: (l) => formatMoney(l.paid) },
              { header: "Balance", align: "right", cell: (l) => formatMoney(l.balance) },
            ]}
          />
        </>
      )}

      <FormDialog
        open={writingOff}
        onOpenChange={setWritingOff}
        title="Write off bad debt"
        submitLabel="Write off"
        busy={writeOff.isPending}
        onSubmit={() => writeOff.mutate(off)}
      >
        <p className="text-sm text-muted-foreground">
          The oldest unpaid sales are cleared first and the amount is booked as a bad debt expense.
          Owed now: {formatMoney(c.balance)}.
        </p>
        <Field label="Amount to write off">
          <Input
            required
            inputMode="decimal"
            value={off.amount}
            onChange={(e) => setOff((o) => ({ ...o, amount: e.target.value }))}
          />
        </Field>
        <Field label="Reason">
          <Input
            required
            value={off.reason}
            onChange={(e) => setOff((o) => ({ ...o, reason: e.target.value }))}
          />
        </Field>
      </FormDialog>
      <FormDialog
        open={paying}
        onOpenChange={setPaying}
        title="Receive payment"
        busy={receive.isPending}
        onSubmit={() => receive.mutate({ ...pay, date: today() })}
      >
        <Field label="Amount">
          <Input
            required
            inputMode="decimal"
            value={pay.amount}
            onChange={(e) => setPay((p) => ({ ...p, amount: e.target.value }))}
          />
        </Field>
        <Field label="Method">
          <NativeSelect
            value={pay.method}
            onChange={(e) =>
              setPay((p) => ({ ...p, method: e.target.value, money_account_id: "" }))
            }
          >
            <option value="cash">Cash</option>
            <option value="mobile_money">Mobile money</option>
            <option value="bank">Bank</option>
          </NativeSelect>
        </Field>
        <Field label="Into account">
          <NativeSelect
            required
            value={pay.money_account_id}
            onChange={(e) => setPay((p) => ({ ...p, money_account_id: e.target.value }))}
          >
            <option value="">Choose…</option>
            {typed.map((a) => (
              <option key={a.id} value={a.id}>
                {a.name}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field label="Reference">
          <Input
            value={pay.reference}
            onChange={(e) => setPay((p) => ({ ...p, reference: e.target.value }))}
          />
        </Field>
        <p className="text-xs text-muted-foreground">
          The payment clears the oldest unpaid sales first.
        </p>
      </FormDialog>
      <FormDialog
        open={limiting}
        onOpenChange={setLimiting}
        title="Credit limit"
        busy={setCredit.isPending}
        onSubmit={() =>
          setCredit.mutate({
            credit_limit: limit.credit_limit,
            payment_terms_days: Number(limit.payment_terms_days) || 0,
          })
        }
      >
        <Field label="Credit limit">
          <Input
            required
            inputMode="decimal"
            value={limit.credit_limit}
            onChange={(e) => setLimit((l) => ({ ...l, credit_limit: e.target.value }))}
          />
        </Field>
        <Field label="Payment terms (days)">
          <Input
            inputMode="numeric"
            value={limit.payment_terms_days}
            onChange={(e) => setLimit((l) => ({ ...l, payment_terms_days: e.target.value }))}
          />
        </Field>
      </FormDialog>
    </>
  );
}
