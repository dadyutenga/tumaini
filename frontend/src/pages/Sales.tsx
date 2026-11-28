import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Download, Search, ShoppingCart } from "lucide-react";
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
  Pager,
  SegmentedChips,
  StatusBadge,
} from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { useDebounce } from "@/hooks/useDebounce";
import { useGet, useGetPage, useWrite } from "@/hooks/useApi";
import { downloadCsv } from "@/lib/csv";
import { formatDate, formatMoney, toNumber } from "@/lib/formatters";
import type { MoneyAccount, SaleRow } from "@/types";
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";
import { statusLabel } from "@/features/i18n/status";
import { useSearchBox } from "@/hooks/useSearchBox";

interface ReturnRow {
  id: string;
  number: string;
  sale_id: string;
  sale_number: string;
  status: string;
  resolution: "refund" | "exchange";
  reason: string;
  refund_total: string;
  requested_by?: string;
  created_at: string;
}

export default function Sales() {
  const { t } = useI18n();
  const { can, canAny, user } = useAuth();
  const canViewReturns = canAny("shop.sale.view_all", "shop.return.create", "shop.return.approve");
  const canApproveReturn = can("shop.return.approve");
  const canManageReturn = canAny("shop.return.create", "shop.return.approve");
  const [page, setPage] = useState(1);
  const [retPage, setRetPage] = useState(1);
  const [q, setQ] = useSearchBox(() => setPage(1));
  const [status, setStatus] = useState("");
  const [method, setMethod] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const search = useDebounce(q, 300);
  const navigate = useNavigate();

  const [completing, setCompleting] = useState<ReturnRow | null>(null);
  const [refundMethod, setRefundMethod] = useState("cash");
  const [account, setAccount] = useState("");
  const [refundRef, setRefundRef] = useState("");

  const sales = useGetPage<SaleRow>("/sales", {
    q: search,
    status,
    method,
    from,
    to,
    page,
    per_page: 25,
  });
  const returns = useGetPage<ReturnRow>(
    "/sale-returns",
    {
      from,
      to,
      page: retPage,
      per_page: 25,
    },
    { enabled: canViewReturns },
  );
  const accounts = useGet<MoneyAccount[]>("/money-accounts", undefined, {
    enabled: canManageReturn,
  });

  const refreshReturns = ["/sale-returns", "/sales", "/stock", "/money", "/customers"];
  const decideReturn = useWrite<{ id: string; action: "approve" | "reject" | "cancel" }, unknown>(
    "POST",
    (b) => `/sale-returns/${b.id}/${b.action}`,
    {
      toBody: () => ({}),
      invalidate: refreshReturns,
      success: t.sales.returnUpdated,
    },
  );
  const completeReturn = useWrite<Record<string, unknown>, unknown>(
    "POST",
    () => `/sale-returns/${completing?.id}/complete`,
    {
      idempotent: true,
      invalidate: refreshReturns,
      success: t.sales.returnCompleted,
      onSuccess: () => {
        setCompleting(null);
        setAccount("");
        setRefundRef("");
      },
    },
  );

  const rows = sales.data?.data ?? [];
  const activeAccounts = (accounts.data ?? []).filter((a) => a.is_active);
  const pageTotal = rows.reduce((s, r) => s + toNumber(r.total), 0);
  const pagePaid = rows.reduce((s, r) => s + toNumber(r.paid_total), 0);
  const pageBalance = rows.reduce((s, r) => s + toNumber(r.balance), 0);

  function exportCsv() {
    downloadCsv("sales-history.csv", [
      [
        t.sales.receiptNo,
        t.common.date,
        t.common.customer,
        t.sales.cashier2,
        t.common.status,
        t.common.total,
        t.common.paid,
        t.common.balance,
      ],
      ...rows.map((s) => [
        s.number,
        formatDate(s.business_date),
        s.customer_name ?? t.sales.walkIn,
        s.cashier_name ?? "",
        s.status,
        s.total,
        s.paid_total,
        s.balance,
      ]),
    ]);
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title={t.sales.salesReturns}
        description={t.sales.salesHistoryReceiptsAndReturns}
        actions={
          <>
            {rows.length > 0 && (
              <Button variant="outline" size="sm" onClick={exportCsv}>
                <Download size={14} /> {t.common.exportCsv}
              </Button>
            )}
            {can("shop.sale.create") && (
              <Button size="sm" onClick={() => navigate("/pos")}>
                <ShoppingCart size={14} /> {t.common.newSale}
              </Button>
            )}
          </>
        }
      />

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard
          label={t.sales.matchingSales}
          value={sales.data?.meta?.total ?? rows.length}
          trend={fmt(t.sales.showingOnPage, { count: rows.length })}
        />
        <StatCard label={t.sales.pageTotal} value={formatMoney(pageTotal)} />
        <StatCard
          label={t.sales.collected}
          value={formatMoney(pagePaid)}
          tone={pagePaid > 0 ? "good" : "neutral"}
        />
        <StatCard
          label={t.sales.outstandingBalance}
          value={formatMoney(pageBalance)}
          tone={pageBalance > 0 ? "bad" : "neutral"}
        />
      </div>

      <Tabs defaultValue="sales">
        <TabsList>
          <TabsTrigger value="sales">{t.sales.salesHistory}</TabsTrigger>
          {canViewReturns && <TabsTrigger value="returns">{t.sales.returns}</TabsTrigger>}
        </TabsList>

        <TabsContent value="sales" className="space-y-4">
          <FilterBar className="justify-between">
            <div className="flex flex-1 flex-wrap items-center gap-2.5">
              <div className="relative min-w-[220px] flex-1 sm:max-w-xs">
                <Search
                  size={15}
                  className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-[var(--od-ink-muted)]"
                />
                <Input
                  aria-label={t.sales.searchSales}
                  className="pl-9"
                  placeholder={t.sales.receiptCustomerOrPhone}
                  value={q}
                  onChange={(e) => {
                    setQ(e.target.value);
                    setPage(1);
                  }}
                />
              </div>
              <SegmentedChips
                ariaLabel={t.sales.saleStatus}
                value={status}
                onChange={(v) => {
                  setStatus(v);
                  setPage(1);
                }}
                options={[
                  { value: "", label: t.common.all },
                  { value: "completed", label: t.common.completed },
                  { value: "pending_approval", label: t.common.pending },
                  { value: "voided", label: t.sales.voided },
                ]}
              />
            </div>

            <div className="flex flex-wrap items-center gap-2">
              <NativeSelect
                aria-label={t.sales.paymentMethod}
                className="w-36"
                value={method}
                onChange={(e) => {
                  setMethod(e.target.value);
                  setPage(1);
                }}
              >
                <option value="">{t.sales.allMethods}</option>
                <option value="cash">{t.common.cash}</option>
                <option value="mobile_money">{t.common.mobileMoney}</option>
                <option value="bank">{t.common.bank}</option>
              </NativeSelect>
              <Input
                type="date"
                aria-label={t.common.fromDate}
                className="w-36"
                value={from}
                onChange={(e) => {
                  setFrom(e.target.value);
                  setPage(1);
                }}
              />
              <Input
                type="date"
                aria-label={t.common.toDate}
                className="w-36"
                value={to}
                onChange={(e) => {
                  setTo(e.target.value);
                  setPage(1);
                }}
              />
            </div>
          </FilterBar>

          <DataGrid
            rows={rows}
            loading={sales.isLoading}
            rowKey={(s) => s.id}
            empty={t.sales.noSalesMatchTheseFilters}
            onRowClick={(s) => navigate(`/sales/${s.id}`)}
            columns={[
              {
                header: t.common.receipt2,
                sortValue: (s) => s.number,
                cell: (s) => <span className="od-mono font-medium">{s.number}</span>,
              },
              {
                header: t.common.date,
                sortValue: (s) => s.business_date,
                cell: (s) => (
                  <span className="text-[var(--od-ink-secondary)]">
                    {formatDate(s.business_date)}
                  </span>
                ),
              },
              {
                header: t.common.customer,
                sortValue: (s) => s.customer_name ?? t.sales.walkIn,
                cell: (s) => s.customer_name ?? t.sales.walkIn,
              },
              {
                header: t.sales.cashier2,
                cell: (s) => (
                  <span className="text-[var(--od-ink-secondary)]">{s.cashier_name ?? "—"}</span>
                ),
              },
              { header: t.common.status, cell: (s) => <StatusBadge value={s.status} /> },
              {
                header: t.common.total,
                align: "right",
                sortValue: (s) => toNumber(s.total),
                cell: (s) => <span className="font-medium">{formatMoney(s.total)}</span>,
              },
              {
                header: t.common.paid,
                align: "right",
                sortValue: (s) => toNumber(s.paid_total),
                cell: (s) => formatMoney(s.paid_total),
              },
              {
                header: t.common.balance,
                align: "right",
                sortValue: (s) => toNumber(s.balance),
                cell: (s) => (
                  <span
                    className={
                      toNumber(s.balance) > 0 ? "font-semibold text-[var(--od-bad)]" : undefined
                    }
                  >
                    {formatMoney(s.balance)}
                  </span>
                ),
              },
            ]}
          />
          <Pager meta={sales.data?.meta} onPage={setPage} />
        </TabsContent>

        <TabsContent value="returns">
          <DataGrid
            rows={returns.data?.data}
            loading={returns.isLoading}
            rowKey={(r) => r.id}
            empty={t.sales.noReturnsRecordedYet}
            onRowClick={(r) => navigate(`/sales/${r.sale_id}`)}
            columns={[
              {
                header: t.sales.return,
                cell: (r) => <span className="od-mono font-medium">{r.number}</span>,
              },
              {
                header: t.sales.sale2,
                cell: (r) => (
                  <span className="od-mono text-[var(--od-ink-secondary)]">{r.sale_number}</span>
                ),
              },
              { header: t.common.date, cell: (r) => formatDate(r.created_at) },
              { header: t.common.reason, cell: (r) => r.reason || "—" },
              {
                header: t.sales.resolution,
                cell: (r) => <span className="capitalize">{statusLabel(t, r.resolution)}</span>,
              },
              { header: t.common.status, cell: (r) => <StatusBadge value={r.status} /> },
              {
                header: t.sales.refund,
                align: "right",
                cell: (r) => <span className="font-medium">{formatMoney(r.refund_total)}</span>,
              },
              {
                header: "",
                cell: (r) => {
                  const isOwn =
                    (Boolean(user?.membership_id) && r.requested_by === user?.membership_id) ||
                    (Boolean(user?.id) && r.requested_by === user?.id);
                  return (
                    <div className="flex justify-end gap-1.5" onClick={(e) => e.stopPropagation()}>
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
          <Pager meta={returns.data?.meta} onPage={setRetPage} />
        </TabsContent>
      </Tabs>

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
    </div>
  );
}
