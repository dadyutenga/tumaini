import { useEffect, useState } from "react";
import { Send } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Field, NativeSelect } from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { useGet, useWrite } from "@/hooks/useApi";
import type { MoneyAccount } from "@/types";

type Value = string | number | boolean;
type Group = Record<string, Value>;
type Rule = { in_app: boolean; email: boolean; sms: boolean; days_before: number | null };
type SettingsMap = Record<string, Record<string, unknown>>;

const titles: Record<string, string> = {
  receipt: "Receipts",
  invoice: "Invoices & Quotations",
  payroll: "Payroll Rules",
  notifications: "Reminder Days & Message Templates",
  system: "System & Inventory Defaults",
  tax: "Tax",
  currency: "Currency",
  sales: "Sales & Discounts",
};

const label = (key: string) => {
  const text = key.replaceAll("_", " ");
  return text.charAt(0).toUpperCase() + text.slice(1);
};
const isScalar = (v: unknown): v is Value =>
  typeof v === "string" || typeof v === "number" || typeof v === "boolean";

const RULE_LABELS: Record<string, string> = {
  low_stock: "Low stock alert",
  debt_due: "Customer / supplier debt due",
  task_overdue: "Services: Task overdue",
  task_assigned: "Services: Task assigned",
  renewal_due: "Services: Renewal due",
  salary_due: "Payroll: Salary payday reminder",
  approval_waiting: "Approval awaiting decision",
  approval_decided: "Approval approved / rejected",
  design_brief_assigned: "Design: Job assigned",
  design_feedback: "Design: Client feedback",
  design_deadline: "Design: Job deadline approaching",
  design_approved: "Design: Client approved",
  print_ready: "Design: Print order ready",
  account_locked: "Security: Account locked",
  day_closed: "Finance: Day closed",
  project_stage_changed: "Services: Project stage changed",
  payment_received: "Finance: Payment received",
};

function GroupForm({
  name,
  initial,
  canEdit,
  extraDefaults,
  onChange,
}: {
  name: string;
  initial: Group;
  canEdit: boolean;
  extraDefaults?: Group;
  onChange?: (next: Group) => void;
}) {
  const merged = { ...extraDefaults, ...initial };
  const [draft, setDraft] = useState<Group>(merged);
  useEffect(() => {
    setDraft({ ...extraDefaults, ...initial });
  }, [initial]);

  const save = useWrite<Group, unknown>("PUT", `/settings/${name}`, {
    invalidate: ["/settings", "/me"],
    success: `${titles[name] ?? name} saved`,
  });

  const entries = Object.entries(merged);
  if (entries.length === 0) return null;

  function patch(k: string, val: Value) {
    setDraft((d) => {
      const next = { ...d, [k]: val };
      onChange?.(next);
      return next;
    });
  }

  return (
    <form
      className="rounded-2xl border bg-card p-5"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate(draft);
      }}
    >
      <h3 className="mb-3 font-semibold">{titles[name] ?? label(name)}</h3>
      <div className="grid gap-3 sm:grid-cols-2">
        {entries.map(([key, original]) => {
          const isTemplate = key.includes("template") || key === "terms" || key === "footer";
          return (
            <label
              key={key}
              className={`flex flex-col gap-1 text-sm ${isTemplate ? "sm:col-span-2" : ""}`}
            >
              {typeof original === "boolean" ? (
                <span className="flex items-center gap-2 pt-1">
                  <input
                    type="checkbox"
                    disabled={!canEdit}
                    checked={Boolean(draft[key])}
                    onChange={(e) => patch(key, e.target.checked)}
                  />
                  {label(key)}
                </span>
              ) : key === "language" ? (
                <>
                  <span className="text-muted-foreground">{label(key)}</span>
                  <NativeSelect
                    disabled={!canEdit}
                    value={String(draft[key] ?? "en")}
                    onChange={(e) => patch(key, e.target.value)}
                  >
                    <option value="en">English (en)</option>
                    <option value="sw">Kiswahili (sw)</option>
                  </NativeSelect>
                </>
              ) : key === "paper_width" ? (
                <>
                  <span className="text-muted-foreground">Thermal paper width</span>
                  <NativeSelect
                    disabled={!canEdit}
                    value={String(draft[key] ?? 80)}
                    onChange={(e) => patch(key, Number(e.target.value))}
                  >
                    <option value="58">58 mm (narrow)</option>
                    <option value="80">80 mm (standard)</option>
                  </NativeSelect>
                </>
              ) : isTemplate ? (
                <>
                  <span className="text-muted-foreground">{label(key)}</span>
                  <Textarea
                    rows={2}
                    disabled={!canEdit}
                    value={String(draft[key] ?? "")}
                    onChange={(e) => patch(key, e.target.value)}
                  />
                </>
              ) : (
                <>
                  <span className="text-muted-foreground">{label(key)}</span>
                  <Input
                    disabled={!canEdit}
                    inputMode={typeof original === "number" ? "decimal" : undefined}
                    value={String(draft[key] ?? "")}
                    onChange={(e) =>
                      patch(
                        key,
                        typeof original === "number" ? Number(e.target.value) || 0 : e.target.value,
                      )
                    }
                  />
                </>
              )}
            </label>
          );
        })}
      </div>
      {canEdit && (
        <Button type="submit" className="mt-4" disabled={save.isPending}>
          {save.isPending ? "Saving…" : "Save"}
        </Button>
      )}
    </form>
  );
}

