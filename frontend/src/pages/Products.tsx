import { useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Download, Grid2x2, List, Plus, ShoppingCart, Upload } from "lucide-react";
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
import { FileThumbnail } from "@/components/shared/FilePreview";
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
  unit_id: string;
  kind: "stock" | "service";
  sell_price: string;
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
  unit_id: "",
  kind: "stock",
  sell_price: "",
  reorder_level: "0",
  warranty_months: "0",
  track_serial: false,
  photo_file: "",
  description: "",
};

export default function Products() {
  const { can } = useAuth();
  const navigate = useNavigate();
  const manage = can("products.manage");
  const showCost = can("products.view_cost");
  const canCategories = can("categories.manage");
  const canImport = can("products.import");

  const [q, setQ] = useState("");
  const [catFilter, setCatFilter] = useState("");
  const [page, setPage] = useState(1);
  const [low, setLow] = useState(false);
  const [view, setView] = useState<"catalog" | "table">(manage ? "table" : "catalog");
  const search = useDebounce(q, 300);
  const { data, isLoading, refetch } = useGetPage<Product>("/products", {
    q: search,
    category_id: catFilter || undefined,
    page,
    per_page: 25,
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
        ? { ...blank, unit_id: units.data?.[0]?.id ?? "" }
        : {
            name: product.name,
            sku: product.sku,
            barcode: product.barcode ?? "",
            category_id: product.category_id ?? "",
            unit_id: product.unit_id,
            kind: product.kind,
            sell_price: product.sell_price,
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

        <TabsContent value="products">
          <div className="mb-4 flex flex-wrap items-center gap-3">
            <Input
              aria-label="Search products"
              placeholder="Search name, SKU or barcode…"
              className="max-w-xs"
              value={q}
              onChange={(e) => {
                setQ(e.target.value);
                setPage(1);
              }}
            />
            <NativeSelect
              aria-label="Category"
              className="w-44"
              value={catFilter}
              onChange={(e) => {
                setCatFilter(e.target.value);
                setPage(1);
              }}
            >
              <option value="">All categories</option>
              {(categories.data ?? []).map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </NativeSelect>
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={low}
                onChange={(e) => {
                  setLow(e.target.checked);
                  setPage(1);
                }}
              />{" "}
              Low stock only
            </label>
          </div>

          {view === "catalog" ? (
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
              {(data?.data ?? []).map((p) => {
                const qty = toNumber(p.qty_on_hand);
                const isLow = p.kind === "stock" && qty <= toNumber(p.reorder_level);
                return (
                  <div
                    key={p.id}
                    className="flex flex-col justify-between gap-3 rounded-2xl border bg-card p-4"
                  >
                    <div className="flex gap-3">
                      {p.photo_file && (
                        <FileThumbnail fileId={p.photo_file} fileName={p.name} mimeType="image/*" />
                      )}
                      <div className="min-w-0 flex-1">
                        <button
                          type="button"
                          onClick={() => navigate(`/products/${p.id}`)}
                          className="text-left font-semibold hover:text-primary"
                        >
                          {p.name}
                        </button>
                        <p className="text-xs text-muted-foreground">{p.sku}</p>
                        <p className="mt-2 text-lg font-bold tabular-nums">
                          {formatMoney(p.sell_price)}
                        </p>
                        <p
                          className={`text-xs ${
                            qty <= 0 && p.kind === "stock"
                              ? "font-medium text-destructive"
                              : isLow
                                ? "font-medium text-amber-600"
                                : "text-muted-foreground"
                          }`}
                        >
                          {p.kind === "service"
                            ? "Service item"
                            : qty <= 0
                              ? "Out of stock"
                              : `In stock: ${formatQty(p.qty_on_hand)} ${p.unit}`}
                        </p>
                      </div>
                    </div>
                    <div className="flex justify-end gap-2">
                      {can("sales.create") && p.is_active && (
                        <Button
                          size="sm"
                          disabled={p.kind === "stock" && qty <= 0}
                          onClick={() => navigate(`/pos?add=${p.id}`)}
                        >
                          <ShoppingCart size={14} /> Sell
                        </Button>
                      )}
                      {manage && (
                        <Button size="sm" variant="outline" onClick={() => open(p)}>
                          Edit
                        </Button>
                      )}
                    </div>
                  </div>
                );
              })}
            </div>
          ) : (
            <DataGrid
              rows={data?.data}
              loading={isLoading}
              rowKey={(p) => p.id}
              empty="No products found"
              onRowClick={(p) => navigate(`/products/${p.id}`)}
              columns={[
                { header: "Name", cell: (p) => <span className="font-medium">{p.name}</span> },
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
                  cell: (p) =>
                    p.kind === "service" ? "n/a" : `${formatQty(p.qty_on_hand)} ${p.unit}`,
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
                      {can("sales.create") && p.is_active && (
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
        onSubmit={() =>
          save.mutate({
            name: draft.name,
            sku: draft.sku,
            barcode: draft.barcode,
            category_id: draft.category_id || null,
            unit_id: draft.unit_id || null,
            kind: draft.kind,
            sell_price: draft.sell_price,
            reorder_level: draft.reorder_level || "0",
            warranty_months: Number(draft.warranty_months) || 0,
            track_serial: draft.track_serial,
            photo_file: draft.photo_file || null,
            description: draft.description,
          })
        }
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
            <NativeSelect value={draft.unit_id} onChange={set("unit_id")}>
              {units.data?.map((u) => (
                <option key={u.id} value={u.id}>
                  {u.name} ({u.short_name})
                </option>
              ))}
            </NativeSelect>
          </Field>
          <Field label="Selling price *">
            <Input required inputMode="decimal" value={draft.sell_price} onChange={set("sell_price")} />
          </Field>
          <Field label="Minimum stock level">
            <Input inputMode="decimal" value={draft.reorder_level} onChange={set("reorder_level")} />
          </Field>
          <Field label="Warranty (months)">
            <Input
              inputMode="numeric"
              value={draft.warranty_months}
              onChange={set("warranty_months")}
            />
          </Field>
          <Field label="Type">
            <NativeSelect value={draft.kind} onChange={set("kind")}>
              <option value="stock">Stocked product</option>
              <option value="service">Service (no stock)</option>
            </NativeSelect>
          </Field>
        </div>
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
