import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { ArrowLeft } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { DataGrid, Field, FormDialog, PageHeader, StatusBadge } from "@/components/shared/kit";
import { PageLoader } from "@/components/shared/Display";
import { AttachmentsSection } from "@/components/shared/AttachmentsSection";
import { useAuth } from "@/features/auth/auth";
import { useGet, useWrite } from "@/hooks/useApi";
import { formatDate, formatMoney, formatQty, toNumber } from "@/lib/formatters";

interface Line {
  id: string;
  product_id: string;
  product_name: string;
  qty_ordered: string;
  qty_received: string;
  qty_outstanding: string;
  unit_cost: string;
  line_total: string;
}
interface Receipt {
  id: string;
  number?: string;
}
interface Order {
  id: string;
  number: string;
  supplier: string;
  supplier_id: string;
  status: string;
  order_date: string;
  total: string;
  note: string | null;
  items: Line[];
  receipts: Receipt[];
}

export default function PurchaseOrderDetail() {
  const { id } = useParams();
  const { can } = useAuth();
  const order = useGet<Order>(`/purchase-orders/${id}`);
  const [receiving, setReceiving] = useState(false);
  const [qtys, setQtys] = useState<Record<string, string>>({});
  const refresh = ["/purchase-orders", "/stock", "/products", "/goods-receipts"];
  const submit = useWrite("POST", `/purchase-orders/${id}/submit`, {
    invalidate: refresh,
    success: "Order submitted",
  });
  const cancel = useWrite("POST", `/purchase-orders/${id}/cancel`, {
    invalidate: refresh,
    success: "Order cancelled",
  });
  const close = useWrite("POST", `/purchase-orders/${id}/close`, {
    invalidate: refresh,
    success: "Order closed",
  });
  const receive = useWrite("POST", "/goods-receipts", {
    idempotent: true,
    invalidate: refresh,
    success: "Goods received into stock",
    onSuccess: () => setReceiving(false),
  });

  if (order.isLoading || !order.data) return <PageLoader />;
  const o = order.data;
  const receivable = ["approved", "partially_received"].includes(o.status);

  return (
    <>
      <Link
        to="/purchasing"
        className="mb-3 inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft size={14} /> Purchasing
      </Link>
      <PageHeader
        title={o.number}
        description={`${o.supplier} · ${formatDate(o.order_date)} · ${formatMoney(o.total)}`}
        actions={
          <div className="flex items-center gap-2">
            <StatusBadge value={o.status} />
            {o.status === "draft" && can("po.create") && (
              <>
                <Button
                  variant="outline"
                  onClick={() => cancel.mutate({})}
                  disabled={cancel.isPending}
                >
                  Cancel
                </Button>
                <Button onClick={() => submit.mutate({})} disabled={submit.isPending}>
                  Submit
                </Button>
              </>
            )}
            {receivable && can("grn.create") && (
              <Button
                onClick={() => {
                  setQtys(
                    Object.fromEntries(o.items.map((l) => [l.product_id, l.qty_outstanding])),
                  );
                  setReceiving(true);
                }}
              >
                Receive goods
              </Button>
            )}
            {o.status === "partially_received" && can("po.approve") && (
              <Button variant="outline" onClick={() => close.mutate({})}>
                Close order
              </Button>
            )}
          </div>
        }
      />
      <DataGrid
        rows={o.items}
        rowKey={(l) => l.id}
        columns={[
          { header: "Product", cell: (l) => l.product_name },
          { header: "Ordered", align: "right", cell: (l) => formatQty(l.qty_ordered) },
          { header: "Received", align: "right", cell: (l) => formatQty(l.qty_received) },
          { header: "Outstanding", align: "right", cell: (l) => formatQty(l.qty_outstanding) },
          { header: "Unit cost", align: "right", cell: (l) => formatMoney(l.unit_cost) },
          { header: "Total", align: "right", cell: (l) => formatMoney(l.line_total) },
        ]}
      />
      {o.note && <p className="mt-3 text-sm text-muted-foreground">{o.note}</p>}
      <section className="mt-6">
        <AttachmentsSection entityType="purchase_order" entityId={o.id} />
      </section>
      <FormDialog
        open={receiving}
        onOpenChange={setReceiving}
        title="Receive goods"
        submitLabel="Receive into stock"
        busy={receive.isPending}
        wide
        onSubmit={() =>
          receive.mutate({
            po_id: o.id,
            items: o.items
              .filter((l) => toNumber(qtys[l.product_id]) > 0)
              .map((l) => ({
                product_id: l.product_id,
                qty: qtys[l.product_id],
                unit_cost: l.unit_cost,
              })),
          })
        }
      >
        {o.items.map((l) => (
          <Field
            key={l.id}
            label={`${l.product_name} (outstanding ${formatQty(l.qty_outstanding)})`}
          >
            <Input
              inputMode="decimal"
              value={qtys[l.product_id] ?? ""}
              onChange={(e) => setQtys((q) => ({ ...q, [l.product_id]: e.target.value }))}
            />
          </Field>
        ))}
      </FormDialog>
    </>
  );
}
