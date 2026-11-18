import { useEffect, useState } from "react";
import { Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  DataGrid,
  Field,
  FormDialog,
  NativeSelect,
  PageHeader,
  Pager,
  StatusBadge,
} from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { useGet, useGetPage, useWrite } from "@/hooks/useApi";
import { formatDate, today } from "@/lib/formatters";

interface Day {
  id: string;
  day: string;
  employee_id: string;
  employee_name: string;
  status: string;
  first_in: string | null;
  last_out: string | null;
  worked_minutes: number | null;
  late_minutes: number | null;
  overtime_minutes: number | null;
  overtime_status: string;
  needs_review: boolean;
}
interface Option {
  id: string;
  name: string;
}
interface ScheduleRow {
  weekday: number;
  is_working: boolean;
  start_time: string | null;
  end_time: string | null;
  break_minutes: number;
}

const time = (v: string | null) =>
  v ? new Date(v).toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit" }) : "-";
const hours = (m: number | null) => (m ? `${Math.floor(m / 60)}h ${m % 60}m` : "-");
const dayNames = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];
const statuses = ["present", "late", "absent", "half_day", "leave", "holiday", "day_off"];

function History({ overtimeOnly }: { overtimeOnly: boolean }) {
  const { can } = useAuth();
  const [from, setFrom] = useState(() =>
    new Date(Date.now() - 14 * 864e5).toLocaleDateString("en-CA"),
  );
  const [to, setTo] = useState(today());
  const [page, setPage] = useState(1);
  const list = useGetPage<Day>(overtimeOnly ? "/attendance/overtime" : "/attendance/days", {
    from,
    to,
    page,
  });
  const employees = useGet<Option[]>(
    "/employees",
    { per_page: 100 },
    { enabled: can("employees.view") },
  );
  const [marking, setMarking] = useState(false);
  const blank = { employee_id: "", day: today(), status: "absent", note: "" };
  const [draft, setDraft] = useState(blank);
  const refresh = ["/attendance"];
  const mark = useWrite<typeof draft, unknown>("POST", "/attendance/days", {
    invalidate: refresh,
    success: "Attendance recorded",
    onSuccess: () => {
      setMarking(false);
      setDraft(blank);
    },
  });
  const decide = useWrite<{ day: Day; action: string }, unknown>(
    "POST",
    (b) => `/attendance/overtime/${b.day.id}/${b.action}`,
    { invalidate: refresh, success: "Overtime updated", toBody: () => ({}) },
  );
  return (
    <>
      <div className="mb-4 flex flex-wrap items-end gap-3">
        <Field label="From">
          <Input type="date" value={from} onChange={(e) => setFrom(e.target.value)} />
        </Field>
        <Field label="To">
          <Input type="date" value={to} onChange={(e) => setTo(e.target.value)} />
        </Field>
        {!overtimeOnly && can("attendance.edit") && (
          <Button className="ml-auto" onClick={() => setMarking(true)}>
            <Plus size={16} /> Record a day
          </Button>
        )}
      </div>
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(d) => d.id}
        empty="No attendance in this period"
        columns={[
          { header: "Date", cell: (d) => formatDate(d.day) },
          {
            header: "Employee",
            cell: (d) => <span className="font-medium">{d.employee_name}</span>,
          },
          { header: "In", cell: (d) => time(d.first_in) },
          { header: "Out", cell: (d) => time(d.last_out) },
          { header: "Worked", align: "right", cell: (d) => hours(d.worked_minutes) },
          {
            header: overtimeOnly ? "Overtime" : "Late",
            align: "right",
            cell: (d) =>
              overtimeOnly
                ? hours(d.overtime_minutes)
                : d.late_minutes
                  ? `${d.late_minutes} min`
                  : "-",
          },
          {
            header: "Status",
            cell: (d) => <StatusBadge value={overtimeOnly ? d.overtime_status : d.status} />,
          },
          {
            header: "",
            cell: (d) =>
              overtimeOnly &&
              d.overtime_status === "pending" &&
              can("overtime.approve") && (
                <div className="flex gap-2">
                  <Button size="sm" onClick={() => decide.mutate({ day: d, action: "approve" })}>
                    Approve
                  </Button>
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => decide.mutate({ day: d, action: "reject" })}
                  >
                    Reject
                  </Button>
                </div>
              ),
          },
        ]}
      />
      <Pager meta={list.data?.meta} onPage={setPage} />
      <FormDialog
        open={marking}
        onOpenChange={setMarking}
        title="Record a day"
        busy={mark.isPending}
        onSubmit={() => mark.mutate(draft)}
      >
        <Field label="Employee">
          <NativeSelect
            required
            value={draft.employee_id}
            onChange={(e) => setDraft((d) => ({ ...d, employee_id: e.target.value }))}
          >
            <option value="">Choose…</option>
            {employees.data?.map((e) => (
              <option key={e.id} value={e.id}>
                {e.name}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label="Date">
            <Input
              required
              type="date"
              value={draft.day}
              onChange={(e) => setDraft((d) => ({ ...d, day: e.target.value }))}
            />
          </Field>
          <Field label="Status">
            <NativeSelect
              value={draft.status}
              onChange={(e) => setDraft((d) => ({ ...d, status: e.target.value }))}
            >
              {statuses.map((s) => (
                <option key={s} value={s}>
                  {s.replace("_", " ")}
                </option>
              ))}
            </NativeSelect>
          </Field>
        </div>
        <Field label="Note">
          <Input
            value={draft.note}
            onChange={(e) => setDraft((d) => ({ ...d, note: e.target.value }))}
          />
        </Field>
      </FormDialog>
    </>
  );
}

function Schedule() {
  const rows = useGet<ScheduleRow[]>("/schedules");
  const [week, setWeek] = useState<ScheduleRow[]>([]);
  useEffect(() => {
    const byDay = new Map((rows.data ?? []).map((r) => [r.weekday, r]));
    setWeek(
      [1, 2, 3, 4, 5, 6, 0].map(
        (d) =>
          byDay.get(d) ?? {
            weekday: d,
            is_working: d !== 0,
            start_time: "08:00",
            end_time: "17:00",
            break_minutes: 60,
          },
      ),
    );
  }, [rows.data]);
  const save = useWrite<ScheduleRow[], unknown>("PUT", "/schedules", {
    invalidate: ["/schedules"],
    success: "Schedule saved",
    toBody: (days) => ({
      days: days.map((d) => ({
        weekday: d.weekday,
        is_working: d.is_working,
        start_time: d.is_working ? d.start_time?.slice(0, 5) : "",
        end_time: d.is_working ? d.end_time?.slice(0, 5) : "",
        break_minutes: d.break_minutes,
      })),
    }),
  });
  const set = (weekday: number, patch: Partial<ScheduleRow>) =>
    setWeek((w) => w.map((r) => (r.weekday === weekday ? { ...r, ...patch } : r)));
  return (
    <form
      className="max-w-2xl space-y-2 rounded-2xl border bg-card p-5"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate(week);
      }}
    >
      <p className="mb-2 text-sm text-muted-foreground">
        The branch working week. Lateness and overtime are worked out against these hours.
      </p>
      {week.map((r) => (
        <div
          key={r.weekday}
          className="grid grid-cols-[7rem_5rem_1fr_1fr_5rem] items-center gap-2 text-sm"
        >
          <span className="font-medium">{dayNames[r.weekday]}</span>
          <label className="flex items-center gap-1">
            <input
              type="checkbox"
              checked={r.is_working}
              onChange={(e) => set(r.weekday, { is_working: e.target.checked })}
            />
            Works
          </label>
          <Input
            aria-label={`${dayNames[r.weekday]} start`}
            type="time"
            disabled={!r.is_working}
            value={(r.start_time ?? "").slice(0, 5)}
            onChange={(e) => set(r.weekday, { start_time: e.target.value })}
          />
          <Input
            aria-label={`${dayNames[r.weekday]} end`}
            type="time"
            disabled={!r.is_working}
            value={(r.end_time ?? "").slice(0, 5)}
            onChange={(e) => set(r.weekday, { end_time: e.target.value })}
          />
          <Input
            aria-label={`${dayNames[r.weekday]} break minutes`}
            inputMode="numeric"
            disabled={!r.is_working}
            value={String(r.break_minutes)}
            onChange={(e) => set(r.weekday, { break_minutes: Number(e.target.value) || 0 })}
          />
        </div>
      ))}
      <Button type="submit" disabled={save.isPending}>
        {save.isPending ? "Saving…" : "Save schedule"}
      </Button>
    </form>
  );
}

export default function AttendanceAdmin() {
  const { can } = useAuth();
  return (
    <>
      <PageHeader
        title="Attendance"
        description="Daily records, overtime approval and the working week."
      />
      <Tabs defaultValue="days">
        <TabsList className="mb-4">
          <TabsTrigger value="days">Daily records</TabsTrigger>
          <TabsTrigger value="overtime">Overtime</TabsTrigger>
          {can("schedules.manage") && <TabsTrigger value="schedule">Working week</TabsTrigger>}
        </TabsList>
        <TabsContent value="days">
          <History overtimeOnly={false} />
        </TabsContent>
        <TabsContent value="overtime">
          <History overtimeOnly />
        </TabsContent>
        {can("schedules.manage") && (
          <TabsContent value="schedule">
            <Schedule />
          </TabsContent>
        )}
      </Tabs>
    </>
  );
}
