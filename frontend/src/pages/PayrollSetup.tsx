import { useState } from "react";
import { Plus } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
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
import { useGet, useGetPage, useWrite } from "@/hooks/useApi";
import { SimpleList } from "@/pages/HrSetup";
import { formatDate, formatMoney, today } from "@/lib/formatters";
import { useI18n } from "@/features/i18n/i18n";
import type { Translations } from "@/features/i18n/i18n";

interface Rule {
  id: string;
  name?: string;
  basis?: string;
  calc?: string;
  percent?: string | null;
  fixed_amount?: string | null;
  value?: string | null;
  employee_id: string | null;
  employee_name?: string | null;
  effective_from?: string | null;
  effective_to?: string | null;
  is_active: boolean;
}
interface Adjustment {
  id: string;
  employee_name: string;
  period: string;
  kind: string;
  amount: string;
  reason: string;
}
interface Option {
  id: string;
  name: string;
}

const basisLabelFor = (t: Translations): Record<string, string> => ({
  sale_percent: t.payroll.ofSales,
  project_fixed: t.payroll.fixedPerCompletedProject,
  percent_of_sales: t.payroll.ofSales,
  fixed_per_project: t.payroll.fixedPerCompletedProject,
});

function Commissions() {
  const { t } = useI18n();
  const { can } = useAuth();
  const list = useGet<Rule[]>("/commission-rules");
  const employees = useGet<Option[]>("/employees", { per_page: 100 });
  return (
    <SimpleList
      path="/commission-rules"
      title={t.payroll.newCommissionRule}
      rows={list.data}
      loading={list.isLoading}
      canCreate={can("hr.compensation.manage")}
      blank={{
        name: "",
        employee_id: "",
        basis: "sale_percent",
        value: "",
        effective_from: today(),
      }}
      toBody={(d) => ({
        name: d.name.trim() || undefined,
        employee_id: d.employee_id || undefined,
        basis: d.basis,
        percent: d.basis === "sale_percent" ? d.value : undefined,
        fixed_amount: d.basis === "project_fixed" ? d.value : undefined,
        effective_from: d.effective_from || undefined,
      })}
      columns={[
        {
          header: t.payroll.rule,
          cell: (r) => (
            <span className="font-medium text-[var(--od-ink)]">
              {r.name || t.payroll.commission}
            </span>
          ),
        },
        {
          header: t.payroll.appliesTo,
          cell: (r) =>
            r.employee_name ??
            (r.employee_id
              ? (employees.data?.find((e) => e.id === r.employee_id)?.name ?? t.common.employee)
              : t.payroll.allEligibleStaff),
        },
        {
          header: t.payroll.basis,
          cell: (r) => (
            <Badge variant="secondary">
              {basisLabelFor(t)[r.basis ?? r.calc ?? ""] ?? r.basis ?? "-"}
            </Badge>
          ),
        },
        {
          header: t.payroll.rateAmount,
          align: "right",
          cell: (r) => (
            <span className="od-tabular font-semibold text-[var(--od-ink)]">
              {r.percent != null
                ? `${Number(r.percent)}%`
                : r.fixed_amount != null
                  ? formatMoney(r.fixed_amount)
                  : r.value != null
                    ? String(Number(r.value))
                    : "-"}
            </span>
          ),
        },
        {
          header: t.common.effectiveFrom,
          cell: (r) => (r.effective_from ? formatDate(r.effective_from) : "-"),
        },
        {
          header: t.common.status,
          cell: (r) => <StatusBadge value={r.is_active ? "active" : "inactive"} />,
        },
      ]}
      form={(d, set) => (
        <>
          <Field label={t.payroll.ruleName}>
            <Input
              required
              placeholder={t.payroll.eGShopSalesCommission}
              value={d.name}
              onChange={(e) => set({ name: e.target.value })}
            />
          </Field>
          <Field label={t.payroll.employeeLeaveBlankForAll}>
            <NativeSelect
              value={d.employee_id}
              onChange={(e) => set({ employee_id: e.target.value })}
            >
              <option value="">{t.payroll.allEligibleStaff}</option>
              {employees.data?.map((o) => (
                <option key={o.id} value={o.id}>
                  {o.name}
                </option>
              ))}
            </NativeSelect>
          </Field>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
            <Field label={t.payroll.basis}>
              <NativeSelect value={d.basis} onChange={(e) => set({ basis: e.target.value })}>
                <option value="sale_percent">{t.payroll.ofSales}</option>
                <option value="project_fixed">{t.payroll.fixedPerCompletedProject}</option>
              </NativeSelect>
            </Field>
            <Field label={d.basis === "sale_percent" ? t.payroll.percent : t.common.amount}>
              <Input
                required
                inputMode="decimal"
                className="od-tabular"
                value={d.value}
                onChange={(e) => set({ value: e.target.value })}
              />
            </Field>
            <Field label={t.common.effectiveFrom}>
              <Input
                type="date"
                value={d.effective_from}
                onChange={(e) => set({ effective_from: e.target.value })}
              />
            </Field>
          </div>
        </>
      )}
    />
  );
}

