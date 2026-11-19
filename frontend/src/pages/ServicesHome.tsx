import { Link } from "react-router-dom";
import { PageHeader } from "@/components/shared/kit";
import { PageLoader } from "@/components/shared/Display";
import { Tile } from "@/features/services/shared";
import { useGet } from "@/hooks/useApi";
import { formatDate, formatMoney, toNumber } from "@/lib/formatters";
import type { ServicesDashboard } from "@/types/services";

function Card({ title, to, children }: { title: string; to?: string; children: React.ReactNode }) {
  return (
    <section className="rounded-2xl border bg-card p-5">
      <div className="mb-3 flex items-center justify-between">
        <h2 className="font-semibold">{title}</h2>
        {to && (
          <Link className="text-xs text-primary underline" to={to}>
            Open
          </Link>
        )}
      </div>
      {children}
    </section>
  );
}

/** Business B at a glance. Each card appears only when the signed-in role may see that part. */
export default function ServicesHome() {
  const dash = useGet<ServicesDashboard>("/services/dashboard");
  if (dash.isLoading) return <PageLoader />;
  const d = dash.data ?? {};
  const stages = d.projects_by_stage ?? [];
  const max = Math.max(1, ...stages.map((s) => s.projects));

  return (
    <>
      <PageHeader
        title="Services"
        description="Projects, design work, money owed and what is due next."
      />
      <div className="mb-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
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
      <div className="grid gap-4 lg:grid-cols-2">
        {d.projects_by_stage && (
          <Card title="Projects by stage" to="/projects">
            {stages.length === 0 ? (
              <p className="text-sm text-muted-foreground">No open projects.</p>
            ) : (
              <ul className="space-y-2">
                {stages.map((s) => (
                  <li key={s.stage} className="text-sm">
                    <div className="mb-1 flex justify-between">
                      <span>{s.stage}</span>
                      <span className="tabular-nums text-muted-foreground">{s.projects}</span>
                    </div>
                    <div className="h-2 rounded-full bg-muted">
                      <div
                        className="h-2 rounded-full bg-primary"
                        style={{ width: `${(s.projects / max) * 100}%` }}
                      />
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </Card>
        )}
        {d.overdue_tasks && (
          <Card title="Overdue tasks" to="/service-tasks">
            {d.overdue_tasks.length === 0 ? (
              <p className="text-sm text-muted-foreground">Nothing is late.</p>
            ) : (
              <ul className="divide-y text-sm">
                {d.overdue_tasks.map((t) => (
                  <li key={t.id} className="flex items-center justify-between gap-3 py-2">
                    <span className="font-medium">{t.title}</span>
                    <span className="shrink-0 text-xs text-destructive">
                      {t.assignee_name ? `${t.assignee_name} · ` : ""}due {formatDate(t.due_date)}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </Card>
        )}
        {d.renewals_due && (
          <Card title="Renewals due in 30 days" to="/renewals">
            {d.renewals_due.length === 0 ? (
              <p className="text-sm text-muted-foreground">Nothing is due soon.</p>
            ) : (
              <ul className="divide-y text-sm">
                {d.renewals_due.map((r) => (
                  <li key={r.id} className="flex items-center justify-between gap-3 py-2">
                    <span>
                      <span className="font-medium">{r.client_name}</span> · {r.item}
                    </span>
                    <span
                      className={`shrink-0 text-xs ${r.days_left <= 7 ? "text-destructive" : "text-muted-foreground"}`}
                    >
                      {r.days_left < 0 ? `${-r.days_left} days late` : `${r.days_left} days`}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </Card>
        )}
      </div>
    </>
  );
}