/** Payment methods enabled/disabled + default money account mapping (SRS Section 13). */
export function PaymentMethodsPanel() {
  const { can } = useAuth();
  const canEdit = can("settings.manage");
  const methods =
    useGet<Record<string, { enabled?: boolean; money_account_id?: string | null }>>(
      "/payment-methods",
    );
  const accounts = useGet<MoneyAccount[]>("/money-accounts", undefined, {
    enabled: can("finance.account.view"),
  });
  const [draft, setDraft] = useState<
    Record<string, { enabled: boolean; money_account_id: string }>
  >({
    cash: { enabled: true, money_account_id: "" },
    mobile_money: { enabled: true, money_account_id: "" },
    bank: { enabled: true, money_account_id: "" },
    credit: { enabled: true, money_account_id: "" },
  });

  useEffect(() => {
    if (!methods.data) return;
    setDraft({
      cash: {
        enabled: methods.data.cash?.enabled ?? true,
        money_account_id: methods.data.cash?.money_account_id ?? "",
      },
      mobile_money: {
        enabled: methods.data.mobile_money?.enabled ?? true,
        money_account_id: methods.data.mobile_money?.money_account_id ?? "",
      },
      bank: {
        enabled: methods.data.bank?.enabled ?? true,
        money_account_id: methods.data.bank?.money_account_id ?? "",
      },
      credit: {
        enabled: methods.data.credit?.enabled ?? true,
        money_account_id: "",
      },
    });
  }, [methods.data]);

  const save = useWrite<Record<string, unknown>, unknown>("PUT", "/payment-methods", {
    invalidate: ["/payment-methods"],
    success: "Payment methods updated",
  });

  const activeAccounts = (accounts.data ?? []).filter((a) => a.is_active);
  const specs: Array<{ key: string; label: string; allowedTypes: string[] | null }> = [
    { key: "cash", label: "Cash", allowedTypes: ["cash", "petty_cash"] },
    {
      key: "mobile_money",
      label: "Mobile money (M-Pesa / Tigo / Airtel)",
      allowedTypes: ["mobile_money"],
    },
    { key: "bank", label: "Bank transfer / card", allowedTypes: ["bank"] },
    { key: "credit", label: "Customer credit (pay later)", allowedTypes: null },
  ];

  function handleSave() {
    const body: Record<string, unknown> = {};
    for (const sp of specs) {
      const cur = draft[sp.key];
      if (!cur) continue;
      body[sp.key] =
        sp.allowedTypes === null
          ? { enabled: cur.enabled }
          : { enabled: cur.enabled, money_account_id: cur.money_account_id || null };
    }
    save.mutate(body);
  }

  return (
    <div className="space-y-4 rounded-2xl border bg-card p-5">
      <div>
        <h3 className="font-semibold">Payment Methods &amp; Default Accounts</h3>
        <p className="text-xs text-muted-foreground">
          Choose which payment methods cashiers can accept and which money account receives each
          one.
        </p>
      </div>
      <div className="divide-y rounded-xl border">
        {specs.map((sp) => {
          const cur = draft[sp.key] ?? { enabled: true, money_account_id: "" };
          const matching = sp.allowedTypes
            ? activeAccounts.filter((a) => sp.allowedTypes!.includes(a.type))
            : [];
          return (
            <div key={sp.key} className="flex flex-wrap items-center justify-between gap-3 p-3">
              <label className="flex items-center gap-2 text-sm font-medium">
                <input
                  type="checkbox"
                  disabled={!canEdit}
                  checked={cur.enabled}
                  onChange={(e) =>
                    setDraft((d) => ({
                      ...d,
                      [sp.key]: { ...cur, enabled: e.target.checked },
                    }))
                  }
                />
                {sp.label}
              </label>
              {sp.allowedTypes && (
                <NativeSelect
                  className="w-56"
                  disabled={!canEdit || !cur.enabled}
                  value={cur.money_account_id}
                  onChange={(e) =>
                    setDraft((d) => ({
                      ...d,
                      [sp.key]: { ...cur, money_account_id: e.target.value },
                    }))
                  }
                >
                  <option value="">No default account</option>
                  {matching.map((a) => (
                    <option key={a.id} value={a.id}>
                      {a.name}
                    </option>
                  ))}
                </NativeSelect>
              )}
            </div>
          );
        })}
      </div>
      {canEdit && (
        <Button disabled={save.isPending} onClick={handleSave}>
          {save.isPending ? "Saving…" : "Save payment methods"}
        </Button>
      )}
    </div>
  );
}