function Adjustments() {
  const { t } = useI18n();
  const { canAny } = useAuth();
  const canManage = canAny("hr.compensation.manage", "hr.payroll.manage");
  const [page, setPage] = useState(1);
  const list = useGetPage<Adjustment>("/pay-adjustments", { page });
  const employees = useGet<Option[]>("/employees", { per_page: 100 });
  const blank = { employee_id: "", period: today(), kind: "bonus", amount: "", reason: "" };
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState(blank);
  const create = useWrite<typeof draft, unknown>("POST", "/pay-adjustments", {
    invalidate: ["/pay-adjustments"],
    success: t.payroll.adjustmentSavedItWillBe,
    onSuccess: () => {
      setOpen(false);
      setDraft(blank);
    },
  });
  const remove = useWrite<Adjustment, unknown>("DELETE", (a) => `/pay-adjustments/${a.id}`, {
    invalidate: ["/pay-adjustments"],
    success: t.payroll.adjustmentRemoved,
  });
  return (
    <div className="space-y-4">
      {canManage && (
        <FilterBar className="justify-end">
          <Button onClick={() => setOpen(true)}>
            <Plus size={16} /> {t.common.newAdjustment}
          </Button>
        </FilterBar>
      )}
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(a) => a.id}
        empty={t.payroll.noBonusesOrOneOff}
        columns={[
          {
            header: t.common.employee,
            cell: (a) => (
              <span className="font-medium text-[var(--od-ink)]">{a.employee_name}</span>
            ),
          },
          { header: t.common.period, cell: (a) => formatDate(a.period) },
          { header: t.common.kind, cell: (a) => <StatusBadge value={a.kind} /> },
          { header: t.common.reason, cell: (a) => a.reason },
          {
            header: t.common.amount,
            align: "right",
            cell: (a) => (
              <span
                className={`od-tabular font-semibold ${
                  a.kind === "deduction" ? "text-[var(--od-bad)]" : "text-[var(--od-good)]"
                }`}
              >
                {a.kind === "deduction" ? "−" : "+"}
                {formatMoney(a.amount)}
              </span>
            ),
          },
          {
            header: "",
            cell: (a) =>
              canManage && (
                <div className="flex justify-end">
                  <Button
                    size="sm"
                    variant="ghost"
                    className="text-[var(--od-bad)] hover:text-[var(--od-bad)]"
                    onClick={() => remove.mutate(a)}
                  >
                    {t.payroll.remove}
                  </Button>
                </div>
              ),
          },
        ]}
      />
      <Pager meta={list.data?.meta} onPage={setPage} />
      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title={t.payroll.newPayAdjustment}
        busy={create.isPending}
        onSubmit={() => create.mutate(draft)}
      >
        <Field label={t.common.employee}>
          <NativeSelect
            required
            value={draft.employee_id}
            onChange={(e) => setDraft((d) => ({ ...d, employee_id: e.target.value }))}
          >
            <option value="">{t.common.choose}</option>
            {employees.data?.map((e) => (
              <option key={e.id} value={e.id}>
                {e.name}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
          <Field label={t.common.kind}>
            <NativeSelect
              value={draft.kind}
              onChange={(e) => setDraft((d) => ({ ...d, kind: e.target.value }))}
            >
              <option value="bonus">{t.common.bonus}</option>
              <option value="deduction">{t.common.deduction}</option>
            </NativeSelect>
          </Field>
          <Field label={t.common.amount}>
            <Input
              required
              inputMode="decimal"
              className="od-tabular"
              value={draft.amount}
              onChange={(e) => setDraft((d) => ({ ...d, amount: e.target.value }))}
            />
          </Field>
          <Field label={t.payroll.payPeriodDate}>
            <Input
              required
              type="date"
              value={draft.period}
              onChange={(e) => setDraft((d) => ({ ...d, period: e.target.value }))}
            />
          </Field>
        </div>
        <Field label={t.common.reason}>
          <Input
            required
            value={draft.reason}
            onChange={(e) => setDraft((d) => ({ ...d, reason: e.target.value }))}
          />
        </Field>
      </FormDialog>
    </div>
  );
}

export default function PayrollSetup() {
  const { t } = useI18n();
  const { canAny } = useAuth();
  const canCommissions = canAny(
    "hr.compensation.view",
    "hr.compensation.manage",
    "hr.payroll.view",
  );
  const canAdjustments = canAny(
    "hr.compensation.view",
    "hr.compensation.manage",
    "hr.payroll.view",
    "hr.payroll.manage",
  );
  return (
    <div className="space-y-6">
      <PageHeader title={t.common.payrollSetup} description={t.payroll.commissionRulesAndOneOff} />
      <Tabs defaultValue={canCommissions ? "commissions" : "adjustments"}>
        <TabsList className="mb-4">
          {canCommissions && (
            <TabsTrigger value="commissions">{t.payroll.commissionRules}</TabsTrigger>
          )}
          {canAdjustments && (
            <TabsTrigger value="adjustments">{t.payroll.payAdjustments}</TabsTrigger>
          )}
        </TabsList>
        {canCommissions && (
          <TabsContent value="commissions">
            <Commissions />
          </TabsContent>
        )}
        {canAdjustments && (
          <TabsContent value="adjustments">
            <Adjustments />
          </TabsContent>
        )}
      </Tabs>
    </div>
  );
}
