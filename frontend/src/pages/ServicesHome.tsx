import { Link } from "react-router-dom";
import { PageHeader, SectionCard } from "@/components/shared/kit";
import { PageLoader } from "@/components/shared/Display";
import { Tile } from "@/features/services/shared";
import { useGet } from "@/hooks/useApi";
import { formatDate, formatMoney, toNumber } from "@/lib/formatters";
import type { ServicesDashboard } from "@/types/services";

/** Business B at a glance. Each card appears only when the signed-in role may see that part. */
export default function ServicesHome() {
  const dash = useGet<ServicesDashboard>("/services/dashboard");
  if (dash.isLoading) return <PageLoader />;
  const d = dash.data ?? {};
  const stages = d.projects_by_stage ?? [];
  const max = Math.max(1, ...stages.map((s) => s.projects));

  return (
    <div className="space-y-6">
      <PageHeader
        title="Services"
        description="Projects, design work, receivables and upcoming renewals."
      />

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {d.late_projects !== undefined && (
          <Tile
            label="Late projects"
            value={d.late_projects}
            tone={d.late_projects > 0 ? "bad" : undefined}
          />
        )}
        {d.overdue_task_count !== undefined && (
          <Tile
            label="Overdue tasks"
            value={d.overdue_task_count}
            tone={d.overdue_task_count > 0 ? "bad" : undefined}
          />
        )}
        {d.debts && <Tile label="Clients owe us" value={formatMoney(d.debts.owed_to_us)} />}
        {d.debts && (
          <Tile
            label={`Overdue (${d.debts.overdue_invoices} invoices)`}
            value={formatMoney(d.debts.overdue)}
            tone={toNumber(d.debts.overdue) > 0 ? "bad" : undefined}
          />
        )}
        {d.we_owe_contractors !== undefined && (
          <Tile label="We owe contractors" value={formatMoney(d.we_owe_contractors)} />
        )}
        {d.design && <Tile label="Design in progress" value={d.design.in_progress} />}
        {d.design && <Tile label="Waiting for client" value={d.design.waiting_client} />}
        {d.design && (
          <Tile
            label="Design due today · changes asked"
            value={`${d.design.due_today} · ${d.design.changes_requested}`}
            tone={d.design.overdue > 0 ? "bad" : undefined}
          />
        )}
      </div>

      <div className="grid gap-5 lg:grid-cols-2">
        {d.projects_by_stage && (
          <SectionCard
            title="Projects by stage"
            subtitle="Open work distribution"
            actions={
              <Link
                className="text-xs font-medium text-[var(--od-ink-secondary)] underline underline-offset-4 hover:text-[var(--od-ink)]"
                to="/projects"
              >
                Open board
              </Link>
            }
          >
            {stages.length === 0 ? (
              <p className="od-caption py-2">No open projects.</p>
            ) : (
              <ul className="space-y-3 pt-1 text-[13px]">
                {stages.map((s) => (
                  <li key={s.stage}>
                    <div className="mb-1.5 flex justify-between text-xs">
                      <span className="font-medium text-[var(--od-ink)]">{s.stage}</span>
                      <span className="od-tabular font-semibold text-[var(--od-ink-secondary)]">
                        {s.projects}
                      </span>
                    </div>
                    <div className="h-2 overflow-hidden rounded-full bg-[var(--od-neutral-soft)]">
                      <div
                        className="h-full rounded-full"
                        style={{
                          width: `${(s.projects / max) * 100}%`,
                          backgroundColor: "var(--od-series-1)",
                        }}
                      />
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </SectionCard>
        )}

        {d.overdue_tasks && (
          <SectionCard
            title="Overdue tasks"
            subtitle="Tasks past their due date"
            actions={
              <Link
                className="text-xs font-medium text-[var(--od-ink-secondary)] underline underline-offset-4 hover:text-[var(--od-ink)]"
                to="/service-tasks"
              >
                Open tasks
              </Link>
            }
          >
            {d.overdue_tasks.length === 0 ? (
              <p className="od-caption py-2">Nothing is late.</p>
            ) : (
              <ul className="divide-y divide-[var(--od-border)] text-[13px]">
                {d.overdue_tasks.map((t) => (
                  <li key={t.id} className="flex items-center justify-between gap-3 py-2.5">
                    <span className="truncate font-medium text-[var(--od-ink)]">{t.title}</span>
                    <span className="shrink-0 text-xs font-medium text-[var(--od-bad)]">
                      {t.assignee_name ? `${t.assignee_name} · ` : ""}due {formatDate(t.due_date)}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </SectionCard>
        )}

        {d.renewals_due && (
          <SectionCard
            title="Renewals due in 30 days"
            subtitle="Upcoming recurring service renewals"
            actions={
              <Link
                className="text-xs font-medium text-[var(--od-ink-secondary)] underline underline-offset-4 hover:text-[var(--od-ink)]"
                to="/renewals"
              >
                Open renewals
              </Link>
            }
          >
            {d.renewals_due.length === 0 ? (
              <p className="od-caption py-2">Nothing is due soon.</p>
            ) : (
              <ul className="divide-y divide-[var(--od-border)] text-[13px]">
                {d.renewals_due.map((r) => (
                  <li key={r.id} className="flex items-center justify-between gap-3 py-2.5">
                    <span className="min-w-0 truncate">
                      <span className="font-medium text-[var(--od-ink)]">{r.client_name}</span>
                      <span className="text-[var(--od-ink-secondary)]"> · {r.item}</span>
                    </span>
                    <span
                      className={`od-tabular shrink-0 text-xs font-medium ${
                        r.days_left <= 7 ? "text-[var(--od-bad)]" : "text-[var(--od-ink-muted)]"
                      }`}
                    >
                      {r.days_left < 0 ? `${-r.days_left} days late` : `${r.days_left} days`}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </SectionCard>
        )}
      </div>
    </div>
  );
}
