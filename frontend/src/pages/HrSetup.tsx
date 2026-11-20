import { useState, type ReactNode } from "react";
import { Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  DataGrid,
  Field,
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
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState<Draft>(blank);
  const create = useWrite<Draft, unknown>("POST", path, {
    invalidate,
    success: "Saved",
    toBody,
    onSuccess: () => {
      setOpen(false);
      setDraft(blank);
    },
  });
  return (
    <>
      {canCreate && (
        <div className="mb-4 flex justify-end">
          <Button onClick={() => setOpen(true)}>
            <Plus size={16} /> {title}
          </Button>
        </div>
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
    </>
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
  const { can } = useAuth();
  const list = useGet<LeaveType[]>("/leave-types");
  return (
    <SimpleList
      path="/leave-types"
      title="New leave type"
      rows={list.data}
      loading={list.isLoading}
      canCreate={can("hr.employee.manage")}
      blank={{ name: "", days_per_year: "21", is_paid: true }}
      columns={[
        { header: "Name", cell: (l) => <span className="font-medium">{l.name}</span> },
        { header: "Days a year", align: "right", cell: (l) => String(Number(l.days_per_year)) },
        { header: "Paid", cell: (l) => (l.is_paid ? "Yes" : "No") },
        {
          header: "Status",
          cell: (l) => <StatusBadge value={l.is_active ? "active" : "inactive"} />,
        },
      ]}
      form={(d, set) => (
        <>
          <Field label="Name">
            <Input required value={d.name} onChange={(e) => set({ name: e.target.value })} />
          </Field>
          <Field label="Days a year">
            <Input
              inputMode="decimal"
              value={d.days_per_year}
              onChange={(e) => set({ days_per_year: e.target.value })}
            />
          </Field>
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={d.is_paid}
              onChange={(e) => set({ is_paid: e.target.checked })}
            />
            Paid leave
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
    success: "Advance / loan recorded",
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
      success: "Done",
      onSuccess: () => {
        setActing(null);
        setAmount("");
      },
    },
  );
  const canDisburse = canAny("hr.compensation.manage", "finance.payment.make");
  const canRepay = canAny("hr.compensation.manage", "finance.payment.receive");
  return (
    <>
      <div className="mb-4 flex justify-end">
        {can("hr.compensation.manage") && (
          <Button onClick={() => setOpen(true)}>
            <Plus size={16} /> New advance / loan
          </Button>
        )}
      </div>
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(l) => l.id}
        empty="No loans or advances"
        columns={[
          {
            header: "Employee",
            cell: (l) => <span className="font-medium">{l.employee_name}</span>,
          },
          { header: "Type", cell: (l) => <span className="capitalize">{l.kind}</span> },
          { header: "Principal", align: "right", cell: (l) => formatMoney(l.principal) },
          { header: "Monthly", align: "right", cell: (l) => formatMoney(l.monthly_deduction) },
          { header: "Balance", align: "right", cell: (l) => formatMoney(l.balance) },
          { header: "Status", cell: (l) => <StatusBadge value={l.status} /> },
          {
            header: "",
            cell: (l) => (
              <div className="flex gap-2">
                {((l.status === "active" && !l.payment_id) || l.status === "approved") &&
                  canDisburse && (
                    <Button size="sm" onClick={() => setActing({ loan: l, mode: "disburse" })}>
                      Disburse
                    </Button>
                  )}
                {(l.status === "active" || l.status === "disbursed") && canRepay && (
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => setActing({ loan: l, mode: "repay" })}
                  >
                    Repay
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
        title="New loan or advance"
        busy={create.isPending}
        onSubmit={() => create.mutate(draft)}
      >
        <div className="grid grid-cols-2 gap-3">
          <Field label="Employee">
            <NativeSelect
              required
              value={draft.employee_id}
              onChange={(e) => setDraft((d) => ({ ...d, employee_id: e.target.value }))}
            >
              <option value="">Choose…</option>
              {employees.data?.map((e) => (
                <option key={e.id} value={e.id}>
                  {e.name}
                </option>
              ))}
            </NativeSelect>
          </Field>
          <Field label="Type">
            <NativeSelect
              value={draft.kind}
              onChange={(e) => setDraft((d) => ({ ...d, kind: e.target.value }))}
            >
              <option value="loan">Loan (monthly instalments)</option>
              <option value="advance">Salary advance (full next run)</option>
            </NativeSelect>
          </Field>
        </div>
        <div className="grid grid-cols-2 gap-3">
          <Field label="Amount">
            <Input
              required
              inputMode="decimal"
              value={draft.principal}
              onChange={(e) => setDraft((d) => ({ ...d, principal: e.target.value }))}
            />
          </Field>
          {draft.kind === "loan" && (
            <Field label="Deducted each month">
              <Input
                inputMode="decimal"
                placeholder="Defaults to full amount"
                value={draft.monthly_deduction}
                onChange={(e) => setDraft((d) => ({ ...d, monthly_deduction: e.target.value }))}
              />
            </Field>
          )}
        </div>
        <Field label="Reason">
          <Input
            value={draft.reason}
            onChange={(e) => setDraft((d) => ({ ...d, reason: e.target.value }))}
          />
        </Field>
      </FormDialog>
      <FormDialog
        open={acting !== null}
        onOpenChange={(o) => !o && setActing(null)}
        title={`${acting?.mode === "repay" ? "Repayment for" : "Disburse"} ${acting?.loan.employee_name ?? ""}`}
        busy={act.isPending}
        onSubmit={() =>
          act.mutate(
            acting?.mode === "repay"
              ? { amount, money_account_id: account }
              : { money_account_id: account },
          )
        }
      >
        {acting?.mode === "repay" && (
          <Field label="Amount">
            <Input
              required
              inputMode="decimal"
              value={amount}
              onChange={(e) => setAmount(e.target.value)}
            />
          </Field>
        )}
        <Field label={acting?.mode === "repay" ? "Received into" : "Pay from"}>
          <NativeSelect required value={account} onChange={(e) => setAccount(e.target.value)}>
            <option value="">Choose…</option>
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
    </>
  );
}

export default function HrSetup() {
  const { can, canAny } = useAuth();
  const canLeaveTypes = can("hr.employee.manage");
  const canLoans = canAny("hr.compensation.view", "hr.compensation.manage", "hr.payroll.view");
  return (
    <>
      <PageHeader title="HR setup" description="Leave types and staff advances / loans." />
      <Tabs defaultValue={canLeaveTypes ? "leave" : "loans"}>
        <TabsList className="mb-4 flex-wrap">
          {canLeaveTypes && <TabsTrigger value="leave">Leave types</TabsTrigger>}
          {canLoans && <TabsTrigger value="loans">Advances &amp; loans</TabsTrigger>}
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
    </>
  );
}
