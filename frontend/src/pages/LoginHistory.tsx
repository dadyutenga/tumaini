import { useState } from "react";
import { Download, RotateCcw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  DataGrid,
  Field,
  FilterBar,
  PageHeader,
  Pager,
  StatusBadge,
} from "@/components/shared/kit";
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
    <div className="space-y-6">
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
      <FilterBar className="items-end">
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
        {(from || to) && (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => {
              setFrom("");
              setTo("");
              setPage(1);
            }}
          >
            <RotateCcw size={13} /> Clear
          </Button>
        )}
      </FilterBar>
      <DataGrid
        rows={rows}
        loading={query.isLoading}
        rowKey={(e) => e.id}
        empty="No sign-in attempts recorded"
        columns={[
          {
            header: "Time",
            cell: (e) => (
              <span className="od-tabular text-xs text-[var(--od-ink-secondary)]">
                {formatDateTime(e.created_at)}
              </span>
            ),
          },
          {
            header: "User / identifier",
            cell: (e) => (
              <span className="od-mono font-medium text-[var(--od-ink)]">{e.identifier}</span>
            ),
          },
          {
            header: "Result",
            cell: (e) => (
              <div className="flex flex-wrap items-center gap-2">
                <StatusBadge value={e.success ? "active" : "rejected"} />
                {!e.success && e.reason && (
                  <span className="text-xs text-[var(--od-bad)]">{e.reason}</span>
                )}
              </div>
            ),
          },
          {
            header: "IP address",
            cell: (e) => (
              <span className="od-mono text-xs text-[var(--od-ink-secondary)]">{e.ip ?? "-"}</span>
            ),
          },
          {
            header: "Device",
            cell: (e) => (
              <span
                className="line-clamp-1 max-w-xs text-xs text-[var(--od-ink-muted)]"
                title={e.user_agent ?? ""}
              >
                {e.user_agent || "-"}
              </span>
            ),
          },
        ]}
      />
      <Pager meta={query.data?.meta} onPage={setPage} />
    </div>
  );
}
