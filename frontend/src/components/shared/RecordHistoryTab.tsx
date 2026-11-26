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
      <div className="od-card p-6 text-sm text-[var(--od-ink-secondary)]">
        Detailed change history is available to administrators.
      </div>
    );
  }

  return (
    <DataGrid
      rows={audit.data}
      loading={audit.isLoading}
      rowKey={(a) => a.id}
      empty="No history entries recorded yet"
      columns={[
        {
          header: "When",
          cell: (a) => (
            <span className="od-tabular text-xs text-[var(--od-ink-secondary)]">
              {formatDateTime(a.created_at)}
            </span>
          ),
        },
        {
          header: "Who",
          cell: (a) => (
            <span className="font-medium text-[var(--od-ink)]">
              {a.user_name ?? a.user_id?.slice(0, 8) ?? "System"}
            </span>
          ),
        },
        {
          header: "Action",
          cell: (a) => (
            <code className="od-mono rounded-md border border-[var(--od-border)] bg-[var(--od-surface-subtle)] px-1.5 py-0.5 text-xs text-[var(--od-ink)]">
              {a.action}
            </code>
          ),
        },
        {
          header: "Old value",
          cell: (a) => summarize(a.before),
          className: "max-w-xs whitespace-normal text-xs text-[var(--od-ink-muted)]",
        },
        {
          header: "New value",
          cell: (a) => summarize(a.after),
          className: "max-w-xs whitespace-normal text-xs text-[var(--od-ink-secondary)]",
        },
      ]}
    />
  );
}
