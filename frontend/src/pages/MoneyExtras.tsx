import { useState } from "react";
import { Lock, Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  DataGrid,
  Field,
  FormDialog,
  NativeSelect,
  Pager,
  StatusBadge,
} from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { useGet, useGetPage, useWrite } from "@/hooks/useApi";
import { formatDate, formatMoney, today } from "@/lib/formatters";
import type { MoneyAccount } from "@/types";

interface Payment {
  id: string;
  number: string;
  payment_date: string;
  direction: "in" | "out";
  kind: string;
  party_name?: string | null;
  account_name: string;
  amount: string;
  status: string;
}

export function PaymentsTab() {
  const { can } = useAuth();
  const [direction, setDirection] = useState("");
  const [page, setPage] = useState(1);
  const list = useGetPage<Payment>("/payments", { direction: direction || undefined, page });
  const [voiding, setVoiding] = useState<Payment | null>(null);
  const [reason, setReason] = useState("");
  const voidPayment = useWrite<{ reason: string }, unknown>(
    "POST",
    () => `/payments/${voiding?.id}/void`,
    {
      idempotent: true,
      invalidate: ["/payments", "/money", "/transactions", "/customers", "/sales"],
      success: "Payment voided",
      onSuccess: () => {
        setVoiding(null);
        setReason("");
      },
    },
  );
  return (
    <>
      <NativeSelect
        aria-label="Direction"
        className="mb-4 max-w-[12rem]"
        value={direction}
        onChange={(e) => {
          setDirection(e.target.value);
          setPage(1);
        }}
      >
        <option value="">Money in and out</option>
        <option value="in">Money in</option>
        <option value="out">Money out</option>
      </NativeSelect>
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(p) => p.id}
        empty="No payments yet"
        columns={[
          { header: "Number", cell: (p) => <span className="font-medium">{p.number}</span> },
          { header: "Date", cell: (p) => formatDate(p.payment_date) },
          { header: "Kind", cell: (p) => p.kind.replaceAll("_", " ") },
          { header: "Party", cell: (p) => p.party_name ?? "-" },
          { header: "Account", cell: (p) => p.account_name },
          {
            header: "Amount",
            align: "right",
            cell: (p) => (
              <span className={p.direction === "out" ? "text-destructive" : ""}>
                {p.direction === "out" ? "-" : ""}
                {formatMoney(p.amount)}
              </span>
            ),
          },
          { header: "Status", cell: (p) => <StatusBadge value={p.status} /> },
          {
            header: "",
            cell: (p) =>
              can("payments.void") &&
              p.status !== "voided" && (
                <Button size="sm" variant="ghost" onClick={() => setVoiding(p)}>
                  Void
                </Button>
              ),
          },
        ]}
      />
      <Pager meta={list.data?.meta} onPage={setPage} />
      <FormDialog
        open={voiding !== null}
        onOpenChange={(o) => !o && setVoiding(null)}
        title={`Void ${voiding?.number ?? "payment"}`}
        submitLabel="Void payment"
        busy={voidPayment.isPending}
        onSubmit={() => voidPayment.mutate({ reason })}
      >
        <Field label="Reason">
          <Input required value={reason} onChange={(e) => setReason(e.target.value)} />
        </Field>
      </FormDialog>
    </>
  );
}

interface IncomeCategory {
  id: string;
  name: string;
  is_active: boolean;
}

export function IncomeButton({ accounts }: { accounts: MoneyAccount[] }) {
  const { can } = useAuth();
  const categories = useGet<IncomeCategory[]>("/income-categories", undefined, {
    enabled: can("income.create"),
  });
  const blank = {
    kind: "income",
    category_id: "",
    amount: "",
    money_account_id: "",
    description: "",
    date: today(),
  };
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState(blank);
  const create = useWrite<typeof draft, unknown>("POST", "/income", {
    idempotent: true,
    invalidate: ["/money", "/transactions", "/payments"],
    success: "Income recorded",
    onSuccess: () => {
      setOpen(false);
      setDraft(blank);
    },
    toBody: (d) => ({ ...d, category_id: d.category_id || undefined }),
  });
  if (!can("income.create")) return null;
  return (
    <>
      <Button variant="outline" onClick={() => setOpen(true)}>
        <Plus size={16} /> Record income
      </Button>
      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title="Record income"
        busy={create.isPending}
        onSubmit={() => create.mutate(draft)}
      >
        <div className="grid grid-cols-2 gap-3">
          <Field label="Type">
            <NativeSelect
              value={draft.kind}
              onChange={(e) => setDraft((d) => ({ ...d, kind: e.target.value }))}
            >
              <option value="income">Other income</option>
              <option value="capital">Owner capital</option>
            </NativeSelect>
          </Field>
          <Field label="Category">
            <NativeSelect
              value={draft.category_id}
              onChange={(e) => setDraft((d) => ({ ...d, category_id: e.target.value }))}
            >
              <option value="">Default</option>
              {categories.data
                ?.filter((c) => c.is_active)
                .map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name}
                  </option>
                ))}
            </NativeSelect>
          </Field>
        </div>
        <Field label="Received into">
          <NativeSelect
            required
            value={draft.money_account_id}
            onChange={(e) => setDraft((d) => ({ ...d, money_account_id: e.target.value }))}
          >
            <option value="">Choose…</option>
            {accounts.map((a) => (
              <option key={a.id} value={a.id}>
                {a.name}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label="Amount">
            <Input
              required
              inputMode="decimal"
              value={draft.amount}
              onChange={(e) => setDraft((d) => ({ ...d, amount: e.target.value }))}
            />
          </Field>
          <Field label="Date">
            <Input
              type="date"
              value={draft.date}
              onChange={(e) => setDraft((d) => ({ ...d, date: e.target.value }))}
            />
          </Field>
        </div>
        <Field label="Description">
          <Input
            required
            value={draft.description}
            onChange={(e) => setDraft((d) => ({ ...d, description: e.target.value }))}
          />
        </Field>
      </FormDialog>
    </>
  );
}

interface Lock {
  locked_until: string | null;
  locked_by_name: string | null;
}

export function PeriodLock() {
  const { can } = useAuth();
  const lock = useGet<Lock>("/periods/lock", undefined, { enabled: can("periods.lock") });
  const [open, setOpen] = useState(false);
  const [until, setUntil] = useState("");
  const save = useWrite<{ locked_until: string | null }, unknown>("PUT", "/periods/lock", {
    invalidate: ["/periods"],
    success: "Period lock updated",
    onSuccess: () => setOpen(false),
  });
  if (!can("periods.lock") || !lock.data) return null;
  return (
    <>
      <div className="mb-4 flex flex-wrap items-center gap-3 rounded-2xl border bg-card p-4 text-sm">
        <Lock size={16} className="text-muted-foreground" />
        <span className="mr-auto">
          {lock.data.locked_until
            ? `Books are closed up to ${formatDate(lock.data.locked_until)}. Nothing can be posted before that date.`
            : "No period is locked. Lock a closed month so old figures cannot change."}
        </span>
        <Button
          size="sm"
          variant="outline"
          onClick={() => {
            setUntil(lock.data?.locked_until ?? "");
            setOpen(true);
          }}
        >
          Change
        </Button>
      </div>
      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title="Lock accounting period"
        busy={save.isPending}
        onSubmit={() => save.mutate({ locked_until: until || null })}
      >
        <Field label="Lock everything up to and including">
          <Input type="date" value={until} onChange={(e) => setUntil(e.target.value)} />
        </Field>
        <p className="text-sm text-muted-foreground">Leave the date empty to remove the lock.</p>
      </FormDialog>
    </>
  );
}
