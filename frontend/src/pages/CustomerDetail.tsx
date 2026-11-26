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
  SectionCard,
  StatusBadge,
} from "@/components/shared/kit";
import { PageLoader, StatCard } from "@/components/shared/Display";
import { AttachmentsSection } from "@/components/shared/AttachmentsSection";
import { SendOutboundButton } from "@/components/shared/SendOutbound";
import { PdfButton } from "@/features/services/shared";
import { useAuth } from "@/features/auth/auth";
import { useGet, useWrite } from "@/hooks/useApi";
import { formatDate, formatMoney, today, toNumber } from "@/lib/formatters";
import type { Customer, MoneyAccount } from "@/types";

interface Line {
  date: string;
  number: string;
  type: string;
  description: string;
  debit: string;
  credit: string;
  balance: string;
  created_at?: string;
}
interface Statement {
  lines: Line[];
  opening_balance?: string;
  closing_balance: string;
}

export default function CustomerDetail() {
  const { id = "" } = useParams();
  const { can, canAny } = useAuth();
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const invalidRange = Boolean(from && to && to < from);
  const customer = useGet<Customer>(`/customers/${id}`);
  const statement = useGet<Statement>(
    `/customers/${id}/statement`,
    { from: from || undefined, to: to || undefined },
    {
      enabled: can("finance.debt.view") && !invalidRange,
    },
  );
  const accounts = useGet<MoneyAccount[]>("/money-accounts", undefined, {
    enabled: canAny("finance.account.view", "finance.payment.receive", "shop.sale.create"),
  });
  const [paying, setPaying] = useState(false);
  const [limiting, setLimiting] = useState(false);
  const [pay, setPay] = useState({
    amount: "",
    method: "cash",
    money_account_id: "",
    reference: "",
  });
  const [creditLimit, setCreditLimit] = useState("");

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
      <div className="od-card items-center py-10 text-center">
        <p className="text-[14px] font-semibold text-[var(--od-bad)]">Customer not found.</p>
        <Button asChild variant="outline" size="sm" className="mt-3">
          <Link to="/customers">Back to customers</Link>
        </Button>
      </div>
    );
  const typed = (accounts.data ?? []).filter((a) => a.is_active && a.type === pay.method);
  const availableCredit = Math.max(0, toNumber(c.credit_limit) - toNumber(c.balance));

  return (
    <div className="space-y-6">
      <PageHeader
        backTo="/customers"
        backLabel="Customers"
        title={c.name}
        badge={<StatusBadge value={c.is_active ? "active" : "inactive"} />}
        description={[c.phone, c.email].filter(Boolean).join(" · ") || undefined}
        actions={
          <>
            {can("finance.debt.view") && (
              <Button
                size="sm"
                variant="outline"
                onClick={() => {
                  setCreditLimit(c.credit_limit);
                  setLimiting(true);
                }}
              >
                Credit limit
              </Button>
            )}
            {can("finance.payment.receive") && (
              <Button size="sm" onClick={() => setPaying(true)} disabled={toNumber(c.balance) <= 0}>
                Receive payment
              </Button>
            )}
          </>
        }
      />

      <div className="grid gap-4 sm:grid-cols-3">
        <StatCard
          label="Owes"
          value={formatMoney(c.balance)}
          tone={toNumber(c.balance) > 0 ? "bad" : "good"}
        />
        <StatCard label="Credit limit" value={formatMoney(c.credit_limit)} />
        <StatCard
          label="Available credit"
          value={formatMoney(availableCredit)}
          tone={availableCredit > 0 ? "good" : "neutral"}
        />
      </div>

      {can("finance.debt.view") && (
        <section className="space-y-3">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <h2 className="od-section-title">Statement</h2>
              <p className="od-caption">Chronological sales and payment ledger</p>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <Input
                type="date"
                aria-label="Statement from date"
                className="w-36"
                value={from}
                onChange={(e) => setFrom(e.target.value)}
              />
              <Input
                type="date"
                aria-label="Statement to date"
                className="w-36"
                value={to}
                onChange={(e) => setTo(e.target.value)}
              />
              {!invalidRange && (
                <>
                  <PdfButton
                    size="sm"
                    path={`/customers/${id}/statement`}
                    query={{ from, to }}
                    filename={`statement-${c.name}.pdf`}
                  >
                    Print statement
                  </PdfButton>
                  <SendOutboundButton
                    size="sm"
                    label="Send statement"
                    title="Send customer statement"
                    path={`/customers/${id}/statement/send`}
                    extraBody={{ from: from || undefined, to: to || undefined }}
                    successMessage="Statement queued"
                    phone={c.phone}
                    whatsappText={`Statement for ${c.name}: closing balance ${formatMoney(statement.data?.closing_balance ?? c.balance)}.`}
                  />
                </>
              )}
            </div>
          </div>
          {invalidRange && (
            <p className="text-xs font-medium text-[var(--od-bad)]">to must not be before from</p>
          )}
          <DataGrid
            rows={invalidRange ? [] : statement.data?.lines}
            loading={!invalidRange && statement.isLoading}
            rowKey={(l) =>
              `${l.date}-${l.created_at ?? ""}-${l.number}-${l.type}-${l.debit}-${l.credit}`
            }
            empty={invalidRange ? "Fix the date range to view the statement" : "No activity yet"}
            columns={[
              {
                header: "Date",
                cell: (l) => (
                  <span className="text-[var(--od-ink-secondary)]">{formatDate(l.date)}</span>
                ),
              },
              {
                header: "Document",
                cell: (l) => <span className="od-mono font-medium">{l.number || "—"}</span>,
              },
              {
                header: "Type",
                cell: (l) => (
                  <span className="capitalize">
                    {(l.type || l.description || "—").replaceAll("_", " ")}
                  </span>
                ),
              },
              {
                header: "Debit",
                align: "right",
                cell: (l) => (toNumber(l.debit) > 0 ? formatMoney(l.debit) : "—"),
              },
              {
                header: "Credit",
                align: "right",
                cell: (l) =>
                  toNumber(l.credit) > 0 ? (
                    <span className="text-[var(--od-good)]">{formatMoney(l.credit)}</span>
                  ) : (
                    "—"
                  ),
              },
              {
                header: "Balance",
                align: "right",
                cell: (l) => <span className="font-medium">{formatMoney(l.balance)}</span>,
              },
            ]}
          />
        </section>
      )}

      {id && (
        <SectionCard title="Attachments">
          <AttachmentsSection entityType="customer" entityId={id} />
        </SectionCard>
      )}

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
        <Field label={pay.method === "cash" ? "Reference (optional)" : "Reference *"}>
          <Input
            required={pay.method !== "cash"}
            value={pay.reference}
            onChange={(e) => setPay((p) => ({ ...p, reference: e.target.value }))}
          />
        </Field>
        <p className="od-caption">The payment clears the oldest unpaid sales first.</p>
      </FormDialog>

      <FormDialog
        open={limiting}
        onOpenChange={setLimiting}
        title="Credit limit"
        busy={setCredit.isPending}
        onSubmit={() =>
          setCredit.mutate({
            credit_limit: creditLimit,
          })
        }
      >
        <Field label="Credit limit">
          <Input
            required
            inputMode="decimal"
            value={creditLimit}
            onChange={(e) => setCreditLimit(e.target.value)}
          />
        </Field>
      </FormDialog>
    </div>
  );
}
