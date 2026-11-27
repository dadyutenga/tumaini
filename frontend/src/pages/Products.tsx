import { useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  AlertCircle,
  Download,
  Package,
  Pencil,
  Plus,
  Search,
  ShoppingCart,
  Upload,
} from "lucide-react";
import { toast } from "sonner";
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
  Pager,
  SegmentedChips,
  StatusBadge,
} from "@/components/shared/kit";
import { ProductImage } from "@/components/shared/ProductImage";
import { useAuth } from "@/features/auth/auth";
import { useDebounce } from "@/hooks/useDebounce";
import { errorMessage, useGet, useGetPage, useWrite } from "@/hooks/useApi";
import { download, session, uploadFile } from "@/lib/api";
import { formatMoney, formatQty, toNumber } from "@/lib/formatters";
import { cn } from "@/lib/utils";
import type { Category, Product, Unit } from "@/types";
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";

interface Draft {
  name: string;
  sku: string;
  barcode: string;
  category_id: string;
  unit: string;
  sell_price: string;
  buy_price: string;
  reason: string;
  reorder_level: string;
  warranty_months: string;
  track_serial: boolean;
  photo_file: string;
  description: string;
}
const blank: Draft = {
  name: "",
  sku: "",
  barcode: "",
  category_id: "",
  unit: "pcs",
  sell_price: "",
  buy_price: "",
  reason: "",
  reorder_level: "0",
  warranty_months: "0",
  track_serial: false,
  photo_file: "",
  description: "",
};