/** Documents tab: Receipt & Invoice settings + live thermal receipt preview (SRS Section 13). */
export function DocumentsSettingsPanel({ businessName }: { businessName: string }) {
  const { can } = useAuth();
  const canEdit = can("settings.manage");
  const settings = useGet<SettingsMap>("/settings");
  const [receiptPreview, setReceiptPreview] = useState<Group>({});

  const receiptFlat = settings.data?.receipt
    ? (Object.fromEntries(
        Object.entries(settings.data.receipt).filter(([, v]) => isScalar(v)),
      ) as Group)
    : {};
  const invoiceFlat = settings.data?.invoice
    ? (Object.fromEntries(
        Object.entries(settings.data.invoice).filter(([, v]) => isScalar(v)),
      ) as Group)
    : {};

  useEffect(() => {
    setReceiptPreview(receiptFlat);
  }, [settings.data?.receipt]);

  const widthClass = Number(receiptPreview.paper_width) === 58 ? "w-56" : "w-72";

  return (
    <div className="grid gap-6 lg:grid-cols-[1fr_20rem]">
      <div className="space-y-4">
        <GroupForm
          name="receipt"
          initial={receiptFlat}
          canEdit={canEdit}
          onChange={setReceiptPreview}
        />
        <GroupForm name="invoice" initial={invoiceFlat} canEdit={canEdit} />
      </div>

      <div className="rounded-2xl border bg-muted/30 p-4">
        <p className="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          Live Receipt Preview ({String(receiptPreview.paper_width ?? 80)}mm)
        </p>
        <div
          className={`mx-auto rounded-lg border bg-white p-4 font-mono text-[11px] text-zinc-900 shadow-sm ${widthClass}`}
        >
          <div className="text-center">
            {Boolean(receiptPreview.show_logo) && (
              <div className="mx-auto mb-1 grid h-8 w-8 place-items-center rounded bg-zinc-200 text-[9px] font-bold">
                LOGO
              </div>
            )}
            <p className="font-bold">{businessName || "Your Business"}</p>
            {receiptPreview.header && (
              <p className="text-[10px] text-zinc-600">{String(receiptPreview.header)}</p>
            )}
          </div>
          <div className="my-2 border-t border-dashed border-zinc-400" />
          <p>Receipt: INV-00042</p>
          {Boolean(receiptPreview.show_cashier) && <p>Cashier: Amina</p>}
          <div className="my-2 border-t border-dashed border-zinc-400" />
          <div className="flex justify-between">
            <span>2x Sugar 1kg</span>
            <span>6,000</span>
          </div>
          <div className="flex justify-between">
            <span>1x Cooking Oil</span>
            <span>8,500</span>
          </div>
          <div className="my-2 border-t border-dashed border-zinc-400" />
          {Boolean(receiptPreview.show_tax) && (
            <div className="flex justify-between text-zinc-600">
              <span>VAT (18%)</span>
              <span>2,212</span>
            </div>
          )}
          <div className="flex justify-between font-bold">
            <span>TOTAL</span>
            <span>TZS 14,500</span>
          </div>
          {receiptPreview.footer && (
            <>
              <div className="my-2 border-t border-dashed border-zinc-400" />
              <p className="text-center text-[10px] text-zinc-600">
                {String(receiptPreview.footer)}
              </p>
            </>
          )}
        </div>
      </div>
    </div>
  );
}

