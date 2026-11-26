import { useState } from "react";
import { DatabaseBackup, Download, RefreshCw, ShieldAlert } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { DataGrid, PageHeader, SectionCard, StatusBadge } from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { errorMessage, useGet, useWrite } from "@/hooks/useApi";
import { download } from "@/lib/api";
import { formatDateTime } from "@/lib/formatters";

interface BackupExport {
  id: string;
  status: string;
  user_name?: string;
  file_id?: string | null;
  error?: string | null;
  created_at: string;
  finished_at?: string | null;
}

interface BackupsPayload {
  exports: BackupExport[];
  last_automatic_backup_at?: string | null;
}

export default function Backups() {
  const { me } = useAuth();
  const [refreshing, setRefreshing] = useState(false);
  const query = useGet<BackupsPayload>("/backups");
  const runNow = useWrite("POST", "/backups/export", {
    invalidate: ["/backups"],
    success: "Backup export started. Refresh in a moment to download.",
  });

  const exports = query.data?.exports ?? [];
  const lastAuto = query.data?.last_automatic_backup_at;

  return (
    <div className="space-y-6">
      <PageHeader
        title="Backups"
        description="Automatic daily backups and on-demand business data exports."
        actions={
          <div className="flex gap-2">
            <Button
              variant="outline"
              size="sm"
              disabled={refreshing}
              onClick={async () => {
                setRefreshing(true);
                await query.refetch();
                setRefreshing(false);
              }}
            >
              <RefreshCw size={14} className={refreshing ? "animate-spin" : undefined} /> Refresh
            </Button>
            <Button size="sm" disabled={runNow.isPending} onClick={() => runNow.mutate(undefined)}>
              <DatabaseBackup size={14} /> Run backup now
            </Button>
          </div>
        }
      />

      <div className="grid gap-4 sm:grid-cols-2">
        <SectionCard
          title="Last automatic nightly backup"
          subtitle="Scheduled host database protection"
        >
          <p className="od-tabular text-lg font-semibold text-[var(--od-ink)]">
            {lastAuto ? formatDateTime(lastAuto) : "Configured via host schedule"}
          </p>
          <p className="od-caption mt-2">
            Nightly PostgreSQL dumps are run by the deployment script (
            <code className="od-mono rounded border border-[var(--od-border)] bg-[var(--od-surface-subtle)] px-1.5 py-0.5 text-[11px] text-[var(--od-ink)]">
              deploy/backup.sh
            </code>
            ).
          </p>
        </SectionCard>
        {me?.is_owner && (
          <SectionCard
            title="Restore a backup (Owner only)"
            subtitle="Offline recovery procedure"
            actions={<ShieldAlert size={16} className="text-[var(--od-warning)]" />}
          >
            <p className="text-sm leading-relaxed text-[var(--od-ink-secondary)]">
              Download any completed backup archive below for safekeeping. Full database restores
              are performed offline by the Owner using{" "}
              <code className="od-mono rounded border border-[var(--od-border)] bg-[var(--od-surface-subtle)] px-1.5 py-0.5 text-xs text-[var(--od-ink)]">
                pg_restore
              </code>{" "}
              to prevent partial tenant corruption while staff are selling.
            </p>
          </SectionCard>
        )}
      </div>

      <DataGrid
        rows={exports}
        loading={query.isLoading}
        rowKey={(b) => b.id}
        empty="No backups generated yet. Click Run backup now to create one."
        columns={[
          {
            header: "Requested",
            cell: (b) => <span className="od-tabular">{formatDateTime(b.created_at)}</span>,
          },
          {
            header: "Finished",
            cell: (b) => (
              <span className="od-tabular">
                {b.finished_at ? formatDateTime(b.finished_at) : "-"}
              </span>
            ),
          },
          { header: "Requested by", cell: (b) => b.user_name ?? "-" },
          {
            header: "Status",
            cell: (b) => (
              <div className="flex flex-wrap items-center gap-2">
                <StatusBadge
                  value={
                    b.status === "done"
                      ? "approved"
                      : b.status === "failed"
                        ? "rejected"
                        : "pending"
                  }
                />
                {b.error && <span className="text-xs text-[var(--od-bad)]">{b.error}</span>}
              </div>
            ),
          },
          {
            header: "",
            className: "text-right",
            cell: (b) =>
              b.status === "done" && b.file_id ? (
                <div className="flex justify-end">
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() =>
                      download(
                        `/files/${b.file_id}`,
                        {},
                        `business-backup-${b.created_at.slice(0, 10)}.zip`,
                      ).catch((e) => toast.error(errorMessage(e)))
                    }
                  >
                    <Download size={14} /> Download ZIP
                  </Button>
                </div>
              ) : null,
          },
        ]}
      />
    </div>
  );
}
