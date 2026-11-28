import { useState } from "react";
import { Plus, Search, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  DataGrid,
  Field,
  FilterBar,
  FormDialog,
  NativeSelect,
  PageHeader,
  SegmentedChips,
  StatusBadge,
} from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { label } from "@/features/services/helpers";
import { useGet, useWrite } from "@/hooks/useApi";
import { api } from "@/lib/api";
import { formatMoney, toNumber } from "@/lib/formatters";
import type { CatalogDetail, CatalogItem } from "@/types/services";
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";
import { useSearchBox } from "@/hooks/useSearchBox";

const CATEGORIES = [
  "business",
  "design",
  "printing",
  "licensing",
  "consulting",
  "package",
  "other",
];

interface Draft {
  id?: string;
  name: string;
  category: string;
  description: string;
  standard_price: string;
  government_fees: string;
  included_revisions: string;
  is_package: boolean;
  stages: { name: string; est_days: string }[];
  documents: { name: string; stage_position: string }[];
  package_services: string[];
}

const blank: Draft = {
  name: "",
  category: "business",
  description: "",
  standard_price: "",
  government_fees: "0",
  included_revisions: "0",
  is_package: false,
  stages: [],
  documents: [],
  package_services: [],
};

/** The API replaces stages, documents and package contents with whatever is sent. */
function toBody(d: Draft) {
  return {
    name: d.name,
    category: d.category,
    description: d.description,
    standard_price: d.standard_price || "0",
    government_fees: d.government_fees || "0",
    included_revisions: Number(d.included_revisions) || 0,
    is_package: d.is_package,
    stages: d.is_package
      ? []
      : d.stages.map((s) => ({ name: s.name, est_days: Number(s.est_days) || 0 })),
    documents: d.is_package
      ? []
      : d.documents.map((x) => ({
          name: x.name,
          stage_position: x.stage_position ? Number(x.stage_position) : undefined,
        })),
    package_services: d.is_package ? d.package_services : [],
  };
}

