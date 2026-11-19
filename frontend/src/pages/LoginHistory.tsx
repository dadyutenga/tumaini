import { useState } from "react";
import { Download } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { DataGrid, Field, PageHeader, Pager, StatusBadge } from "@/components/shared/kit";
import { useGetPage } from "@/hooks/useApi";
import { downloadCsv } from "@/lib/csv";
import { formatDateTime } from "@/lib/formatters";

interface LoginEvent {
  id: string;
  user_id: string | null;
  identifier: string;
  success: boolean;
  reason: string | null;
  ip: string | null;
  user_agent: string | null;
  created_at: string;
}

export default function LoginHistory() {
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [page, setPage] = useState(1);
  const query = useGetPage<LoginEvent>("/login-history", { from, to, page, per_page: 25 });
  const rows = query.data?.data ?? [];

  function exportCsv() {
    downloadCsv("login-history.csv", [
      ["Time", "Identifier", "Result", "IP", "Device"],
      ...rows.map((r) => [
        formatDateTime(r.created_at),
        r.identifier,
        r.success ? "Success" : `Failed (${r.reason ?? "error"})`,
        r.ip ?? "",
        r.user_agent ?? "",
      ]),
    ]);
  }

  return (
    <>
      <PageHeader
        title="Login history"
        description="Every sign-in attempt, including device, IP address and failures."
        actions={
          rows.length > 0 && (
            <Button variant="outline" size="sm" onClick={exportCsv}>
              <Download size={14} /> Export CSV
            </Button>
          )
        }
      />
      <div className="mb-4 flex flex-wrap items-end gap-3">
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
        loading={query.isLoading}
        rowKey={(e) => e.id}
        empty="No sign-in attempts recorded"
        columns={[
          { header: "Time", cell: (e) => formatDateTime(e.created_at) },
          { header: "User / identifier", cell: (e) => <span className="font-medium">{e.identifier}</span> },
          {
            header: "Result",
            cell: (e) => (
              <div className="flex items-center gap-2">
                <StatusBadge value={e.success ? "active" : "rejected"} />
                {!e.success && e.reason && (
                  <span className="text-xs text-muted-foreground">{e.reason}</span>
                )}
              </div>
            ),
          },
          { header: "IP address", cell: (e) => e.ip ?? "-" },
          {
            header: "Device",
            cell: (e) => (
              <span className="line-clamp-1 max-w-xs text-xs text-muted-foreground" title={e.user_agent ?? ""}>
                {e.user_agent || "-"}
              </span>
            ),
          },
        ]}
      />
      <Pager meta={query.data?.meta} onPage={setPage} />
    </>
  );
}
