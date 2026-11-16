import { useState } from "react";
import { Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  DataGrid,
  Field,
  FormDialog,
  NativeSelect,
  PageHeader,
  Pager,
  StatusBadge,
} from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { useDebounce } from "@/hooks/useDebounce";
import { useGet, useGetPage, useWrite } from "@/hooks/useApi";
import { formatMoney, formatQty } from "@/lib/formatters";
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
  description: "",
};

export default function Products() {
  const { can } = useAuth();
  const [q, setQ] = useState("");
  const [page, setPage] = useState(1);
  const [low, setLow] = useState(false);
  const search = useDebounce(q, 300);
  const { data, isLoading } = useGetPage<Product>("/products", {
    q: search,
    page,
    per_page: 25,
    low_stock: low || undefined,
  });
  const [editing, setEditing] = useState<Product | "new" | null>(null);
  const [draft, setDraft] = useState<Draft>(blank);
  const categories = useGet<Category[]>("/categories");
  const units = useGet<Unit[]>("/units");
  const manage = can("products.manage");
  const showCost = can("products.view_cost");

  const save = useWrite<Draft, Product>(
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
            description: product.description ?? "",
          },
    );
  }
  const set = (k: keyof Draft) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    setDraft((d) => ({ ...d, [k]: e.target.value }));

  return (
    <>
      <PageHeader
        title="Products"
        description="What you sell, its price and its stock level at the active branch."
        actions={
          manage && (
            <Button onClick={() => open("new")}>
              <Plus size={16} /> New product
            </Button>
          )
        }
      />
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
      <DataGrid
        rows={data?.data}
        loading={isLoading}
        rowKey={(p) => p.id}
        empty="No products found"
        onRowClick={manage ? open : undefined}
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
            cell: (p) => (p.kind === "service" ? "n/a" : `${formatQty(p.qty_on_hand)} ${p.unit}`),
          },
          {
            header: "Status",
            cell: (p) => <StatusBadge value={p.is_active ? "active" : "inactive"} />,
          },
          ...(manage
            ? [
                {
                  header: "",
                  cell: (p: Product) => (
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={(e) => {
                        e.stopPropagation();
                        toggle.mutate(p);
                      }}
                    >
                      {p.is_active ? "Deactivate" : "Activate"}
                    </Button>
                  ),
                },
              ]
            : []),
        ]}
      />
      <Pager meta={data?.meta} onPage={setPage} />
      <FormDialog
        open={editing !== null}
        onOpenChange={(o) => !o && setEditing(null)}
        title={editing === "new" ? "New product" : "Edit product"}
        busy={save.isPending}
        onSubmit={() => save.mutate(draft)}
        wide
      >
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Name" className="sm:col-span-2">
            <Input required value={draft.name} onChange={set("name")} />
          </Field>
          <Field label="SKU">
            <Input value={draft.sku} onChange={set("sku")} placeholder="Generated if empty" />
          </Field>
          <Field label="Barcode">
            <Input value={draft.barcode} onChange={set("barcode")} />
          </Field>
          <Field label="Selling price">
            <Input
              required
              inputMode="decimal"
              value={draft.sell_price}
              onChange={set("sell_price")}
            />
          </Field>
          <Field label="Reorder level">
            <Input
              inputMode="decimal"
              value={draft.reorder_level}
              onChange={set("reorder_level")}
            />
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
            <NativeSelect required value={draft.unit_id} onChange={set("unit_id")}>
              <option value="">Choose…</option>
              {units.data?.map((u) => (
                <option key={u.id} value={u.id}>
                  {u.name}
                </option>
              ))}
            </NativeSelect>
          </Field>
          <Field label="Type">
            <NativeSelect value={draft.kind} onChange={set("kind")}>
              <option value="stock">Stocked item</option>
              <option value="service">Service</option>
            </NativeSelect>
          </Field>
          <Field label="Description" className="sm:col-span-2">
            <Input value={draft.description} onChange={set("description")} />
          </Field>
        </div>
      </FormDialog>
    </>
  );
}
