import { useEffect, useState } from "react";
import { Send } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Field, NativeSelect } from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { ReceiptPreview } from "@/features/business/ReceiptPreview";
import { useGet, useWrite } from "@/hooks/useApi";
import type { MoneyAccount } from "@/types";
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";
import type { Translations } from "@/features/i18n/i18n";

type Value = string | number | boolean;
type Group = Record<string, Value>;
type Rule = { in_app: boolean; email: boolean; sms: boolean; days_before: number | null };
type SettingsMap = Record<string, Record<string, unknown>>;

const titlesFor = (t: Translations): Record<string, string> => ({
  attendance: t.common.attendance,
  receipt: t.common.receipts,
  invoice: t.settings.invoicesQuotations,
  payroll: t.settings.payrollRules,
  notifications: t.settings.reminderDaysMessageTemplates,
  system: t.settings.systemInventoryDefaults,
  tax: t.common.tax,
  currency: t.settings.currency,
  sales: t.settings.salesDiscounts,
});

/** The name of a setting. A setting we have no wording for is shown from its key. */
const label = (t: Translations, key: string) => {
  const names = t.settings.fields as Record<string, string>;
  if (Object.prototype.hasOwnProperty.call(names, key)) return names[key];
  const text = key.replaceAll("_", " ");
  return text.charAt(0).toUpperCase() + text.slice(1);
};
const isScalar = (v: unknown): v is Value =>
  typeof v === "string" || typeof v === "number" || typeof v === "boolean";

