import { useState } from "react";
import { Download, Pencil, Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  DataGrid,
  Field,
  FilterBar,
  FormDialog,
  NativeSelect,
  PageHeader,
  Pager,
  StatusBadge,
} from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { useGet, useGetPage, useWrite } from "@/hooks/useApi";
import { downloadCsv } from "@/lib/csv";
import { formatDate, today } from "@/lib/formatters";
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";

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
interface SummaryEmp {
  employee_id: string;
  employee_name: string;
  employee_no: string;
  days_present: number;
  late_count: number;
  late_minutes: number;
  days_absent: number;
  half_days: number;
  leave_days: number;
  holidays: number;
  needs_review: number;
  overtime_hours: string;
  worked_hours: string;
}
interface SummaryRes {
  month: string;
  from: string;
  to: string;
  employees: SummaryEmp[];
}

const time = (v: string | null) =>
  v ? new Date(v).toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit" }) : "-";
const timeInput = (v: string | null) =>
  v ? new Date(v).toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit" }) : "";
const hours = (m: number | null) => (m ? `${Math.floor(m / 60)}h ${m % 60}m` : "-");
const statuses = ["present", "late", "absent", "half_day", "on_leave", "holiday", "day_off"];

function History({ overtimeOnly }: { overtimeOnly: boolean }) {
  const { t } = useI18n();
  const { can, canAny } = useAuth();
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
    { enabled: canAny("hr.employee.view", "hr.employee.manage") },
  );
  const [marking, setMarking] = useState(false);
  const blank = { employee_id: "", day: today(), status: "absent", note: "" };
  const [draft, setDraft] = useState(blank);

  const [correcting, setCorrecting] = useState<Day | null>(null);
  const [corrForm, setCorrForm] = useState({
    status: "present",
    first_in: "",
    last_out: "",
    reason: "",
  });

  const refresh = ["/attendance"];
  const mark = useWrite<typeof draft, unknown>("POST", "/attendance/days", {
    invalidate: refresh,
    success: t.hr.attendanceRecorded,
    onSuccess: () => {
      setMarking(false);
      setDraft(blank);
    },
  });
  const correct = useWrite<Record<string, unknown>, unknown>(
    "PUT",
    () => `/attendance/days/${correcting?.id}`,
    {
      invalidate: refresh,
      success: t.hr.attendanceCorrected,
      onSuccess: () => {
        setCorrecting(null);
        setCorrForm({ status: "present", first_in: "", last_out: "", reason: "" });
      },
    },
  );
  const decide = useWrite<{ day: Day; action: string }, unknown>(
    "POST",
    (b) => `/attendance/overtime/${b.day.id}/${b.action}`,
    { invalidate: refresh, success: t.hr.overtimeUpdated, toBody: () => ({}) },
  );

  function openCorrect(d: Day) {
    setCorrecting(d);
    setCorrForm({
      status: d.status,
      first_in: timeInput(d.first_in),
      last_out: timeInput(d.last_out),
      reason: "",
    });
  }

  function submitCorrection() {
    if (!correcting) return;
    const dayStr = correcting.day.slice(0, 10);
    const toIso = (hhmm: string) => (hhmm ? new Date(`${dayStr}T${hhmm}:00`).toISOString() : null);
    correct.mutate({
      status: corrForm.status,
      first_in: corrForm.first_in ? toIso(corrForm.first_in) : undefined,
      last_out: corrForm.last_out ? toIso(corrForm.last_out) : undefined,
      reason: corrForm.reason,
    });
  }

  return (
    <div className="space-y-4">
      <FilterBar className="items-end">
        <Field label={t.common.from}>
          <Input type="date" value={from} onChange={(e) => setFrom(e.target.value)} />
        </Field>
        <Field label={t.common.to}>
          <Input type="date" value={to} onChange={(e) => setTo(e.target.value)} />
        </Field>
        {!overtimeOnly && can("hr.attendance.manage") && (
          <Button className="ml-auto" onClick={() => setMarking(true)}>
            <Plus size={16} /> {t.hr.recordADay}
          </Button>
        )}
      </FilterBar>
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(d) => d.id}
        empty={t.hr.noAttendanceInThisPeriod}
        columns={[
          { header: t.common.date, cell: (d) => formatDate(d.day) },
          {
            header: t.common.employee,
            cell: (d) => (
              <span className="font-medium text-[var(--od-ink)]">{d.employee_name}</span>
            ),
          },
          {
            header: t.hr.in,
            cell: (d) => <span className="od-mono text-xs">{time(d.first_in)}</span>,
          },
          {
            header: t.common.out,
            cell: (d) => <span className="od-mono text-xs">{time(d.last_out)}</span>,
          },
          {
            header: t.hr.worked,
            align: "right",
            cell: (d) => <span className="od-tabular">{hours(d.worked_minutes)}</span>,
          },
          {
            header: overtimeOnly ? t.common.overtime : t.common.late,
            align: "right",
            cell: (d) =>
              overtimeOnly ? (
                <span className="od-tabular font-medium text-[var(--od-ink)]">
                  {hours(d.overtime_minutes)}
                </span>
              ) : d.late_minutes ? (
                <span className="od-tabular font-medium text-[var(--od-warning)]">
                  {fmt(t.hr.min, { late_minutes: d.late_minutes })}
                </span>
              ) : (
                <span className="text-[var(--od-ink-muted)]">-</span>
              ),
          },
          {
            header: t.common.status,
            cell: (d) => <StatusBadge value={overtimeOnly ? d.overtime_status : d.status} />,
          },
          {
            header: "",
            cell: (d) => (
              <div className="flex justify-end gap-1.5">
                {!overtimeOnly && can("hr.attendance.manage") && (
                  <Button size="sm" variant="ghost" onClick={() => openCorrect(d)}>
                    <Pencil size={13} /> {t.hr.correct}
                  </Button>
                )}
                {overtimeOnly && d.overtime_status === "pending" && can("hr.attendance.manage") && (
                  <>
                    <Button size="sm" onClick={() => decide.mutate({ day: d, action: "approve" })}>
                      {t.common.approve}
                    </Button>
                    <Button
                      size="sm"
                      variant="outline"
                      className="text-[var(--od-bad)] hover:text-[var(--od-bad)]"
                      onClick={() => decide.mutate({ day: d, action: "reject" })}
                    >
                      {t.common.reject}
                    </Button>
                  </>
                )}
              </div>
            ),
          },
        ]}
      />
      <Pager meta={list.data?.meta} onPage={setPage} />

      <FormDialog
        open={marking}
        onOpenChange={setMarking}
        title={t.hr.recordADay}
        busy={mark.isPending}
        onSubmit={() => mark.mutate(draft)}
      >
        <Field label={t.common.employee}>
          <NativeSelect
            required
            value={draft.employee_id}
            onChange={(e) => setDraft((d) => ({ ...d, employee_id: e.target.value }))}
          >
            <option value="">{t.common.choose}</option>
            {employees.data?.map((e) => (
              <option key={e.id} value={e.id}>
                {e.name}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label={t.common.date}>
            <Input
              required
              type="date"
              value={draft.day}
              onChange={(e) => setDraft((d) => ({ ...d, day: e.target.value }))}
            />
          </Field>
          <Field label={t.common.status}>
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
        <Field label={t.common.note}>
          <Input
            value={draft.note}
            onChange={(e) => setDraft((d) => ({ ...d, note: e.target.value }))}
          />
        </Field>
      </FormDialog>

      <FormDialog
        open={correcting !== null}
        onOpenChange={(o) => !o && setCorrecting(null)}
        title={fmt(t.hr.correctAttendance, {
          employee_name: correcting?.employee_name ?? "",
          value: correcting ? formatDate(correcting.day) : "",
        })}
        submitLabel={t.hr.saveCorrection}
        busy={correct.isPending}
        onSubmit={submitCorrection}
      >
        <Field label={t.common.status}>
          <NativeSelect
            value={corrForm.status}
            onChange={(e) => setCorrForm((c) => ({ ...c, status: e.target.value }))}
          >
            {statuses.map((s) => (
              <option key={s} value={s}>
                {s.replace("_", " ")}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label={t.hr.clockInTime}>
            <Input
              type="time"
              value={corrForm.first_in}
              onChange={(e) => setCorrForm((c) => ({ ...c, first_in: e.target.value }))}
            />
          </Field>
          <Field label={t.hr.clockOutTime}>
            <Input
              type="time"
              value={corrForm.last_out}
              onChange={(e) => setCorrForm((c) => ({ ...c, last_out: e.target.value }))}
            />
          </Field>
        </div>
        <Field label={t.hr.reasonForCorrectionRequired}>
          <Input
            required
            value={corrForm.reason}
            onChange={(e) => setCorrForm((c) => ({ ...c, reason: e.target.value }))}
            placeholder={t.hr.eGForgotToClock}
          />
        </Field>
      </FormDialog>
    </div>
  );
}

function MonthlySummary() {
  const { t } = useI18n();
  const [month, setMonth] = useState(() => today().slice(0, 7));
  const res = useGet<SummaryRes>("/attendance/summary", { month });
  const rows = res.data?.employees ?? [];

  function exportCsv() {
    downloadCsv(`attendance-summary-${month}.csv`, [
      [
        t.hr.employeeNo,
        t.common.name2,
        t.hr.present,
        t.common.late,
        t.hr.lateMins,
        t.hr.absent,
        t.hr.halfDay,
        t.hr.leave,
        t.hr.workedHrs,
        t.hr.overtimeHrs,
      ],
      ...rows.map((r) => [
        r.employee_no,
        r.employee_name,
        r.days_present,
        r.late_count,
        r.late_minutes,
        r.days_absent,
        r.half_days,
        r.leave_days,
        r.worked_hours,
        r.overtime_hours,
      ]),
    ]);
  }

  return (
    <div className="space-y-4">
      <FilterBar className="items-end justify-between">
        <Field label={t.common.month}>
          <Input
            type="month"
            className="w-44"
            value={month}
            onChange={(e) => setMonth(e.target.value)}
          />
        </Field>
        <Button variant="outline" size="sm" disabled={!rows.length} onClick={exportCsv}>
          <Download size={14} /> {t.common.exportCsv}
        </Button>
      </FilterBar>
      <DataGrid
        rows={rows}
        loading={res.isLoading}
        rowKey={(r) => r.employee_id}
        empty={t.hr.noAttendanceRecordsForThis}
        columns={[
          {
            header: t.common.employee,
            cell: (r) => (
              <div>
                <p className="font-medium text-[var(--od-ink)]">{r.employee_name}</p>
                <p className="od-mono text-xs text-[var(--od-ink-muted)]">{r.employee_no}</p>
              </div>
            ),
          },
          {
            header: t.hr.present,
            align: "right",
            cell: (r) => <span className="od-tabular">{r.days_present}</span>,
          },
          {
            header: t.common.late,
            align: "right",
            cell: (r) =>
              r.late_count ? (
                <span className="od-tabular font-medium text-[var(--od-warning)]">
                  {r.late_count} ({r.late_minutes}m)
                </span>
              ) : (
                <span className="od-tabular text-[var(--od-ink-muted)]">0</span>
              ),
          },
          {
            header: t.hr.absent,
            align: "right",
            cell: (r) =>
              r.days_absent ? (
                <span className="od-tabular font-medium text-[var(--od-bad)]">{r.days_absent}</span>
              ) : (
                <span className="od-tabular text-[var(--od-ink-muted)]">0</span>
              ),
          },
          {
            header: t.hr.halfDay,
            align: "right",
            cell: (r) => <span className="od-tabular">{r.half_days}</span>,
          },
          {
            header: t.hr.leave,
            align: "right",
            cell: (r) => <span className="od-tabular">{r.leave_days}</span>,
          },
          {
            header: t.hr.workedHrs,
            align: "right",
            cell: (r) => (
              <span className="od-tabular font-medium text-[var(--od-ink)]">{r.worked_hours}h</span>
            ),
          },
          {
            header: t.hr.otHrs,
            align: "right",
            cell: (r) => <span className="od-tabular">{r.overtime_hours}h</span>,
          },
        ]}
      />
    </div>
  );
}

export default function AttendanceAdmin() {
  const { t } = useI18n();
  return (
    <div className="space-y-6">
      <PageHeader
        title={t.common.attendance}
        description={t.hr.dailyRecordsMonthlySummariesCorrections}
      />
      <Tabs defaultValue="days">
        <TabsList className="mb-4">
          <TabsTrigger value="days">{t.hr.dailyRecords}</TabsTrigger>
          <TabsTrigger value="summary">{t.hr.monthlySummary}</TabsTrigger>
          <TabsTrigger value="overtime">{t.common.overtime}</TabsTrigger>
        </TabsList>
        <TabsContent value="days">
          <History overtimeOnly={false} />
        </TabsContent>
        <TabsContent value="summary">
          <MonthlySummary />
        </TabsContent>
        <TabsContent value="overtime">
          <History overtimeOnly />
        </TabsContent>
      </Tabs>
    </div>
  );
}
