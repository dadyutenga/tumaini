import { useState, type ReactNode } from "react";
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
  type Col,
} from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { useGet, useGetPage, useWrite } from "@/hooks/useApi";
import { formatMoney, today } from "@/lib/formatters";
import type { MoneyAccount } from "@/types";
import { useI18n } from "@/features/i18n/i18n";

/** A list with a "new" dialog, shared by the small setup tables. */
export function SimpleList<Row extends { id: string }, Draft extends object>({
  path,
  rows,
  loading,
  columns,
  blank,
  title,
  form,
  toBody,
  invalidate = [path],
  canCreate = true,
}: {
  path: string;
  rows: Row[] | undefined;
  loading: boolean;
  columns: Col<Row>[];
  blank: Draft;
  title: string;
  form: (draft: Draft, set: (patch: Partial<Draft>) => void) => ReactNode;
  toBody?: (draft: Draft) => unknown;
  invalidate?: string[];
  canCreate?: boolean;
}) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState<Draft>(blank);
  const create = useWrite<Draft, unknown>("POST", path, {
    invalidate,
    success: t.hr.saved,
    toBody,
    onSuccess: () => {
      setOpen(false);
      setDraft(blank);
    },
  });
  return (
    <div className="space-y-4">
      {canCreate && (
        <FilterBar className="justify-end">
          <Button onClick={() => setOpen(true)}>
            <Plus size={16} /> {title}
          </Button>
        </FilterBar>
      )}
      <DataGrid rows={rows} loading={loading} rowKey={(r) => r.id} columns={columns} />
      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title={title}
        busy={create.isPending}
        onSubmit={() => create.mutate(draft)}
      >
        {form(draft, (patch) => setDraft((d) => ({ ...d, ...patch })))}
      </FormDialog>
    </div>
  );
}

interface LeaveType {
  id: string;
  name: string;
  days_per_year: string;
  is_paid: boolean;
  is_active: boolean;
}

function LeaveTypes() {
  const { t } = useI18n();
  const { can } = useAuth();
  const list = useGet<LeaveType[]>("/leave-types");
  return (
    <SimpleList
      path="/leave-types"
      title={t.hr.newLeaveType}
      rows={list.data}
      loading={list.isLoading}
      canCreate={can("hr.employee.manage")}
      blank={{ name: "", days_per_year: "21", is_paid: true }}
      columns={[
        {
          header: t.common.name2,
          cell: (l) => <span className="font-medium text-[var(--od-ink)]">{l.name}</span>,
        },
        {
          header: t.hr.daysAYear,
          align: "right",
          cell: (l) => <span className="od-tabular">{String(Number(l.days_per_year))}</span>,
        },
        {
          header: t.common.paid,
          cell: (l) =>
            l.is_paid ? (
              <Badge variant="secondary">{t.common.paid}</Badge>
            ) : (
              <span className="text-[var(--od-ink-muted)]">{t.hr.unpaid}</span>
            ),
        },
        {
          header: t.common.status,
          cell: (l) => <StatusBadge value={l.is_active ? "active" : "inactive"} />,
        },
      ]}
      form={(d, set) => (
        <>
          <Field label={t.common.name2}>
            <Input required value={d.name} onChange={(e) => set({ name: e.target.value })} />
          </Field>
          <Field label={t.hr.daysAYear}>
            <Input
              inputMode="decimal"
              className="od-tabular"
              value={d.days_per_year}
              onChange={(e) => set({ days_per_year: e.target.value })}
            />
          </Field>
          <label className="flex cursor-pointer items-center gap-2.5 rounded-xl border border-[var(--od-border)] bg-[var(--od-surface-subtle)] px-3.5 py-2.5 text-sm text-[var(--od-ink)]">
            <input
              type="checkbox"
              checked={d.is_paid}
              onChange={(e) => set({ is_paid: e.target.checked })}
            />
            {t.hr.paidLeave}
          </label>
        </>
      )}
    />
  );
}

interface Loan {
  id: string;
  employee_name: string;
  kind: string;
  start_period?: string;
  principal: string;
  balance: string;
  monthly_deduction: string;
  status: string;
  payment_id?: string | null;
}
interface EmployeeOption {
  id: string;
  name: string;
}