export default function ServiceCatalog() {
  const { t } = useI18n();
  const { can } = useAuth();
  const manage = can("services.catalog.manage");
  const [category, setCategory] = useState("");
  const [q, setQ] = useSearchBox();
  const list = useGet<CatalogItem[]>("/service-catalog", {
    category,
    q,
    all: manage || undefined,
    per_page: 100,
  });
  const all = useGet<CatalogItem[]>("/service-catalog", { per_page: 100 });
  const [draft, setDraft] = useState<Draft | null>(null);
  const [loading, setLoading] = useState(false);

  const create = useWrite<Draft>("POST", "/service-catalog", {
    invalidate: ["/service-catalog"],
    success: t.services.serviceAdded,
    toBody: toBody,
    onSuccess: () => setDraft(null),
  });
  const edit = useWrite<Draft>("PUT", (d) => `/service-catalog/${d.id}`, {
    invalidate: ["/service-catalog"],
    success: t.services.serviceSaved,
    toBody: toBody,
    onSuccess: () => setDraft(null),
  });
  const toggle = useWrite<{ id: string; active: boolean }>(
    "POST",
    (b) => `/service-catalog/${b.id}/${b.active ? "activate" : "deactivate"}`,
    {
      invalidate: ["/service-catalog"],
      success: t.services.serviceUpdated,
      toBody: () => undefined,
    },
  );

  async function open(item: CatalogItem) {
    setLoading(true);
    try {
      const d = await api.get<CatalogDetail>(`/service-catalog/${item.id}`);
      setDraft({
        id: d.id,
        name: d.name,
        category: d.category,
        description: d.description ?? "",
        standard_price: d.standard_price,
        government_fees: d.government_fees,
        included_revisions: String(d.included_revisions),
        is_package: d.is_package,
        stages: d.stages.map((s) => ({ name: s.name, est_days: String(s.est_days) })),
        documents: d.documents.map((x) => ({
          name: x.name,
          stage_position: x.stage_position ? String(x.stage_position) : "",
        })),
        package_services: (d.package_services as { id: string }[]).map((x) =>
          typeof x === "string" ? x : x.id,
        ),
      });
    } finally {
      setLoading(false);
    }
  }

  const set = <K extends keyof Draft>(k: K, v: Draft[K]) =>
    setDraft((d) => (d ? { ...d, [k]: v } : d));
  const singles = (all.data ?? []).filter(
    (s) => !s.is_package && s.is_active && s.id !== draft?.id,
  );

  return (
    <div className="space-y-6">
      <PageHeader
        title={t.common.serviceCatalog}
        description={t.services.standardServicesFixedPricingGovernment}
        actions={
          manage && (
            <Button size="sm" onClick={() => setDraft(blank)}>
              <Plus size={14} /> {t.services.newService}
            </Button>
          )
        }
      />

      <FilterBar className="justify-between">
        <div className="relative min-w-60 flex-1 sm:max-w-xs">
          <Search
            size={15}
            className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-[var(--od-ink-muted)]"
          />
          <Input
            aria-label={t.services.searchServices}
            className="pl-9"
            placeholder={t.services.searchServices2}
            value={q}
            onChange={(e) => setQ(e.target.value)}
          />
        </div>
        <SegmentedChips
          ariaLabel={t.common.category}
          value={category}
          onChange={setCategory}
          options={[
            { value: "", label: t.common.all },
            ...CATEGORIES.map((c) => ({ value: c, label: label(c) })),
          ]}
        />
      </FilterBar>

      <DataGrid
        rows={list.data}
        loading={list.isLoading || loading}
        rowKey={(s) => s.id}
        empty={t.services.noServicesYetAddWhat}
        onRowClick={manage ? (s) => void open(s) : undefined}
        columns={[
          {
            header: t.services.service,
            sortValue: (s) => s.name,
            cell: (s) => (
              <div>
                <p className="font-medium">{s.name}</p>
                {s.is_package && (
                  <p className="od-caption">
                    {fmt(t.services.packageOfServices, { service_count: s.service_count })}
                  </p>
                )}
              </div>
            ),
          },
          {
            header: t.common.category,
            sortValue: (s) => s.category,
            cell: (s) => (
              <span className="text-[var(--od-ink-secondary)]">{label(s.category)}</span>
            ),
          },
          {
            header: t.services.stages2,
            align: "right",
            sortValue: (s) => s.stage_count,
            cell: (s) => s.stage_count,
          },
          {
            header: t.common.price,
            align: "right",
            sortValue: (s) => toNumber(s.standard_price),
            cell: (s) => <span className="font-medium">{formatMoney(s.standard_price)}</span>,
          },
          {
            header: t.services.govtFees,
            align: "right",
            sortValue: (s) => toNumber(s.government_fees),
            cell: (s) => formatMoney(s.government_fees),
          },
          {
            header: t.common.status,
            cell: (s) => <StatusBadge value={s.is_active ? "active" : "inactive"} />,
          },
          {
            header: "",
            className: "text-right",
            cell: (s) =>
              manage ? (
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={(e) => {
                    e.stopPropagation();
                    toggle.mutate({ id: s.id, active: !s.is_active });
                  }}
                >
                  {s.is_active ? t.services.hide : t.services.show}
                </Button>
              ) : null,
          },
        ]}
      />

      {draft && (
        <FormDialog
          open
          onOpenChange={(o) => !o && setDraft(null)}
          title={draft.id ? t.services.editService : t.services.newService}
          description={t.services.projectsAlreadyStartedKeepTheir}
          busy={create.isPending || edit.isPending}
          onSubmit={() => (draft.id ? edit.mutate(draft) : create.mutate(draft))}
          wide
        >
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label={t.common.name} className="sm:col-span-2">
              <Input required value={draft.name} onChange={(e) => set("name", e.target.value)} />
            </Field>
            <Field label={t.common.category}>
              <NativeSelect
                value={draft.category}
                onChange={(e) => set("category", e.target.value)}
              >
                {CATEGORIES.map((c) => (
                  <option key={c} value={c}>
                    {label(c)}
                  </option>
                ))}
              </NativeSelect>
            </Field>
            <Field label={t.services.standardPrice}>
              <Input
                required
                inputMode="decimal"
                value={draft.standard_price}
                onChange={(e) => set("standard_price", e.target.value)}
              />
            </Field>
            <Field label={t.services.governmentFees}>
              <Input
                inputMode="decimal"
                value={draft.government_fees}
                onChange={(e) => set("government_fees", e.target.value)}
              />
            </Field>
            <Field label={t.services.revisionsIncludedDesign}>
              <Input
                inputMode="numeric"
                value={draft.included_revisions}
                onChange={(e) => set("included_revisions", e.target.value)}
              />
            </Field>
            <Field label={t.common.description} className="sm:col-span-2">
              <Textarea
                rows={2}
                value={draft.description}
                onChange={(e) => set("description", e.target.value)}
              />
            </Field>
          </div>
          <label className="flex items-center gap-2 text-[13px] text-[var(--od-ink-secondary)]">
            <input
              type="checkbox"
              checked={draft.is_package}
              onChange={(e) => set("is_package", e.target.checked)}
            />
            {t.services.thisIsAPackageThat}
          </label>

          {draft.is_package ? (
            <Field label={t.services.servicesInThePackage}>
              <div className="max-h-48 space-y-1.5 overflow-y-auto rounded-[var(--od-radius-sm,8px)] border border-[var(--od-border-strong)] bg-[var(--od-surface-subtle)] p-3">
                {singles.length === 0 && (
                  <p className="od-caption">{t.services.addSingleServicesFirst}</p>
                )}
                {singles.map((s) => (
                  <label key={s.id} className="flex items-center gap-2 text-[13px]">
                    <input
                      type="checkbox"
                      checked={draft.package_services.includes(s.id)}
                      onChange={(e) =>
                        set(
                          "package_services",
                          e.target.checked
                            ? [...draft.package_services, s.id]
                            : draft.package_services.filter((x) => x !== s.id),
                        )
                      }
                    />
                    {s.name}
                  </label>
                ))}
              </div>
            </Field>
          ) : (
            <>
              <Field label={t.services.stagesInOrder}>
                <div className="space-y-2">
                  {draft.stages.map((s, i) => (
                    <div key={i} className="grid grid-cols-[2rem_1fr_6rem_auto] items-center gap-2">
                      <span className="od-tabular text-xs text-[var(--od-ink-muted)]">
                        {i + 1}.
                      </span>
                      <Input
                        aria-label={fmt(t.services.stageName2, { i: i + 1 })}
                        required
                        value={s.name}
                        onChange={(e) =>
                          set(
                            "stages",
                            draft.stages.map((x, j) =>
                              j === i ? { ...x, name: e.target.value } : x,
                            ),
                          )
                        }
                      />
                      <Input
                        aria-label={fmt(t.services.stageDays, { i: i + 1 })}
                        inputMode="numeric"
                        placeholder={t.common.days}
                        value={s.est_days}
                        onChange={(e) =>
                          set(
                            "stages",
                            draft.stages.map((x, j) =>
                              j === i ? { ...x, est_days: e.target.value } : x,
                            ),
                          )
                        }
                      />
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        aria-label={t.services.removeStage}
                        onClick={() =>
                          set(
                            "stages",
                            draft.stages.filter((_, j) => j !== i),
                          )
                        }
                      >
                        <Trash2 size={14} />
                      </Button>
                    </div>
                  ))}
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => set("stages", [...draft.stages, { name: "", est_days: "1" }])}
                  >
                    <Plus size={14} /> {t.services.addStage}
                  </Button>
                </div>
              </Field>
              <Field label={t.services.documentsTheClientMustBring}>
                <div className="space-y-2">
                  {draft.documents.map((d, i) => (
                    <div key={i} className="grid grid-cols-[1fr_8rem_auto] items-center gap-2">
                      <Input
                        aria-label={fmt(t.services.documentName2, { i: i + 1 })}
                        required
                        value={d.name}
                        onChange={(e) =>
                          set(
                            "documents",
                            draft.documents.map((x, j) =>
                              j === i ? { ...x, name: e.target.value } : x,
                            ),
                          )
                        }
                      />
                      <NativeSelect
                        aria-label={fmt(t.services.documentStage, { i: i + 1 })}
                        value={d.stage_position}
                        onChange={(e) =>
                          set(
                            "documents",
                            draft.documents.map((x, j) =>
                              j === i ? { ...x, stage_position: e.target.value } : x,
                            ),
                          )
                        }
                      >
                        <option value="">{t.services.anyStage}</option>
                        {draft.stages.map((s, k) => (
                          <option key={k} value={k + 1}>
                            {fmt(t.services.stage2, { k: k + 1 })}
                          </option>
                        ))}
                      </NativeSelect>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        aria-label={t.services.removeDocument}
                        onClick={() =>
                          set(
                            "documents",
                            draft.documents.filter((_, j) => j !== i),
                          )
                        }
                      >
                        <Trash2 size={14} />
                      </Button>
                    </div>
                  ))}
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() =>
                      set("documents", [...draft.documents, { name: "", stage_position: "" }])
                    }
                  >
                    <Plus size={14} /> {t.services.addDocument}
                  </Button>
                </div>
              </Field>
            </>
          )}
        </FormDialog>
      )}
    </div>
  );
}
