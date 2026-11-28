import { useEffect, useState, type FormEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { PageLoader } from "@/components/shared/Display";
import { Field } from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { useI18n } from "@/features/i18n/i18n";
import { errorMessage, useGet, useWrite } from "@/hooks/useApi";
import { uploadTo } from "@/lib/api";
import type { BusinessUnit } from "@/types";
import { useBusinessName } from "./BusinessBadges";
import { ReceiptPreview } from "./ReceiptPreview";

/** The business profile values a business falls back to. */
export interface ProfileFallback {
  name?: string;
  phone?: string | null;
  address?: string | null;
  tax_number?: string | null;
}

const FIELDS = [
  "display_name",
  "phone",
  "address",
  "tin",
  "receipt_header",
  "receipt_footer",
] as const;
type Draft = Record<(typeof FIELDS)[number], string>;
const draftOf = (u: BusinessUnit) =>
  Object.fromEntries(FIELDS.map((f) => [f, u[f] ?? ""])) as Draft;

function UnitCard({ unit, profile }: { unit: BusinessUnit; profile?: ProfileFallback }) {
  const { t } = useI18n();
  const { can } = useAuth();
  const manage = can("settings.manage");
  const name = useBusinessName();
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState<Draft>(() => draftOf(unit));
  const [uploading, setUploading] = useState(false);
  useEffect(() => setDraft(draftOf(unit)), [unit]);

  const save = useWrite<Draft, BusinessUnit>("PATCH", `/business-units/${unit.code}`, {
    invalidate: ["/business-units"],
    success: t.business.saved,
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate(draft);
  };
  async function pickLogo(file: File | undefined) {
    if (!file) return;
    setUploading(true);
    try {
      await uploadTo(`/business-units/${unit.code}/logo`, file);
      toast.success(t.business.logoSaved);
      void queryClient.invalidateQueries({ queryKey: ["/business-units"] });
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setUploading(false);
    }
  }
  const input = (field: keyof Draft, fallback?: string | null) => ({
    value: draft[field],
    placeholder: fallback ?? "",
    disabled: !manage,
    onChange: (e: { target: { value: string } }) =>
      setDraft((d) => ({ ...d, [field]: e.target.value })),
  });

  return (
    <section aria-label={name(unit.code)} className="od-card space-y-4 p-6">
      <h2 className="od-card-title text-[var(--od-ink)]">{name(unit.code)}</h2>
      <form onSubmit={submit} className="space-y-3">
        <Field label={t.business.displayName}>
          <Input maxLength={200} {...input("display_name", profile?.name)} />
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label={t.business.phone}>
            <Input {...input("phone", profile?.phone)} />
          </Field>
          <Field label={t.business.tin}>
            <Input maxLength={40} {...input("tin", profile?.tax_number)} />
          </Field>
        </div>
        <Field label={t.business.address}>
          <Input maxLength={500} {...input("address", profile?.address)} />
        </Field>
        <Field label={t.business.receiptHeader}>
          <Textarea rows={2} maxLength={500} {...input("receipt_header")} />
        </Field>
        <Field label={t.business.receiptFooter}>
          <Textarea rows={2} maxLength={500} {...input("receipt_footer")} />
        </Field>
        {manage && (
          <div className="flex flex-wrap items-center justify-between gap-3">
            <label className="text-xs text-[var(--od-ink-secondary)]">
              <span className="mb-1 block font-medium">{t.business.uploadLogo}</span>
              <input
                type="file"
                accept="image/png,image/jpeg,image/webp"
                disabled={uploading}
                onChange={(e) => void pickLogo(e.target.files?.[0])}
              />
            </label>
            <Button type="submit" disabled={save.isPending}>
              {t.business.save}
            </Button>
          </div>
        )}
      </form>
      <div>
        <p className="od-kpi-label mb-2">{t.business.preview}</p>
        <ReceiptPreview
          name={draft.display_name || profile?.name || name(unit.code)}
          header={draft.receipt_header}
          footer={draft.receipt_footer}
          details={[
            draft.address || profile?.address || "",
            draft.phone || profile?.phone || "",
            draft.tin || profile?.tax_number
              ? `${t.business.tin}: ${draft.tin || profile?.tax_number}`
              : "",
          ].filter(Boolean)}
          showLogo={Boolean(unit.logo_file_id)}
        />
      </div>
    </section>
  );
}

/** Settings → Businesses: what the shop and the services business each print. */
export function BusinessUnitsPanel({ profile }: { profile?: ProfileFallback }) {
  const { t } = useI18n();
  const units = useGet<BusinessUnit[]>("/business-units");
  if (units.isLoading) return <PageLoader />;
  return (
    <div className="space-y-4">
      <p className="od-caption">{t.business.intro}</p>
      <div className="grid gap-5 lg:grid-cols-2">
        {(units.data ?? []).map((unit) => (
          <UnitCard key={unit.code} unit={unit} profile={profile} />
        ))}
      </div>
    </div>
  );
}
