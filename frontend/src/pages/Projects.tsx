import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Progress } from "@/components/ui/progress";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { DataGrid, NativeSelect, PageHeader, StatusBadge } from "@/components/shared/kit";
import { EmptyState, PageLoader } from "@/components/shared/Display";
import { useAuth } from "@/features/auth/auth";
import { NewProjectDialog } from "@/features/services/NewProjectDialog";
import { daysUntil } from "@/features/services/helpers";
import { useDebounce } from "@/hooks/useDebounce";
import { useGet } from "@/hooks/useApi";
import { formatDate, formatMoney, toNumber } from "@/lib/formatters";
import type { ProjectRow } from "@/types/services";

function Board() {
  const navigate = useNavigate();
  const board = useGet<{ stage: string; projects: ProjectRow[] }[]>("/projects/board");
  if (board.isLoading) return <PageLoader />;
  if (!board.data?.length) return <EmptyState title="No open projects" />;
  return (
    <div className="flex gap-4 overflow-x-auto pb-2">
      {board.data.map((col) => (
        <section key={col.stage} className="w-72 shrink-0 rounded-2xl border bg-muted/30 p-3">
          <h2 className="mb-3 flex items-center justify-between text-sm font-semibold">
            {col.stage}
            <span className="rounded-full bg-background px-2 py-0.5 text-xs text-muted-foreground">
              {col.projects.length}
            </span>
          </h2>
          <div className="space-y-2">
            {col.projects.map((p) => {
              const left = daysUntil(p.stage_deadline);
              const late = left !== null && left < 0 && p.stage_status !== "done";
              return (
                <button
                  key={p.id}
                  type="button"
                  onClick={() => navigate(`/projects/${p.id}`)}
                  className="w-full rounded-xl border bg-card p-3 text-left text-sm shadow-sm transition hover:border-primary"
                >
                  <p className="font-medium">{p.client_name}</p>
                  <p className="text-xs text-muted-foreground">{p.name}</p>
                  <Progress
                    className="mt-2 h-1.5"
                    value={p.stages_total ? (p.stages_done / p.stages_total) * 100 : 0}
                  />
                  <div className="mt-2 flex items-center justify-between text-xs">
                    <StatusBadge value={p.stage_status ?? p.status} />
                    {p.stage_deadline && (
                      <span
                        className={late ? "font-medium text-destructive" : "text-muted-foreground"}
                      >
                        {late ? `${-left!}d late` : `Due ${formatDate(p.stage_deadline)}`}
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
  // Staff only see the profit figures when the API sends them.
  return (
    <>
      <PageHeader
        title="Projects"
        description="Every client job, stage by stage."
        actions={
          can("projects.manage") && (
            <Button onClick={() => setCreating(true)}>
              <Plus size={16} /> New project
            </Button>
          )
        }
      />
      <Tabs defaultValue="table">
        <TabsList className="mb-4">
          <TabsTrigger value="table">Table</TabsTrigger>
          <TabsTrigger value="board">Board</TabsTrigger>
        </TabsList>
        <TabsContent value="table">
          <div className="mb-4 flex flex-wrap items-center gap-3">
            <Input
              aria-label="Search projects"
              className="max-w-xs"
              placeholder="Search project or client…"
              value={q}
              onChange={(e) => setQ(e.target.value)}
            />
            <NativeSelect
              aria-label="Status"
              className="w-44"
              value={status}
              onChange={(e) => setStatus(e.target.value)}
            >
              <option value="">All statuses</option>
              <option value="in_progress">In progress</option>
              <option value="on_hold">On hold</option>
              <option value="completed">Completed</option>
              <option value="cancelled">Cancelled</option>
            </NativeSelect>
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" checked={late} onChange={(e) => setLate(e.target.checked)} />
              Late only
            </label>
          </div>
          <DataGrid
            rows={projects.data}
            loading={projects.isLoading}
            rowKey={(p) => p.id}
            empty="No projects match"
            onRowClick={(p) => navigate(`/projects/${p.id}`)}
            columns={[
              { header: "Number", cell: (p) => p.number },
              {
                header: "Project",
                cell: (p) => (
                  <div>
                    <p className="font-medium">{p.name}</p>
                    <p className="text-xs text-muted-foreground">{p.client_name}</p>
                  </div>
                ),
              },
              {
                header: "Progress",
                className: "min-w-36",
                cell: (p) => (
                  <div>
                    <Progress
                      className="h-1.5"
                      value={p.stages_total ? (p.stages_done / p.stages_total) * 100 : 0}
                    />
                    <p className="mt-1 text-xs text-muted-foreground">
                      {p.stages_done} of {p.stages_total} · {p.current_stage ?? "done"}
                    </p>
                  </div>
                ),
              },
              { header: "Status", cell: (p) => <StatusBadge value={p.status} /> },
              { header: "Price", align: "right", cell: (p) => formatMoney(p.total_price) },
              { header: "Paid", align: "right", cell: (p) => formatMoney(p.paid) },
              {
                header: "Balance",
                align: "right",
                cell: (p) => (
                  <span className={toNumber(p.balance) > 0 ? "text-destructive" : ""}>
                    {formatMoney(p.balance)}
                  </span>
                ),
              },
              { header: "Profit", align: "right", cell: (p) => formatMoney(p.profit) },
            ]}
          />
        </TabsContent>
        <TabsContent value="board">
          <Board />
        </TabsContent>
      </Tabs>
      <NewProjectDialog open={creating} onOpenChange={setCreating} />
    </>
  );
}
