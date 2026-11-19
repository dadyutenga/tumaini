import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { ArrowLeft, Barcode, ShoppingCart } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { DataGrid, PageHeader, StatusBadge } from "@/components/shared/kit";
import { PageLoader } from "@/components/shared/Display";
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
  qty_change: string;
  balance_after: string;
  note?: string | null;
}

interface PriceChange {
  id: string;
  old_price: string;
  new_price: string;
  changed_at: string;
}

export default function ProductDetail() {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const { can } = useAuth();
  const [previewOpen, setPreviewOpen] = useState(false);

  const detail = useGet<ProductDetailPayload>(`/products/${id}`);
  const stockCard = useGet<{ movements?: StockMove[] }>(
    `/products/${id}/stock-card`,
    undefined,
    { enabled: can("stock.view") },
  );
  const priceHistory = useGet<PriceChange[]>(`/products/${id}/price-history`);

  if (detail.isLoading) return <PageLoader />;
  const pr = detail.data?.product;
  if (!pr) {
    return (
      <p className="text-destructive">
        Product not found.{" "}
        <Link className="underline" to="/products">
          Back to products
        </Link>
      </p>
    );
  }

  const branchLevels = detail.data?.stock ?? [];
  const totalQty = branchLevels.reduce((s, b) => s + toNumber(b.qty_on_hand), 0);
  const avgCost = branchLevels[0]?.avg_cost ? toNumber(branchLevels[0].avg_cost) : null;
  const unitProfit = avgCost !== null ? toNumber(pr.sell_price) - avgCost : null;

  return (
    <div className="space-y-6">
      <PageHeader
        title={pr.name}
        description={`SKU ${pr.sku}${pr.barcode ? ` · Barcode ${pr.barcode}` : ""}${pr.warranty_months ? ` · ${pr.warranty_months} months warranty` : ""}`}
        actions={
          <div className="flex flex-wrap gap-2">
            <Button variant="outline" size="sm" asChild>
              <Link to="/products">
                <ArrowLeft size={14} /> Products
              </Link>
            </Button>
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
            {can("sales.create") && pr.is_active && (
              <Button size="sm" onClick={() => navigate(`/pos?add=${pr.id}`)}>
                <ShoppingCart size={14} /> Sell
              </Button>
            )}
          </div>
        }
      />

      <div className="flex flex-wrap items-start gap-6">
        {pr.photo_file && (
          <FileThumbnail
            fileId={pr.photo_file}
            fileName={pr.name}
            mimeType="image/*"
            onOpen={() => setPreviewOpen(true)}
          />
        )}
        <div className="grid flex-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <div className="rounded-2xl border bg-card p-4">
            <p className="text-xs text-muted-foreground">Selling price</p>
            <p className="mt-1 text-xl font-bold tabular-nums">{formatMoney(pr.sell_price)}</p>
          </div>
          <div className="rounded-2xl border bg-card p-4">
            <p className="text-xs text-muted-foreground">Stock across branches</p>
            <p className="mt-1 text-xl font-bold">
              {pr.kind === "service" ? "n/a" : `${formatQty(totalQty)} ${pr.unit}`}
            </p>
          </div>
          {unitProfit !== null && (
            <div className="rounded-2xl border bg-card p-4">
              <p className="text-xs text-muted-foreground">Profit per unit</p>
              <p className="mt-1 text-xl font-bold tabular-nums">{formatMoney(unitProfit)}</p>
              <p className="text-xs text-muted-foreground">Avg cost {formatMoney(avgCost)}</p>
            </div>
          )}
          <div className="rounded-2xl border bg-card p-4">
            <p className="text-xs text-muted-foreground">Status</p>
            <div className="mt-2 flex items-center gap-2">
              <StatusBadge value={pr.is_active ? "active" : "inactive"} />
              {pr.track_serial && (
                <span className="rounded bg-muted px-2 py-0.5 text-xs">Serial/IMEI tracked</span>
              )}
            </div>
          </div>
        </div>
      </div>

      <Tabs defaultValue="stock">
        <TabsList>
          <TabsTrigger value="stock">Stock by branch</TabsTrigger>
          <TabsTrigger value="movements">Stock history</TabsTrigger>
          <TabsTrigger value="prices">Price history</TabsTrigger>
          <TabsTrigger value="history">Audit history</TabsTrigger>
        </TabsList>

        <TabsContent value="stock">
          <DataGrid
            rows={branchLevels}
            rowKey={(b) => b.branch_id}
            empty="No branch stock records"
            columns={[
              { header: "Branch", cell: (b) => <span className="font-medium">{b.branch}</span> },
              { header: "On hand", align: "right", cell: (b) => formatQty(b.qty_on_hand) },
              ...(can("products.view_cost")
                ? [
                    {
                      header: "Avg cost",
                      align: "right" as const,
                      cell: (b: { avg_cost?: string }) =>
                        b.avg_cost ? formatMoney(b.avg_cost) : "-",
                    },
                  ]
                : []),
            ]}
          />
        </TabsContent>

        <TabsContent value="movements">
          <DataGrid
            rows={stockCard.data?.movements ?? []}
            loading={stockCard.isLoading}
            rowKey={(m) => m.id}
            empty="No stock movements recorded yet"
            columns={[
              { header: "When", cell: (m) => formatDateTime(m.created_at) },
              { header: "Type", cell: (m) => m.type },
              { header: "Change", align: "right", cell: (m) => formatQty(m.qty_change) },
              { header: "Balance", align: "right", cell: (m) => formatQty(m.balance_after) },
              { header: "Note", cell: (m) => m.note ?? "-" },
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
              { header: "New price", align: "right", cell: (p) => formatMoney(p.new_price) },
            ]}
          />
        </TabsContent>

        <TabsContent value="history">
          <RecordHistoryTab entityType="product" entityId={pr.id} />
        </TabsContent>
      </Tabs>

      <FilePreviewDialog
        fileId={previewOpen ? pr.photo_file ?? null : null}
        fileName={pr.name}
        onClose={() => setPreviewOpen(false)}
      />
    </div>
  );
}
