import { useState } from "react";
import { Download } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { DataGrid, Field, PageHeader, Pager } from "@/components/shared/kit";
import { useDebounce } from "@/hooks/useDebounce";
import { useGetPage } from "@/hooks/useApi";
import { downloadCsv } from "@/lib/csv";
import { formatDateTime } from "@/lib/formatters";

interface Entry {
  id: string;
  action: string;
  entity_type: string;
  entity_id: string | null;
  user_id: string | null;
  user_name?: string | null;
  before?: unknown;
  after?: unknown;
  created_at: string;
}

function summarize(value: unknown): string {
  if (value === null || value === undefined) return "-";
  if (typeof value !== "object") return String(value);
  const pairs = Object.entries(value as Record<string, unknown>).filter(
    ([, v]) => v !== null && v !== undefined && v !== "",
  );
  if (pairs.length === 0) return "-";
  return pairs
    .slice(0, 5)
    .map(([k, v]) => `${k}: ${typeof v === "object" ? JSON.stringify(v) : String(v)}`)
    .join(" · ");
}

export default function Audit() {
  const [action, setAction] = useState("");
  const [entityType, setEntityType] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [page, setPage] = useState(1);
  const debouncedAction = useDebounce(action, 300);
  const debouncedEntity = useDebounce(entityType, 300);
  const query = useGetPage<Entry>("/audit-logs", {
    action: debouncedAction,
    entity_type: debouncedEntity,
    from,
    to,
    page,
    per_page: 25,
  });
  const rows = query.data?.data ?? [];

  function exportCsv() {
    downloadCsv("audit-log.csv", [
      ["Time", "User", "Action", "Record", "Record ID", "Old value", "New value"],
      ...rows.map((e) => [
        formatDateTime(e.created_at),
        e.user_name ?? e.user_id ?? "System",
        e.action,
        e.entity_type,
        e.entity_id ?? "",
        summarize(e.before),
        summarize(e.after),
      ]),
    ]);
  }

  return (
    <>
      <PageHeader
        title="Audit log"
        description="Who did what, and when, with old and new values. Read only."
        actions={
          rows.length > 0 && (
            <Button variant="outline" size="sm" onClick={exportCsv}>
              <Download size={14} /> Export CSV
            </Button>
          )
        }
      />
      <div className="mb-4 flex flex-wrap items-end gap-3">
        <Field label="Action">
          <Input
            aria-label="Filter by action"
            placeholder="e.g. sale.create"
            value={action}
            onChange={(e) => {
              setAction(e.target.value);
              setPage(1);
            }}
          />
        </Field>
        <Field label="Module / record">
          <Input
            aria-label="Filter by module"
            placeholder="e.g. sale, svc_project, user"
            value={entityType}
            onChange={(e) => {
              setEntityType(e.target.value);
              setPage(1);
            }}
          />
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
        loading={query.isLoading}
        rowKey={(e) => e.id}
        empty="No activity recorded"
        columns={[
          { header: "Time", cell: (e) => formatDateTime(e.created_at) },
          {
            header: "User",
            cell: (e) => (
              <span className="font-medium">
                {e.user_name ?? e.user_id?.slice(0, 8) ?? "System"}
              </span>
            ),
          },
          { header: "Action", cell: (e) => <code className="text-xs">{e.action}</code> },
          {
            header: "Record",
            cell: (e) => (
              <span>
                {e.entity_type}
                {e.entity_id && (
                  <code className="ml-1 text-xs text-muted-foreground">
                    #{e.entity_id.slice(0, 8)}
                  </code>
                )}
              </span>
            ),
          },
          {
            header: "Old value",
            cell: (e) => summarize(e.before),
            className: "max-w-xs whitespace-normal text-xs text-muted-foreground",
          },
          {
            header: "New value",
            cell: (e) => summarize(e.after),
            className: "max-w-xs whitespace-normal text-xs",
          },
        ]}
      />
      <Pager meta={query.data?.meta} onPage={setPage} />
    </>
  );
}
