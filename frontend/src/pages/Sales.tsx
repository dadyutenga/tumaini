import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Download } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  DataGrid,
  Field,
  NativeSelect,
  PageHeader,
  Pager,
  StatusBadge,
} from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { useDebounce } from "@/hooks/useDebounce";
import { useGetPage } from "@/hooks/useApi";
import { downloadCsv } from "@/lib/csv";
import { formatDate, formatMoney } from "@/lib/formatters";
import type { SaleRow } from "@/types";

interface ReturnRow {
  id: string;
  number: string;
  sale_id: string;
  sale_number: string;
  status: string;
  reason: string;
  refund_method: string;
  refund_total: string;
  created_at: string;
}

export default function Sales() {
  const { canAny } = useAuth();
  const canViewReturns = canAny("shop.sale.view_all", "shop.return.create", "shop.return.approve");
  const [page, setPage] = useState(1);
  const [retPage, setRetPage] = useState(1);
  const [q, setQ] = useState("");
  const [status, setStatus] = useState("");
  const [method, setMethod] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const search = useDebounce(q, 300);
  const navigate = useNavigate();

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

  const rows = sales.data?.data ?? [];

  function exportCsv() {
    downloadCsv("sales-history.csv", [
      ["Receipt No", "Date", "Customer", "Cashier", "Status", "Total", "Paid", "Balance"],
      ...rows.map((s) => [
        s.number,
        formatDate(s.business_date),
        s.customer_name ?? "Walk-in",
        s.cashier_name ?? "",
        s.status,
        s.total,
        s.paid_total,
        s.balance,
      ]),
    ]);
  }

  return (
    <>
      <PageHeader
        title="Sales & returns"
        description="Sales history, receipts and returns at the active location."
        actions={
          rows.length > 0 && (
            <Button variant="outline" size="sm" onClick={exportCsv}>
              <Download size={14} /> Export CSV
            </Button>
          )
        }
      />

      <Tabs defaultValue="sales">
        <TabsList className="mb-4">
          <TabsTrigger value="sales">Sales history</TabsTrigger>
          {canViewReturns && <TabsTrigger value="returns">Returns</TabsTrigger>}
        </TabsList>

        <TabsContent value="sales">
          <div className="mb-4 flex flex-wrap items-end gap-3">
            <Field label="Search">
              <Input
                placeholder="Receipt #, customer or phone…"
                value={q}
                onChange={(e) => {
                  setQ(e.target.value);
                  setPage(1);
                }}
              />
            </Field>
            <Field label="Status">
              <NativeSelect
                value={status}
                onChange={(e) => {
                  setStatus(e.target.value);
                  setPage(1);
                }}
              >
                <option value="">All statuses</option>
                <option value="completed">Completed</option>
                <option value="pending_approval">Waiting approval</option>
                <option value="partially_refunded">Partially refunded</option>
                <option value="refunded">Refunded</option>
                <option value="voided">Voided</option>
              </NativeSelect>
            </Field>
            <Field label="Payment">
              <NativeSelect
                value={method}
                onChange={(e) => {
                  setMethod(e.target.value);
                  setPage(1);
                }}
              >
                <option value="">All methods</option>
                <option value="cash">Cash</option>
                <option value="mobile_money">Mobile money</option>
                <option value="bank">Bank</option>
              </NativeSelect>
            </Field>
            <Field label="From">
              <Input
                type="date"
                value={from}
                onChange={(e) => {
                  setFrom(e.target.value);
                  setPage(1);
                }}
              />
            </Field>
            <Field label="To">
              <Input
                type="date"
                value={to}
                onChange={(e) => {
                  setTo(e.target.value);
                  setPage(1);
                }}
              />
            </Field>
          </div>
          <DataGrid
            rows={rows}
            loading={sales.isLoading}
            rowKey={(s) => s.id}
            empty="No sales match these filters"
            onRowClick={(s) => navigate(`/sales/${s.id}`)}
            columns={[
              { header: "Receipt", cell: (s) => <span className="font-medium">{s.number}</span> },
              { header: "Date", cell: (s) => formatDate(s.business_date) },
              { header: "Customer", cell: (s) => s.customer_name ?? "Walk-in" },
              { header: "Cashier", cell: (s) => s.cashier_name ?? "-" },
              { header: "Status", cell: (s) => <StatusBadge value={s.status} /> },
              { header: "Total", align: "right", cell: (s) => formatMoney(s.total) },
              { header: "Paid", align: "right", cell: (s) => formatMoney(s.paid_total) },
              { header: "Balance", align: "right", cell: (s) => formatMoney(s.balance) },
            ]}
          />
          <Pager meta={sales.data?.meta} onPage={setPage} />
        </TabsContent>

        <TabsContent value="returns">
          <DataGrid
            rows={returns.data?.data}
            loading={returns.isLoading}
            rowKey={(r) => r.id}
            empty="No returns recorded yet"
            onRowClick={(r) => navigate(`/sales/${r.sale_id}`)}
            columns={[
              { header: "Return #", cell: (r) => <span className="font-medium">{r.number}</span> },
              { header: "Sale #", cell: (r) => r.sale_number },
              { header: "Date", cell: (r) => formatDate(r.created_at) },
              { header: "Reason", cell: (r) => r.reason || "-" },
              { header: "Refund method", cell: (r) => r.refund_method },
              { header: "Status", cell: (r) => <StatusBadge value={r.status} /> },
              { header: "Refund", align: "right", cell: (r) => formatMoney(r.refund_total) },
            ]}
          />
          <Pager meta={returns.data?.meta} onPage={setRetPage} />
        </TabsContent>
      </Tabs>
    </>
  );
}
