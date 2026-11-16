import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  DataGrid,
  Field,
  FormDialog,
  NativeSelect,
  PageHeader,
  StatusBadge,
} from "@/components/shared/kit";
import { PageLoader } from "@/components/shared/Display";
import { useAuth } from "@/features/auth/auth";
import { useGet, useWrite } from "@/hooks/useApi";
import { formatDate, formatMoney, formatQty, toNumber } from "@/lib/formatters";
import type { MoneyAccount, Sale, SaleItem, Till } from "@/types";

export default function SaleDetail() {
  const { id = "" } = useParams();
  const { can } = useAuth();
  const { data: sale, isLoading, error } = useGet<Sale>(`/sales/${id}`);
  const [returning, setReturning] = useState(false);
  const [qtys, setQtys] = useState<Record<string, string>>({});
  const [reason, setReason] = useState("");
  const [method, setMethod] = useState("cash");
  const [account, setAccount] = useState("");
  const till = useGet<Till | null>("/tills/current", undefined, {
    enabled: can("tills.open_close"),
  });
  const accounts = useGet<MoneyAccount[]>("/money-accounts", undefined, {
    enabled: can("accounts.view"),
  });

  const ret = useWrite<unknown>("POST", `/sales/${id}/returns`, {
    idempotent: true,
    invalidate: ["/sales", "/products", "/stock", "/money", "/customers"],
    success: "Return recorded",
    onSuccess: () => {
      setReturning(false);
      setQtys({});
    },
  });

  if (isLoading) return <PageLoader />;
  if (error || !sale)
    return (
      <p className="text-destructive">
        This sale could not be loaded.{" "}
        <Link className="underline" to="/sales">
          Back to sales
        </Link>
      </p>
    );

  const lines = Object.entries(qtys).filter(([, q]) => toNumber(q) > 0);
  return (
    <>
      <PageHeader
        title={`Sale ${sale.number}`}
        description={sale.business_date ? formatDate(sale.business_date) : undefined}
        actions={
          <>
            <StatusBadge value={sale.status} />
            {can("sales.refund_request") && sale.status === "completed" && (
              <Button variant="outline" onClick={() => setReturning(true)}>
                Return items
              </Button>
            )}
            <Button variant="outline" onClick={() => window.print()}>
              Print
            </Button>
          </>
        }
      />
      <DataGrid<SaleItem>
        rows={sale.items}
        rowKey={(i) => i.id ?? i.product_id}
        columns={[
          { header: "Product", cell: (i) => i.product_name },
          { header: "Qty", align: "right", cell: (i) => formatQty(i.qty) },
          { header: "Returned", align: "right", cell: (i) => formatQty(i.qty_returned) },
          { header: "Unit price", align: "right", cell: (i) => formatMoney(i.unit_price) },
          { header: "Total", align: "right", cell: (i) => formatMoney(i.line_total) },
        ]}
      />
      <dl className="ml-auto mt-4 w-full max-w-xs space-y-1 text-sm">
        {(
          [
            ["Subtotal", sale.subtotal],
            ["Discount", sale.discount_total],
            ["Tax included", sale.tax_total],
            ["Total", sale.total],
            ["Paid", sale.paid_total],
            ["Balance due", sale.balance],
          ] as const
        ).map(([l, v]) => (
          <div key={l} className="flex justify-between">
            <dt className="text-muted-foreground">{l}</dt>
            <dd className="tabular-nums">{formatMoney(v)}</dd>
          </div>
        ))}
        {sale.profit !== undefined && (
          <div className="flex justify-between border-t pt-1 font-medium">
            <dt>Profit</dt>
            <dd className="tabular-nums">{formatMoney(sale.profit)}</dd>
          </div>
        )}
      </dl>

      <FormDialog
        open={returning}
        onOpenChange={setReturning}
        title="Return items"
        description="Enter the quantity coming back for each line."
        busy={ret.isPending}
        submitLabel="Record return"
        onSubmit={() =>
          ret.mutate({
            reason,
            refund_method: method,
            items: lines.map(([sale_item_id, qty]) => ({ sale_item_id, qty })),
          })
        }
      >
        {sale.items.map((i) => (
          <div key={i.id} className="flex items-center justify-between gap-3">
            <span className="text-sm">
              {i.product_name}{" "}
              <span className="text-muted-foreground">
                (max {formatQty(toNumber(i.qty) - toNumber(i.qty_returned))})
              </span>
            </span>
            <Input
              aria-label={`Return quantity of ${i.product_name}`}
              className="w-24"
              inputMode="decimal"
              value={i.id ? (qtys[i.id] ?? "") : ""}
              onChange={(e) => i.id && setQtys((q) => ({ ...q, [i.id!]: e.target.value }))}
            />
          </div>
        ))}
        <Field label="Refund as">
          <NativeSelect value={method} onChange={(e) => setMethod(e.target.value)}>
            <option value="cash">Cash from the till</option>
            <option value="reduce_debt">Reduce customer's debt</option>
            <option value="customer_credit">Customer credit</option>
          </NativeSelect>
        </Field>
        {method === "cash" && accounts.data && (
          <Field label="Refund from account">
            <NativeSelect
              value={account || till.data?.money_account_id || ""}
              onChange={(e) => setAccount(e.target.value)}
            >
              <option value="">Choose…</option>
              {accounts.data
                .filter((a) => a.is_active && a.type === "cash")
                .map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.name}
                  </option>
                ))}
            </NativeSelect>
          </Field>
        )}
        <Field label="Reason">
          <Input required value={reason} onChange={(e) => setReason(e.target.value)} />
        </Field>
        {lines.length === 0 && (
          <p className="text-xs text-muted-foreground">Enter at least one quantity.</p>
        )}
      </FormDialog>
    </>
  );
}
