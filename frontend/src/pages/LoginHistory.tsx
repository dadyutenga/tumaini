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
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";
import { statusLabel } from "@/features/i18n/status";

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
  const { t } = useI18n();
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [page, setPage] = useState(1);
  const query = useGetPage<LoginEvent>("/login-history", { from, to, page, per_page: 25 });
  const rows = query.data?.data ?? [];

  function exportCsv() {
    downloadCsv("login-history.csv", [
      [t.common.time, t.admin.identifier, t.common.result, "IP", t.common.device],
      ...rows.map((r) => [
        formatDateTime(r.created_at),
        r.identifier,
        r.success
          ? t.admin.success
          : fmt(t.admin.failedReason, { reason: statusLabel(t, r.reason ?? "error") }),
        r.ip ?? "",
        r.user_agent ?? "",
      ]),
    ]);
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title={t.common.loginHistory}
        description={t.admin.everySignInAttemptIncluding}
        actions={
          rows.length > 0 && (
            <Button variant="outline" size="sm" onClick={exportCsv}>
              <Download size={14} /> {t.common.exportCsv}
            </Button>
          )
        }
      />
      <FilterBar className="items-end">
        <Field label={t.common.from}>
          <Input
            type="date"
            value={from}
            onChange={(e) => {
              setFrom(e.target.value);
              setPage(1);
            }}
          />
        </Field>
        <Field label={t.common.to}>
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
            <RotateCcw size={13} /> {t.common.clear}
          </Button>
        )}
      </FilterBar>
      <DataGrid
        rows={rows}
        loading={query.isLoading}
        rowKey={(e) => e.id}
        empty={t.admin.noSignInAttemptsRecorded}
        columns={[
          {
            header: t.common.time,
            cell: (e) => (
              <span className="od-tabular text-xs text-[var(--od-ink-secondary)]">
                {formatDateTime(e.created_at)}
              </span>
            ),
          },
          {
            header: t.admin.userIdentifier,
            cell: (e) => (
              <span className="od-mono font-medium text-[var(--od-ink)]">{e.identifier}</span>
            ),
          },
          {
            header: t.common.result,
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
            header: t.admin.ipAddress,
            cell: (e) => (
              <span className="od-mono text-xs text-[var(--od-ink-secondary)]">{e.ip ?? "-"}</span>
            ),
          },
          {
            header: t.common.device,
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
