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
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";

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
  const { t } = useI18n();
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
    success: t.sales.returnRequestedForApproval,
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
      success: t.sales.returnUpdated,
    },
  );

  const completeReturn = useWrite<Record<string, unknown>, unknown>(
    "POST",
    () => `/sale-returns/${completing?.id}/complete`,
    {
      idempotent: true,
      invalidate: refresh,
      success: t.sales.returnCompleted,
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
    success: t.sales.saleVoidSubmitted,
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
          {t.sales.thisSaleCouldNotBe}
        </p>
        <Button asChild variant="outline" size="sm" className="mt-3">
          <Link to="/sales">{t.sales.backToSales}</Link>
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
        backLabel={t.sales.salesReturns}
        title={fmt(t.sales.sale, { number: sale.number ?? "" })}
        badge={<StatusBadge value={sale.status} />}
        description={[
          sale.business_date ? formatDate(sale.business_date) : null,
          sale.customer_name ? `Customer: ${sale.customer_name}` : t.sales.walkInCustomer,
          receiptPreview.data?.cashier_name ? `Cashier: ${receiptPreview.data.cashier_name}` : null,
        ]
          .filter(Boolean)
          .join(" · ")}
        actions={
          <>
            {can("shop.return.create") && sale.status === "completed" && (
              <Button variant="outline" size="sm" onClick={() => setReturning(true)}>
                {t.sales.returnItems}
              </Button>
            )}
            {canVoid && sale.status === "completed" && (
              <Button variant="outline" size="sm" onClick={() => setVoiding(true)}>
                {can("shop.sale.void") ? t.sales.voidSale : t.common.requestVoid}
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
              <Download size={14} /> {t.common.receiptPdf}
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
              {t.sales.invoicePdf}
            </Button>
            <SendOutboundButton
              label={t.common.sendReceipt}
              title={t.common.sendReceipt}
              path={`/sales/${id}/send-receipt`}
              successMessage={t.common.receiptQueued}
              phone={sale.customer_phone}
              size="sm"
              whatsappText={fmt(t.sales.receiptTotalPaid, {
                number: sale.number ?? "",
                total: formatMoney(sale.total),
                paid_total: formatMoney(sale.paid_total),
                value:
                  toNumber(sale.balance) > 0
                    ? fmt(t.sales.balance, { balance: formatMoney(sale.balance) })
                    : "",
              })}
            />
            <Button
              variant="outline"
              size="sm"
              disabled={printingReceipt}
              onClick={() => void printThermalReceipt()}
            >
              <Printer size={14} /> {printingReceipt ? t.common.printing : t.common.print}
            </Button>
          </>
        }
      />

      {/* Summary KPIs */}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard label={t.common.total} value={formatMoney(sale.total)} />
        <StatCard
          label={t.common.paid}
          value={formatMoney(sale.paid_total)}
          tone={toNumber(sale.paid_total) > 0 ? "good" : "neutral"}
        />
        <StatCard
          label={t.sales.balanceDue}
          value={formatMoney(sale.balance)}
          tone={toNumber(sale.balance) > 0 ? "bad" : "neutral"}
        />
        {sale.profit !== undefined ? (
          <StatCard
            label={t.common.profit}
            value={formatMoney(sale.profit)}
            tone={toNumber(sale.profit) >= 0 ? "good" : "bad"}
          />
        ) : (
          <StatCard label={t.sales.items} value={sale.items.length} />
        )}
      </div>

      {/* Thermal receipt print layout (visible only during window.print()) */}
      <div className="print-document mx-auto hidden max-w-xs space-y-2 p-4 font-mono text-xs text-black print:block">
        <div className="text-center">
          <p className="text-sm font-bold">
            {receiptPreview.data?.business?.name || t.common.receipt2}
          </p>
          {receiptPreview.data?.business?.address && <p>{receiptPreview.data.business.address}</p>}
          {receiptPreview.data?.business?.phone && <p>{receiptPreview.data.business.phone}</p>}
        </div>
        {showCopyBanner && (
          <p className="border-y border-dashed border-black py-1 text-center font-bold tracking-widest">
            {t.common.copy2}
          </p>
        )}
        <div className="space-y-0.5 border-b border-dashed border-black pb-2">
          <p>{fmt(t.sales.receipt, { number: sale.number ?? "" })}</p>
          {sale.business_date && (
            <p>{fmt(t.sales.date, { business_date: formatDate(sale.business_date) })}</p>
          )}
          {receiptPreview.data?.cashier_name && (
            <p>{fmt(t.sales.cashier, { cashier_name: receiptPreview.data.cashier_name })}</p>
          )}
          <p>
            {fmt(t.sales.customer, {
              customer_name:
                receiptPreview.data?.customer_name || sale.customer_name || t.common.walkIn,
            })}
          </p>
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
            <span>{t.common.total2}</span>
            <span>{formatMoney(sale.total)}</span>
          </div>
          <div className="flex justify-between">
            <span>{t.common.paid}</span>
            <span>{formatMoney(sale.paid_total)}</span>
          </div>
          {toNumber(sale.balance) > 0 && (
            <div className="flex justify-between">
              <span>{t.common.balance}</span>
              <span>{formatMoney(sale.balance)}</span>
            </div>
          )}
        </div>
        <p className="pt-2 text-center">
          {receiptPreview.data?.receipt_footer ||
            sale.receipt_footer ||
            t.sales.thankYouForYourBusiness}
        </p>
      </div>

      {/* Line Items + Financial Breakdown */}
      <div className="space-y-4">
        <DataGrid<SaleItem>
          rows={sale.items}
          rowKey={(i) => i.id ?? i.product_id}
          columns={[
            {
              header: t.common.product,
              cell: (i) => <span className="font-medium">{i.product_name}</span>,
            },
            { header: t.common.qty, align: "right", cell: (i) => formatQty(i.qty) },
            { header: t.sales.returned, align: "right", cell: (i) => formatQty(i.qty_returned) },
            { header: t.common.unitPrice, align: "right", cell: (i) => formatMoney(i.unit_price) },
            {
              header: t.common.total,
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
                  [t.common.subtotal, sale.subtotal],
                  [t.common.discount, sale.discount_total],
                  [t.common.taxIncluded, sale.tax_total],
                  [t.common.total, sale.total],
                  [t.common.paid, sale.paid_total],
                  [t.sales.balanceDue, sale.balance],
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
                  <dt>{t.common.profit}</dt>
                  <dd className="od-tabular text-[var(--od-good)]">{formatMoney(sale.profit)}</dd>
                </div>
              )}
            </dl>
          </SectionCard>
        </div>
      </div>

      {canViewReturns && returnRows.length > 0 && (
        <section className="space-y-3">
          <h2 className="od-section-title">{t.sales.returns}</h2>
          <DataGrid
            rows={returnRows}
            loading={saleReturns.isLoading}
            rowKey={(r) => r.id}
            columns={[
              {
                header: t.sales.return,
                cell: (r) => <span className="od-mono font-medium">{r.number}</span>,
              },
              { header: t.common.date, cell: (r) => formatDate(r.created_at) },
              {
                header: t.sales.resolution,
                cell: (r) => <span className="capitalize">{r.resolution}</span>,
              },
              { header: t.common.reason, cell: (r) => r.reason || "—" },
              { header: t.sales.refund, align: "right", cell: (r) => formatMoney(r.refund_total) },
              { header: t.common.status, cell: (r) => <StatusBadge value={r.status} /> },
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
                            {t.common.approve}
                          </Button>
                          <Button
                            size="sm"
                            variant="outline"
                            disabled={decideReturn.isPending}
                            onClick={() => decideReturn.mutate({ id: r.id, action: "reject" })}
                          >
                            {t.common.reject}
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
                          {t.sales.complete}
                        </Button>
                      )}
                      {(r.status === "requested" || r.status === "approved") && canManageReturn && (
                        <Button
                          size="sm"
                          variant="ghost"
                          disabled={decideReturn.isPending}
                          onClick={() => decideReturn.mutate({ id: r.id, action: "cancel" })}
                        >
                          {t.common.cancel}
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
        title={t.sales.requestItemReturn}
        description={t.sales.enterTheQuantityComingBack}
        busy={ret.isPending}
        submitLabel={t.sales.submitReturnRequest}
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
                  {fmt(t.sales.max, { qty: formatQty(toNumber(i.qty) - toNumber(i.qty_returned)) })}
                </span>
              </span>
              <Input
                aria-label={fmt(t.sales.returnQuantityOf, { product_name: i.product_name })}
                inputMode="decimal"
                placeholder="0"
                value={itemId ? (qtys[itemId] ?? "") : ""}
                onChange={(e) => itemId && setQtys((q) => ({ ...q, [itemId]: e.target.value }))}
              />
              <NativeSelect
                aria-label={fmt(t.sales.conditionOf, { product_name: i.product_name })}
                value={cond}
                onChange={(e) =>
                  itemId &&
                  setConditions((c) => ({
                    ...c,
                    [itemId]: e.target.value as "resellable" | "damaged",
                  }))
                }
              >
                <option value="resellable">{t.sales.resellable}</option>
                <option value="damaged">{t.common.damaged}</option>
              </NativeSelect>
            </div>
          );
        })}
        <Field label={t.sales.resolution}>
          <NativeSelect
            value={resolution}
            onChange={(e) => setResolution(e.target.value as "refund" | "exchange")}
          >
            <option value="refund">{t.sales.refundMoney}</option>
            <option value="exchange">{t.sales.exchange}</option>
          </NativeSelect>
        </Field>
        <Field label={t.sales.reasonMin3Characters}>
          <Input
            required
            minLength={3}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
          />
        </Field>
        {lines.length === 0 && <p className="od-caption">{t.sales.enterAtLeastOneQuantity}</p>}
      </FormDialog>

      <FormDialog
        open={completing !== null}
        onOpenChange={(o) => !o && setCompleting(null)}
        title={fmt(t.sales.completeReturn, { number: completing?.number ?? "" })}
        submitLabel={t.sales.completeReturn2}
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
              {t.sales.refundAmount}{" "}
              <strong className="od-tabular text-[var(--od-ink)]">
                {formatMoney(completing.refund_total)}
              </strong>
            </p>
            <Field label={t.sales.refundMethod}>
              <NativeSelect value={refundMethod} onChange={(e) => setRefundMethod(e.target.value)}>
                <option value="cash">{t.common.cash}</option>
                <option value="mobile_money">{t.common.mobileMoney}</option>
                <option value="bank">{t.sales.bankTransfer}</option>
              </NativeSelect>
            </Field>
            <Field label={t.sales.refundFromAccount}>
              <NativeSelect required value={account} onChange={(e) => setAccount(e.target.value)}>
                <option value="">{t.common.choose}</option>
                {activeAccounts.map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.name}
                  </option>
                ))}
              </NativeSelect>
            </Field>
            <Field label={t.common.referenceOptional}>
              <Input value={refundRef} onChange={(e) => setRefundRef(e.target.value)} />
            </Field>
          </>
        ) : (
          <p className="text-[13px] text-[var(--od-ink-secondary)]">
            {t.sales.confirmCompletionOfThisExchange}
          </p>
        )}
      </FormDialog>

      <FormDialog
        open={voiding}
        onOpenChange={setVoiding}
        title={t.sales.voidThisSale}
        submitLabel={t.sales.voidSale}
        busy={voidSale.isPending}
        onSubmit={() => voidSale.mutate({ reason: voidReason })}
      >
        <p className="text-[13px] text-[var(--od-ink-secondary)]">{t.sales.stockGoesBackAndThe}</p>
        <Field label={t.common.reason}>
          <Input required value={voidReason} onChange={(e) => setVoidReason(e.target.value)} />
        </Field>
      </FormDialog>

      {id && (
        <SectionCard title={t.common.attachments2}>
          <AttachmentsSection entityType="sale" entityId={id} />
        </SectionCard>
      )}

      <SectionCard title={t.common.history} subtitle={t.sales.auditTrailForThisSale}>
        <RecordHistoryTab entityType="sale" entityId={id} />
      </SectionCard>
    </div>
  );
}
