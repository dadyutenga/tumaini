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

interface Component {
  id: string;
  name: string;
  kind: string;
  calc: string;
  value: string | null;
  is_taxable: boolean;
  applies_to_all: boolean;
  is_active: boolean;
}
interface Rule {
  id: string;
  employee_id: string | null;
  position_id: string | null;
  calc: string;
  value: string;
  min_target: string | null;
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

const calcLabel: Record<string, string> = {
  fixed: "Fixed amount",
  percent_of_base: "% of base pay",
  percent_of_gross: "% of gross",
  bracket: "Brackets",
  percent_of_sales: "% of sales",
  fixed_per_sale: "Fixed per sale",
};

function Components() {
  const list = useGet<Component[]>("/salary-components");
  const toggle = useWrite<Component, unknown>("PUT", (c) => `/salary-components/${c.id}`, {
    invalidate: ["/salary-components"],
    success: "Updated",
    toBody: (c) => ({ is_active: !c.is_active }),
  });
  return (
    <SimpleList
      path="/salary-components"
      title="New component"
      rows={list.data}
      loading={list.isLoading}
      blank={{ name: "", kind: "allowance", calc: "fixed", value: "", is_taxable: true }}
      columns={[
        { header: "Name", cell: (c) => <span className="font-medium">{c.name}</span> },
        { header: "Kind", cell: (c) => c.kind.replaceAll("_", " ") },
        { header: "How", cell: (c) => calcLabel[c.calc] ?? c.calc },
        {
          header: "Value",
          align: "right",
          cell: (c) => (c.value ? String(Number(c.value)) : "-"),
        },
        {
          header: "Status",
          cell: (c) => <StatusBadge value={c.is_active ? "active" : "inactive"} />,
        },
        {
          header: "",
          cell: (c) => (
            <Button size="sm" variant="ghost" onClick={() => toggle.mutate(c)}>
              {c.is_active ? "Turn off" : "Turn on"}
            </Button>
          ),
        },
      ]}
      form={(d, set) => (
        <>
          <Field label="Name">
            <Input required value={d.name} onChange={(e) => set({ name: e.target.value })} />
          </Field>
          <div className="grid grid-cols-2 gap-3">
            <Field label="Kind">
              <NativeSelect value={d.kind} onChange={(e) => set({ kind: e.target.value })}>
                <option value="allowance">Allowance (adds to pay)</option>
                <option value="deduction">Deduction (taken from pay)</option>
                <option value="employer_contribution">Employer contribution</option>
              </NativeSelect>
            </Field>
            <Field label="How it is worked out">
              <NativeSelect value={d.calc} onChange={(e) => set({ calc: e.target.value })}>
                <option value="fixed">Fixed amount</option>
                <option value="percent_of_base">% of base pay</option>
                <option value="percent_of_gross">% of gross</option>
              </NativeSelect>
            </Field>
          </div>
          <Field label={d.calc === "fixed" ? "Amount" : "Percent"}>
            <Input
              required
              inputMode="decimal"
              value={d.value}
              onChange={(e) => set({ value: e.target.value })}
            />
          </Field>
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={d.is_taxable}
              onChange={(e) => set({ is_taxable: e.target.checked })}
            />
            Counts as taxable pay
          </label>
        </>
      )}
    />
  );
}

function Commissions() {
  const list = useGet<Rule[]>("/commission-rules");
  const positions = useGet<Option[]>("/positions");
  const employees = useGet<Option[]>("/employees", { per_page: 100 });
  const who = (r: Rule) =>
    r.employee_id
      ? (employees.data?.find((e) => e.id === r.employee_id)?.name ?? "Employee")
      : r.position_id
        ? (positions.data?.find((p) => p.id === r.position_id)?.name ?? "Position")
        : "-";
  return (
    <SimpleList
      path="/commission-rules"
      title="New commission rule"
      rows={list.data}
      loading={list.isLoading}
      blank={{
        applies: "position",
        target_id: "",
        calc: "percent_of_sales",
        value: "",
        min_target: "",
      }}
      toBody={(d) => ({
        employee_id: d.applies === "employee" ? d.target_id || undefined : undefined,
        position_id: d.applies === "position" ? d.target_id || undefined : undefined,
        calc: d.calc,
        value: d.value,
        min_target: d.min_target || undefined,
      })}
      columns={[
        { header: "Applies to", cell: (r) => <span className="font-medium">{who(r)}</span> },
        { header: "Method", cell: (r) => calcLabel[r.calc] ?? r.calc },
        { header: "Value", align: "right", cell: (r) => String(Number(r.value)) },
        {
          header: "Above sales of",
          align: "right",
          cell: (r) => (r.min_target ? formatMoney(r.min_target) : "-"),
        },
        {
          header: "Status",
          cell: (r) => <StatusBadge value={r.is_active ? "active" : "inactive"} />,
        },
      ]}
      form={(d, set) => (
        <>
          <div className="grid grid-cols-2 gap-3">
            <Field label="Applies to">
              <NativeSelect
                value={d.applies}
                onChange={(e) => set({ applies: e.target.value, target_id: "" })}
              >
                <option value="position">A position</option>
                <option value="employee">One employee</option>
              </NativeSelect>
            </Field>
            <Field label={d.applies === "position" ? "Position" : "Employee"}>
              <NativeSelect
                required
                value={d.target_id}
                onChange={(e) => set({ target_id: e.target.value })}
              >
                <option value="">Choose…</option>
                {(d.applies === "position" ? positions.data : employees.data)?.map((o) => (
                  <option key={o.id} value={o.id}>
                    {o.name}
                  </option>
                ))}
              </NativeSelect>
            </Field>
          </div>
          <div className="grid grid-cols-3 gap-3">
            <Field label="Method">
              <NativeSelect value={d.calc} onChange={(e) => set({ calc: e.target.value })}>
                <option value="percent_of_sales">% of sales</option>
                <option value="fixed_per_sale">Fixed per sale</option>
              </NativeSelect>
            </Field>
            <Field label="Value">
              <Input
                required
                inputMode="decimal"
                value={d.value}
                onChange={(e) => set({ value: e.target.value })}
              />
            </Field>
            <Field label="Only above">
              <Input
                inputMode="decimal"
                value={d.min_target}
                onChange={(e) => set({ min_target: e.target.value })}
              />
            </Field>
          </div>
        </>
      )}
    />
  );
}

function Adjustments() {
  const { can } = useAuth();
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
        {can("adjustments.manage") && (
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
            cell: (a) => (
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
  const { can } = useAuth();
  return (
    <>
      <PageHeader
        title="Payroll setup"
        description="Allowances, deductions, sales commission and one-off bonuses."
      />
      <Tabs defaultValue={can("components.manage") ? "components" : "adjustments"}>
        <TabsList className="mb-4">
          {can("components.manage") && <TabsTrigger value="components">Components</TabsTrigger>}
          {can("components.manage") && <TabsTrigger value="commissions">Commission</TabsTrigger>}
          {can("adjustments.manage") && <TabsTrigger value="adjustments">Adjustments</TabsTrigger>}
        </TabsList>
        {can("components.manage") && (
          <TabsContent value="components">
            <Components />
          </TabsContent>
        )}
        {can("components.manage") && (
          <TabsContent value="commissions">
            <Commissions />
          </TabsContent>
        )}
        {can("adjustments.manage") && (
          <TabsContent value="adjustments">
            <Adjustments />
          </TabsContent>
        )}
      </Tabs>
    </>
  );
}
