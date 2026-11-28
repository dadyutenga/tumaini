import { useState } from "react";
import { DatabaseBackup, Download, RefreshCw, ShieldAlert } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { DataGrid, PageHeader, SectionCard, StatusBadge } from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { errorMessage, useGet, useWrite } from "@/hooks/useApi";
import { download } from "@/lib/api";
import { formatDateTime } from "@/lib/formatters";
import { useI18n } from "@/features/i18n/i18n";

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
  const { t } = useI18n();
  const { me } = useAuth();
  const [refreshing, setRefreshing] = useState(false);
  const query = useGet<BackupsPayload>("/backups");
  const runNow = useWrite("POST", "/backups/export", {
    invalidate: ["/backups"],
    success: t.admin.backupExportStartedRefreshIn,
  });

  const exports = query.data?.exports ?? [];
  const lastAuto = query.data?.last_automatic_backup_at;

  return (
    <div className="space-y-6">
      <PageHeader
        title={t.common.backups}
        description={t.admin.automaticDailyBackupsAndOn}
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
              <RefreshCw size={14} className={refreshing ? "animate-spin" : undefined} />{" "}
              {t.admin.refresh}
            </Button>
            <Button size="sm" disabled={runNow.isPending} onClick={() => runNow.mutate(undefined)}>
              <DatabaseBackup size={14} /> {t.admin.runBackupNow}
            </Button>
          </div>
        }
      />

      <div className="grid gap-4 sm:grid-cols-2">
        <SectionCard
          title={t.admin.lastAutomaticNightlyBackup}
          subtitle={t.admin.scheduledHostDatabaseProtection}
        >
          <p className="od-tabular text-lg font-semibold text-[var(--od-ink)]">
            {lastAuto ? formatDateTime(lastAuto) : t.admin.configuredViaHostSchedule}
          </p>
          <p className="od-caption mt-2">
            {t.admin.nightlyPostgresqlDumpsAreRun}
            <code className="od-mono rounded border border-[var(--od-border)] bg-[var(--od-surface-subtle)] px-1.5 py-0.5 text-[11px] text-[var(--od-ink)]">
              deploy/backup.sh
            </code>
            ).
          </p>
        </SectionCard>
        {me?.is_owner && (
          <SectionCard
            title={t.admin.restoreABackupOwnerOnly}
            subtitle={t.admin.offlineRecoveryProcedure}
            actions={<ShieldAlert size={16} className="text-[var(--od-warning)]" />}
          >
            <p className="text-sm leading-relaxed text-[var(--od-ink-secondary)]">
              {t.admin.downloadAnyCompletedBackupArchive}{" "}
              <code className="od-mono rounded border border-[var(--od-border)] bg-[var(--od-surface-subtle)] px-1.5 py-0.5 text-xs text-[var(--od-ink)]">
                pg_restore
              </code>{" "}
              {t.admin.toPreventPartialTenantCorruption}
            </p>
          </SectionCard>
        )}
      </div>

      <DataGrid
        rows={exports}
        loading={query.isLoading}
        rowKey={(b) => b.id}
        empty={t.admin.noBackupsGeneratedYetClick}
        columns={[
          {
            header: t.common.requested,
            cell: (b) => <span className="od-tabular">{formatDateTime(b.created_at)}</span>,
          },
          {
            header: t.admin.finished,
            cell: (b) => (
              <span className="od-tabular">
                {b.finished_at ? formatDateTime(b.finished_at) : "-"}
              </span>
            ),
          },
          { header: t.admin.requestedBy, cell: (b) => b.user_name ?? "-" },
          {
            header: t.common.status,
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
                    <Download size={14} /> {t.admin.downloadZip}
                  </Button>
                </div>
              ) : null,
          },
        ]}
      />
    </div>
  );
}