const ruleLabels = (t: Translations): Record<string, string> => ({
  low_stock: t.settings.lowStockAlert,
  debt_due: t.settings.customerSupplierDebtDue,
  task_overdue: t.settings.servicesTaskOverdue,
  task_assigned: t.settings.servicesTaskAssigned,
  renewal_due: t.settings.servicesRenewalDue,
  salary_due: t.settings.payrollSalaryPaydayReminder,
  approval_waiting: t.settings.approvalAwaitingDecision,
  approval_decided: t.settings.approvalApprovedRejected,
  design_brief_assigned: t.settings.designJobAssigned,
  design_feedback: t.settings.designClientFeedback,
  design_deadline: t.settings.designJobDeadlineApproaching,
  design_approved: t.settings.designClientApproved,
  print_ready: t.settings.designPrintOrderReady,
  account_locked: t.settings.securityAccountLocked,
  day_closed: t.settings.financeDayClosed,
  project_stage_changed: t.settings.servicesProjectStageChanged,
  payment_received: t.settings.financePaymentReceived,
});

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
  const { t } = useI18n();
  const merged = { ...extraDefaults, ...initial };
  const [draft, setDraft] = useState<Group>(merged);
  useEffect(() => {
    setDraft({ ...extraDefaults, ...initial });
  }, [initial]);

  const save = useWrite<Group, unknown>("PUT", `/settings/${name}`, {
    invalidate: ["/settings", "/me"],
    success: fmt(t.settings.saved, { value: titlesFor(t)[name] ?? name }),
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
      className="od-card p-5"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate(draft);
      }}
    >
      <h3 className="od-card-title mb-3.5 text-[var(--od-ink)]">
        {titlesFor(t)[name] ?? label(t, name)}
      </h3>
      <div className="grid gap-3.5 sm:grid-cols-2">
        {entries.map(([key, original]) => {
          const isTemplate = key.includes("template") || key === "terms" || key === "footer";
          return (
            <label
              key={key}
              className={`flex flex-col gap-1.5 text-sm ${isTemplate ? "sm:col-span-2" : ""}`}
            >
              {typeof original === "boolean" ? (
                <span className="flex cursor-pointer items-center gap-2 rounded-xl border border-[var(--od-border)] bg-[var(--od-surface-subtle)] px-3 py-2 text-[var(--od-ink)]">
                  <input
                    type="checkbox"
                    disabled={!canEdit}
                    checked={Boolean(draft[key])}
                    onChange={(e) => patch(key, e.target.checked)}
                  />
                  {label(t, key)}
                </span>
              ) : key === "language" ? (
                <>
                  <span className="text-xs font-medium text-[var(--od-ink-secondary)]">
                    {label(t, key)}
                  </span>
                  <NativeSelect
                    disabled={!canEdit}
                    value={String(draft[key] ?? "en")}
                    onChange={(e) => patch(key, e.target.value)}
                  >
                    <option value="en">{t.settings.englishEn}</option>
                    <option value="sw">{t.settings.kiswahiliSw}</option>
                  </NativeSelect>
                </>
              ) : key === "paper_width" ? (
                <>
                  <span className="text-xs font-medium text-[var(--od-ink-secondary)]">
                    {t.settings.thermalPaperWidth}
                  </span>
                  <NativeSelect
                    disabled={!canEdit}
                    value={String(draft[key] ?? 80)}
                    onChange={(e) => patch(key, Number(e.target.value))}
                  >
                    <option value="58">{t.settings.n58MmNarrow}</option>
                    <option value="80">{t.settings.n80MmStandard}</option>
                  </NativeSelect>
                </>
              ) : isTemplate ? (
                <>
                  <span className="text-xs font-medium text-[var(--od-ink-secondary)]">
                    {label(t, key)}
                  </span>
                  <Textarea
                    rows={2}
                    disabled={!canEdit}
                    value={String(draft[key] ?? "")}
                    onChange={(e) => patch(key, e.target.value)}
                  />
                </>
              ) : (
                <>
                  <span className="text-xs font-medium text-[var(--od-ink-secondary)]">
                    {label(t, key)}
                  </span>
                  <Input
                    disabled={!canEdit}
                    inputMode={typeof original === "number" ? "decimal" : undefined}
                    className={typeof original === "number" ? "od-tabular" : undefined}
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
        <div className="mt-4 border-t border-[var(--od-border)] pt-4">
          <Button type="submit" disabled={save.isPending}>
            {save.isPending ? t.common.saving : t.common.save}
          </Button>
        </div>
      )}
    </form>
  );
}

/** Payment methods enabled/disabled + default money account mapping (SRS Section 13). */
export function PaymentMethodsPanel() {
  const { t } = useI18n();
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
    success: t.settings.paymentMethodsUpdated,
  });

  const activeAccounts = (accounts.data ?? []).filter((a) => a.is_active);
  const specs: Array<{ key: string; label: string; allowedTypes: string[] | null }> = [
    { key: "cash", label: t.common.cash, allowedTypes: ["cash", "petty_cash"] },
    {
      key: "mobile_money",
      label: t.settings.mobileMoneyMPesaTigo,
      allowedTypes: ["mobile_money"],
    },
    { key: "bank", label: t.settings.bankTransferCard, allowedTypes: ["bank"] },
    { key: "credit", label: t.settings.customerCreditPayLater, allowedTypes: null },
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
    <div className="od-card space-y-4 p-5">
      <div>
        <h3 className="od-card-title text-[var(--od-ink)]">
          {t.settings.paymentMethodsDefaultAccounts}
        </h3>
        <p className="od-card-subtitle">{t.settings.chooseWhichPaymentMethodsCashiers}</p>
      </div>
      <div className="divide-y divide-[var(--od-border)] overflow-hidden rounded-xl border border-[var(--od-border)]">
        {specs.map((sp) => {
          const cur = draft[sp.key] ?? { enabled: true, money_account_id: "" };
          const matching = sp.allowedTypes
            ? activeAccounts.filter((a) => sp.allowedTypes!.includes(a.type))
            : [];
          return (
            <div key={sp.key} className="flex flex-wrap items-center justify-between gap-3 p-3.5">
              <label className="flex cursor-pointer items-center gap-2.5 text-sm font-medium text-[var(--od-ink)]">
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
                  <option value="">{t.settings.noDefaultAccount}</option>
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
          {save.isPending ? t.common.saving : t.settings.savePaymentMethods}
        </Button>
      )}
    </div>
  );
}

/** Documents tab: Receipt & Invoice settings + live thermal receipt preview (SRS Section 13). */
export function DocumentsSettingsPanel({ businessName }: { businessName: string }) {
  const { t } = useI18n();
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

      <div className="od-card h-fit bg-[var(--od-surface-subtle)] p-4">
        <p className="od-kpi-label mb-3">
          {fmt(t.settings.liveReceiptPreviewMm, {
            paper_width: String(receiptPreview.paper_width ?? 80),
          })}
        </p>
        <ReceiptPreview
          name={businessName || t.settings.yourBusiness}
          header={receiptPreview.header ? String(receiptPreview.header) : ""}
          footer={receiptPreview.footer ? String(receiptPreview.footer) : ""}
          showLogo={Boolean(receiptPreview.show_logo)}
          showCashier={Boolean(receiptPreview.show_cashier)}
          showTax={Boolean(receiptPreview.show_tax)}
          widthClass={widthClass}
        />
      </div>
    </div>
  );
}

/** Notifications tab: all 25 event channel toggles + SMS/WhatsApp templates + Test Notification sender. */
export function NotificationsSettingsPanel() {
  const { t } = useI18n();
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
      success: t.settings.notificationRulesSaved,
    },
  );

  const sendTest = useWrite<{ channel: string; recipient: string; message: string }, unknown>(
    "POST",
    "/notifications/test",
    {
      invalidate: ["/notifications"],
      success: t.settings.testNotificationSent,
    },
  );

  const flatNotif = Object.fromEntries(
    Object.entries(notifGroup).filter(([, v]) => isScalar(v)),
  ) as Group;

  const ruleKeys = Object.keys(rules);

  return (
    <div className="max-w-4xl space-y-6">
      <div className="od-card p-5">
        <div className="mb-3.5 flex flex-wrap items-center justify-between gap-2">
          <div>
            <h3 className="od-card-title text-[var(--od-ink)]">
              {fmt(t.settings.alertChannelsByEvent, { count: ruleKeys.length })}
            </h3>
            <p className="od-card-subtitle">{t.settings.chooseWhichAlertsGoTo}</p>
          </div>
          {canEdit && ruleKeys.length > 0 && (
            <Button
              size="sm"
              disabled={saveRules.isPending}
              onClick={() => saveRules.mutate({ rules })}
            >
              {saveRules.isPending ? t.common.saving : t.settings.saveRules}
            </Button>
          )}
        </div>
        <div className="max-h-96 overflow-auto rounded-xl border border-[var(--od-border)]">
          <table className="w-full text-sm">
            <thead className="sticky top-0 bg-[var(--od-surface-subtle)] text-xs text-[var(--od-ink-secondary)] backdrop-blur">
              <tr>
                <th className="px-3.5 py-2.5 text-left font-medium">{t.settings.event}</th>
                <th className="px-3 py-2.5 text-center font-medium">{t.settings.inApp}</th>
                <th className="px-3 py-2.5 text-center font-medium">{t.common.email}</th>
                <th className="px-3 py-2.5 text-center font-medium">SMS</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[var(--od-border)]">
              {ruleKeys.map((k) => {
                const r = rules[k];
                return (
                  <tr key={k} className="hover:bg-[var(--od-surface-subtle)]/60">
                    <td className="px-3.5 py-2.5 font-medium text-[var(--od-ink)]">
                      {ruleLabels(t)[k] ?? label(t, k)}
                    </td>
                    {(["in_app", "email", "sms"] as const).map((col) => (
                      <td key={col} className="px-3 py-2.5 text-center">
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
        <div className="od-card space-y-3.5 p-5">
          <div>
            <h3 className="od-card-title text-[var(--od-ink)]">
              {t.settings.sendTestNotification}
            </h3>
            <p className="od-card-subtitle">{t.settings.verifyThatYourNotificationBell}</p>
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label={t.common.channel}>
              <NativeSelect value={testChannel} onChange={(e) => setTestChannel(e.target.value)}>
                <option value="in_app">{t.settings.inAppNotification}</option>
                <option value="sms">SMS</option>
                <option value="whatsapp">WhatsApp</option>
                <option value="email">{t.common.email}</option>
              </NativeSelect>
            </Field>
            <Field
              label={
                testChannel === "in_app"
                  ? t.settings.recipientOptional
                  : t.settings.recipientPhoneOrEmail
              }
            >
              <Input
                value={testRecipient}
                onChange={(e) => setTestRecipient(e.target.value)}
                placeholder={t.settings.n2557OrNameCompanyCom}
              />
            </Field>
          </div>
          <Field label={t.common.message}>
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
            <Send size={14} />{" "}
            {sendTest.isPending ? t.settings.sending : t.settings.sendTestMessage}
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
