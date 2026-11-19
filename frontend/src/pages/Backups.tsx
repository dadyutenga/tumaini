import { useState } from "react";
import { DatabaseBackup, Download, RefreshCw } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { DataGrid, PageHeader, StatusBadge } from "@/components/shared/kit";
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
    <>
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
              <RefreshCw size={14} /> Refresh
            </Button>
            <Button size="sm" disabled={runNow.isPending} onClick={() => runNow.mutate(undefined)}>
              <DatabaseBackup size={14} /> Run backup now
            </Button>
          </div>
        }
      />

      <div className="mb-6 grid gap-4 sm:grid-cols-2">
        <div className="rounded-2xl border bg-card p-5">
          <p className="text-xs uppercase tracking-wide text-muted-foreground">
            Last automatic nightly backup
          </p>
          <p className="mt-1 text-lg font-semibold">
            {lastAuto ? formatDateTime(lastAuto) : "Configured via host schedule"}
          </p>
          <p className="mt-1 text-xs text-muted-foreground">
            Nightly PostgreSQL dumps are run by the deployment script (<code>deploy/backup.sh</code>).
          </p>
        </div>
        {me?.is_owner && (
          <div className="rounded-2xl border bg-card p-5">
            <p className="text-xs uppercase tracking-wide text-muted-foreground">
              Restore a backup (Owner only)
            </p>
            <p className="mt-1 text-sm text-muted-foreground">
              Download any completed backup archive below for safekeeping. Full database restores are
              performed offline by the Owner using <code>pg_restore</code> to prevent partial tenant
              corruption while staff are selling.
            </p>
          </div>
        )}
      </div>

      <DataGrid
        rows={exports}
        loading={query.isLoading}
        rowKey={(b) => b.id}
        empty="No backups generated yet. Click Run backup now to create one."
        columns={[
          { header: "Requested", cell: (b) => formatDateTime(b.created_at) },
          { header: "Finished", cell: (b) => (b.finished_at ? formatDateTime(b.finished_at) : "-") },
          { header: "Requested by", cell: (b) => b.user_name ?? "-" },
          {
            header: "Status",
            cell: (b) => (
              <div className="flex items-center gap-2">
                <StatusBadge
                  value={b.status === "done" ? "approved" : b.status === "failed" ? "rejected" : "pending"}
                />
                {b.error && <span className="text-xs text-destructive">{b.error}</span>}
              </div>
            ),
          },
          {
            header: "",
            className: "text-right",
            cell: (b) =>
              b.status === "done" && b.file_id ? (
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
              ) : null,
          },
        ]}
      />
    </>
  );
}
