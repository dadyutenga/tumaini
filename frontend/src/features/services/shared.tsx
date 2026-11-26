import { useState, type ReactNode } from "react";
import { FileDown } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Field, FormDialog, NativeSelect } from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { useStaff } from "@/features/services/helpers";
import { download } from "@/lib/api";
import { errorMessage, useGet, useWrite } from "@/hooks/useApi";
import { today } from "@/lib/formatters";
import type { MoneyAccount } from "@/types";
import type { Client } from "@/types/services";

export function StaffSelect({
  value,
  onChange,
  empty = "Unassigned",
}: {
  value: string;
  onChange: (id: string) => void;
  empty?: string;
}) {
  const staff = useStaff();
  const { can } = useAuth();
  if (!can("security.user.manage")) return null;
  return (
    <NativeSelect value={value} onChange={(e) => onChange(e.target.value)}>
      <option value="">{empty}</option>
      {staff.map((u) => (
        <option key={u.id} value={u.id}>
          {u.name}
        </option>
      ))}
    </NativeSelect>
  );
}

export function ClientSelect({
  value,
  onChange,
  required,
}: {
  value: string;
  onChange: (id: string) => void;
  required?: boolean;
}) {
  const clients = useGet<Client[]>("/clients", { is_active: true, per_page: 100 });
  return (
    <NativeSelect required={required} value={value} onChange={(e) => onChange(e.target.value)}>
      <option value="">Choose a client…</option>
      {clients.data?.map((c) => (
        <option key={c.id} value={c.id}>
          {c.name}
          {c.phone ? ` · ${c.phone}` : ""}
        </option>
      ))}
    </NativeSelect>
  );
}

/** Downloads a PDF from an authenticated endpoint. */
export function PdfButton({
  path,
  filename,
  children = "PDF",
  query = {},
  size = "default",
  disabled = false,
}: {
  path: string;
  filename: string;
  children?: ReactNode;
  query?: Record<string, string>;
  size?: "default" | "sm" | "lg" | "icon";
  disabled?: boolean;
}) {
  const [busy, setBusy] = useState(false);
  return (
    <Button
      type="button"
      size={size}
      variant="outline"
      disabled={busy || disabled}
      onClick={async (e) => {
        e.stopPropagation();
        setBusy(true);
        try {
          await download(path, { format: "pdf", ...query }, filename);
        } catch (error) {
          toast.error(errorMessage(error));
        } finally {
          setBusy(false);
        }
      }}
    >
      <FileDown size={size === "sm" ? 14 : 16} /> {children}
    </Button>
  );
}

const METHODS = [
  ["cash", "Cash"],
  ["mobile_money", "Mobile money"],
  ["bank", "Bank"],
] as const;

export interface PayValues {
  amount: string;
  method: string;
  money_account_id: string;
  reference: string;
}

const blankPay: PayValues = { amount: "", method: "cash", money_account_id: "", reference: "" };

/** Dialog that collects a payment: amount, method, the account it goes into, and a reference. */
export function PaymentDialog({
  open,
  onOpenChange,
  title,
  path,
  hint,
  defaultAmount,
  invalidate,
  success = "Payment recorded",
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  path: string;
  hint?: string;
  defaultAmount?: string;
  invalidate: string[];
  success?: string;
}) {
  const { canAny } = useAuth();
  const accounts = useGet<MoneyAccount[]>("/money-accounts", undefined, {
    enabled: canAny("finance.account.view", "finance.payment.receive", "services.invoice.manage"),
  });
  const [pay, setPay] = useState<PayValues>(blankPay);
  const write = useWrite<unknown>("POST", path, {
    idempotent: true,
    invalidate,
    success,
    onSuccess: () => {
      setPay(blankPay);
      onOpenChange(false);
    },
  });
  const typed = (accounts.data ?? []).filter((a) => a.is_active && a.type === pay.method);
  const set = (k: keyof PayValues, v: string) => setPay((p) => ({ ...p, [k]: v }));
  return (
    <FormDialog
      open={open}
      onOpenChange={onOpenChange}
      title={title}
      description={hint}
      busy={write.isPending}
      submitLabel="Record payment"
      onSubmit={() =>
        write.mutate({
          ...pay,
          amount: pay.amount || defaultAmount || "",
          money_account_id: pay.money_account_id || typed[0]?.id || "",
          date: today(),
        })
      }
    >
      <Field label="Amount">
        <Input
          required
          inputMode="decimal"
          placeholder={defaultAmount}
          value={pay.amount}
          onChange={(e) => set("amount", e.target.value)}
        />
      </Field>
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Method">
          <NativeSelect
            value={pay.method}
            onChange={(e) =>
              setPay((p) => ({ ...p, method: e.target.value, money_account_id: "" }))
            }
          >
            {METHODS.map(([v, l]) => (
              <option key={v} value={v}>
                {l}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field label="Account">
          <NativeSelect
            required
            value={pay.money_account_id || typed[0]?.id || ""}
            onChange={(e) => set("money_account_id", e.target.value)}
          >
            {typed.length === 0 && <option value="">No account for this method</option>}
            {typed.map((a) => (
              <option key={a.id} value={a.id}>
                {a.name}
              </option>
            ))}
          </NativeSelect>
        </Field>
      </div>
      <Field label="Reference (optional)">
        <Input value={pay.reference} onChange={(e) => set("reference", e.target.value)} />
      </Field>
    </FormDialog>
  );
}

/** Dialog that asks for a reason before a destructive action. */
export function ReasonDialog({
  open,
  onOpenChange,
  title,
  description,
  submitLabel,
  busy,
  onSubmit,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: string;
  submitLabel: string;
  busy?: boolean;
  onSubmit: (reason: string) => void;
}) {
  const [reason, setReason] = useState("");
  return (
    <FormDialog
      open={open}
      onOpenChange={onOpenChange}
      title={title}
      description={description}
      submitLabel={submitLabel}
      busy={busy}
      onSubmit={() => {
        onSubmit(reason);
        setReason("");
      }}
    >
      <Field label="Reason">
        <Input required value={reason} onChange={(e) => setReason(e.target.value)} />
      </Field>
    </FormDialog>
  );
}

/** A small stat tile used on the Services pages. */
export function Tile({
  label: text,
  value,
  tone,
}: {
  label: string;
  value: ReactNode;
  tone?: "bad";
}) {
  return (
    <section className="rounded-2xl border bg-card p-5">
      <p className="text-sm text-muted-foreground">{text}</p>
      <p className={`mt-2 text-2xl font-semibold ${tone === "bad" ? "text-destructive" : ""}`}>
        {value}
      </p>
    </section>
  );
}
