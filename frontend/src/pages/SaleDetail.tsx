import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { Download, Printer } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  DataGrid,
  Field,
  FormDialog,
  NativeSelect,
  PageHeader,
  SectionCard,
  StatusBadge,
} from "@/components/shared/kit";
import { PageLoader, StatCard } from "@/components/shared/Display";
import { RecordHistoryTab } from "@/components/shared/RecordHistoryTab";
import { AttachmentsSection } from "@/components/shared/AttachmentsSection";
import { SendOutboundButton } from "@/components/shared/SendOutbound";
import { useAuth } from "@/features/auth/auth";
import { toast } from "sonner";
import { errorMessage, useGet, useGetPage, useWrite } from "@/hooks/useApi";
import { api, download } from "@/lib/api";
import { formatDate, formatMoney, formatQty, toNumber } from "@/lib/formatters";
import type { MoneyAccount, Sale, SaleItem } from "@/types";

interface ReceiptPreview {
  number?: string;
  business_date?: string;
  cashier_name?: string;
  customer_name?: string;
  copy?: boolean;
  receipt_prints?: number;
  receipt_footer?: string;
  business?: { name?: string; address?: string; phone?: string; tax_number?: string };
}

interface SaleReturnRow {
  id: string;
  number: string;
  status: string;
  resolution: "refund" | "exchange";
  reason: string;
  refund_total: string;
  requested_by?: string;
  created_at: string;
}

