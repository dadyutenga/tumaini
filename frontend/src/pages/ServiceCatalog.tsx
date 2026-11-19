import { useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  DataGrid,
  Field,
  FormDialog,
  NativeSelect,
  PageHeader,
  StatusBadge,
} from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { label } from "@/features/services/helpers";
import { useGet, useWrite } from "@/hooks/useApi";
import { api } from "@/lib/api";
import { formatMoney } from "@/lib/formatters";
import type { CatalogDetail, CatalogItem } from "@/types/services";

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
  const { can } = useAuth();
  const manage = can("service_catalog.manage");
  const [category, setCategory] = useState("");
  const [q, setQ] = useState("");
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
    success: "Service added",
    toBody: toBody,
    onSuccess: () => setDraft(null),
  });
  const edit = useWrite<Draft>("PUT", (d) => `/service-catalog/${d.id}`, {
    invalidate: ["/service-catalog"],
    success: "Service saved",
    toBody: toBody,
    onSuccess: () => setDraft(null),
  });
  const toggle = useWrite<{ id: string; active: boolean }>(
    "POST",
    (b) => `/service-catalog/${b.id}/${b.active ? "activate" : "deactivate"}`,
    { invalidate: ["/service-catalog"], success: "Service updated", toBody: () => undefined },
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
    <>
      <PageHeader
        title="Service catalog"
        description="What you sell, its standard price and the stages every job goes through."
        actions={
          manage && (
            <Button onClick={() => setDraft(blank)}>
              <Plus size={16} /> New service
            </Button>
          )
        }
      />
      <div className="mb-4 flex flex-wrap gap-3">
        <Input
          aria-label="Search services"
          className="max-w-xs"
          placeholder="Search services…"
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
        <NativeSelect
          aria-label="Category"
          className="w-44"
          value={category}
          onChange={(e) => setCategory(e.target.value)}
        >
          <option value="">All categories</option>
          {CATEGORIES.map((c) => (
            <option key={c} value={c}>
              {label(c)}
            </option>
          ))}
        </NativeSelect>
      </div>
      <DataGrid
        rows={list.data}
        loading={list.isLoading || loading}
        rowKey={(s) => s.id}
        empty="No services yet. Add what you sell so projects can start from it."
        onRowClick={manage ? (s) => void open(s) : undefined}
        columns={[
          {
            header: "Service",
            cell: (s) => (
              <div>
                <p className="font-medium">{s.name}</p>
                {s.is_package && (
                  <p className="text-xs text-muted-foreground">
                    Package of {s.service_count} services
                  </p>
                )}
              </div>
            ),
          },
          { header: "Category", cell: (s) => label(s.category) },
          { header: "Stages", align: "right", cell: (s) => s.stage_count },
          { header: "Price", align: "right", cell: (s) => formatMoney(s.standard_price) },
          { header: "Govt fees", align: "right", cell: (s) => formatMoney(s.government_fees) },
          {
            header: "Status",
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
                  {s.is_active ? "Hide" : "Show"}
                </Button>
              ) : null,
          },
        ]}
      />
      {draft && (
        <FormDialog
          open
          onOpenChange={(o) => !o && setDraft(null)}
          title={draft.id ? "Edit service" : "New service"}
          description="Projects already started keep their own copy of the stages."
          busy={create.isPending || edit.isPending}
          onSubmit={() => (draft.id ? edit.mutate(draft) : create.mutate(draft))}
          wide
        >
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="Name *" className="sm:col-span-2">
              <Input required value={draft.name} onChange={(e) => set("name", e.target.value)} />
            </Field>
            <Field label="Category">
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
            <Field label="Standard price">
              <Input
                required
                inputMode="decimal"
                value={draft.standard_price}
                onChange={(e) => set("standard_price", e.target.value)}
              />
            </Field>
            <Field label="Government fees">
              <Input
                inputMode="decimal"
                value={draft.government_fees}
                onChange={(e) => set("government_fees", e.target.value)}
              />
            </Field>
            <Field label="Revisions included (design)">
              <Input
                inputMode="numeric"
                value={draft.included_revisions}
                onChange={(e) => set("included_revisions", e.target.value)}
              />
            </Field>
            <Field label="Description" className="sm:col-span-2">
              <Textarea
                rows={2}
                value={draft.description}
                onChange={(e) => set("description", e.target.value)}
              />
            </Field>
          </div>
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={draft.is_package}
              onChange={(e) => set("is_package", e.target.checked)}
            />
            This is a package that bundles other services
          </label>

          {draft.is_package ? (
            <Field label="Services in the package">
              <div className="max-h-48 space-y-1 overflow-y-auto rounded-md border p-2">
                {singles.length === 0 && (
                  <p className="text-sm text-muted-foreground">Add single services first.</p>
                )}
                {singles.map((s) => (
                  <label key={s.id} className="flex items-center gap-2 text-sm">
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
              <Field label="Stages, in order">
                <div className="space-y-2">
                  {draft.stages.map((s, i) => (
                    <div key={i} className="grid grid-cols-[2rem_1fr_6rem_auto] items-center gap-2">
                      <span className="text-sm text-muted-foreground">{i + 1}.</span>
                      <Input
                        aria-label={`Stage ${i + 1} name`}
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
                        aria-label={`Stage ${i + 1} days`}
                        inputMode="numeric"
                        placeholder="Days"
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
                        aria-label="Remove stage"
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
                    <Plus size={14} /> Add stage
                  </Button>
                </div>
              </Field>
              <Field label="Documents the client must bring">
                <div className="space-y-2">
                  {draft.documents.map((d, i) => (
                    <div key={i} className="grid grid-cols-[1fr_8rem_auto] items-center gap-2">
                      <Input
                        aria-label={`Document ${i + 1} name`}
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
                        aria-label={`Document ${i + 1} stage`}
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
                        <option value="">Any stage</option>
                        {draft.stages.map((s, k) => (
                          <option key={k} value={k + 1}>
                            Stage {k + 1}
                          </option>
                        ))}
                      </NativeSelect>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        aria-label="Remove document"
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
                    <Plus size={14} /> Add document
                  </Button>
                </div>
              </Field>
            </>
          )}
        </FormDialog>
      )}
    </>
  );
}
