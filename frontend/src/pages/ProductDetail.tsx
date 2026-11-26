import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { Barcode, ShoppingCart } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { DataGrid, PageHeader, StatusBadge } from "@/components/shared/kit";
import { PageLoader, StatCard } from "@/components/shared/Display";
import { FilePreviewDialog, FileThumbnail } from "@/components/shared/FilePreview";
import { RecordHistoryTab } from "@/components/shared/RecordHistoryTab";
import { useAuth } from "@/features/auth/auth";
import { errorMessage, useGet } from "@/hooks/useApi";
import { download } from "@/lib/api";
import { formatDateTime, formatMoney, formatQty, toNumber } from "@/lib/formatters";
import type { Product } from "@/types";

interface ProductDetailPayload {
  product: Product;
  stock: { branch_id: string; branch: string; qty_on_hand: string; avg_cost?: string }[];
}

interface StockMove {
  id: string;
  created_at: string;
  type: string;
  qty: string;
  qty_after?: string;
  balance?: string;
  source_type?: string | null;
}

interface PriceChange {
  id: string;
  old_price: string;
  new_price: string;
  reason?: string | null;
  changed_at: string;
}

export default function ProductDetail() {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const { can } = useAuth();
  const [previewOpen, setPreviewOpen] = useState(false);

  const detail = useGet<ProductDetailPayload>(`/products/${id}`);
  const stockCard = useGet<StockMove[]>(`/products/${id}/stock-card`, undefined, {
    enabled: can("shop.stock.view"),
  });
  const priceHistory = useGet<PriceChange[]>(`/products/${id}/price-history`);

  if (detail.isLoading) return <PageLoader />;
  const pr = detail.data?.product;
  if (!pr) {
    return (
      <div className="od-card items-center py-10 text-center">
        <p className="text-[14px] font-semibold text-[var(--od-bad)]">Product not found.</p>
        <Button asChild variant="outline" size="sm" className="mt-3">
          <Link to="/products">Back to products</Link>
        </Button>
      </div>
    );
  }

  const branchLevels = detail.data?.stock ?? [];
  const totalQty = branchLevels.reduce((s, b) => s + toNumber(b.qty_on_hand), 0);
  const avgCost = branchLevels[0]?.avg_cost ? toNumber(branchLevels[0].avg_cost) : null;
  const unitProfit = avgCost !== null ? toNumber(pr.sell_price) - avgCost : null;

  return (
    <div className="space-y-6">
      <PageHeader
        backTo="/products"
        backLabel="Products"
        title={pr.name}
        badge={
          <div className="flex items-center gap-2">
            <StatusBadge value={pr.is_active ? "active" : "inactive"} />
            {pr.track_serial && (
              <span className="rounded-full bg-[var(--od-neutral-soft)] px-2.5 py-0.5 text-[11px] font-medium text-[var(--od-ink-secondary)]">
                Serial / IMEI tracked
              </span>
            )}
          </div>
        }
        description={`SKU ${pr.sku}${pr.barcode ? ` · Barcode ${pr.barcode}` : ""}${pr.warranty_months ? ` · ${pr.warranty_months} months warranty` : ""}`}
        actions={
          <div className="flex flex-wrap gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() =>
                download(
                  `/products/${pr.id}/barcode-label`,
                  {},
                  `barcode-${pr.barcode ?? pr.sku}.pdf`,
                ).catch((e) => toast.error(errorMessage(e)))
              }
            >
              <Barcode size={14} /> Barcode label
            </Button>
            {can("shop.sale.create") && pr.is_active && (
              <Button size="sm" onClick={() => navigate(`/pos?add=${pr.id}`)}>
                <ShoppingCart size={14} /> Sell
              </Button>
            )}
          </div>
        }
      />

      <div className="flex flex-wrap items-start gap-5">
        {pr.photo_file && (
          <div className="od-card-flush overflow-hidden p-1.5">
            <FileThumbnail
              fileId={pr.photo_file}
              fileName={pr.name}
              mimeType="image/*"
              onOpen={() => setPreviewOpen(true)}
            />
          </div>
        )}
        <div className="grid flex-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <StatCard label="Selling price" value={formatMoney(pr.sell_price)} />
          <StatCard
            label="Stock across locations"
            value={`${formatQty(totalQty)} ${pr.unit}`}
            trend={
              totalQty <= 0
                ? "Out of stock"
                : totalQty <= toNumber(pr.reorder_level)
                  ? `Below reorder (${formatQty(pr.reorder_level)})`
                  : `Reorder level ${formatQty(pr.reorder_level)}`
            }
            tone={totalQty <= toNumber(pr.reorder_level) ? "bad" : "neutral"}
          />
          {can("shop.product.view_cost") && unitProfit !== null && (
            <StatCard
              label="Profit per unit"
              value={formatMoney(unitProfit)}
              trend={`Avg cost ${formatMoney(avgCost)}`}
              tone={unitProfit >= 0 ? "good" : "bad"}
            />
          )}
          <StatCard
            label="SKU / unit"
            value={<span className="od-mono text-[20px]">{pr.sku}</span>}
            trend={pr.warranty_months ? `${pr.warranty_months}m warranty` : `Unit: ${pr.unit}`}
          />
        </div>
      </div>

      <Tabs defaultValue="stock">
        <TabsList>
          <TabsTrigger value="stock">Stock by location</TabsTrigger>
          <TabsTrigger value="movements">Stock history</TabsTrigger>
          <TabsTrigger value="prices">Price history</TabsTrigger>
          <TabsTrigger value="history">Audit history</TabsTrigger>
        </TabsList>

        <TabsContent value="stock">
          <DataGrid
            rows={branchLevels}
            rowKey={(b) => b.branch_id}
            empty="No location stock records"
            columns={[
              { header: "Location", cell: (b) => <span className="font-medium">{b.branch}</span> },
              {
                header: "On hand",
                align: "right",
                cell: (b) => <span className="font-medium">{formatQty(b.qty_on_hand)}</span>,
              },
              ...(can("shop.product.view_cost")
                ? [
                    {
                      header: "Avg cost",
                      align: "right" as const,
                      cell: (b: { avg_cost?: string }) =>
                        b.avg_cost ? formatMoney(b.avg_cost) : "—",
                    },
                  ]
                : []),
            ]}
          />
        </TabsContent>

        <TabsContent value="movements">
          <DataGrid
            rows={stockCard.data ?? []}
            loading={stockCard.isLoading}
            rowKey={(m) => m.id}
            empty="No stock movements recorded yet"
            columns={[
              { header: "When", cell: (m) => formatDateTime(m.created_at) },
              {
                header: "Type",
                cell: (m) => (
                  <span className="font-medium capitalize">{m.type.replaceAll("_", " ")}</span>
                ),
              },
              {
                header: "Change",
                align: "right",
                cell: (m) => {
                  const n = toNumber(m.qty);
                  return (
                    <span
                      className={
                        n > 0
                          ? "font-semibold text-[var(--od-good)]"
                          : n < 0
                            ? "font-semibold text-[var(--od-bad)]"
                            : undefined
                      }
                    >
                      {n > 0 ? `+${formatQty(m.qty)}` : formatQty(m.qty)}
                    </span>
                  );
                },
              },
              {
                header: "Balance",
                align: "right",
                cell: (m) => formatQty(m.qty_after ?? m.balance),
              },
              {
                header: "Source",
                cell: (m) =>
                  m.source_type ? (
                    <span className="capitalize text-[var(--od-ink-secondary)]">
                      {m.source_type.replaceAll("_", " ")}
                    </span>
                  ) : (
                    "—"
                  ),
              },
            ]}
          />
        </TabsContent>

        <TabsContent value="prices">
          <DataGrid
            rows={priceHistory.data}
            loading={priceHistory.isLoading}
            rowKey={(p) => p.id}
            empty="No price changes recorded"
            columns={[
              { header: "When", cell: (p) => formatDateTime(p.changed_at) },
              { header: "Old price", align: "right", cell: (p) => formatMoney(p.old_price) },
              {
                header: "New price",
                align: "right",
                cell: (p) => <span className="font-medium">{formatMoney(p.new_price)}</span>,
              },
              { header: "Reason", cell: (p) => p.reason || "—" },
            ]}
          />
        </TabsContent>

        <TabsContent value="history">
          <RecordHistoryTab entityType="product" entityId={pr.id} />
        </TabsContent>
      </Tabs>

      <FilePreviewDialog
        fileId={previewOpen ? (pr.photo_file ?? null) : null}
        fileName={pr.name}
        onClose={() => setPreviewOpen(false)}
      />
    </div>
  );
}
