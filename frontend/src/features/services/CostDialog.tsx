import { useState } from "react";
import { Input } from "@/components/ui/input";
import { Field, FormDialog, NativeSelect } from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { COST_KINDS, label } from "@/features/services/helpers";
import { useGet, useWrite } from "@/hooks/useApi";
import { today } from "@/lib/formatters";
import type { MoneyAccount } from "@/types";
import type { Contractor } from "@/types/services";
import { useI18n } from "@/features/i18n/i18n";

const blank = {
  kind: "government_fee",
  description: "",
  amount: "",
  contractor_id: "",
  account: "",
};

/**
 * Records a cost against a project or a design job. Paid from an account it is an expense now.
 * With a contractor and no account it is owed and shows up in that contractor's statement.
 */
export function CostDialog({
  open,
  onOpenChange,
  target,
  invalidate,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  target: { project_id?: string; design_job_id?: string };
  invalidate: string[];
}) {
  const { t } = useI18n();
  const { can, canAny } = useAuth();
  const contractors = useGet<Contractor[]>(
    "/contractors",
    { per_page: 100 },
    { enabled: can("services.contractor.manage") },
  );
  const accounts = useGet<MoneyAccount[]>("/money-accounts", undefined, {
    enabled: canAny(
      "finance.account.view",
      "finance.payment.make",
      "finance.expense.enter",
      "services.invoice.manage",
    ),
  });
  const [draft, setDraft] = useState(blank);
  const write = useWrite("POST", "/service-costs", {
    idempotent: true,
    invalidate,
    success: t.services.costRecorded,
    onSuccess: () => {
      setDraft(blank);
      onOpenChange(false);
    },
  });
  const set = (k: keyof typeof blank, v: string) => setDraft((d) => ({ ...d, [k]: v }));
  const live = (accounts.data ?? []).filter((a) => a.is_active);
  // "owed" means nothing is paid out now: the amount is booked to the contractor's account.
  const account = draft.account || (draft.contractor_id ? "owed" : (live[0]?.id ?? ""));

  return (
    <FormDialog
      open={open}
      onOpenChange={onOpenChange}
      title={t.services.addACost}
      description={t.services.governmentFeesMaterialsPrintingAnd}
      busy={write.isPending}
      onSubmit={() =>
        write.mutate({
          ...target,
          kind: draft.kind,
          description: draft.description,
          amount: draft.amount,
          contractor_id: draft.contractor_id || undefined,
          money_account_id: account === "owed" ? undefined : account || undefined,
          date: today(),
        })
      }
    >
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label={t.common.type}>
          <NativeSelect value={draft.kind} onChange={(e) => set("kind", e.target.value)}>
            {COST_KINDS.map((k) => (
              <option key={k} value={k}>
                {label(k)}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field label={t.common.amount}>
          <Input
            required
            inputMode="decimal"
            value={draft.amount}
            onChange={(e) => set("amount", e.target.value)}
          />
        </Field>
        <Field label={t.services.whatWasItFor} className="sm:col-span-2">
          <Input
            required
            value={draft.description}
            onChange={(e) => set("description", e.target.value)}
          />
        </Field>
        {can("services.contractor.manage") && (
          <Field label={t.services.contractorOptional}>
            <NativeSelect
              value={draft.contractor_id}
              onChange={(e) =>
                setDraft((d) => ({ ...d, contractor_id: e.target.value, account: "" }))
              }
            >
              <option value="">{t.common.none}</option>
              {contractors.data
                ?.filter((c) => c.is_active)
                .map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name}
                  </option>
                ))}
            </NativeSelect>
          </Field>
        )}
        <Field label={t.services.paidFrom}>
          <NativeSelect value={account} onChange={(e) => set("account", e.target.value)}>
            {draft.contractor_id && <option value="owed">{t.services.notPaidYetWeOwe}</option>}
            {live.map((a) => (
              <option key={a.id} value={a.id}>
                {a.name}
              </option>
            ))}
          </NativeSelect>
        </Field>
      </div>
    </FormDialog>
  );
}