/** Notifications tab: all 25 event channel toggles + SMS/WhatsApp templates + Test Notification sender. */
export function NotificationsSettingsPanel() {
  const { can } = useAuth();
  const canEdit = can("settings.manage");
  const settings = useGet<SettingsMap>("/settings");

  const notifGroup = (settings.data?.notifications ?? {}) as Record<string, unknown>;
  const serverRules = (notifGroup.rules ?? {}) as Record<string, Rule>;

  const [rules, setRules] = useState<Record<string, Rule>>({});
  useEffect(() => {
    if (Object.keys(serverRules).length > 0) {
      setRules(serverRules);
    }
  }, [settings.data?.notifications]);

  const [testChannel, setTestChannel] = useState("in_app");
  const [testRecipient, setTestRecipient] = useState("");
  const [testMessage, setTestMessage] = useState(
    "Hello! This is a test notification from Tumaini BMS.",
  );

  const saveRules = useWrite<{ rules: Record<string, Rule> }, unknown>(
    "PUT",
    "/settings/notifications",
    {
      invalidate: ["/settings"],
      success: "Notification rules saved",
    },
  );

  const sendTest = useWrite<{ channel: string; recipient: string; message: string }, unknown>(
    "POST",
    "/notifications/test",
    {
      invalidate: ["/notifications"],
      success: "Test notification sent!",
    },
  );

  const flatNotif = Object.fromEntries(
    Object.entries(notifGroup).filter(([, v]) => isScalar(v)),
  ) as Group;

  const ruleKeys = Object.keys(rules);

  return (
    <div className="max-w-4xl space-y-6">
      <div className="rounded-2xl border bg-card p-5">
        <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
          <div>
            <h3 className="font-semibold">Alert Channels by Event ({ruleKeys.length})</h3>
            <p className="text-xs text-muted-foreground">
              Choose which alerts go to In-App, Email, or SMS across Shop, Finance, HR, Services and
              Design.
            </p>
          </div>
          {canEdit && ruleKeys.length > 0 && (
            <Button
              size="sm"
              disabled={saveRules.isPending}
              onClick={() => saveRules.mutate({ rules })}
            >
              {saveRules.isPending ? "Saving…" : "Save rules"}
            </Button>
          )}
        </div>
        <div className="max-h-96 overflow-auto rounded-xl border">
          <table className="w-full text-sm">
            <thead className="sticky top-0 bg-muted/80 text-xs backdrop-blur">
              <tr>
                <th className="px-3 py-2 text-left">Event</th>
                <th className="px-3 py-2 text-center">In-App</th>
                <th className="px-3 py-2 text-center">Email</th>
                <th className="px-3 py-2 text-center">SMS</th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {ruleKeys.map((k) => {
                const r = rules[k];
                return (
                  <tr key={k}>
                    <td className="px-3 py-2 font-medium">{RULE_LABELS[k] ?? label(k)}</td>
                    {(["in_app", "email", "sms"] as const).map((col) => (
                      <td key={col} className="px-3 py-2 text-center">
                        <input
                          type="checkbox"
                          disabled={!canEdit}
                          checked={Boolean(r[col])}
                          onChange={(e) =>
                            setRules((prev) => ({
                              ...prev,
                              [k]: { ...r, [col]: e.target.checked },
                            }))
                          }
                        />
                      </td>
                    ))}
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </div>

      <GroupForm
        name="notifications"
        initial={flatNotif}
        extraDefaults={{
          supplier_due_days: 3,
          salary_due_days: 3,
          sms_template_receipt:
            "Thank you for shopping with us! Receipt {{number}}, Total {{total}}.",
          sms_template_debt: "Friendly reminder: balance {{balance}} is due. Thank you.",
          sms_template_renewal: "Reminder: your {{service}} renews on {{date}}.",
          whatsapp_template_update: "Hello {{client}}, update on {{project}}: {{message}}",
        }}
        canEdit={canEdit}
      />

      {canEdit && (
        <div className="space-y-3 rounded-2xl border bg-card p-5">
          <h3 className="font-semibold">Send Test Notification</h3>
          <p className="text-xs text-muted-foreground">
            Verify that your notification bell and message templates work as expected.
          </p>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="Channel">
              <NativeSelect value={testChannel} onChange={(e) => setTestChannel(e.target.value)}>
                <option value="in_app">In-App notification</option>
                <option value="sms">SMS</option>
                <option value="whatsapp">WhatsApp</option>
                <option value="email">Email</option>
              </NativeSelect>
            </Field>
            <Field
              label={
                testChannel === "in_app" ? "Recipient (optional)" : "Recipient phone or email *"
              }
            >
              <Input
                value={testRecipient}
                onChange={(e) => setTestRecipient(e.target.value)}
                placeholder="+2557... or name@company.com"
              />
            </Field>
          </div>
          <Field label="Message">
            <Input value={testMessage} onChange={(e) => setTestMessage(e.target.value)} />
          </Field>
          <Button
            variant="outline"
            disabled={sendTest.isPending}
            onClick={() =>
              sendTest.mutate({
                channel: testChannel,
                recipient: testRecipient,
                message: testMessage,
              })
            }
          >
            <Send size={14} /> {sendTest.isPending ? "Sending…" : "Send test message"}
          </Button>
        </div>
      )}
    </div>
  );
}

/** System, Payroll, Currency, Tax & Sales discount preferences. */
export function SettingsGroups({ groups }: { groups?: string[] }) {
  const { can } = useAuth();
  const settings = useGet<SettingsMap>("/settings");
  if (!settings.data) return null;
  const order = groups ?? ["tax", "currency", "sales", "payroll", "system"];
  const extra: Record<string, Group> = {
    system: { default_low_stock: 5 },
    sales: { discount_limit_percent: 20 },
  };
  return (
    <div className="grid max-w-4xl gap-4">
      {order.map((g) => {
        const raw = settings.data[g] ?? {};
        const flat = Object.fromEntries(
          Object.entries(raw).filter(([, v]) => isScalar(v)),
        ) as Group;
        return (
          <GroupForm
            key={g}
            name={g}
            initial={flat}
            extraDefaults={extra[g]}
            canEdit={can("settings.manage")}
          />
        );
      })}
    </div>
  );
}
