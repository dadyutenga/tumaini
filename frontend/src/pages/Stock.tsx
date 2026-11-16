import { useState } from "react";
import { ArrowDownToLine, ArrowUpFromLine } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { DataGrid, Field, FormDialog, NativeSelect, PageHeader } from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { useGet, useWrite } from "@/hooks/useApi";
import { formatMoney, formatQty, toNumber } from "@/lib/formatters";

interface Level {
  product_id: string;
  name: string;
  sku: string;
  qty_on_hand: string;
  reorder_level: string;
  avg_cost?: string;
  value?: string;
}
interface LowRow extends Level {
  suggest_qty?: string;
}

type Direction = "in" | "out";
const REASONS: Record<Direction, [string, string][]> = {
  in: [
    ["opening", "Opening stock"],
    ["purchase", "Stock received"],
    ["other", "Other"],
  ],
  out: [
    ["damage", "Damaged"],
    ["expired", "Expired"],
    ["loss", "Lost or stolen"],
    ["own_use", "Own use"],
    ["other", "Other"],
  ],
};

export default function Stock() {
  const { can } = useAuth();
  const levels = useGet<Level[]>("/stock/levels");
  const low = useGet<LowRow[]>("/stock/low");
  const [direction, setDirection] = useState<Direction | null>(null);
  const [form, setForm] = useState({
    product_id: "",
    qty: "",
    unit_cost: "",
    reason: "",
    note: "",
  });
  const productList = levels;

  const post = useWrite<unknown>("POST", "/stock-entries", {
    idempotent: true,
    invalidate: ["/stock", "/products"],
    success: "Stock updated",
    onSuccess: () => setDirection(null),
  });

  function openDialog(d: Direction) {
    setDirection(d);
    setForm({ product_id: "", qty: "", unit_cost: "", reason: REASONS[d][0][0], note: "" });
  }
  const set =
    (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
      setForm((f) => ({ ...f, [k]: e.target.value }));

  const showCost = can("products.view_cost");
  const columns = [
    { header: "Product", cell: (r: Level) => <span className="font-medium">{r.name}</span> },
    { header: "SKU", cell: (r: Level) => r.sku },
    { header: "On hand", align: "right" as const, cell: (r: Level) => formatQty(r.qty_on_hand) },
    {
      header: "Reorder at",
      align: "right" as const,
      cell: (r: Level) => formatQty(r.reorder_level),
    },
  ];

  return (
    <>
      <PageHeader
        title="Stock"
        description="Quantities at the active branch."
        actions={
          <>
            {can("stock.in") && (
              <Button onClick={() => openDialog("in")}>
                <ArrowDownToLine size={16} /> Stock in
              </Button>
            )}
            {can("stock.out") && (
              <Button variant="outline" onClick={() => openDialog("out")}>
                <ArrowUpFromLine size={16} /> Stock out
              </Button>
            )}
          </>
        }
      />
      <Tabs defaultValue="levels">
        <TabsList>
          <TabsTrigger value="levels">Levels</TabsTrigger>
          <TabsTrigger value="low">Low stock {low.data ? `(${low.data.length})` : ""}</TabsTrigger>
        </TabsList>
        <TabsContent value="levels">
          <DataGrid
            rows={levels.data}
            loading={levels.isLoading}
            rowKey={(r) => r.product_id}
            columns={[
              ...columns,
              ...(showCost
                ? [
                    {
                      header: "Avg cost",
                      align: "right" as const,
                      cell: (r: Level) => formatMoney(r.avg_cost),
                    },
                    {
                      header: "Value",
                      align: "right" as const,
                      cell: (r: Level) => formatMoney(r.value),
                    },
                  ]
                : []),
            ]}
          />
          {showCost && levels.data && (
            <p className="mt-3 text-right text-sm text-muted-foreground">
              Total value:{" "}
              <strong>
                {formatMoney(levels.data.reduce((sum, r) => sum + toNumber(r.value), 0))}
              </strong>
            </p>
          )}
        </TabsContent>
        <TabsContent value="low">
          <DataGrid
            rows={low.data}
            loading={low.isLoading}
            rowKey={(r) => r.product_id}
            empty="Nothing is running low"
            columns={[
              ...columns,
              { header: "Suggested order", align: "right", cell: (r) => formatQty(r.suggest_qty) },
            ]}
          />
        </TabsContent>
      </Tabs>

      <FormDialog
        open={direction !== null}
        onOpenChange={(o) => !o && setDirection(null)}
        title={direction === "in" ? "Stock in" : "Stock out"}
        busy={post.isPending}
        submitLabel="Post"
        onSubmit={() =>
          post.mutate({
            direction,
            reason: form.reason,
            note: form.note,
            items: [
              {
                product_id: form.product_id,
                qty: form.qty,
                unit_cost: direction === "in" ? form.unit_cost || "0" : "0",
              },
            ],
          })
        }
      >
        <Field label="Product">
          <NativeSelect required value={form.product_id} onChange={set("product_id")}>
            <option value="">Choose…</option>
            {productList.data?.map((p) => (
              <option key={p.product_id} value={p.product_id}>
                {p.name} ({p.sku})
              </option>
            ))}
          </NativeSelect>
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label="Quantity">
            <Input required inputMode="decimal" value={form.qty} onChange={set("qty")} />
          </Field>
          {direction === "in" && (
            <Field label="Unit cost">
              <Input
                required
                inputMode="decimal"
                value={form.unit_cost}
                onChange={set("unit_cost")}
              />
            </Field>
          )}
        </div>
        <Field label="Reason">
          <NativeSelect value={form.reason} onChange={set("reason")}>
            {direction &&
              REASONS[direction].map(([v, l]) => (
                <option key={v} value={v}>
                  {l}
                </option>
              ))}
          </NativeSelect>
        </Field>
        <Field label="Note">
          <Input value={form.note} onChange={set("note")} />
        </Field>
      </FormDialog>
    </>
  );
}
