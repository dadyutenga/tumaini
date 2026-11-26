import { useEffect, useState } from "react";
import { Plus, Upload } from "lucide-react";
import { toast } from "sonner";
import { useQueryClient } from "@tanstack/react-query";
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
  StatusBadge,
} from "@/components/shared/kit";
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
  const branches = useGet<Branch[]>("/branches");
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
    kind: "shop" | "office" | "warehouse";
    address: string;
    phone: string;
  } | null>(null);
  const addBranch = useWrite<unknown, unknown>("POST", "/branches", {
    invalidate: ["/branches"],
    success: "Location created",
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

  const manage = can("settings.manage");
  const field = (k: keyof Business) => ({
    value: (form?.[k] as string | null) ?? "",
    disabled: !manage,
    onChange: (e: React.ChangeEvent<HTMLInputElement>) =>
      setForm((f) => (f ? { ...f, [k]: e.target.value } : f)),
  });

  return (
    <div className="space-y-6">
      <PageHeader title={t.settings.title} description={t.settings.subtitle} />
      <Tabs defaultValue="business">
        <TabsList className="mb-4 flex flex-wrap">
          <TabsTrigger value="business">Business</TabsTrigger>
          <TabsTrigger value="branches">Locations</TabsTrigger>
          <TabsTrigger value="finance">Finance &amp; Tax</TabsTrigger>
          <TabsTrigger value="documents">Documents</TabsTrigger>
          <TabsTrigger value="notifications">Notifications</TabsTrigger>
          <TabsTrigger value="system">System &amp; Payroll</TabsTrigger>
        </TabsList>

        <TabsContent value="business">
          {form && (
            <form
              className="od-card max-w-2xl space-y-5 p-6"
              onSubmit={(e) => {
                e.preventDefault();
                save.mutate(form);
              }}
            >
              <div className="flex flex-wrap items-center gap-4 border-b border-[var(--od-border)] pb-5">
                <div className="flex h-16 w-16 items-center justify-center overflow-hidden rounded-xl border border-[var(--od-border-strong)] bg-[var(--od-surface-subtle)]">
                  {logoUrl ? (
                    <img
                      src={logoUrl}
                      alt="Business logo"
                      className="h-full w-full object-contain"
                    />
                  ) : (
                    <span className="od-mono text-xs font-bold text-[var(--od-ink-muted)]">
                      LOGO
                    </span>
                  )}
                </div>
                <div className="space-y-1">
                  <p className="od-card-title text-[var(--od-ink)]">Business Logo</p>
                  <p className="od-caption">
                    Appears on printed invoices, quotations and thermal receipts (PNG or JPEG, max 2
                    MB).
                  </p>
                  {manage && (
                    <label className="mt-1 inline-flex cursor-pointer items-center gap-1.5 rounded-lg border border-[var(--od-border-strong)] bg-[var(--od-surface)] px-3 py-1.5 text-xs font-medium text-[var(--od-ink)] transition hover:bg-[var(--od-surface-subtle)]">
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

              <div className="grid gap-3.5 sm:grid-cols-2">
                <Field label="Business name" className="sm:col-span-2">
                  <Input required {...field("name")} />
                </Field>
                <Field label="Legal name">
                  <Input {...field("legal_name")} />
                </Field>
                <Field label="Registration number (BRELA)">
                  <Input className="od-mono" {...field("reg_number")} />
                </Field>
                <Field label="Tax number (TIN / VRN)">
                  <Input className="od-mono" {...field("tax_number")} />
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
                  <Input className="od-mono" {...field("currency")} disabled />
                </Field>
                <Field label="Time zone">
                  <Input className="od-mono" {...field("timezone")} disabled />
                </Field>
              </div>
              {manage && (
                <div className="border-t border-[var(--od-border)] pt-4">
                  <Button type="submit" disabled={save.isPending}>
                    {save.isPending ? "Saving…" : "Save changes"}
                  </Button>
                </div>
              )}
            </form>
          )}
        </TabsContent>

        <TabsContent value="branches" className="space-y-4">
          {manage && (
            <FilterBar className="justify-end">
              <Button onClick={() => setBranch({ name: "", kind: "shop", address: "", phone: "" })}>
                <Plus size={16} /> New location
              </Button>
            </FilterBar>
          )}
          <DataGrid
            rows={branches.data}
            loading={branches.isLoading}
            rowKey={(b) => b.id}
            columns={[
              {
                header: "Location",
                cell: (b) => (
                  <div className="flex items-center gap-2">
                    <span className="font-medium text-[var(--od-ink)]">{b.name}</span>
                    {b.is_main && <Badge variant="secondary">Main</Badge>}
                  </div>
                ),
              },
              {
                header: "Type",
                cell: (b) => (
                  <Badge variant="outline" className="capitalize">
                    {b.kind ?? "shop"}
                  </Badge>
                ),
              },
              { header: "Address", cell: (b) => b.address ?? "-" },
              {
                header: "Phone",
                cell: (b) => <span className="od-mono text-xs">{b.phone ?? "-"}</span>,
              },
              {
                header: "Status",
                cell: (b) => <StatusBadge value={b.is_active ? "active" : "inactive"} />,
              },
            ]}
          />
        </TabsContent>

        <TabsContent value="finance" className="max-w-4xl space-y-6">
          <div className="space-y-3">
            <div className="flex items-center justify-between gap-2">
              <h3 className="od-section-title">Tax Rates</h3>
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
                {
                  header: "Name",
                  cell: (tx) => <span className="font-medium text-[var(--od-ink)]">{tx.name}</span>,
                },
                {
                  header: "Rate",
                  align: "right",
                  cell: (tx) => <span className="od-tabular font-medium">{tx.rate}%</span>,
                },
                {
                  header: "Default",
                  cell: (tx) => (tx.is_default ? <Badge variant="secondary">Default</Badge> : ""),
                },
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
        title="New location"
        busy={addBranch.isPending}
        onSubmit={() => addBranch.mutate(branch)}
      >
        {branch && (
          <>
            <Field label="Name">
              <Input
                required
                value={branch.name}
                onChange={(e) => setBranch({ ...branch, name: e.target.value })}
              />
            </Field>
            <Field label="Location type">
              <NativeSelect
                value={branch.kind}
                onChange={(e) =>
                  setBranch({
                    ...branch,
                    kind: e.target.value as "shop" | "office" | "warehouse",
                  })
                }
              >
                <option value="shop">Shop (retail)</option>
                <option value="office">Office (services)</option>
                <option value="warehouse">Warehouse (stock only)</option>
              </NativeSelect>
            </Field>
            <Field label="Address">
              <Input
                value={branch.address}
                onChange={(e) => setBranch({ ...branch, address: e.target.value })}
              />
            </Field>
            <Field label="Phone">
              <Input
                value={branch.phone}
                onChange={(e) => setBranch({ ...branch, phone: e.target.value })}
              />
            </Field>
          </>
        )}
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
                className="od-tabular"
                value={tax.rate}
                onChange={(e) => setTax({ ...tax, rate: e.target.value })}
              />
            </Field>
          </>
        )}
      </FormDialog>
    </div>
  );
}
