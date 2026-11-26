import { useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  AlertCircle,
  Download,
  Grid2x2,
  List,
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
  FormDialog,
  NativeSelect,
  PageHeader,
  Pager,
  StatusBadge,
} from "@/components/shared/kit";
import { ProductImage } from "@/components/shared/ProductImage";
import { useAuth } from "@/features/auth/auth";
import { useDebounce } from "@/hooks/useDebounce";
import { errorMessage, useGet, useGetPage, useWrite } from "@/hooks/useApi";
import { download, session, uploadFile } from "@/lib/api";
import { formatMoney, formatQty, toNumber } from "@/lib/formatters";
import type { Category, Product, Unit } from "@/types";

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
      success: "Product saved",
      onSuccess: () => setEditing(null),
    },
  );
  const toggle = useWrite<Product>(
    "POST",
    (p) => `/products/${p.id}/${p.is_active ? "deactivate" : "activate"}`,
    {
      invalidate: ["/products"],
      success: "Product updated",
    },
  );
  const saveCategory = useWrite<{ name: string }>(
    catDialog && catDialog !== "new" ? "PUT" : "POST",
    catDialog && catDialog !== "new" ? `/categories/${catDialog.id}` : "/categories",
    {
      invalidate: ["/categories"],
      success: "Category saved",
      onSuccess: () => {
        setCatDialog(null);
        setCatName("");
      },
    },
  );
  const deleteCategory = useWrite<{ id: string }>("DELETE", (b) => `/categories/${b.id}`, {
    invalidate: ["/categories"],
    success: "Category removed",
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
      if (!res.ok) throw new Error(json.error?.message ?? "Import failed");
      toast.success(
        `Imported: ${json.data?.created ?? 0} created, ${json.data?.updated ?? 0} updated.`,
      );
      void refetch();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setImporting(false);
    }
  }

  return (
    <>
      <PageHeader
        title="Products"
        description={
          manage
            ? "Manage products, prices, warranties, serial tracking and categories."
            : "Product catalog: search, check stock and tap Sell to add to POS."
        }
        actions={
          <div className="flex flex-wrap gap-2">
            <div className="inline-flex rounded-lg border bg-card p-0.5">
              <Button
                size="sm"
                variant={view === "catalog" ? "default" : "ghost"}
                onClick={() => setView("catalog")}
              >
                <Grid2x2 size={14} /> Catalog
              </Button>
              <Button
                size="sm"
                variant={view === "table" ? "default" : "ghost"}
                onClick={() => setView("table")}
              >
                <List size={14} /> Table
              </Button>
            </div>
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
                  <Upload size={14} /> {importing ? "Importing…" : "Import Excel/CSV"}
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
              <Download size={14} /> Excel
            </Button>
            {manage && (
              <Button size="sm" onClick={() => open("new")}>
                <Plus size={16} /> New product
              </Button>
            )}
          </div>
        }
      />

      <Tabs defaultValue="products">
        {canCategories && (
          <TabsList className="mb-4">
            <TabsTrigger value="products">Products</TabsTrigger>
            <TabsTrigger value="categories">Categories</TabsTrigger>
          </TabsList>
        )}

        <TabsContent value="products" className="space-y-4">
          {/* Search + Quick Filters */}
          <div className="flex flex-wrap items-center gap-3">
            <div className="relative min-w-60 flex-1 sm:max-w-md">
              <Search
                size={16}
                className="pointer-events-none absolute top-1/2 left-3.5 -translate-y-1/2 text-muted-foreground"
              />
              <Input
                aria-label="Search products"
                placeholder="Search products by name, SKU or barcode…"
                className="h-10 rounded-xl bg-card pl-10 shadow-xs"
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
              className={`inline-flex h-10 items-center gap-1.5 rounded-xl border px-3.5 text-sm font-medium transition ${
                low
                  ? "border-amber-500/40 bg-amber-500/12 text-amber-700 dark:text-amber-400"
                  : "bg-card text-muted-foreground hover:border-primary/40 hover:text-foreground"
              }`}
            >
              <AlertCircle size={15} />
              Low stock only
            </button>
          </div>

          {/* Horizontal Scrollable Category Pills */}
          {(categories.data ?? []).length > 0 && (
            <div className="no-scrollbar flex items-center gap-2 overflow-x-auto pb-1">
              <button
                type="button"
                onClick={() => {
                  setCatFilter("");
                  setPage(1);
                }}
                className={`shrink-0 rounded-full px-3.5 py-1.5 text-xs font-semibold transition ${
                  catFilter === ""
                    ? "bg-primary text-primary-foreground shadow-xs"
                    : "border bg-card text-muted-foreground hover:border-primary/40 hover:text-foreground"
                }`}
              >
                All items
              </button>
              {(categories.data ?? []).map((c) => (
                <button
                  key={c.id}
                  type="button"
                  onClick={() => {
                    setCatFilter(c.id);
                    setPage(1);
                  }}
                  className={`shrink-0 rounded-full px-3.5 py-1.5 text-xs font-semibold transition ${
                    catFilter === c.id
                      ? "bg-primary text-primary-foreground shadow-xs"
                      : "border bg-card text-muted-foreground hover:border-primary/40 hover:text-foreground"
                  }`}
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
                  <div
                    key={i}
                    className="overflow-hidden rounded-2xl border bg-card shadow-xs animate-pulse"
                  >
                    <div className="aspect-[4/3] bg-muted" />
                    <div className="space-y-2 p-4">
                      <div className="h-4 w-3/4 rounded bg-muted" />
                      <div className="h-3 w-1/3 rounded bg-muted" />
                      <div className="mt-3 h-6 w-1/2 rounded bg-muted" />
                    </div>
                  </div>
                ))}
              </div>
            ) : (data?.data ?? []).length === 0 ? (
              <div className="flex flex-col items-center justify-center rounded-2xl border bg-card py-16 text-center shadow-xs">
                <div className="flex h-14 w-14 items-center justify-center rounded-2xl bg-primary/10 text-primary">
                  <Package size={26} />
                </div>
                <p className="mt-4 text-base font-semibold">No products found</p>
                <p className="mt-1 text-sm text-muted-foreground">
                  {q || catFilter || low
                    ? "Try clearing your search or category filter."
                    : "Add your first product to start selling from the catalog."}
                </p>
                {manage && !q && !catFilter && !low && (
                  <Button className="mt-4" size="sm" onClick={() => open("new")}>
                    <Plus size={15} /> New product
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
                      className="group flex flex-col overflow-hidden rounded-2xl border bg-card shadow-xs transition duration-200 hover:-translate-y-0.5 hover:border-primary/45 hover:shadow-md"
                    >
                      {/* E-Commerce Photo Header */}
                      <button
                        type="button"
                        onClick={() => navigate(`/products/${p.id}`)}
                        className="relative aspect-[4/3] w-full overflow-hidden bg-muted text-left focus:outline-none"
                      >
                        <ProductImage fileId={p.photo_file} name={p.name} />
                        {categoryName && (
                          <span className="absolute top-2.5 left-2.5 max-w-[55%] truncate rounded-full bg-background/90 px-2.5 py-0.5 text-[11px] font-semibold text-foreground shadow-xs backdrop-blur">
                            {categoryName}
                          </span>
                        )}
                        <span
                          className={`absolute top-2.5 right-2.5 rounded-full px-2.5 py-0.5 text-[11px] font-semibold shadow-xs backdrop-blur ${
                            !p.is_active
                              ? "bg-muted/95 text-muted-foreground"
                              : outOfStock
                                ? "bg-destructive text-destructive-foreground"
                                : isLow
                                  ? "bg-amber-500 text-white"
                                  : "bg-primary text-primary-foreground"
                          }`}
                        >
                          {!p.is_active
                            ? "Inactive"
                            : outOfStock
                              ? "Out of stock"
                              : `${formatQty(p.qty_on_hand)} ${p.unit}`}
                        </span>
                      </button>

                      {/* Card Details & Actions */}
                      <div className="flex flex-1 flex-col justify-between p-3.5 sm:p-4">
                        <div>
                          <button
                            type="button"
                            onClick={() => navigate(`/products/${p.id}`)}
                            className="line-clamp-2 text-left text-sm font-semibold leading-snug text-foreground transition group-hover:text-primary sm:text-[15px]"
                          >
                            {p.name}
                          </button>
                          <p className="mt-1 truncate text-xs text-muted-foreground">
                            {p.sku}
                            {p.warranty_months ? ` · ${p.warranty_months}m warranty` : ""}
                          </p>
                        </div>

                        <div className="mt-3 space-y-3 border-t pt-3">
                          <div className="flex items-baseline justify-between gap-2">
                            <p className="text-base font-bold tracking-tight text-foreground tabular-nums sm:text-lg">
                              {formatMoney(p.sell_price)}
                            </p>
                            {showCost && p.avg_cost && (
                              <span className="truncate text-[11px] text-muted-foreground tabular-nums">
                                Cost {formatMoney(p.avg_cost)}
                              </span>
                            )}
                          </div>

                          <div className="flex items-center gap-2">
                            {can("shop.sale.create") && p.is_active ? (
                              <Button
                                size="sm"
                                className="flex-1 rounded-xl"
                                disabled={outOfStock}
                                onClick={() => navigate(`/pos?add=${p.id}`)}
                              >
                                <ShoppingCart size={14} />
                                {outOfStock ? "Out of stock" : "Sell"}
                              </Button>
                            ) : (
                              <Button
                                size="sm"
                                variant="outline"
                                className="flex-1 rounded-xl"
                                onClick={() => navigate(`/products/${p.id}`)}
                              >
                                View
                              </Button>
                            )}
                            {manage && (
                              <Button
                                size="icon"
                                variant="outline"
                                className="h-8 w-8 shrink-0 rounded-xl"
                                aria-label={`Edit ${p.name}`}
                                onClick={() => open(p)}
                              >
                                <Pencil size={14} />
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
              empty="No products found"
              onRowClick={(p) => navigate(`/products/${p.id}`)}
              columns={[
                {
                  header: "Product",
                  cell: (p) => (
                    <div className="flex items-center gap-3">
                      <div className="h-10 w-10 shrink-0 overflow-hidden rounded-xl border bg-muted">
                        <ProductImage fileId={p.photo_file} name={p.name} size="thumb" />
                      </div>
                      <div className="min-w-0">
                        <p className="truncate font-medium">{p.name}</p>
                        {p.category_id && categoryNames.get(p.category_id) && (
                          <p className="text-xs text-muted-foreground">
                            {categoryNames.get(p.category_id)}
                          </p>
                        )}
                      </div>
                    </div>
                  ),
                },
                { header: "SKU", cell: (p) => p.sku },
                { header: "Price", align: "right", cell: (p) => formatMoney(p.sell_price) },
                ...(showCost
                  ? [
                      {
                        header: "Avg cost",
                        align: "right" as const,
                        cell: (p: Product) => (p.avg_cost ? formatMoney(p.avg_cost) : "-"),
                      },
                    ]
                  : []),
                {
                  header: "On hand",
                  align: "right",
                  cell: (p) => `${formatQty(p.qty_on_hand)} ${p.unit}`,
                },
                {
                  header: "Status",
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
                          Sell
                        </Button>
                      )}
                      {manage && (
                        <>
                          <Button variant="ghost" size="sm" onClick={() => open(p)}>
                            Edit
                          </Button>
                          <Button variant="ghost" size="sm" onClick={() => toggle.mutate(p)}>
                            {p.is_active ? "Deactivate" : "Activate"}
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
          <TabsContent value="categories">
            <div className="mb-3 flex justify-end">
              <Button
                size="sm"
                onClick={() => {
                  setCatName("");
                  setCatDialog("new");
                }}
              >
                <Plus size={14} /> New category
              </Button>
            </div>
            <DataGrid
              rows={categories.data}
              loading={categories.isLoading}
              rowKey={(c) => c.id}
              empty="No categories yet"
              columns={[
                { header: "Name", cell: (c) => <span className="font-medium">{c.name}</span> },
                {
                  header: "Status",
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
                        Rename
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        className="text-destructive"
                        onClick={() => deleteCategory.mutate({ id: c.id })}
                      >
                        Delete
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
        title={editing === "new" ? "New product" : "Edit product"}
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
        <Field label="Name *">
          <Input required value={draft.name} onChange={set("name")} />
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label="SKU">
            <Input value={draft.sku} onChange={set("sku")} placeholder="Auto if blank" />
          </Field>
          <Field label="Barcode">
            <Input value={draft.barcode} onChange={set("barcode")} />
          </Field>
          <Field label="Category">
            <NativeSelect value={draft.category_id} onChange={set("category_id")}>
              <option value="">None</option>
              {categories.data?.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </NativeSelect>
          </Field>
          <Field label="Unit">
            <NativeSelect value={draft.unit} onChange={set("unit")}>
              {units.data?.map((u) => (
                <option key={u.id} value={u.id}>
                  {u.name} ({u.short_name})
                </option>
              ))}
            </NativeSelect>
          </Field>
          <Field label="Selling price *">
            <Input
              required
              inputMode="decimal"
              value={draft.sell_price}
              onChange={set("sell_price")}
            />
          </Field>
          {showCost && (
            <Field label="Buying price">
              <Input inputMode="decimal" value={draft.buy_price} onChange={set("buy_price")} />
            </Field>
          )}
          <Field label="Minimum stock level">
            <Input
              inputMode="decimal"
              value={draft.reorder_level}
              onChange={set("reorder_level")}
            />
          </Field>
          <Field label="Warranty (months)">
            <Input
              inputMode="numeric"
              value={draft.warranty_months}
              onChange={set("warranty_months")}
            />
          </Field>
        </div>
        {editing !== "new" && editing && draft.sell_price !== editing.sell_price && (
          <Field label="Reason for price change">
            <Input
              value={draft.reason}
              onChange={set("reason")}
              placeholder="Optional note recorded in price history"
            />
          </Field>
        )}
        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={draft.track_serial}
            onChange={(e) => setDraft((d) => ({ ...d, track_serial: e.target.checked }))}
          />
          Track serial numbers / IMEI at POS
        </label>
        <Field label="Product photo">
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
                toast.success("Photo uploaded");
              } catch (err) {
                toast.error(errorMessage(err));
              } finally {
                setUploadingPhoto(false);
              }
            }}
          />
          <div className="flex items-center gap-2">
            <Button
              type="button"
              size="sm"
              variant="outline"
              disabled={uploadingPhoto}
              onClick={() => photoInput.current?.click()}
            >
              <Upload size={14} /> {uploadingPhoto ? "Uploading…" : "Upload photo"}
            </Button>
            {draft.photo_file && (
              <span className="text-xs text-emerald-700 dark:text-emerald-400">Photo attached</span>
            )}
          </div>
        </Field>
      </FormDialog>

      <FormDialog
        open={catDialog !== null}
        onOpenChange={(o) => !o && setCatDialog(null)}
        title={catDialog === "new" ? "New category" : "Rename category"}
        busy={saveCategory.isPending}
        onSubmit={() => saveCategory.mutate({ name: catName })}
      >
        <Field label="Category name *">
          <Input required value={catName} onChange={(e) => setCatName(e.target.value)} />
        </Field>
      </FormDialog>
    </>
  );
}