export default function Products() {
  const { t } = useI18n();
  const { can } = useAuth();
  const navigate = useNavigate();
  const manage = can("shop.product.manage");
  const showCost = can("shop.product.view_cost");
  const canCategories = can("shop.product.manage");
  const canImport = can("shop.product.manage");

  const [q, setQ] = useState("");
  const [catFilter, setCatFilter] = useState("");
  const [page, setPage] = useState(1);
  const [low, setLow] = useState(false);
  const [view, setView] = useState<"catalog" | "table">("catalog");
  const search = useDebounce(q, 300);
  const { data, isLoading, refetch } = useGetPage<Product>("/products", {
    q: search,
    category_id: catFilter || undefined,
    page,
    per_page: 24,
    low_stock: low || undefined,
  });
  const [editing, setEditing] = useState<Product | "new" | null>(null);
  const [draft, setDraft] = useState<Draft>(blank);
  const [uploadingPhoto, setUploadingPhoto] = useState(false);
  const photoInput = useRef<HTMLInputElement>(null);
  const importInput = useRef<HTMLInputElement>(null);
  const [importing, setImporting] = useState(false);

  const [catDialog, setCatDialog] = useState<Category | "new" | null>(null);
  const [catName, setCatName] = useState("");

  const categories = useGet<Category[]>("/categories");
  const units = useGet<Unit[]>("/units");
  const categoryNames = useMemo(
    () => new Map((categories.data ?? []).map((c) => [c.id, c.name])),
    [categories.data],
  );

  const save = useWrite<Record<string, unknown>, Product>(
    editing && editing !== "new" ? "PUT" : "POST",
    editing && editing !== "new" ? `/products/${editing.id}` : "/products",
    {
      invalidate: ["/products", "/stock"],
      success: t.products.productSaved,
      onSuccess: () => setEditing(null),
    },
  );
  const toggle = useWrite<Product>(
    "POST",
    (p) => `/products/${p.id}/${p.is_active ? "deactivate" : "activate"}`,
    {
      invalidate: ["/products"],
      success: t.products.productUpdated,
    },
  );
  const saveCategory = useWrite<{ name: string }>(
    catDialog && catDialog !== "new" ? "PUT" : "POST",
    catDialog && catDialog !== "new" ? `/categories/${catDialog.id}` : "/categories",
    {
      invalidate: ["/categories"],
      success: t.products.categorySaved,
      onSuccess: () => {
        setCatDialog(null);
        setCatName("");
      },
    },
  );
  const deleteCategory = useWrite<{ id: string }>("DELETE", (b) => `/categories/${b.id}`, {
    invalidate: ["/categories"],
    success: t.products.categoryRemoved,
  });

  function open(product: Product | "new") {
    setEditing(product);
    setDraft(
      product === "new"
        ? { ...blank, unit: units.data?.[0]?.id ?? "pcs" }
        : {
            name: product.name,
            sku: product.sku,
            barcode: product.barcode ?? "",
            category_id: product.category_id ?? "",
            unit: product.unit || "pcs",
            sell_price: product.sell_price,
            buy_price: product.buy_price ?? "",
            reason: "",
            reorder_level: product.reorder_level,
            warranty_months: String(product.warranty_months ?? 0),
            track_serial: Boolean(product.track_serial),
            photo_file: product.photo_file ?? "",
            description: product.description ?? "",
          },
    );
  }
  const set = (k: keyof Draft) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    setDraft((d) => ({ ...d, [k]: e.target.value }));

  async function importFile(file: File) {
    setImporting(true);
    try {
      const form = new FormData();
      form.append("file", file);
      const headers: Record<string, string> = {};
      const token = session.getToken();
      if (token) headers.Authorization = `Bearer ${token}`;
      const res = await fetch("/api/v1/products/import?commit=true", {
        method: "POST",
        headers,
        credentials: "include",
        body: form,
      });
      const json = (await res.json()) as {
        data?: { created?: number; updated?: number; errors?: unknown[] };
        error?: { message?: string };
      };
      if (!res.ok) throw new Error(json.error?.message ?? t.products.importFailed);
      toast.success(
        fmt(t.products.importedCreatedUpdated, {
          created: json.data?.created ?? 0,
          updated: json.data?.updated ?? 0,
        }),
      );
      void refetch();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setImporting(false);
    }
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title={t.common.products}
        description={
          manage
            ? t.products.manageProductsPricesWarrantiesSerial
            : t.products.productCatalogSearchCheckStock
        }
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <SegmentedChips
              ariaLabel={t.common.viewMode}
              value={view}
              onChange={setView}
              options={[
                { value: "catalog", label: t.common.catalog },
                { value: "table", label: t.common.table },
              ]}
            />
            {canImport && (
              <>
                <input
                  ref={importInput}
                  type="file"
                  accept=".csv,.xlsx"
                  className="hidden"
                  onChange={(e) => {
                    const f = e.target.files?.[0];
                    e.target.value = "";
                    if (f) void importFile(f);
                  }}
                />
                <Button
                  size="sm"
                  variant="outline"
                  disabled={importing}
                  onClick={() => importInput.current?.click()}
                >
                  <Upload size={14} />{" "}
                  {importing ? t.products.importing : t.products.importExcelCsv}
                </Button>
              </>
            )}
            <Button
              size="sm"
              variant="outline"
              onClick={() =>
                download("/products/export", { format: "xlsx", q: search }, "products.xlsx").catch(
                  (e) => toast.error(errorMessage(e)),
                )
              }
            >
              <Download size={14} /> {t.products.excel}
            </Button>
            {manage && (
              <Button size="sm" onClick={() => open("new")}>
                <Plus size={14} /> {t.products.newProduct}
              </Button>
            )}
          </div>
        }
      />

      <Tabs defaultValue="products">
        {canCategories && (
          <TabsList>
            <TabsTrigger value="products">{t.common.products}</TabsTrigger>
            <TabsTrigger value="categories">{t.common.categories}</TabsTrigger>
          </TabsList>
        )}

        <TabsContent value="products" className="space-y-4">
          <FilterBar>
            <div className="relative min-w-60 flex-1 sm:max-w-md">
              <Search
                size={15}
                className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-[var(--od-ink-muted)]"
              />
              <Input
                aria-label={t.products.searchProducts}
                placeholder={t.products.searchProductsByNameSku}
                className="pl-9"
                value={q}
                onChange={(e) => {
                  setQ(e.target.value);
                  setPage(1);
                }}
              />
            </div>
            <button
              type="button"
              onClick={() => {
                setLow((v) => !v);
                setPage(1);
              }}
              className={cn(
                "inline-flex h-9 cursor-pointer items-center gap-1.5 rounded-[var(--od-app-radius,10px)] border px-3 text-[13px] font-medium transition-colors",
                low
                  ? "border-transparent bg-[var(--od-warning-soft)] text-[var(--od-warning)]"
                  : "border-[var(--od-border-strong)] bg-[var(--od-surface)] text-[var(--od-ink-secondary)] hover:text-[var(--od-ink)]",
              )}
            >
              <AlertCircle size={14} />
              {t.products.lowStockOnly}
            </button>
          </FilterBar>

          {(categories.data ?? []).length > 0 && (
            <div className="no-scrollbar flex items-center gap-1.5 overflow-x-auto pb-0.5">
              <button
                type="button"
                onClick={() => {
                  setCatFilter("");
                  setPage(1);
                }}
                className={cn(
                  "shrink-0 cursor-pointer rounded-full border px-3 py-1 text-xs font-medium transition-colors",
                  catFilter === ""
                    ? "border-transparent bg-[var(--od-app-primary)] text-[var(--od-app-primary-foreground)]"
                    : "border-[var(--od-border-strong)] bg-[var(--od-surface)] text-[var(--od-ink-secondary)] hover:text-[var(--od-ink)]",
                )}
              >
                {t.common.allItems}
              </button>
              {(categories.data ?? []).map((c) => (
                <button
                  key={c.id}
                  type="button"
                  onClick={() => {
                    setCatFilter(c.id);
                    setPage(1);
                  }}
                  className={cn(
                    "shrink-0 cursor-pointer rounded-full border px-3 py-1 text-xs font-medium transition-colors",
                    catFilter === c.id
                      ? "border-transparent bg-[var(--od-app-primary)] text-[var(--od-app-primary-foreground)]"
                      : "border-[var(--od-border-strong)] bg-[var(--od-surface)] text-[var(--od-ink-secondary)] hover:text-[var(--od-ink)]",
                  )}
                >
                  {c.name}
                </button>
              ))}
            </div>
          )}

          {view === "catalog" ? (
            isLoading ? (
              <div className="grid grid-cols-2 gap-4 md:grid-cols-3 xl:grid-cols-4">
                {Array.from({ length: 8 }).map((_, i) => (
                  <div key={i} className="od-card-flush animate-pulse overflow-hidden">
                    <div className="aspect-[4/3] bg-[var(--od-surface-subtle)]" />
                    <div className="space-y-2 p-4">
                      <div className="h-4 w-3/4 rounded bg-[var(--od-surface-subtle)]" />
                      <div className="h-3 w-1/3 rounded bg-[var(--od-surface-subtle)]" />
                      <div className="mt-3 h-5 w-1/2 rounded bg-[var(--od-surface-subtle)]" />
                    </div>
                  </div>
                ))}
              </div>
            ) : (data?.data ?? []).length === 0 ? (
              <div className="od-card items-center py-14 text-center">
                <div className="grid h-11 w-11 place-items-center rounded-full bg-[var(--od-surface-subtle)] text-[var(--od-ink-muted)]">
                  <Package size={20} />
                </div>
                <p className="mt-3 text-[14px] font-semibold">{t.products.noProductsFound}</p>
                <p className="od-caption mt-1">
                  {q || catFilter || low
                    ? t.products.tryClearingYourSearchOr
                    : t.products.addYourFirstProductTo}
                </p>
                {manage && !q && !catFilter && !low && (
                  <Button className="mt-4" size="sm" onClick={() => open("new")}>
                    <Plus size={14} /> {t.products.newProduct}
                  </Button>
                )}
              </div>
            ) : (
              <div className="grid grid-cols-2 gap-4 md:grid-cols-3 xl:grid-cols-4">
                {(data?.data ?? []).map((p) => {
                  const qty = toNumber(p.qty_on_hand);
                  const outOfStock = qty <= 0;
                  const isLow = !outOfStock && qty <= toNumber(p.reorder_level);
                  const categoryName = p.category_id ? categoryNames.get(p.category_id) : undefined;
                  return (
                    <article
                      key={p.id}
                      className="od-card-flush group flex flex-col overflow-hidden transition-colors hover:border-[var(--od-ink-muted)]"
                    >
                      <button
                        type="button"
                        onClick={() => navigate(`/products/${p.id}`)}
                        className="relative aspect-[4/3] w-full cursor-pointer overflow-hidden bg-[var(--od-surface-subtle)] text-left focus:outline-none"
                      >
                        <ProductImage fileId={p.photo_file} name={p.name} />
                        {categoryName && (
                          <span className="absolute top-2.5 left-2.5 max-w-[55%] truncate rounded-full bg-[var(--od-surface)]/90 px-2.5 py-0.5 text-[11px] font-medium text-[var(--od-ink)] ring-1 ring-[var(--od-border-strong)] backdrop-blur">
                            {categoryName}
                          </span>
                        )}
                        <span
                          className={cn(
                            "od-tabular absolute top-2.5 right-2.5 rounded-full px-2.5 py-0.5 text-[11px] font-semibold backdrop-blur",
                            !p.is_active
                              ? "bg-[var(--od-surface)]/90 text-[var(--od-ink-muted)]"
                              : outOfStock
                                ? "bg-[var(--od-bad-pill)] text-[var(--od-badge-on-solid)]"
                                : isLow
                                  ? "bg-[var(--od-warning-mark)] text-[var(--od-badge-on-warning)]"
                                  : "bg-[var(--od-good-pill)] text-[var(--od-badge-on-solid)]",
                          )}
                        >
                          {!p.is_active
                            ? t.products.inactive
                            : outOfStock
                              ? t.common.outOfStock
                              : `${formatQty(p.qty_on_hand)} ${p.unit}`}
                        </span>
                      </button>

                      <div className="flex flex-1 flex-col justify-between p-4">
                        <div>
                          <button
                            type="button"
                            onClick={() => navigate(`/products/${p.id}`)}
                            className="line-clamp-2 cursor-pointer text-left text-[14px] font-semibold leading-snug text-[var(--od-ink)] group-hover:underline"
                          >
                            {p.name}
                          </button>
                          <p className="od-mono mt-1 truncate text-[11px] text-[var(--od-ink-muted)]">
                            {p.sku}
                            {p.warranty_months
                              ? ` ${fmt(t.common.mWarranty, { warranty_months: p.warranty_months })}`
                              : ""}
                          </p>
                        </div>

                        <div className="mt-3 space-y-3 border-t border-[var(--od-border)] pt-3">
                          <div className="flex items-baseline justify-between gap-2">
                            <p className="od-tabular text-[16px] font-semibold tracking-tight text-[var(--od-ink)]">
                              {formatMoney(p.sell_price)}
                            </p>
                            {showCost && p.avg_cost && (
                              <span className="od-tabular truncate text-[11px] text-[var(--od-ink-muted)]">
                                {fmt(t.products.cost, { avg_cost: formatMoney(p.avg_cost) })}
                              </span>
                            )}
                          </div>

                          <div className="flex items-center gap-2">
                            {can("shop.sale.create") && p.is_active ? (
                              <Button
                                size="sm"
                                className="flex-1"
                                disabled={outOfStock}
                                onClick={() => navigate(`/pos?add=${p.id}`)}
                              >
                                <ShoppingCart size={13} />
                                {outOfStock ? t.common.outOfStock : t.products.sell}
                              </Button>
                            ) : (
                              <Button
                                size="sm"
                                variant="outline"
                                className="flex-1"
                                onClick={() => navigate(`/products/${p.id}`)}
                              >
                                {t.common.view}
                              </Button>
                            )}
                            {manage && (
                              <Button
                                size="icon"
                                variant="outline"
                                className="h-8 w-8 shrink-0"
                                aria-label={fmt(t.common.edit2, { name: p.name })}
                                onClick={() => open(p)}
                              >
                                <Pencil size={13} />
                              </Button>
                            )}
                          </div>
                        </div>
                      </div>
                    </article>
                  );
                })}
              </div>
            )
          ) : (
            <DataGrid
              rows={data?.data}
              loading={isLoading}
              rowKey={(p) => p.id}
              empty={t.products.noProductsFound}
              onRowClick={(p) => navigate(`/products/${p.id}`)}
              columns={[
                {
                  header: t.common.product,
                  sortValue: (p) => p.name,
                  cell: (p) => (
                    <div className="flex items-center gap-3">
                      <div className="h-9 w-9 shrink-0 overflow-hidden rounded-[var(--od-radius-sm,8px)] border border-[var(--od-border)] bg-[var(--od-surface-subtle)]">
                        <ProductImage fileId={p.photo_file} name={p.name} size="thumb" />
                      </div>
                      <div className="min-w-0">
                        <p className="truncate font-medium">{p.name}</p>
                        {p.category_id && categoryNames.get(p.category_id) && (
                          <p className="od-caption">{categoryNames.get(p.category_id)}</p>
                        )}
                      </div>
                    </div>
                  ),
                },
                {
                  header: "SKU",
                  cell: (p) => (
                    <span className="od-mono text-xs text-[var(--od-ink-secondary)]">{p.sku}</span>
                  ),
                },
                {
                  header: t.common.price,
                  align: "right",
                  sortValue: (p) => toNumber(p.sell_price),
                  cell: (p) => <span className="font-medium">{formatMoney(p.sell_price)}</span>,
                },
                ...(showCost
                  ? [
                      {
                        header: t.common.avgCost,
                        align: "right" as const,
                        cell: (p: Product) => (p.avg_cost ? formatMoney(p.avg_cost) : "—"),
                      },
                    ]
                  : []),
                {
                  header: t.common.onHand,
                  align: "right",
                  sortValue: (p) => toNumber(p.qty_on_hand),
                  cell: (p) => `${formatQty(p.qty_on_hand)} ${p.unit}`,
                },
                {
                  header: t.common.status,
                  cell: (p) => <StatusBadge value={p.is_active ? "active" : "inactive"} />,
                },
                {
                  header: "",
                  className: "text-right",
                  cell: (p: Product) => (
                    <div className="flex justify-end gap-1" onClick={(e) => e.stopPropagation()}>
                      {can("shop.sale.create") && p.is_active && (
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => navigate(`/pos?add=${p.id}`)}
                        >
                          {t.products.sell}
                        </Button>
                      )}
                      {manage && (
                        <>
                          <Button variant="ghost" size="sm" onClick={() => open(p)}>
                            {t.common.edit}
                          </Button>
                          <Button variant="ghost" size="sm" onClick={() => toggle.mutate(p)}>
                            {p.is_active ? t.common.deactivate : t.common.activate}
                          </Button>
                        </>
                      )}
                    </div>
                  ),
                },
              ]}
            />
          )}
          <Pager meta={data?.meta} onPage={setPage} />
        </TabsContent>

        {canCategories && (
          <TabsContent value="categories" className="space-y-4">
            <div className="flex justify-end">
              <Button
                size="sm"
                onClick={() => {
                  setCatName("");
                  setCatDialog("new");
                }}
              >
                <Plus size={14} /> {t.common.newCategory}
              </Button>
            </div>
            <DataGrid
              rows={categories.data}
              loading={categories.isLoading}
              rowKey={(c) => c.id}
              empty={t.products.noCategoriesYet}
              columns={[
                {
                  header: t.common.name2,
                  cell: (c) => <span className="font-medium">{c.name}</span>,
                },
                {
                  header: t.common.status,
                  cell: (c) => <StatusBadge value={c.is_active ? "active" : "inactive"} />,
                },
                {
                  header: "",
                  className: "text-right",
                  cell: (c) => (
                    <div className="flex justify-end gap-2">
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() => {
                          setCatName(c.name);
                          setCatDialog(c);
                        }}
                      >
                        {t.products.rename}
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        className="text-[var(--od-bad)] hover:text-[var(--od-bad)]"
                        onClick={() => deleteCategory.mutate({ id: c.id })}
                      >
                        {t.common.delete}
                      </Button>
                    </div>
                  ),
                },
              ]}
            />
          </TabsContent>
        )}
      </Tabs>

      {/* Product Form */}
      <FormDialog
        open={editing !== null}
        onOpenChange={(o) => !o && setEditing(null)}
        title={editing === "new" ? t.products.newProduct : t.products.editProduct}
        busy={save.isPending || uploadingPhoto}
        onSubmit={() => {
          const payload: Record<string, unknown> = {
            name: draft.name,
            sku: draft.sku,
            barcode: draft.barcode,
            category_id: draft.category_id || null,
            unit: draft.unit || "pcs",
            sell_price: draft.sell_price,
            reorder_level: draft.reorder_level || "0",
            warranty_months: Number(draft.warranty_months) || 0,
            track_serial: draft.track_serial,
            photo_file: draft.photo_file || null,
            description: draft.description,
          };
          if (showCost && draft.buy_price !== "") payload.buy_price = draft.buy_price;
          if (draft.reason.trim()) payload.reason = draft.reason.trim();
          save.mutate(payload);
        }}
      >
        <Field label={t.common.name}>
          <Input required value={draft.name} onChange={set("name")} />
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label="SKU">
            <Input value={draft.sku} onChange={set("sku")} placeholder={t.products.autoIfBlank} />
          </Field>
          <Field label={t.products.barcode}>
            <Input value={draft.barcode} onChange={set("barcode")} />
          </Field>
          <Field label={t.common.category}>
            <NativeSelect value={draft.category_id} onChange={set("category_id")}>
              <option value="">{t.common.none}</option>
              {categories.data?.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </NativeSelect>
          </Field>
          <Field label={t.products.unit}>
            <NativeSelect value={draft.unit} onChange={set("unit")}>
              {units.data?.map((u) => (
                <option key={u.id} value={u.id}>
                  {u.name} ({u.short_name})
                </option>
              ))}
            </NativeSelect>
          </Field>
          <Field label={t.products.sellingPrice}>
            <Input
              required
              inputMode="decimal"
              value={draft.sell_price}
              onChange={set("sell_price")}
            />
          </Field>
          {showCost && (
            <Field label={t.products.buyingPrice}>
              <Input inputMode="decimal" value={draft.buy_price} onChange={set("buy_price")} />
            </Field>
          )}
          <Field label={t.products.minimumStockLevel}>
            <Input
              inputMode="decimal"
              value={draft.reorder_level}
              onChange={set("reorder_level")}
            />
          </Field>
          <Field label={t.products.warrantyMonths}>
            <Input
              inputMode="numeric"
              value={draft.warranty_months}
              onChange={set("warranty_months")}
            />
          </Field>
        </div>
        {editing !== "new" && editing && draft.sell_price !== editing.sell_price && (
          <Field label={t.products.reasonForPriceChange}>
            <Input
              value={draft.reason}
              onChange={set("reason")}
              placeholder={t.products.optionalNoteRecordedInPrice}
            />
          </Field>
        )}
        <label className="flex items-center gap-2 text-[13px] text-[var(--od-ink-secondary)]">
          <input
            type="checkbox"
            checked={draft.track_serial}
            onChange={(e) => setDraft((d) => ({ ...d, track_serial: e.target.checked }))}
          />
          {t.products.trackSerialNumbersImeiAt}
        </label>
        <Field label={t.products.productPhoto}>
          <input
            ref={photoInput}
            type="file"
            accept="image/*"
            className="hidden"
            onChange={async (e) => {
              const f = e.target.files?.[0];
              e.target.value = "";
              if (!f) return;
              setUploadingPhoto(true);
              try {
                const fid = await uploadFile(f);
                setDraft((d) => ({ ...d, photo_file: fid }));
                toast.success(t.products.photoUploaded);
              } catch (err) {
                toast.error(errorMessage(err));
              } finally {
                setUploadingPhoto(false);
              }
            }}
          />
          <div className="flex items-center gap-2.5">
            <Button
              type="button"
              size="sm"
              variant="outline"
              disabled={uploadingPhoto}
              onClick={() => photoInput.current?.click()}
            >
              <Upload size={14} /> {uploadingPhoto ? t.common.uploading : t.products.uploadPhoto}
            </Button>
            {draft.photo_file && (
              <span className="text-xs font-medium text-[var(--od-good)]">
                {t.products.photoAttached}
              </span>
            )}
          </div>
        </Field>
      </FormDialog>

      <FormDialog
        open={catDialog !== null}
        onOpenChange={(o) => !o && setCatDialog(null)}
        title={catDialog === "new" ? t.common.newCategory : t.products.renameCategory}
        busy={saveCategory.isPending}
        onSubmit={() => saveCategory.mutate({ name: catName })}
      >
        <Field label={t.products.categoryName}>
          <Input required value={catName} onChange={(e) => setCatName(e.target.value)} />
        </Field>
      </FormDialog>
    </div>
  );
}
