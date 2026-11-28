import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { AlertCircle, ChevronLeft, ChevronRight, Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import {
  DataGrid,
  Field,
  FilterBar,
  FormDialog,
  NativeSelect,
  PageHeader,
  SegmentedChips,
  StatusBadge,
} from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { StaffSelect } from "@/features/services/shared";
import { label, useStaff } from "@/features/services/helpers";
import { useGet, useWrite } from "@/hooks/useApi";
import { formatDate, today } from "@/lib/formatters";
import { cn } from "@/lib/utils";
import type { ProjectRow, Task } from "@/types/services";
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";
import { intlLocale } from "@/features/i18n/active";

const blank = {
  title: "",
  description: "",
  assignee_id: "",
  project_id: "",
  due_date: "",
  priority: "normal",
};

function TaskForm({ onClose }: { onClose: () => void }) {
  const { t } = useI18n();
  const [d, setD] = useState(blank);
  const projects = useGet<ProjectRow[]>("/projects", { status: "in_progress", per_page: 100 });
  const create = useWrite("POST", "/tasks", {
    invalidate: ["/tasks", "/services"],
    success: t.services.taskAdded,
    onSuccess: onClose,
  });
  return (
    <FormDialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={t.common.newTask}
      busy={create.isPending}
      onSubmit={() =>
        create.mutate({
          title: d.title,
          description: d.description || undefined,
          assignee_id: d.assignee_id || undefined,
          project_id: d.project_id || undefined,
          due_date: d.due_date || undefined,
          priority: d.priority,
        })
      }
    >
      <Field label={t.services.task}>
        <Input
          required
          placeholder={t.services.callTheClientAboutThe}
          value={d.title}
          onChange={(e) => setD({ ...d, title: e.target.value })}
        />
      </Field>
      <Field label={t.common.details}>
        <Textarea
          rows={2}
          value={d.description}
          onChange={(e) => setD({ ...d, description: e.target.value })}
        />
      </Field>
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label={t.common.dueDate}>
          <Input
            type="date"
            value={d.due_date}
            onChange={(e) => setD({ ...d, due_date: e.target.value })}
          />
        </Field>
        <Field label={t.common.priority}>
          <NativeSelect
            value={d.priority}
            onChange={(e) => setD({ ...d, priority: e.target.value })}
          >
            <option value="normal">{t.common.normal}</option>
            <option value="urgent">{t.common.urgent}</option>
          </NativeSelect>
        </Field>
        <Field label={t.common.project}>
          <NativeSelect
            value={d.project_id}
            onChange={(e) => setD({ ...d, project_id: e.target.value })}
          >
            <option value="">{t.common.none}</option>
            {projects.data?.map((p) => (
              <option key={p.id} value={p.id}>
                {p.number} · {p.client_name}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field label={t.services.assignTo}>
          <StaffSelect
            value={d.assignee_id}
            onChange={(v) => setD({ ...d, assignee_id: v })}
            empty={t.common.me}
          />
        </Field>
      </div>
    </FormDialog>
  );
}

function Calendar({ tasks }: { tasks: Task[] }) {
  const { t } = useI18n();
  const [month, setMonth] = useState(() => {
    const item = new Date();
    return new Date(item.getFullYear(), item.getMonth(), 1);
  });
  const byDay = useMemo(() => {
    const map = new Map<string, Task[]>();
    for (const item of tasks) {
      if (!item.due_date) continue;
      map.set(item.due_date, [...(map.get(item.due_date) ?? []), item]);
    }
    return map;
  }, [tasks]);
  const first = new Date(month.getFullYear(), month.getMonth(), 1);
  const offset = (first.getDay() + 6) % 7; // week starts on Monday
  const days = new Date(month.getFullYear(), month.getMonth() + 1, 0).getDate();
  const cells = Array.from(
    { length: Math.ceil((offset + days) / 7) * 7 },
    (_, i) => i - offset + 1,
  );
  const pad = (n: number) => String(n).padStart(2, "0");
  const key = (d: number) => `${month.getFullYear()}-${pad(month.getMonth() + 1)}-${pad(d)}`;
  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2">
        <Button
          variant="outline"
          size="icon"
          aria-label={t.services.previousMonth}
          onClick={() => setMonth(new Date(month.getFullYear(), month.getMonth() - 1, 1))}
        >
          <ChevronLeft size={15} />
        </Button>
        <h2 className="w-44 text-center text-[14px] font-semibold">
          {month.toLocaleString(intlLocale(), { month: "long", year: "numeric" })}
        </h2>
        <Button
          variant="outline"
          size="icon"
          aria-label={t.services.nextMonth}
          onClick={() => setMonth(new Date(month.getFullYear(), month.getMonth() + 1, 1))}
        >
          <ChevronRight size={15} />
        </Button>
      </div>
      <div className="od-card-flush grid grid-cols-7 gap-px overflow-hidden bg-[var(--od-border-strong)] text-xs">
        {[
          t.services.mon,
          t.services.tue,
          t.services.wed,
          t.services.thu,
          t.services.fri,
          t.services.sat,
          t.services.sun,
        ].map((d) => (
          <div
            key={d}
            className="bg-[var(--od-surface-subtle)] px-2.5 py-2 text-[11px] font-semibold text-[var(--od-ink-muted)]"
          >
            {d}
          </div>
        ))}
        {cells.map((d, i) => {
          const inMonth = d >= 1 && d <= days;
          const isToday = inMonth && key(d) === today();
          const items = inMonth ? (byDay.get(key(d)) ?? []) : [];
          return (
            <div
              key={i}
              className={cn(
                "min-h-24 bg-[var(--od-surface)] p-2",
                !inMonth && "bg-[var(--od-surface-subtle)]/50",
              )}
            >
              {inMonth && (
                <>
                  <p className="mb-1.5">
                    <span
                      className={cn(
                        "od-tabular inline-flex h-5 min-w-5 items-center justify-center rounded-full px-1 text-[11px] font-medium",
                        isToday
                          ? "bg-[var(--od-app-primary)] font-semibold text-[var(--od-app-primary-foreground)]"
                          : "text-[var(--od-ink-secondary)]",
                      )}
                    >
                      {d}
                    </span>
                  </p>
                  <div className="space-y-1">
                    {items.slice(0, 3).map((item) => (
                      <p
                        key={item.id}
                        title={item.title}
                        className={cn(
                          "truncate rounded px-1.5 py-0.5 text-[11px] font-medium",
                          item.status === "done"
                            ? "bg-[color-mix(in_srgb,var(--od-good-mark)_12%,transparent)] text-[var(--od-good)] line-through"
                            : item.overdue
                              ? "bg-[color-mix(in_srgb,var(--od-bad-mark)_12%,transparent)] text-[var(--od-bad)]"
                              : "bg-[var(--od-warning-soft)] text-[var(--od-warning)]",
                        )}
                      >
                        {item.title}
                      </p>
                    ))}
                    {items.length > 3 && (
                      <p className="od-caption">
                        {fmt(t.services.more, { count: items.length - 3 })}
                      </p>
                    )}
                  </div>
                </>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}

export default function ServiceTasks() {
  const { t } = useI18n();
  const { can } = useAuth();
  const [status, setStatus] = useState("");
  const [assignee, setAssignee] = useState("");
  const [overdue, setOverdue] = useState(false);
  const [adding, setAdding] = useState(false);
  const staff = useStaff();
  const tasks = useGet<Task[]>("/tasks", {
    status,
    assignee_id: assignee,
    overdue: overdue || undefined,
    per_page: 200,
  });
  const setTaskStatus = useWrite<{ id: string; status: string }>("PUT", (b) => `/tasks/${b.id}`, {
    invalidate: ["/tasks", "/services"],
    toBody: (b) => ({ status: b.status }),
  });
  const manage = can("services.task.manage");

  return (
    <div className="space-y-6">
      <PageHeader
        title={t.common.tasksAndCalendar}
        description={t.services.assignFollowUpsTrackDeadlines}
        actions={
          manage && (
            <Button size="sm" onClick={() => setAdding(true)}>
              <Plus size={14} /> {t.common.newTask}
            </Button>
          )
        }
      />

      <Tabs defaultValue="list">
        <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
          <TabsList>
            <TabsTrigger value="list">{t.common.list}</TabsTrigger>
            <TabsTrigger value="calendar">{t.services.calendar}</TabsTrigger>
          </TabsList>

          <FilterBar className="mb-0">
            <SegmentedChips
              ariaLabel={t.services.taskStatus}
              value={status}
              onChange={setStatus}
              options={[
                { value: "", label: t.common.all },
                { value: "todo", label: t.services.toDo },
                { value: "in_progress", label: t.services.inProgress },
                { value: "done", label: t.common.done },
                { value: "cancelled", label: t.common.cancelled },
              ]}
            />
            {staff.length > 0 && (
              <NativeSelect
                aria-label={t.services.person}
                className="w-40"
                value={assignee}
                onChange={(e) => setAssignee(e.target.value)}
              >
                <option value="">{t.services.everyone}</option>
                {staff.map((u) => (
                  <option key={u.id} value={u.id}>
                    {u.name}
                  </option>
                ))}
              </NativeSelect>
            )}
            <button
              type="button"
              onClick={() => setOverdue((v) => !v)}
              className={cn(
                "inline-flex h-9 cursor-pointer items-center gap-1.5 rounded-[var(--od-app-radius,10px)] border px-3 text-[13px] font-medium transition-colors",
                overdue
                  ? "border-transparent bg-[color-mix(in_srgb,var(--od-bad-mark)_12%,transparent)] text-[var(--od-bad)]"
                  : "border-[var(--od-border-strong)] bg-[var(--od-surface)] text-[var(--od-ink-secondary)] hover:text-[var(--od-ink)]",
              )}
            >
              <AlertCircle size={14} /> {t.services.overdueOnly}
            </button>
          </FilterBar>
        </div>

        <TabsContent value="list">
          <DataGrid
            rows={tasks.data}
            loading={tasks.isLoading}
            rowKey={(item) => item.id}
            empty={t.services.noTasksAddOneTo}
            columns={[
              {
                header: t.services.task2,
                sortValue: (item) => item.title,
                cell: (item) => (
                  <div>
                    <p
                      className={cn(
                        "font-medium",
                        item.status === "done" && "text-[var(--od-ink-muted)] line-through",
                      )}
                    >
                      {item.title}
                    </p>
                    {item.project_id && (
                      <Link
                        className="od-mono od-caption underline underline-offset-4 hover:text-[var(--od-ink)]"
                        to={`/projects/${item.project_id}`}
                      >
                        {item.project_number}
                      </Link>
                    )}
                  </div>
                ),
              },
              { header: t.services.assignedTo, cell: (item) => item.assignee_name ?? "—" },
              {
                header: t.common.due2,
                sortValue: (item) => item.due_date ?? "",
                cell: (item) => (
                  <span className={item.overdue ? "font-semibold text-[var(--od-bad)]" : undefined}>
                    {item.due_date ? formatDate(item.due_date) : "—"}
                  </span>
                ),
              },
              {
                header: t.common.priority,
                cell: (item) =>
                  item.priority === "urgent" ? (
                    <StatusBadge value="urgent" />
                  ) : (
                    <span className="text-[var(--od-ink-secondary)]">{t.common.normal}</span>
                  ),
              },
              {
                header: t.common.status,
                cell: (item) => (
                  <NativeSelect
                    aria-label={fmt(t.services.statusOf, { title: item.title })}
                    className="h-8 w-36 text-xs"
                    value={item.status}
                    onChange={(e) => setTaskStatus.mutate({ id: item.id, status: e.target.value })}
                  >
                    {["todo", "in_progress", "done", "cancelled"].map((s) => (
                      <option key={s} value={s}>
                        {label(s)}
                      </option>
                    ))}
                  </NativeSelect>
                ),
              },
            ]}
          />
        </TabsContent>
        <TabsContent value="calendar">
          <Calendar tasks={tasks.data ?? []} />
        </TabsContent>
      </Tabs>

      {adding && <TaskForm onClose={() => setAdding(false)} />}
    </div>
  );
}
