import { useEffect, useState } from "react";
import { Plus, Upload } from "lucide-react";
import { toast } from "sonner";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { DataGrid, Field, FormDialog, PageHeader, StatusBadge } from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { useI18n } from "@/features/i18n/i18n";
import { errorMessage, useGet, useWrite } from "@/hooks/useApi";
import {
  DocumentsSettingsPanel,
  NotificationsSettingsPanel,
  PaymentMethodsPanel,
  SettingsGroups,
} from "@/features/auth/SettingsGroups";
import { fetchFileBlob, session } from "@/lib/api";
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
  logo_file?: string | null;
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
  const { t } = useI18n();
  const qc = useQueryClient();
  const business = useGet<Business>("/business");
  const branches = useGet<Branch[]>("/branches", undefined, { enabled: can("branches.view") });
  const taxes = useGet<Tax[]>("/tax-rates");
  const [form, setForm] = useState<Business | null>(null);
  const [logoUrl, setLogoUrl] = useState<string | null>(null);
  const [uploadingLogo, setUploadingLogo] = useState(false);

  useEffect(() => {
    if (business.data) setForm(business.data);
  }, [business.data]);

  useEffect(() => {
    const fileId = business.data?.logo_file;
    if (!fileId) {
      setLogoUrl(null);
      return;
    }
    const ctrl = new AbortController();
    let objUrl: string | null = null;
    fetchFileBlob(`/files/${fileId}`, ctrl.signal)
      .then((res) => {
        objUrl = res.url;
        setLogoUrl(res.url);
      })
      .catch(() => {
        if (!ctrl.signal.aborted) setLogoUrl(null);
      });
    return () => {
      ctrl.abort();
      if (objUrl) URL.revokeObjectURL(objUrl);
    };
  }, [business.data?.logo_file]);

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

  async function onLogoPicked(file: File | undefined) {
    if (!file) return;
    setUploadingLogo(true);
    try {
      const fd = new FormData();
      fd.append("file", file);
      const headers: Record<string, string> = {};
      const tok = session.getToken();
      if (tok) headers.Authorization = `Bearer ${tok}`;
      const res = await fetch("/api/v1/business/logo", {
        method: "POST",
        headers,
        credentials: "include",
        body: fd,
      });
      if (!res.ok) {
        const j = (await res.json().catch(() => null)) as { error?: { message?: string } } | null;
        throw new Error(j?.error?.message ?? "Could not upload logo");
      }
      toast.success("Business logo updated");
      void qc.invalidateQueries({ queryKey: ["/business"] });
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setUploadingLogo(false);
    }
  }

  const manage = can("business.manage");
  const field = (k: keyof Business) => ({
    value: (form?.[k] as string | null) ?? "",
    disabled: !manage,
    onChange: (e: React.ChangeEvent<HTMLInputElement>) =>
      setForm((f) => (f ? { ...f, [k]: e.target.value } : f)),
  });

  return (
    <>
      <PageHeader title={t.settings.title} description={t.settings.subtitle} />
      <Tabs defaultValue="business">
        <TabsList className="mb-4 flex flex-wrap">
          <TabsTrigger value="business">Business</TabsTrigger>
          {can("branches.view") && <TabsTrigger value="branches">Branches</TabsTrigger>}
          <TabsTrigger value="finance">Finance &amp; Tax</TabsTrigger>
          <TabsTrigger value="documents">Documents</TabsTrigger>
          <TabsTrigger value="notifications">Notifications</TabsTrigger>
          <TabsTrigger value="system">System &amp; Payroll</TabsTrigger>
        </TabsList>

        <TabsContent value="business">
          {form && (
            <form
              className="max-w-2xl space-y-5 rounded-2xl border bg-card p-6"
              onSubmit={(e) => {
                e.preventDefault();
                save.mutate(form);
              }}
            >
              <div className="flex flex-wrap items-center gap-4 border-b pb-4">
                <div className="flex h-16 w-16 items-center justify-center overflow-hidden rounded-xl border bg-muted">
                  {logoUrl ? (
                    <img src={logoUrl} alt="Business logo" className="h-full w-full object-contain" />
                  ) : (
                    <span className="text-xs font-bold text-muted-foreground">LOGO</span>
                  )}
                </div>
                <div className="space-y-1">
                  <p className="text-sm font-medium">Business Logo</p>
                  <p className="text-xs text-muted-foreground">
                    Appears on printed invoices, quotations and thermal receipts (PNG or JPEG, max 2 MB).
                  </p>
                  {manage && (
                    <label className="inline-flex cursor-pointer items-center gap-1.5 rounded-md border bg-background px-3 py-1.5 text-xs font-medium shadow-sm hover:bg-accent">
                      <Upload size={13} />
                      <span>{uploadingLogo ? "Uploading…" : "Upload logo"}</span>
                      <input
                        type="file"
                        accept="image/png,image/jpeg,image/webp"
                        className="hidden"
                        disabled={uploadingLogo}
                        onChange={(e) => {
                          void onLogoPicked(e.target.files?.[0]);
                          e.target.value = "";
                        }}
                      />
                    </label>
                  )}
                </div>
              </div>

              <div className="grid gap-3 sm:grid-cols-2">
                <Field label="Business name" className="sm:col-span-2">
                  <Input required {...field("name")} />
                </Field>
                <Field label="Legal name">
                  <Input {...field("legal_name")} />
                </Field>
                <Field label="Registration number (BRELA)">
                  <Input {...field("reg_number")} />
                </Field>
                <Field label="Tax number (TIN / VRN)">
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
                  {save.isPending ? "Saving…" : "Save changes"}
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

        <TabsContent value="finance" className="max-w-4xl space-y-6">
          <div className="space-y-3">
            <div className="flex items-center justify-between">
              <h3 className="font-semibold">Tax Rates</h3>
              {can("settings.manage") && (
                <Button size="sm" onClick={() => setTax({ name: "", rate: "" })}>
                  <Plus size={15} /> New tax rate
                </Button>
              )}
            </div>
            <DataGrid
              rows={taxes.data}
              loading={taxes.isLoading}
              rowKey={(tx) => tx.id}
              columns={[
                { header: "Name", cell: (tx) => tx.name },
                { header: "Rate", align: "right", cell: (tx) => `${tx.rate}%` },
                { header: "Default", cell: (tx) => (tx.is_default ? "Yes" : "") },
                {
                  header: "Status",
                  cell: (tx) => <StatusBadge value={tx.is_active ? "active" : "inactive"} />,
                },
              ]}
            />
          </div>

          <PaymentMethodsPanel />
          <SettingsGroups groups={["tax", "currency", "sales"]} />
        </TabsContent>

        <TabsContent value="documents">
          <DocumentsSettingsPanel businessName={form?.name ?? ""} />
        </TabsContent>

        <TabsContent value="notifications">
          <NotificationsSettingsPanel />
        </TabsContent>

        <TabsContent value="system">
          <SettingsGroups groups={["system", "payroll"]} />
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
