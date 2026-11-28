import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { AlertCircle, Plus, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Progress } from "@/components/ui/progress";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  DataGrid,
  FilterBar,
  PageHeader,
  SegmentedChips,
  StatusBadge,
} from "@/components/shared/kit";
import { EmptyState, PageLoader } from "@/components/shared/Display";
import { useAuth } from "@/features/auth/auth";
import { NewProjectDialog } from "@/features/services/NewProjectDialog";
import { daysUntil } from "@/features/services/helpers";
import { useDebounce } from "@/hooks/useDebounce";
import { useGet } from "@/hooks/useApi";
import { formatDate, formatMoney, toNumber } from "@/lib/formatters";
import { cn } from "@/lib/utils";
import type { ProjectRow } from "@/types/services";
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";

function Board() {
  const { t } = useI18n();
  const navigate = useNavigate();
  const board = useGet<{ stage: string; projects: ProjectRow[] }[]>("/projects/board");
  if (board.isLoading) return <PageLoader />;
  if (!board.data?.length) return <EmptyState title={t.services.noOpenProjects} />;
  return (
    <div className="scrollbar-thin flex gap-4 overflow-x-auto pb-2">
      {board.data.map((col) => (
        <section
          key={col.stage}
          className="w-72 shrink-0 rounded-[var(--od-radius,16px)] border border-[var(--od-border)] bg-[var(--od-surface-subtle)] p-3"
        >
          <h2 className="mb-3 flex items-center justify-between px-1 text-[13px] font-semibold text-[var(--od-ink)]">
            <span className="truncate">{col.stage}</span>
            <span className="od-tabular rounded-full bg-[var(--od-surface)] px-2 py-0.5 text-[11px] font-medium text-[var(--od-ink-secondary)] ring-1 ring-[var(--od-border-strong)]">
              {col.projects.length}
            </span>
          </h2>
          <div className="space-y-2.5">
            {col.projects.map((p) => {
              const left = daysUntil(p.stage_deadline);
              const late = left !== null && left < 0 && p.stage_status !== "done";
              return (
                <button
                  key={p.id}
                  type="button"
                  onClick={() => navigate(`/projects/${p.id}`)}
                  className="od-card-flush w-full cursor-pointer p-3.5 text-left text-[13px] transition-colors hover:border-[var(--od-ink-muted)]"
                >
                  <p className="font-semibold text-[var(--od-ink)]">{p.client_name}</p>
                  <p className="od-caption mt-0.5 truncate">{p.name}</p>
                  <Progress
                    className="mt-2.5 h-1.5"
                    value={p.stages_total ? (p.stages_done / p.stages_total) * 100 : 0}
                  />
                  <div className="mt-2.5 flex items-center justify-between gap-2 text-xs">
                    <StatusBadge value={p.stage_status ?? p.status} />
                    {p.stage_deadline && (
                      <span
                        className={
                          late ? "font-semibold text-[var(--od-bad)]" : "text-[var(--od-ink-muted)]"
                        }
                      >
                        {late
                          ? fmt(t.common.dLate, { value: -left! })
                          : fmt(t.services.due, { stage_deadline: formatDate(p.stage_deadline) })}
                      </span>
                    )}
                  </div>
                </button>
              );
            })}
          </div>
        </section>
      ))}
    </div>
  );
}

