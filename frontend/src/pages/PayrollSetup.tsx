import { useState } from "react";
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
} from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { useGet, useGetPage, useWrite } from "@/hooks/useApi";
import { SimpleList } from "@/pages/HrSetup";
import { formatDate, formatMoney, today } from "@/lib/formatters";

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

const basisLabel: Record<string, string> = {
  sale_percent: "% of sales",
  project_fixed: "Fixed per completed project",
  percent_of_sales: "% of sales",
  fixed_per_project: "Fixed per completed project",
};

function Commissions() {
  const { can } = useAuth();
  const list = useGet<Rule[]>("/commission-rules");
  const employees = useGet<Option[]>("/employees", { per_page: 100 });
  return (
    <SimpleList
      path="/commission-rules"
      title="New commission rule"
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
          header: "Rule",
          cell: (r) => <span className="font-medium">{r.name || "Commission"}</span>,
        },
        {
          header: "Applies to",
          cell: (r) =>
            r.employee_name ??
            (r.employee_id
              ? (employees.data?.find((e) => e.id === r.employee_id)?.name ?? "Employee")
              : "All eligible staff"),
        },
        { header: "Basis", cell: (r) => basisLabel[r.basis ?? r.calc ?? ""] ?? r.basis ?? "-" },
        {
          header: "Rate / Amount",
          align: "right",
          cell: (r) =>
            r.percent != null
              ? `${Number(r.percent)}%`
              : r.fixed_amount != null
                ? formatMoney(r.fixed_amount)
                : r.value != null
                  ? String(Number(r.value))
                  : "-",
        },
        {
          header: "Effective from",
          cell: (r) => (r.effective_from ? formatDate(r.effective_from) : "-"),
        },
        {
          header: "Status",
          cell: (r) => <StatusBadge value={r.is_active ? "active" : "inactive"} />,
        },
      ]}
      form={(d, set) => (
        <>
          <Field label="Rule name">
            <Input
              required
              placeholder="e.g. Shop sales commission"
              value={d.name}
              onChange={(e) => set({ name: e.target.value })}
            />
          </Field>
          <Field label="Employee (leave blank for all staff)">
            <NativeSelect
              value={d.employee_id}
              onChange={(e) => set({ employee_id: e.target.value })}
            >
              <option value="">All eligible staff</option>
              {employees.data?.map((o) => (
                <option key={o.id} value={o.id}>
                  {o.name}
                </option>
              ))}
            </NativeSelect>
          </Field>
          <div className="grid grid-cols-3 gap-3">
            <Field label="Basis">
              <NativeSelect value={d.basis} onChange={(e) => set({ basis: e.target.value })}>
                <option value="sale_percent">% of sales</option>
                <option value="project_fixed">Fixed per completed project</option>
              </NativeSelect>
            </Field>
            <Field label={d.basis === "sale_percent" ? "Percent (%)" : "Amount"}>
              <Input
                required
                inputMode="decimal"
                value={d.value}
                onChange={(e) => set({ value: e.target.value })}
              />
            </Field>
            <Field label="Effective from">
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
    success: "Adjustment saved. It will be included in the next payroll calculation.",
    onSuccess: () => {
      setOpen(false);
      setDraft(blank);
    },
  });
  const remove = useWrite<Adjustment, unknown>("DELETE", (a) => `/pay-adjustments/${a.id}`, {
    invalidate: ["/pay-adjustments"],
    success: "Adjustment removed",
  });
  return (
    <>
      <div className="mb-4 flex justify-end">
        {canManage && (
          <Button onClick={() => setOpen(true)}>
            <Plus size={16} /> New adjustment
          </Button>
        )}
      </div>
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(a) => a.id}
        empty="No bonuses or one-off deductions"
        columns={[
          {
            header: "Employee",
            cell: (a) => <span className="font-medium">{a.employee_name}</span>,
          },
          { header: "Period", cell: (a) => formatDate(a.period) },
          { header: "Kind", cell: (a) => <StatusBadge value={a.kind} /> },
          { header: "Reason", cell: (a) => a.reason },
          { header: "Amount", align: "right", cell: (a) => formatMoney(a.amount) },
          {
            header: "",
            cell: (a) =>
              canManage && (
                <Button size="sm" variant="ghost" onClick={() => remove.mutate(a)}>
                  Remove
                </Button>
              ),
          },
        ]}
      />
      <Pager meta={list.data?.meta} onPage={setPage} />
      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title="New pay adjustment"
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
        <div className="grid grid-cols-3 gap-3">
          <Field label="Kind">
            <NativeSelect
              value={draft.kind}
              onChange={(e) => setDraft((d) => ({ ...d, kind: e.target.value }))}
            >
              <option value="bonus">Bonus</option>
              <option value="deduction">Deduction</option>
            </NativeSelect>
          </Field>
          <Field label="Amount">
            <Input
              required
              inputMode="decimal"
              value={draft.amount}
              onChange={(e) => setDraft((d) => ({ ...d, amount: e.target.value }))}
            />
          </Field>
          <Field label="Pay period date">
            <Input
              required
              type="date"
              value={draft.period}
              onChange={(e) => setDraft((d) => ({ ...d, period: e.target.value }))}
            />
          </Field>
        </div>
        <Field label="Reason">
          <Input
            required
            value={draft.reason}
            onChange={(e) => setDraft((d) => ({ ...d, reason: e.target.value }))}
          />
        </Field>
      </FormDialog>
    </>
  );
}

export default function PayrollSetup() {
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
    <>
      <PageHeader
        title="Payroll setup"
        description="Commission rules and one-off pay adjustments. Manage employee allowances on each employee profile."
      />
      <Tabs defaultValue={canCommissions ? "commissions" : "adjustments"}>
        <TabsList className="mb-4">
          {canCommissions && <TabsTrigger value="commissions">Commission rules</TabsTrigger>}
          {canAdjustments && <TabsTrigger value="adjustments">Pay adjustments</TabsTrigger>}
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
    </>
  );
}
