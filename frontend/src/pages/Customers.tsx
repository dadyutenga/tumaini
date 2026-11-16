import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { DataGrid, Field, FormDialog, PageHeader, StatusBadge } from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { useDebounce } from "@/hooks/useDebounce";
import { useGet, useWrite } from "@/hooks/useApi";
import { formatMoney, toNumber } from "@/lib/formatters";
import type { Customer } from "@/types";

const blank = {
  name: "",
  phone: "",
  email: "",
  address: "",
  tax_number: "",
  credit_limit: "0",
  payment_terms_days: "0",
  notes: "",
};

export default function Customers() {
  const { can } = useAuth();
  const navigate = useNavigate();
  const [q, setQ] = useState("");
  const search = useDebounce(q, 300);
  const { data, isLoading } = useGet<Customer[]>("/customers", { q: search });
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState(blank);
  const create = useWrite<unknown, Customer>("POST", "/customers", {
    invalidate: ["/customers"],
    success: "Customer added",
    onSuccess: (c) => {
      setOpen(false);
      setDraft(blank);
      navigate(`/customers/${c.id}`);
    },
  });
  const set = (k: keyof typeof blank) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setDraft((d) => ({ ...d, [k]: e.target.value }));
  const owed = (data ?? []).reduce((sum, c) => sum + Math.max(0, toNumber(c.balance)), 0);

  return (
    <>
      <PageHeader
        title="Customers"
        description={data ? `Customers owe you ${formatMoney(owed)} in total.` : undefined}
        actions={
          can("customers.manage") && (
            <Button onClick={() => setOpen(true)}>
              <Plus size={16} /> New customer
            </Button>
          )
        }
      />
      <Input
        aria-label="Search customers"
        className="mb-4 max-w-xs"
        placeholder="Search name or phone…"
        value={q}
        onChange={(e) => setQ(e.target.value)}
      />
      <DataGrid
        rows={data?.filter((c) => !c.is_walk_in)}
        loading={isLoading}
        rowKey={(c) => c.id}
        empty="No customers yet"
        onRowClick={(c) => navigate(`/customers/${c.id}`)}
        columns={[
          { header: "Name", cell: (c) => <span className="font-medium">{c.name}</span> },
          { header: "Phone", cell: (c) => c.phone ?? "-" },
          { header: "Credit limit", align: "right", cell: (c) => formatMoney(c.credit_limit) },
          {
            header: "Owes",
            align: "right",
            cell: (c) => (
              <span className={toNumber(c.balance) > 0 ? "font-medium text-destructive" : ""}>
                {formatMoney(c.balance)}
              </span>
            ),
          },
          {
            header: "Status",
            cell: (c) => <StatusBadge value={c.is_active ? "active" : "inactive"} />,
          },
        ]}
      />
      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title="New customer"
        busy={create.isPending}
        onSubmit={() =>
          create.mutate({ ...draft, payment_terms_days: Number(draft.payment_terms_days) || 0 })
        }
        wide
      >
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Name" className="sm:col-span-2">
            <Input required value={draft.name} onChange={set("name")} />
          </Field>
          <Field label="Phone">
            <Input value={draft.phone} onChange={set("phone")} />
          </Field>
          <Field label="Email">
            <Input type="email" value={draft.email} onChange={set("email")} />
          </Field>
          <Field label="Address" className="sm:col-span-2">
            <Input value={draft.address} onChange={set("address")} />
          </Field>
          {can("customers.credit_limit") && (
            <Field label="Credit limit">
              <Input
                inputMode="decimal"
                value={draft.credit_limit}
                onChange={set("credit_limit")}
              />
            </Field>
          )}
          <Field label="Payment terms (days)">
            <Input
              inputMode="numeric"
              value={draft.payment_terms_days}
              onChange={set("payment_terms_days")}
            />
          </Field>
        </div>
      </FormDialog>
    </>
  );
}
