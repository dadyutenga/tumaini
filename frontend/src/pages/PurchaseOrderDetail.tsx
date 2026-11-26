import { useState } from "react";
import { useParams } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  DataGrid,
  Field,
  FormDialog,
  PageHeader,
  SectionCard,
  StatusBadge,
} from "@/components/shared/kit";
import { PageLoader, StatCard } from "@/components/shared/Display";
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
  const receive = useWrite("POST", "/goods-receipts", {
    idempotent: true,
    invalidate: refresh,
    success: "Goods received into stock",
    onSuccess: () => setReceiving(false),
  });

  if (order.isLoading || !order.data) return <PageLoader />;
  const o = order.data;
  const manage = can("shop.purchase.manage");
  const receivable = ["ordered", "approved", "partially_received"].includes(o.status);

  const orderedQty = o.items.reduce((s, l) => s + toNumber(l.qty_ordered), 0);
  const receivedQty = o.items.reduce((s, l) => s + toNumber(l.qty_received), 0);
  const outstandingQty = o.items.reduce((s, l) => s + toNumber(l.qty_outstanding), 0);

  return (
    <div className="space-y-6">
      <PageHeader
        backTo="/purchasing"
        backLabel="Purchasing"
        title={o.number}
        badge={<StatusBadge value={o.status} />}
        description={`${o.supplier} · Ordered ${formatDate(o.order_date)}`}
        actions={
          <div className="flex flex-wrap items-center gap-2">
            {o.status === "draft" && manage && (
              <>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => cancel.mutate({})}
                  disabled={cancel.isPending}
                >
                  Cancel
                </Button>
                <Button size="sm" onClick={() => submit.mutate({})} disabled={submit.isPending}>
                  Submit
                </Button>
              </>
            )}
            {receivable && manage && (
              <Button
                size="sm"
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
          </div>
        }
      />

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard label="Order total" value={formatMoney(o.total)} />
        <StatCard label="Line items" value={o.items.length} />
        <StatCard
          label="Received"
          value={`${formatQty(receivedQty)} / ${formatQty(orderedQty)}`}
          tone={outstandingQty === 0 && orderedQty > 0 ? "good" : "neutral"}
        />
        <StatCard
          label="Outstanding"
          value={formatQty(outstandingQty)}
          tone={outstandingQty > 0 ? "bad" : "good"}
        />
      </div>

      <DataGrid
        rows={o.items}
        rowKey={(l) => l.id}
        columns={[
          {
            header: "Product",
            cell: (l) => <span className="font-medium">{l.product_name}</span>,
          },
          { header: "Ordered", align: "right", cell: (l) => formatQty(l.qty_ordered) },
          { header: "Received", align: "right", cell: (l) => formatQty(l.qty_received) },
          {
            header: "Outstanding",
            align: "right",
            cell: (l) => (
              <span
                className={
                  toNumber(l.qty_outstanding) > 0 ? "font-semibold text-[var(--od-warning)]" : ""
                }
              >
                {formatQty(l.qty_outstanding)}
              </span>
            ),
          },
          { header: "Unit cost", align: "right", cell: (l) => formatMoney(l.unit_cost) },
          {
            header: "Total",
            align: "right",
            cell: (l) => <span className="font-medium">{formatMoney(l.line_total)}</span>,
          },
        ]}
        totals={[
          "Total",
          formatQty(orderedQty),
          formatQty(receivedQty),
          formatQty(outstandingQty),
          "",
          formatMoney(o.total),
        ]}
      />

      {o.note && (
        <SectionCard title="Order note">
          <p className="text-[13px] leading-relaxed text-[var(--od-ink-secondary)]">{o.note}</p>
        </SectionCard>
      )}

      <SectionCard title="Attachments">
        <AttachmentsSection entityType="purchase_order" entityId={o.id} />
      </SectionCard>

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
    </div>
  );
}
