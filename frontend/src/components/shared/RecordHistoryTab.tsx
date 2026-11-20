import { DataGrid } from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { useGet } from "@/hooks/useApi";
import { formatDateTime } from "@/lib/formatters";

interface AuditEntry {
  id: string;
  created_at: string;
  user_id?: string | null;
  user_name?: string | null;
  action: string;
  before?: unknown;
  after?: unknown;
}

function summarize(value: unknown): string {
  if (value === null || value === undefined) return "-";
  if (typeof value !== "object") return String(value);
  const entries = Object.entries(value as Record<string, unknown>).filter(
    ([, v]) => v !== null && v !== undefined && v !== "",
  );
  if (entries.length === 0) return "-";
  return entries
    .slice(0, 6)
    .map(([k, v]) => `${k}: ${typeof v === "object" ? JSON.stringify(v) : String(v)}`)
    .join(" · ");
}

/** Reusable record History tab (SRS Section 12: every record has a History tab with old & new values). */
export function RecordHistoryTab({
  entityType,
  entityId,
}: {
  entityType: string;
  entityId: string;
}) {
  const { can } = useAuth();
  const audit = useGet<AuditEntry[]>(
    `/audit-logs/entity/${entityType}/${entityId}`,
    { per_page: 50 },
    { enabled: can("security.audit.view") && !!entityId },
  );

  if (!can("security.audit.view")) {
    return (
      <p className="rounded-2xl border bg-card p-6 text-sm text-muted-foreground">
        Detailed change history is available to administrators.
      </p>
    );
  }

  return (
    <DataGrid
      rows={audit.data}
      loading={audit.isLoading}
      rowKey={(a) => a.id}
      empty="No history entries recorded yet"
      columns={[
        { header: "When", cell: (a) => formatDateTime(a.created_at) },
        { header: "Who", cell: (a) => a.user_name ?? a.user_id?.slice(0, 8) ?? "System" },
        { header: "Action", cell: (a) => <span className="font-medium">{a.action}</span> },
        {
          header: "Old value",
          cell: (a) => summarize(a.before),
          className: "max-w-xs whitespace-normal text-xs text-muted-foreground",
        },
        {
          header: "New value",
          cell: (a) => summarize(a.after),
          className: "max-w-xs whitespace-normal text-xs",
        },
      ]}
    />
  );
}