export default function Projects() {
  const { t } = useI18n();
  const { can } = useAuth();
  const navigate = useNavigate();
  const [q, setQ] = useState("");
  const [status, setStatus] = useState("");
  const [late, setLate] = useState(false);
  const search = useDebounce(q, 300);
  const projects = useGet<ProjectRow[]>("/projects", {
    q: search,
    status,
    late: late || undefined,
    per_page: 100,
  });
  const [creating, setCreating] = useState(false);

  return (
    <div className="space-y-6">
      <PageHeader
        title={t.common.projects}
        description={t.services.trackClientJobsStageMilestones}
        actions={
          can("services.project.manage") && (
            <Button size="sm" onClick={() => setCreating(true)}>
              <Plus size={14} /> {t.common.newProject}
            </Button>
          )
        }
      />

      <Tabs defaultValue="table">
        <TabsList>
          <TabsTrigger value="table">{t.common.table}</TabsTrigger>
          <TabsTrigger value="board">{t.common.board}</TabsTrigger>
        </TabsList>

        <TabsContent value="table" className="space-y-4">
          <FilterBar className="justify-between">
            <div className="flex flex-1 flex-wrap items-center gap-2.5">
              <div className="relative min-w-60 flex-1 sm:max-w-xs">
                <Search
                  size={15}
                  className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-[var(--od-ink-muted)]"
                />
                <Input
                  aria-label={t.services.searchProjects}
                  className="pl-9"
                  placeholder={t.services.searchProjectOrClient}
                  value={q}
                  onChange={(e) => setQ(e.target.value)}
                />
              </div>
              <button
                type="button"
                onClick={() => setLate((v) => !v)}
                className={cn(
                  "inline-flex h-9 cursor-pointer items-center gap-1.5 rounded-[var(--od-app-radius,10px)] border px-3 text-[13px] font-medium transition-colors",
                  late
                    ? "border-transparent bg-[color-mix(in_srgb,var(--od-bad-mark)_12%,transparent)] text-[var(--od-bad)]"
                    : "border-[var(--od-border-strong)] bg-[var(--od-surface)] text-[var(--od-ink-secondary)] hover:text-[var(--od-ink)]",
                )}
              >
                <AlertCircle size={14} /> {t.common.lateOnly}
              </button>
            </div>
            <SegmentedChips
              ariaLabel={t.services.projectStatus}
              value={status}
              onChange={setStatus}
              options={[
                { value: "", label: t.common.all },
                { value: "in_progress", label: t.services.inProgress },
                { value: "on_hold", label: t.services.onHold },
                { value: "completed", label: t.common.completed },
                { value: "cancelled", label: t.common.cancelled },
              ]}
            />
          </FilterBar>

          <DataGrid
            rows={projects.data}
            loading={projects.isLoading}
            rowKey={(p) => p.id}
            empty={t.services.noProjectsMatch}
            onRowClick={(p) => navigate(`/projects/${p.id}`)}
            columns={[
              {
                header: t.common.number,
                sortValue: (p) => p.number,
                cell: (p) => <span className="od-mono font-medium">{p.number}</span>,
              },
              {
                header: t.common.project,
                sortValue: (p) => p.name,
                cell: (p) => (
                  <div>
                    <p className="font-medium">{p.name}</p>
                    <p className="od-caption">{p.client_name}</p>
                  </div>
                ),
              },
              {
                header: t.common.progress,
                className: "min-w-36",
                cell: (p) => (
                  <div>
                    <Progress
                      className="h-1.5"
                      value={p.stages_total ? (p.stages_done / p.stages_total) * 100 : 0}
                    />
                    <p className="od-caption od-tabular mt-1">
                      {fmt(t.services.of, {
                        stages_done: p.stages_done,
                        stages_total: p.stages_total,
                        current_stage: p.current_stage ?? t.services.done2,
                      })}
                    </p>
                  </div>
                ),
              },
              { header: t.common.status, cell: (p) => <StatusBadge value={p.status} /> },
              {
                header: t.common.price,
                align: "right",
                sortValue: (p) => toNumber(p.total_price),
                cell: (p) => formatMoney(p.total_price),
              },
              {
                header: t.common.paid,
                align: "right",
                sortValue: (p) => toNumber(p.paid),
                cell: (p) => formatMoney(p.paid),
              },
              {
                header: t.common.balance,
                align: "right",
                sortValue: (p) => toNumber(p.balance),
                cell: (p) => (
                  <span
                    className={toNumber(p.balance) > 0 ? "font-semibold text-[var(--od-bad)]" : ""}
                  >
                    {formatMoney(p.balance)}
                  </span>
                ),
              },
              {
                header: t.common.profit,
                align: "right",
                sortValue: (p) => toNumber(p.profit),
                cell: (p) => (
                  <span className="font-medium text-[var(--od-good)]">{formatMoney(p.profit)}</span>
                ),
              },
            ]}
          />
        </TabsContent>

        <TabsContent value="board">
          <Board />
        </TabsContent>
      </Tabs>

      <NewProjectDialog open={creating} onOpenChange={setCreating} />
    </div>
  );
}
