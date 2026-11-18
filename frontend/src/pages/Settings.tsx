import { useEffect, useState } from "react";
import { Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { DataGrid, Field, FormDialog, PageHeader, StatusBadge } from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { useGet, useWrite } from "@/hooks/useApi";
import { SettingsGroups } from "@/features/auth/SettingsGroups";
import type { Branch } from "@/types";

interface Business {
  name: string;
  legal_name: string | null;
  tax_number: string | null;
  reg_number: string | null;
  phone: string | null;
  email: string | null;
  address: string | null;
  currency: string;
  timezone: string;
}
interface Tax {
  id: string;
  name: string;
  rate: string;
  is_default: boolean;
  is_active: boolean;
}

export default function Settings() {
  const { can } = useAuth();
  const business = useGet<Business>("/business");
  const branches = useGet<Branch[]>("/branches", undefined, { enabled: can("branches.view") });
  const taxes = useGet<Tax[]>("/tax-rates");
  const [form, setForm] = useState<Business | null>(null);
  useEffect(() => {
    if (business.data) setForm(business.data);
  }, [business.data]);
  const save = useWrite<Business, Business>("PUT", "/business", {
    invalidate: ["/business"],
    success: "Business profile saved",
  });

  const [branch, setBranch] = useState<{
    name: string;
    code: string;
    address: string;
    phone: string;
  } | null>(null);
  const addBranch = useWrite<unknown, unknown>("POST", "/branches", {
    invalidate: ["/branches"],
    success: "Branch created",
    onSuccess: () => setBranch(null),
  });
  const [tax, setTax] = useState<{ name: string; rate: string } | null>(null);
  const addTax = useWrite<unknown, unknown>("POST", "/tax-rates", {
    invalidate: ["/tax-rates"],
    success: "Tax rate added",
    onSuccess: () => setTax(null),
  });
  const manage = can("business.manage");
  const field = (k: keyof Business) => ({
    value: (form?.[k] as string | null) ?? "",
    disabled: !manage,
    onChange: (e: React.ChangeEvent<HTMLInputElement>) =>
      setForm((f) => (f ? { ...f, [k]: e.target.value } : f)),
  });

  return (
    <>
      <PageHeader
        title="Settings"
        description="Business profile, branches, tax, receipts, payroll rules and system options."
      />
      <Tabs defaultValue="business">
        <TabsList>
          <TabsTrigger value="business">Business</TabsTrigger>
          {can("branches.view") && <TabsTrigger value="branches">Branches</TabsTrigger>}
          <TabsTrigger value="tax">Tax rates</TabsTrigger>
          <TabsTrigger value="preferences">Preferences</TabsTrigger>
        </TabsList>
        <TabsContent value="preferences">
          <SettingsGroups />
        </TabsContent>
        <TabsContent value="business">
          {form && (
            <form
              className="max-w-2xl space-y-4 rounded-2xl border bg-card p-6"
              onSubmit={(e) => {
                e.preventDefault();
                save.mutate(form);
              }}
            >
              <div className="grid gap-3 sm:grid-cols-2">
                <Field label="Business name" className="sm:col-span-2">
                  <Input required {...field("name")} />
                </Field>
                <Field label="Legal name">
                  <Input {...field("legal_name")} />
                </Field>
                <Field label="Tax number">
                  <Input {...field("tax_number")} />
                </Field>
                <Field label="Phone">
                  <Input {...field("phone")} />
                </Field>
                <Field label="Email">
                  <Input type="email" {...field("email")} />
                </Field>
                <Field label="Address" className="sm:col-span-2">
                  <Input {...field("address")} />
                </Field>
                <Field label="Currency">
                  <Input {...field("currency")} disabled />
                </Field>
                <Field label="Time zone">
                  <Input {...field("timezone")} disabled />
                </Field>
              </div>
              {manage && (
                <Button type="submit" disabled={save.isPending}>
                  Save changes
                </Button>
              )}
            </form>
          )}
        </TabsContent>
        <TabsContent value="branches">
          {can("branches.manage") && (
            <div className="mb-3 flex justify-end">
              <Button onClick={() => setBranch({ name: "", code: "", address: "", phone: "" })}>
                <Plus size={16} /> New branch
              </Button>
            </div>
          )}
          <DataGrid
            rows={branches.data}
            loading={branches.isLoading}
            rowKey={(b) => b.id}
            columns={[
              {
                header: "Branch",
                cell: (b) => (
                  <span className="font-medium">
                    {b.name}
                    {b.is_main && " (main)"}
                  </span>
                ),
              },
              { header: "Code", cell: (b) => b.code },
              { header: "Phone", cell: (b) => b.phone ?? "-" },
              {
                header: "Status",
                cell: (b) => <StatusBadge value={b.is_active ? "active" : "inactive"} />,
              },
            ]}
          />
        </TabsContent>
        <TabsContent value="tax">
          {can("settings.manage") && (
            <div className="mb-3 flex justify-end">
              <Button onClick={() => setTax({ name: "", rate: "" })}>
                <Plus size={16} /> New tax rate
              </Button>
            </div>
          )}
          <DataGrid
            rows={taxes.data}
            loading={taxes.isLoading}
            rowKey={(t) => t.id}
            columns={[
              { header: "Name", cell: (t) => t.name },
              { header: "Rate", align: "right", cell: (t) => `${t.rate}%` },
              { header: "Default", cell: (t) => (t.is_default ? "Yes" : "") },
              {
                header: "Status",
                cell: (t) => <StatusBadge value={t.is_active ? "active" : "inactive"} />,
              },
            ]}
          />
        </TabsContent>
      </Tabs>
      <FormDialog
        open={branch !== null}
        onOpenChange={(o) => !o && setBranch(null)}
        title="New branch"
        busy={addBranch.isPending}
        onSubmit={() => addBranch.mutate(branch)}
      >
        {branch &&
          (["name", "code", "address", "phone"] as const).map((k) => (
            <Field key={k} label={k[0].toUpperCase() + k.slice(1)}>
              <Input
                required={k === "name" || k === "code"}
                value={branch[k]}
                onChange={(e) => setBranch({ ...branch, [k]: e.target.value })}
              />
            </Field>
          ))}
      </FormDialog>
      <FormDialog
        open={tax !== null}
        onOpenChange={(o) => !o && setTax(null)}
        title="New tax rate"
        busy={addTax.isPending}
        onSubmit={() => addTax.mutate({ ...tax, is_active: true })}
      >
        {tax && (
          <>
            <Field label="Name">
              <Input
                required
                value={tax.name}
                onChange={(e) => setTax({ ...tax, name: e.target.value })}
              />
            </Field>
            <Field label="Rate (%)">
              <Input
                required
                inputMode="decimal"
                value={tax.rate}
                onChange={(e) => setTax({ ...tax, rate: e.target.value })}
              />
            </Field>
          </>
        )}
      </FormDialog>
    </>
  );
}
