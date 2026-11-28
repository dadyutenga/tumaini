import { useMemo, useState } from "react";
import { ArrowDownToLine, ArrowUpFromLine, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { StatCard } from "@/components/shared/Display";
import {
  DataGrid,
  Field,
  FilterBar,
  FormDialog,
  NativeSelect,
  PageHeader,
} from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { useGet, useWrite } from "@/hooks/useApi";
import { formatMoney, formatQty, toNumber } from "@/lib/formatters";
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";
import type { Translations } from "@/features/i18n/i18n";
import { useSearchBox } from "@/hooks/useSearchBox";

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
  suggested_qty?: string;
}

type Direction = "in" | "out";
const reasons = (t: Translations): Record<Direction, [string, string][]> => ({
  in: [
    ["opening", t.stock.openingStock],
    ["purchase", t.stock.stockReceived],
    ["other", t.common.other],
  ],
  out: [
    ["damage", t.common.damaged],
    ["expired", t.stock.expired],
    ["loss", t.stock.lostOrStolen],
    ["own_use", t.stock.ownUse],
    ["other", t.common.other],
  ],
});

export default function Stock() {
  const { t } = useI18n();
  const { can } = useAuth();
  const levels = useGet<Level[]>("/stock/levels");
  const low = useGet<LowRow[]>("/stock/low");
  const [q, setQ] = useSearchBox();
  const [direction, setDirection] = useState<Direction | null>(null);
  const [form, setForm] = useState({
    product_id: "",
    qty: "",
    unit_cost: "",
    reason: "",
    note: "",
  });

  const post = useWrite<unknown>("POST", "/stock-entries", {
    idempotent: true,
    invalidate: ["/stock", "/products"],
    success: t.stock.stockUpdated,
    onSuccess: () => setDirection(null),
  });

  function openDialog(d: Direction) {
    setDirection(d);
    setForm({ product_id: "", qty: "", unit_cost: "", reason: reasons(t)[d][0][0], note: "" });
  }
  const set =
    (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
      setForm((f) => ({ ...f, [k]: e.target.value }));

  const showCost = can("shop.product.view_cost");
  const canManage = can("shop.stock.manage");

  const filteredLevels = useMemo(() => {
    const term = q.trim().toLowerCase();
    const rows = levels.data ?? [];
    if (!term) return rows;
    return rows.filter(
      (r) => r.name.toLowerCase().includes(term) || r.sku.toLowerCase().includes(term),
    );
  }, [levels.data, q]);

  const totalValue = useMemo(
    () => (levels.data ?? []).reduce((sum, r) => sum + toNumber(r.value), 0),
    [levels.data],
  );
  const outCount = useMemo(
    () => (levels.data ?? []).filter((r) => toNumber(r.qty_on_hand) <= 0).length,
    [levels.data],
  );

  const columns = [
    {
      header: t.common.product,
      sortValue: (r: Level) => r.name,
      cell: (r: Level) => <span className="font-medium">{r.name}</span>,
    },
    {
      header: "SKU",
      sortValue: (r: Level) => r.sku,
      cell: (r: Level) => (
        <span className="od-mono text-xs text-[var(--od-ink-secondary)]">{r.sku}</span>
      ),
    },
    {
      header: t.common.onHand,
      align: "right" as const,
      sortValue: (r: Level) => toNumber(r.qty_on_hand),
      cell: (r: Level) => {
        const qty = toNumber(r.qty_on_hand);
        const isLow = qty <= toNumber(r.reorder_level);
        return (
          <span
            className={
              qty <= 0
                ? "font-semibold text-[var(--od-bad)]"
                : isLow
                  ? "font-semibold text-[var(--od-warning)]"
                  : "font-medium"
            }
          >
            {formatQty(r.qty_on_hand)}
          </span>
        );
      },
    },
    {
      header: t.stock.reorderAt,
      align: "right" as const,
      sortValue: (r: Level) => toNumber(r.reorder_level),
      cell: (r: Level) => (
        <span className="text-[var(--od-ink-secondary)]">{formatQty(r.reorder_level)}</span>
      ),
    },
  ];

  return (
    <div className="space-y-6">
      <PageHeader
        title={t.common.stock}
        description={t.stock.inventoryQuantitiesAndValuationAt}
        actions={
          canManage && (
            <>
              <Button size="sm" onClick={() => openDialog("in")}>
                <ArrowDownToLine size={14} /> {t.stock.stockIn}
              </Button>
              <Button size="sm" variant="outline" onClick={() => openDialog("out")}>
                <ArrowUpFromLine size={14} /> {t.stock.stockOut}
              </Button>
            </>
          )
        }
      />

      <div
        className={`grid gap-4 sm:grid-cols-2 ${showCost ? "lg:grid-cols-4" : "lg:grid-cols-3"}`}
      >
        <StatCard label={t.stock.trackedItems} value={levels.data?.length ?? 0} />
        <StatCard
          label={t.common.lowStock}
          value={low.data?.length ?? 0}
          tone={(low.data?.length ?? 0) > 0 ? "bad" : "good"}
          trend={
            (low.data?.length ?? 0) > 0 ? t.stock.belowReorderThreshold : t.stock.allLevelsHealthy
          }
        />
        <StatCard
          label={t.common.outOfStock}
          value={outCount}
          tone={outCount > 0 ? "bad" : "neutral"}
        />
        {showCost && <StatCard label={t.stock.totalStockValue} value={formatMoney(totalValue)} />}
      </div>

      <Tabs defaultValue="levels">
        <TabsList>
          <TabsTrigger value="levels">{t.stock.levels}</TabsTrigger>
          <TabsTrigger value="low">
            {fmt(t.stock.lowStock, { value: low.data ? `(${low.data.length})` : "" })}
          </TabsTrigger>
        </TabsList>

        <TabsContent value="levels" className="space-y-4">
          <FilterBar>
            <div className="relative min-w-60 flex-1 sm:max-w-sm">
              <Search
                size={15}
                className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-[var(--od-ink-muted)]"
              />
              <Input
                aria-label={t.stock.filterStockLevels}
                className="pl-9"
                placeholder={t.stock.searchByProductNameOr}
                value={q}
                onChange={(e) => setQ(e.target.value)}
              />
            </div>
          </FilterBar>
          <DataGrid
            rows={filteredLevels}
            loading={levels.isLoading}
            rowKey={(r) => r.product_id}
            empty={t.stock.noStockItemsMatchYour}
            columns={[
              ...columns,
              ...(showCost
                ? [
                    {
                      header: t.common.avgCost,
                      align: "right" as const,
                      sortValue: (r: Level) => toNumber(r.avg_cost),
                      cell: (r: Level) => formatMoney(r.avg_cost),
                    },
                    {
                      header: t.common.value,
                      align: "right" as const,
                      sortValue: (r: Level) => toNumber(r.value),
                      cell: (r: Level) => (
                        <span className="font-medium">{formatMoney(r.value)}</span>
                      ),
                    },
                  ]
                : []),
            ]}
            totals={
              showCost && filteredLevels.length > 0
                ? [
                    t.common.total,
                    "",
                    "",
                    "",
                    "",
                    formatMoney(filteredLevels.reduce((sum, r) => sum + toNumber(r.value), 0)),
                  ]
                : undefined
            }
          />
        </TabsContent>

        <TabsContent value="low">
          <DataGrid
            rows={low.data}
            loading={low.isLoading}
            rowKey={(r) => r.product_id}
            empty={t.stock.nothingIsRunningLow}
            columns={[
              ...columns,
              {
                header: t.stock.suggestedOrder,
                align: "right",
                sortValue: (r) => toNumber(r.suggested_qty),
                cell: (r) => (
                  <span className="font-semibold text-[var(--od-info)]">
                    {formatQty(r.suggested_qty)}
                  </span>
                ),
              },
            ]}
          />
        </TabsContent>
      </Tabs>

      <FormDialog
        open={direction !== null}
        onOpenChange={(o) => !o && setDirection(null)}
        title={direction === "in" ? t.stock.stockIn : t.stock.stockOut}
        busy={post.isPending}
        submitLabel={t.stock.post}
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
        <Field label={t.common.product}>
          <NativeSelect required value={form.product_id} onChange={set("product_id")}>
            <option value="">{t.common.choose}</option>
            {levels.data?.map((p) => (
              <option key={p.product_id} value={p.product_id}>
                {p.name} ({p.sku})
              </option>
            ))}
          </NativeSelect>
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label={t.common.quantity}>
            <Input required inputMode="decimal" value={form.qty} onChange={set("qty")} />
          </Field>
          {direction === "in" && (
            <Field label={t.common.unitCost}>
              <Input
                required
                inputMode="decimal"
                value={form.unit_cost}
                onChange={set("unit_cost")}
              />
            </Field>
          )}
        </div>
        <Field label={t.common.reason}>
          <NativeSelect value={form.reason} onChange={set("reason")}>
            {direction &&
              reasons(t)[direction].map(([v, l]) => (
                <option key={v} value={v}>
                  {l}
                </option>
              ))}
          </NativeSelect>
        </Field>
        <Field label={t.common.note}>
          <Input value={form.note} onChange={set("note")} />
        </Field>
      </FormDialog>
    </div>
  );
}