export default function SaleDetail() {
  const { id = "" } = useParams();
  const { can, canAny, user } = useAuth();
  const { data: sale, isLoading, error } = useGet<Sale>(`/sales/${id}`);
  const receiptPreview = useGet<ReceiptPreview>(`/sales/${id}/receipt`, undefined, {
    enabled: Boolean(id),
  });
  const [printCopy, setPrintCopy] = useState<boolean | null>(null);
  const [printingReceipt, setPrintingReceipt] = useState(false);
  const canViewReturns = canAny("shop.sale.view_all", "shop.return.create", "shop.return.approve");
  const saleReturns = useGetPage<SaleReturnRow>(
    `/sales/${id}/returns`,
    { per_page: 50 },
    { enabled: Boolean(id) && canViewReturns },
  );
  const [returning, setReturning] = useState(false);
  const [voiding, setVoiding] = useState(false);
  const [voidReason, setVoidReason] = useState("");
  const [qtys, setQtys] = useState<Record<string, string>>({});
  const [conditions, setConditions] = useState<Record<string, "resellable" | "damaged">>({});
  const [reason, setReason] = useState("");
  const [resolution, setResolution] = useState<"refund" | "exchange">("refund");
  const [completing, setCompleting] = useState<SaleReturnRow | null>(null);
  const [refundMethod, setRefundMethod] = useState("cash");
  const [account, setAccount] = useState("");
  const [refundRef, setRefundRef] = useState("");
  const accounts = useGet<MoneyAccount[]>("/money-accounts", undefined, {
    enabled: canAny(
      "finance.account.view",
      "finance.payment.receive",
      "finance.payment.make",
      "shop.sale.create",
      "shop.return.create",
      "shop.return.approve",
    ),
  });

  const refresh = ["/sales", "/sale-returns", "/products", "/stock", "/money", "/customers"];
  const ret = useWrite<unknown>("POST", `/sales/${id}/returns`, {
    idempotent: true,
    invalidate: refresh,
    success: "Return requested for approval",
    onSuccess: () => {
      setReturning(false);
      setQtys({});
      setConditions({});
      setReason("");
    },
  });

  const decideReturn = useWrite<{ id: string; action: "approve" | "reject" | "cancel" }, unknown>(
    "POST",
    (b) => `/sale-returns/${b.id}/${b.action}`,
    {
      toBody: () => ({}),
      invalidate: refresh,
      success: "Return updated",
    },
  );

  const completeReturn = useWrite<Record<string, unknown>, unknown>(
    "POST",
    () => `/sale-returns/${completing?.id}/complete`,
    {
      idempotent: true,
      invalidate: refresh,
      success: "Return completed",
      onSuccess: () => {
        setCompleting(null);
        setAccount("");
        setRefundRef("");
      },
    },
  );

  const voidSale = useWrite<{ reason: string }, unknown>("POST", `/sales/${id}/void`, {
    idempotent: true,
    invalidate: refresh,
    success: "Sale void submitted",
    onSuccess: () => {
      setVoiding(false);
      setVoidReason("");
    },
  });

  if (isLoading) return <PageLoader />;
  if (error || !sale)
    return (
      <div className="od-card items-center py-10 text-center">
        <p className="text-[14px] font-semibold text-[var(--od-bad)]">
          This sale could not be loaded.
        </p>
        <Button asChild variant="outline" size="sm" className="mt-3">
          <Link to="/sales">Back to sales</Link>
        </Button>
      </div>
    );

  const lines = Object.entries(qtys).filter(([, q]) => toNumber(q) > 0);
  const activeAccounts = (accounts.data ?? []).filter((a) => a.is_active);
  const canVoid = canAny("shop.sale.void", "shop.sale.request_void");
  const canApproveReturn = can("shop.return.approve");
  const canManageReturn = canAny("shop.return.create", "shop.return.approve");
  const returnRows = saleReturns.data?.data ?? [];
  const showCopyBanner = printCopy ?? Boolean(receiptPreview.data?.copy);

  async function printThermalReceipt() {
    setPrintingReceipt(true);
    try {
      const res = await api.post<{ copy: boolean; receipt_prints: number }>(
        `/sales/${id}/receipt/printed`,
        {},
      );
      setPrintCopy(Boolean(res?.copy));
      await new Promise<void>((r) => requestAnimationFrame(() => requestAnimationFrame(() => r())));
      window.print();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setPrintingReceipt(false);
    }
  }

  return (
    <div className="space-y-6">
      <PageHeader
        backTo="/sales"
        backLabel="Sales & returns"
        title={`Sale ${sale.number}`}
        badge={<StatusBadge value={sale.status} />}
        description={[
          sale.business_date ? formatDate(sale.business_date) : null,
          sale.customer_name ? `Customer: ${sale.customer_name}` : "Walk-in customer",
          receiptPreview.data?.cashier_name ? `Cashier: ${receiptPreview.data.cashier_name}` : null,
        ]
          .filter(Boolean)
          .join(" · ")}
        actions={
          <>
            {can("shop.return.create") && sale.status === "completed" && (
              <Button variant="outline" size="sm" onClick={() => setReturning(true)}>
                Return items
              </Button>
            )}
            {canVoid && sale.status === "completed" && (
              <Button variant="outline" size="sm" onClick={() => setVoiding(true)}>
                {can("shop.sale.void") ? "Void sale" : "Request void"}
              </Button>
            )}
            <Button
              variant="outline"
              size="sm"
              onClick={() =>
                download(
                  `/sales/${id}/receipt`,
                  { format: "pdf" },
                  `receipt-${sale.number}.pdf`,
                ).catch((e) => toast.error(errorMessage(e)))
              }
            >
              <Download size={14} /> Receipt PDF
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() =>
                download(`/sales/${id}/invoice`, { format: "pdf" }, `${sale.number}.pdf`).catch(
                  (e) => toast.error(errorMessage(e)),
                )
              }
            >
              Invoice PDF
            </Button>
            <SendOutboundButton
              label="Send receipt"
              title="Send receipt"
              path={`/sales/${id}/send-receipt`}
              successMessage="Receipt queued"
              phone={sale.customer_phone}
              size="sm"
              whatsappText={`Receipt ${sale.number}: Total ${formatMoney(sale.total)}, Paid ${formatMoney(sale.paid_total)}${
                toNumber(sale.balance) > 0 ? `, Balance ${formatMoney(sale.balance)}` : ""
              }.`}
            />
            <Button
              variant="outline"
              size="sm"
              disabled={printingReceipt}
              onClick={() => void printThermalReceipt()}
            >
              <Printer size={14} /> {printingReceipt ? "Printing…" : "Print"}
            </Button>
          </>
        }
      />

      {/* Summary KPIs */}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard label="Total" value={formatMoney(sale.total)} />
        <StatCard
          label="Paid"
          value={formatMoney(sale.paid_total)}
          tone={toNumber(sale.paid_total) > 0 ? "good" : "neutral"}
        />
        <StatCard
          label="Balance due"
          value={formatMoney(sale.balance)}
          tone={toNumber(sale.balance) > 0 ? "bad" : "neutral"}
        />
        {sale.profit !== undefined ? (
          <StatCard
            label="Profit"
            value={formatMoney(sale.profit)}
            tone={toNumber(sale.profit) >= 0 ? "good" : "bad"}
          />
        ) : (
          <StatCard label="Items" value={sale.items.length} />
        )}
      </div>

      {/* Thermal receipt print layout (visible only during window.print()) */}
      <div className="print-document mx-auto hidden max-w-xs space-y-2 p-4 font-mono text-xs text-black print:block">
        <div className="text-center">
          <p className="text-sm font-bold">{receiptPreview.data?.business?.name || "Receipt"}</p>
          {receiptPreview.data?.business?.address && <p>{receiptPreview.data.business.address}</p>}
          {receiptPreview.data?.business?.phone && <p>{receiptPreview.data.business.phone}</p>}
        </div>
        {showCopyBanner && (
          <p className="border-y border-dashed border-black py-1 text-center font-bold tracking-widest">
            *** COPY ***
          </p>
        )}
        <div className="space-y-0.5 border-b border-dashed border-black pb-2">
          <p>Receipt: {sale.number}</p>
          {sale.business_date && <p>Date: {formatDate(sale.business_date)}</p>}
          {receiptPreview.data?.cashier_name && <p>Cashier: {receiptPreview.data.cashier_name}</p>}
          <p>Customer: {receiptPreview.data?.customer_name || sale.customer_name || "Walk-in"}</p>
        </div>
        <ul className="space-y-1 border-b border-dashed border-black pb-2">
          {sale.items.map((i) => (
            <li key={i.id ?? i.product_id} className="flex justify-between gap-2">
              <span>
                {formatQty(i.qty)} × {i.product_name}
              </span>
              <span>{formatMoney(i.line_total)}</span>
            </li>
          ))}
        </ul>
        <div className="space-y-0.5">
          <div className="flex justify-between font-bold">
            <span>TOTAL</span>
            <span>{formatMoney(sale.total)}</span>
          </div>
          <div className="flex justify-between">
            <span>Paid</span>
            <span>{formatMoney(sale.paid_total)}</span>
          </div>
          {toNumber(sale.balance) > 0 && (
            <div className="flex justify-between">
              <span>Balance</span>
              <span>{formatMoney(sale.balance)}</span>
            </div>
          )}
        </div>
        <p className="pt-2 text-center">
          {receiptPreview.data?.receipt_footer ||
            sale.receipt_footer ||
            "Thank you for your business"}
        </p>
      </div>

      {/* Line Items + Financial Breakdown */}
      <div className="space-y-4">
        <DataGrid<SaleItem>
          rows={sale.items}
          rowKey={(i) => i.id ?? i.product_id}
          columns={[
            {
              header: "Product",
              cell: (i) => <span className="font-medium">{i.product_name}</span>,
            },
            { header: "Qty", align: "right", cell: (i) => formatQty(i.qty) },
            { header: "Returned", align: "right", cell: (i) => formatQty(i.qty_returned) },
            { header: "Unit price", align: "right", cell: (i) => formatMoney(i.unit_price) },
            {
              header: "Total",
              align: "right",
              cell: (i) => <span className="font-medium">{formatMoney(i.line_total)}</span>,
            },
          ]}
        />

        <div className="flex justify-end">
          <SectionCard className="w-full max-w-sm">
            <dl className="space-y-2 text-[13px]">
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
                <div
                  key={l}
                  className={
                    l === "Total"
                      ? "flex justify-between border-t border-[var(--od-border)] pt-2 font-semibold"
                      : "flex justify-between"
                  }
                >
                  <dt className="text-[var(--od-ink-secondary)]">{l}</dt>
                  <dd className="od-tabular font-medium text-[var(--od-ink)]">{formatMoney(v)}</dd>
                </div>
              ))}
              {sale.profit !== undefined && (
                <div className="flex justify-between border-t border-[var(--od-border)] pt-2 font-semibold">
                  <dt>Profit</dt>
                  <dd className="od-tabular text-[var(--od-good)]">{formatMoney(sale.profit)}</dd>
                </div>
              )}
            </dl>
          </SectionCard>
        </div>
      </div>

      {canViewReturns && returnRows.length > 0 && (
        <section className="space-y-3">
          <h2 className="od-section-title">Returns</h2>
          <DataGrid
            rows={returnRows}
            loading={saleReturns.isLoading}
            rowKey={(r) => r.id}
            columns={[
              {
                header: "Return #",
                cell: (r) => <span className="od-mono font-medium">{r.number}</span>,
              },
              { header: "Date", cell: (r) => formatDate(r.created_at) },
              {
                header: "Resolution",
                cell: (r) => <span className="capitalize">{r.resolution}</span>,
              },
              { header: "Reason", cell: (r) => r.reason || "—" },
              { header: "Refund", align: "right", cell: (r) => formatMoney(r.refund_total) },
              { header: "Status", cell: (r) => <StatusBadge value={r.status} /> },
              {
                header: "",
                cell: (r) => {
                  const isOwn =
                    (Boolean(user?.membership_id) && r.requested_by === user?.membership_id) ||
                    (Boolean(user?.id) && r.requested_by === user?.id);
                  return (
                    <div className="flex justify-end gap-1.5">
                      {r.status === "requested" && canApproveReturn && !isOwn && (
                        <>
                          <Button
                            size="sm"
                            disabled={decideReturn.isPending}
                            onClick={() => decideReturn.mutate({ id: r.id, action: "approve" })}
                          >
                            Approve
                          </Button>
                          <Button
                            size="sm"
                            variant="outline"
                            disabled={decideReturn.isPending}
                            onClick={() => decideReturn.mutate({ id: r.id, action: "reject" })}
                          >
                            Reject
                          </Button>
                        </>
                      )}
                      {r.status === "approved" && canManageReturn && (
                        <Button
                          size="sm"
                          onClick={() => {
                            setCompleting(r);
                            setRefundMethod("cash");
                            setAccount(activeAccounts.find((a) => a.type === "cash")?.id ?? "");
                            setRefundRef("");
                          }}
                        >
                          Complete
                        </Button>
                      )}
                      {(r.status === "requested" || r.status === "approved") && canManageReturn && (
                        <Button
                          size="sm"
                          variant="ghost"
                          disabled={decideReturn.isPending}
                          onClick={() => decideReturn.mutate({ id: r.id, action: "cancel" })}
                        >
                          Cancel
                        </Button>
                      )}
                    </div>
                  );
                },
              },
            ]}
          />
        </section>
      )}

      <FormDialog
        open={returning}
        onOpenChange={setReturning}
        title="Request item return"
        description="Enter the quantity coming back. Another staff member with return-approval permission approves it before refund completion."
        busy={ret.isPending}
        submitLabel="Submit return request"
        onSubmit={() =>
          ret.mutate({
            reason: reason.trim(),
            resolution,
            items: lines.map(([sale_item_id, qty]) => {
              const cond = conditions[sale_item_id] ?? "resellable";
              return {
                sale_item_id,
                qty,
                condition: cond,
                restock: cond === "resellable",
              };
            }),
          })
        }
      >
        {sale.items.map((i) => {
          const itemId = i.id ?? "";
          const cond = conditions[itemId] ?? "resellable";
          return (
            <div key={i.id} className="grid grid-cols-[1fr_5.5rem_7.5rem] items-center gap-2">
              <span className="text-[13px]">
                <span className="font-medium">{i.product_name}</span>{" "}
                <span className="od-caption">
                  (max {formatQty(toNumber(i.qty) - toNumber(i.qty_returned))})
                </span>
              </span>
              <Input
                aria-label={`Return quantity of ${i.product_name}`}
                inputMode="decimal"
                placeholder="0"
                value={itemId ? (qtys[itemId] ?? "") : ""}
                onChange={(e) => itemId && setQtys((q) => ({ ...q, [itemId]: e.target.value }))}
              />
              <NativeSelect
                aria-label={`Condition of ${i.product_name}`}
                value={cond}
                onChange={(e) =>
                  itemId &&
                  setConditions((c) => ({
                    ...c,
                    [itemId]: e.target.value as "resellable" | "damaged",
                  }))
                }
              >
                <option value="resellable">Resellable</option>
                <option value="damaged">Damaged</option>
              </NativeSelect>
            </div>
          );
        })}
        <Field label="Resolution">
          <NativeSelect
            value={resolution}
            onChange={(e) => setResolution(e.target.value as "refund" | "exchange")}
          >
            <option value="refund">Refund money</option>
            <option value="exchange">Exchange</option>
          </NativeSelect>
        </Field>
        <Field label="Reason (min 3 characters)">
          <Input
            required
            minLength={3}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
          />
        </Field>
        {lines.length === 0 && <p className="od-caption">Enter at least one quantity.</p>}
      </FormDialog>

      <FormDialog
        open={completing !== null}
        onOpenChange={(o) => !o && setCompleting(null)}
        title={`Complete return ${completing?.number ?? ""}`}
        submitLabel="Complete return"
        busy={completeReturn.isPending}
        onSubmit={() => {
          if (!completing) return;
          const needsPayout =
            completing.resolution === "refund" && toNumber(completing.refund_total) > 0;
          completeReturn.mutate(
            needsPayout
              ? {
                  money_account_id: account,
                  method: refundMethod,
                  reference: refundRef.trim() || undefined,
                }
              : {},
          );
        }}
      >
        {completing &&
        completing.resolution === "refund" &&
        toNumber(completing.refund_total) > 0 ? (
          <>
            <p className="rounded-[var(--od-radius-sm,8px)] bg-[var(--od-surface-subtle)] px-3 py-2 text-[13px] text-[var(--od-ink-secondary)]">
              Refund amount:{" "}
              <strong className="od-tabular text-[var(--od-ink)]">
                {formatMoney(completing.refund_total)}
              </strong>
            </p>
            <Field label="Refund method">
              <NativeSelect value={refundMethod} onChange={(e) => setRefundMethod(e.target.value)}>
                <option value="cash">Cash</option>
                <option value="mobile_money">Mobile money</option>
                <option value="bank">Bank transfer</option>
              </NativeSelect>
            </Field>
            <Field label="Refund from account">
              <NativeSelect required value={account} onChange={(e) => setAccount(e.target.value)}>
                <option value="">Choose…</option>
                {activeAccounts.map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.name}
                  </option>
                ))}
              </NativeSelect>
            </Field>
            <Field label="Reference (optional)">
              <Input value={refundRef} onChange={(e) => setRefundRef(e.target.value)} />
            </Field>
          </>
        ) : (
          <p className="text-[13px] text-[var(--od-ink-secondary)]">
            Confirm completion of this exchange return. Eligible resellable items will be restocked.
          </p>
        )}
      </FormDialog>

      <FormDialog
        open={voiding}
        onOpenChange={setVoiding}
        title="Void this sale"
        submitLabel="Void sale"
        busy={voidSale.isPending}
        onSubmit={() => voidSale.mutate({ reason: voidReason })}
      >
        <p className="text-[13px] text-[var(--od-ink-secondary)]">
          Stock goes back and the money is reversed. Only same-day sales can be voided without
          approval.
        </p>
        <Field label="Reason">
          <Input required value={voidReason} onChange={(e) => setVoidReason(e.target.value)} />
        </Field>
      </FormDialog>

      {id && (
        <SectionCard title="Attachments">
          <AttachmentsSection entityType="sale" entityId={id} />
        </SectionCard>
      )}

      <SectionCard title="History" subtitle="Audit trail for this sale">
        <RecordHistoryTab entityType="sale" entityId={id} />
      </SectionCard>
    </div>
  );
}