function Loans() {
  const { t } = useI18n();
  const { can, canAny } = useAuth();
  const [page, setPage] = useState(1);
  const list = useGetPage<Loan>("/loans", { page });
  const employees = useGet<EmployeeOption[]>("/employees", { per_page: 100 });
  const accounts = useGet<MoneyAccount[]>("/money-accounts", undefined, {
    enabled: canAny(
      "finance.account.view",
      "finance.payment.receive",
      "finance.payment.make",
      "hr.compensation.manage",
    ),
  });
  const blank = {
    employee_id: "",
    kind: "loan",
    principal: "",
    monthly_deduction: "",
    start_period: today(),
    reason: "",
  };
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState(blank);
  const [acting, setActing] = useState<{ loan: Loan; mode: "disburse" | "repay" } | null>(null);
  const [account, setAccount] = useState("");
  const [amount, setAmount] = useState("");
  const done = { invalidate: ["/loans", "/money", "/payroll-runs"] };
  const create = useWrite("POST", "/loans", {
    ...done,
    idempotent: true,
    success: t.hr.advanceLoanRecorded,
    onSuccess: () => {
      setOpen(false);
      setDraft(blank);
    },
    toBody: (d: typeof blank) => ({
      ...d,
      monthly_deduction: d.kind === "loan" && d.monthly_deduction ? d.monthly_deduction : undefined,
    }),
  });
  const act = useWrite<unknown, unknown>(
    "POST",
    () => `/loans/${acting?.loan.id}/${acting?.mode}`,
    {
      ...done,
      idempotent: true,
      success: t.common.done,
      onSuccess: () => {
        setActing(null);
        setAmount("");
      },
    },
  );
  const canDisburse = canAny("hr.compensation.manage", "finance.payment.make");
  const canRepay = canAny("hr.compensation.manage", "finance.payment.receive");
  return (
    <div className="space-y-4">
      {can("hr.compensation.manage") && (
        <FilterBar className="justify-end">
          <Button onClick={() => setOpen(true)}>
            <Plus size={16} /> {t.hr.newAdvanceLoan}
          </Button>
        </FilterBar>
      )}
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(l) => l.id}
        empty={t.hr.noLoansOrAdvances}
        columns={[
          {
            header: t.common.employee,
            cell: (l) => (
              <span className="font-medium text-[var(--od-ink)]">{l.employee_name}</span>
            ),
          },
          {
            header: t.common.type,
            cell: (l) => (
              <Badge variant="secondary" className="capitalize">
                {l.kind}
              </Badge>
            ),
          },
          {
            header: t.hr.principal,
            align: "right",
            cell: (l) => <span className="od-tabular">{formatMoney(l.principal)}</span>,
          },
          {
            header: t.common.monthly,
            align: "right",
            cell: (l) => <span className="od-tabular">{formatMoney(l.monthly_deduction)}</span>,
          },
          {
            header: t.common.balance,
            align: "right",
            cell: (l) => (
              <span
                className={`od-tabular font-semibold ${
                  Number(l.balance) > 0 ? "text-[var(--od-warning)]" : "text-[var(--od-ink)]"
                }`}
              >
                {formatMoney(l.balance)}
              </span>
            ),
          },
          { header: t.common.status, cell: (l) => <StatusBadge value={l.status} /> },
          {
            header: "",
            cell: (l) => (
              <div className="flex justify-end gap-1.5">
                {((l.status === "active" && !l.payment_id) || l.status === "approved") &&
                  canDisburse && (
                    <Button size="sm" onClick={() => setActing({ loan: l, mode: "disburse" })}>
                      {t.hr.disburse}
                    </Button>
                  )}
                {(l.status === "active" || l.status === "disbursed") && canRepay && (
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => setActing({ loan: l, mode: "repay" })}
                  >
                    {t.hr.repay}
                  </Button>
                )}
              </div>
            ),
          },
        ]}
      />
      <Pager meta={list.data?.meta} onPage={setPage} />
      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title={t.hr.newLoanOrAdvance}
        busy={create.isPending}
        onSubmit={() => create.mutate(draft)}
      >
        <div className="grid grid-cols-2 gap-3">
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
          <Field label={t.common.type}>
            <NativeSelect
              value={draft.kind}
              onChange={(e) => setDraft((d) => ({ ...d, kind: e.target.value }))}
            >
              <option value="loan">{t.hr.loanMonthlyInstalments}</option>
              <option value="advance">{t.hr.salaryAdvanceFullNextRun}</option>
            </NativeSelect>
          </Field>
        </div>
        <div className="grid grid-cols-2 gap-3">
          <Field label={t.common.amount}>
            <Input
              required
              inputMode="decimal"
              className="od-tabular"
              value={draft.principal}
              onChange={(e) => setDraft((d) => ({ ...d, principal: e.target.value }))}
            />
          </Field>
          {draft.kind === "loan" && (
            <Field label={t.hr.deductedEachMonth}>
              <Input
                inputMode="decimal"
                className="od-tabular"
                placeholder={t.hr.defaultsToFullAmount}
                value={draft.monthly_deduction}
                onChange={(e) => setDraft((d) => ({ ...d, monthly_deduction: e.target.value }))}
              />
            </Field>
          )}
        </div>
        <Field label={t.common.reason}>
          <Input
            value={draft.reason}
            onChange={(e) => setDraft((d) => ({ ...d, reason: e.target.value }))}
          />
        </Field>
      </FormDialog>
      <FormDialog
        open={acting !== null}
        onOpenChange={(o) => !o && setActing(null)}
        title={`${acting?.mode === "repay" ? t.hr.repaymentFor : t.hr.disburse} ${acting?.loan.employee_name ?? ""}`}
        busy={act.isPending}
        onSubmit={() =>
          act.mutate(
            acting?.mode === "repay"
              ? { amount, money_account_id: account }
              : { money_account_id: account },
          )
        }
      >
        {acting && (
          <div className="rounded-xl border border-[var(--od-border)] bg-[var(--od-surface-subtle)] px-3.5 py-2.5 text-sm text-[var(--od-ink-secondary)]">
            {acting.mode === "repay" ? `${t.hr.currentBalance} ` : `${t.hr.principalToDisburse} `}
            <strong className="od-tabular font-semibold text-[var(--od-ink)]">
              {formatMoney(acting.mode === "repay" ? acting.loan.balance : acting.loan.principal)}
            </strong>
          </div>
        )}
        {acting?.mode === "repay" && (
          <Field label={t.common.amount}>
            <Input
              required
              inputMode="decimal"
              className="od-tabular"
              value={amount}
              onChange={(e) => setAmount(e.target.value)}
            />
          </Field>
        )}
        <Field label={acting?.mode === "repay" ? t.hr.receivedInto : t.common.payFrom}>
          <NativeSelect required value={account} onChange={(e) => setAccount(e.target.value)}>
            <option value="">{t.common.choose}</option>
            {accounts.data
              ?.filter((a) => a.is_active)
              .map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
          </NativeSelect>
        </Field>
      </FormDialog>
    </div>
  );
}

export default function HrSetup() {
  const { t } = useI18n();
  const { can, canAny } = useAuth();
  const canLeaveTypes = can("hr.employee.manage");
  const canLoans = canAny("hr.compensation.view", "hr.compensation.manage", "hr.payroll.view");
  return (
    <div className="space-y-6">
      <PageHeader title={t.common.hrSetup} description={t.hr.leaveTypesAndStaffAdvances} />
      <Tabs defaultValue={canLeaveTypes ? "leave" : "loans"}>
        <TabsList className="mb-4 flex-wrap">
          {canLeaveTypes && <TabsTrigger value="leave">{t.hr.leaveTypes}</TabsTrigger>}
          {canLoans && <TabsTrigger value="loans">{t.hr.advancesLoans2}</TabsTrigger>}
        </TabsList>
        {canLeaveTypes && (
          <TabsContent value="leave">
            <LeaveTypes />
          </TabsContent>
        )}
        {canLoans && (
          <TabsContent value="loans">
            <Loans />
          </TabsContent>
        )}
      </Tabs>
    </div>
  );
}
