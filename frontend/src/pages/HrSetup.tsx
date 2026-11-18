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
import { formatDate, formatMoney, today } from "@/lib/formatters";
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

interface Department {
  id: string;
  name: string;
  head_name: string | null;
  employee_count: number;
}
interface Position {
  id: string;
  name: string;
  default_pay: string | null;
  employee_count: number;
}
interface LeaveType {
  id: string;
  name: string;
  days_per_year: string;
  is_paid: boolean;
  is_active: boolean;
}
interface Holiday {
  id: string;
  day: string;
  name: string;
}

function Departments() {
  const list = useGet<Department[]>("/departments");
  return (
    <SimpleList
      path="/departments"
      title="New department"
      rows={list.data}
      loading={list.isLoading}
      blank={{ name: "" }}
      columns={[
        { header: "Name", cell: (d) => <span className="font-medium">{d.name}</span> },
        { header: "Head", cell: (d) => d.head_name ?? "-" },
        { header: "Staff", align: "right", cell: (d) => d.employee_count },
      ]}
      form={(d, set) => (
        <Field label="Name">
          <Input required value={d.name} onChange={(e) => set({ name: e.target.value })} />
        </Field>
      )}
    />
  );
}

function Positions() {
  const { can } = useAuth();
  const list = useGet<Position[]>("/positions");
  return (
    <SimpleList
      path="/positions"
      title="New position"
      rows={list.data}
      loading={list.isLoading}
      blank={{ name: "", default_pay: "" }}
      toBody={(d) => ({ name: d.name, default_pay: d.default_pay || undefined })}
      columns={[
        { header: "Name", cell: (p) => <span className="font-medium">{p.name}</span> },
        ...(can("employees.view_salary")
          ? [
              {
                header: "Default pay",
                align: "right" as const,
                cell: (p: Position) => formatMoney(p.default_pay),
              },
            ]
          : []),
        { header: "Staff", align: "right", cell: (p) => p.employee_count },
      ]}
      form={(d, set) => (
        <>
          <Field label="Name">
            <Input required value={d.name} onChange={(e) => set({ name: e.target.value })} />
          </Field>
          {can("employees.view_salary") && (
            <Field label="Default pay">
              <Input
                inputMode="decimal"
                value={d.default_pay}
                onChange={(e) => set({ default_pay: e.target.value })}
              />
            </Field>
          )}
        </>
      )}
    />
  );
}

function LeaveTypes() {
  const list = useGet<LeaveType[]>("/leave-types");
  return (
    <SimpleList
      path="/leave-types"
      title="New leave type"
      rows={list.data}
      loading={list.isLoading}
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

function Holidays() {
  const list = useGet<Holiday[]>("/holidays");
  return (
    <SimpleList
      path="/holidays"
      title="New holiday"
      rows={list.data}
      loading={list.isLoading}
      blank={{ day: today(), name: "" }}
      columns={[
        { header: "Date", cell: (h) => formatDate(h.day) },
        { header: "Holiday", cell: (h) => <span className="font-medium">{h.name}</span> },
      ]}
      form={(d, set) => (
        <>
          <Field label="Date">
            <Input
              required
              type="date"
              value={d.day}
              onChange={(e) => set({ day: e.target.value })}
            />
          </Field>
          <Field label="Name">
            <Input required value={d.name} onChange={(e) => set({ name: e.target.value })} />
          </Field>
        </>
      )}
    />
  );
}

interface Loan {
  id: string;
  number: string;
  employee_name: string;
  kind: string;
  principal: string;
  balance: string;
  monthly_deduction: string;
  status: string;
}
interface EmployeeOption {
  id: string;
  name: string;
}

function Loans() {
  const { can } = useAuth();
  const [page, setPage] = useState(1);
  const list = useGetPage<Loan>("/loans", { page });
  const employees = useGet<EmployeeOption[]>("/employees", { per_page: 100 });
  const accounts = useGet<MoneyAccount[]>("/money-accounts", undefined, {
    enabled: can("accounts.view"),
  });
  const blank = {
    employee_id: "",
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
    success: "Loan recorded",
    onSuccess: () => {
      setOpen(false);
      setDraft(blank);
    },
    toBody: (d: typeof blank) => ({ ...d, monthly_deduction: d.monthly_deduction || undefined }),
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
  return (
    <>
      <div className="mb-4 flex justify-end">
        {can("loans.create") && (
          <Button onClick={() => setOpen(true)}>
            <Plus size={16} /> New loan
          </Button>
        )}
      </div>
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(l) => l.id}
        empty="No loans or advances"
        columns={[
          { header: "Number", cell: (l) => <span className="font-medium">{l.number}</span> },
          { header: "Employee", cell: (l) => l.employee_name },
          { header: "Principal", align: "right", cell: (l) => formatMoney(l.principal) },
          { header: "Monthly", align: "right", cell: (l) => formatMoney(l.monthly_deduction) },
          { header: "Balance", align: "right", cell: (l) => formatMoney(l.balance) },
          { header: "Status", cell: (l) => <StatusBadge value={l.status} /> },
          {
            header: "",
            cell: (l) => (
              <div className="flex gap-2">
                {l.status === "approved" && can("loans.approve") && (
                  <Button size="sm" onClick={() => setActing({ loan: l, mode: "disburse" })}>
                    Disburse
                  </Button>
                )}
                {l.status === "disbursed" && can("payments.receive") && (
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
        <div className="grid grid-cols-2 gap-3">
          <Field label="Amount">
            <Input
              required
              inputMode="decimal"
              value={draft.principal}
              onChange={(e) => setDraft((d) => ({ ...d, principal: e.target.value }))}
            />
          </Field>
          <Field label="Deducted each month">
            <Input
              inputMode="decimal"
              value={draft.monthly_deduction}
              onChange={(e) => setDraft((d) => ({ ...d, monthly_deduction: e.target.value }))}
            />
          </Field>
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
        title={`${acting?.mode === "repay" ? "Repayment for" : "Disburse"} ${acting?.loan.number ?? ""}`}
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
  const { can } = useAuth();
  const setup = can("departments.manage");
  return (
    <>
      <PageHeader
        title="HR setup"
        description="Departments, positions, leave rules, public holidays and staff loans."
      />
      <Tabs defaultValue={setup ? "departments" : "loans"}>
        <TabsList className="mb-4 flex-wrap">
          {setup && <TabsTrigger value="departments">Departments</TabsTrigger>}
          {setup && <TabsTrigger value="positions">Positions</TabsTrigger>}
          {setup && <TabsTrigger value="leave">Leave types</TabsTrigger>}
          {can("schedules.manage") && <TabsTrigger value="holidays">Holidays</TabsTrigger>}
          {can("loans.view") && <TabsTrigger value="loans">Loans</TabsTrigger>}
        </TabsList>
        {setup && (
          <TabsContent value="departments">
            <Departments />
          </TabsContent>
        )}
        {setup && (
          <TabsContent value="positions">
            <Positions />
          </TabsContent>
        )}
        {setup && (
          <TabsContent value="leave">
            <LeaveTypes />
          </TabsContent>
        )}
        {can("schedules.manage") && (
          <TabsContent value="holidays">
            <Holidays />
          </TabsContent>
        )}
        {can("loans.view") && (
          <TabsContent value="loans">
            <Loans />
          </TabsContent>
        )}
      </Tabs>
    </>
  );
}
