import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { DataGrid, PageHeader, Pager, StatusBadge } from "@/components/shared/kit";
import { useGetPage } from "@/hooks/useApi";
import { formatDate, formatMoney } from "@/lib/formatters";
import type { SaleRow } from "@/types";

export default function Sales() {
  const [page, setPage] = useState(1);
  const navigate = useNavigate();
  const { data, isLoading } = useGetPage<SaleRow>("/sales", { page, per_page: 25 });
  return (
    <>
      <PageHeader title="Sales" description="Completed sales at the active branch, newest first." />
      <DataGrid
        rows={data?.data}
        loading={isLoading}
        rowKey={(s) => s.id}
        empty="No sales yet"
        onRowClick={(s) => navigate(`/sales/${s.id}`)}
        columns={[
          { header: "Number", cell: (s) => <span className="font-medium">{s.number}</span> },
          { header: "Date", cell: (s) => formatDate(s.business_date) },
          { header: "Status", cell: (s) => <StatusBadge value={s.status} /> },
          { header: "Total", align: "right", cell: (s) => formatMoney(s.total) },
          { header: "Paid", align: "right", cell: (s) => formatMoney(s.paid_total) },
          { header: "Balance", align: "right", cell: (s) => formatMoney(s.balance) },
        ]}
      />
      <Pager meta={data?.meta} onPage={setPage} />
    </>
  );
}
