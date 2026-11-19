import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Clock, LogIn, LogOut, Plus } from "lucide-react";
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
import { formatDate, formatMoney, today } from "@/lib/formatters";

interface Employee {
  id: string;
  employee_no: string;
  name: string;
  phone?: string | null;
  department_name?: string | null;
  position_name?: string | null;
  branch_name?: string;
  status: string;
  hire_date?: string;
  base_pay?: string;
}
interface LeaveType {
  id: string;
  name: string;
  is_active: boolean;
}
interface LeaveRequest {
  id: string;
  employee_id: string;
  employee_name: string;
  leave_type: string;
  start_date: string;
  end_date: string;
  days: string;
  status: string;
  reason?: string | null;
}
interface TodayRow {
  employee_id: string;
  employee_name: string;
  first_in: string | null;
  last_out: string | null;
  state: string;
}
interface Today {
  day: string;
  counts: Record<string, number>;
  employees: TodayRow[];
}

const time = (v: string | null) =>
  v ? new Date(v).toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit" }) : "-";

function ClockCard() {
  const me = useGet<{ id: string; name: string }>("/me/employee");
  const refresh = ["/attendance", "/me"];
  const clockIn = useWrite("POST", "/attendance/clock-in", {
    invalidate: refresh,
    success: "Clocked in",
  });
  const clockOut = useWrite("POST", "/attendance/clock-out", {
    invalidate: refresh,
    success: "Clocked out",
  });
  if (!me.data) return null;
  return (
    <div className="mb-4 flex flex-wrap items-center gap-3 rounded-2xl border bg-card p-4">
      <Clock size={18} className="text-muted-foreground" />
      <div className="mr-auto">
        <p className="font-medium">{me.data.name}</p>
        <p className="text-sm text-muted-foreground">Record your own attendance for today.</p>
      </div>
      <Button onClick={() => clockIn.mutate({})} disabled={clockIn.isPending}>
        <LogIn size={16} /> Clock in
      </Button>
      <Button variant="outline" onClick={() => clockOut.mutate({})} disabled={clockOut.isPending}>
        <LogOut size={16} /> Clock out
      </Button>
    </div>
  );
}

function EmployeesTab() {
  const { can } = useAuth();
  const navigate = useNavigate();
  const [q, setQ] = useState("");
  const [page, setPage] = useState(1);
  const list = useGetPage<Employee>("/employees", { q: q || undefined, page });
  const [open, setOpen] = useState(false);
  const blank = { name: "", phone: "", hire_date: today(), pay_type: "monthly", base_pay: "0" };
  const [draft, setDraft] = useState(blank);
  const create = useWrite("POST", "/employees", {
    invalidate: ["/employees"],
    success: "Employee added",
    onSuccess: () => {
      setOpen(false);
      setDraft(blank);
    },
  });
  return (
    <>
      <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
        <Input
          aria-label="Search employees"
          className="max-w-xs"
          placeholder="Search name, number or phone…"
          value={q}
          onChange={(e) => {
            setQ(e.target.value);
            setPage(1);
          }}
        />
        {can("employees.manage") && (
          <Button onClick={() => setOpen(true)}>
            <Plus size={16} /> New employee
          </Button>
        )}
      </div>
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(e) => e.id}
        empty="No employees yet"
        onRowClick={(e) => navigate(`/employees/${e.id}`)}
        columns={[
          { header: "No.", cell: (e) => e.employee_no },
          { header: "Name", cell: (e) => <span className="font-medium">{e.name}</span> },
          { header: "Position", cell: (e) => e.position_name ?? e.department_name ?? "-" },
          { header: "Branch", cell: (e) => e.branch_name ?? "-" },
          { header: "Hired", cell: (e) => (e.hire_date ? formatDate(e.hire_date) : "-") },
          ...(can("employees.view_salary")
            ? [
                {
                  header: "Base pay",
                  align: "right" as const,
                  cell: (e: Employee) => formatMoney(e.base_pay),
                },
              ]
            : []),
          { header: "Status", cell: (e) => <StatusBadge value={e.status} /> },
        ]}
      />
      <Pager meta={list.data?.meta} onPage={setPage} />
      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title="New employee"
        busy={create.isPending}
        onSubmit={() => create.mutate(draft)}
      >
        <Field label="Full name">
          <Input
            required
            value={draft.name}
            onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))}
          />
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label="Phone">
            <Input
              value={draft.phone}
              onChange={(e) => setDraft((d) => ({ ...d, phone: e.target.value }))}
            />
          </Field>
          <Field label="Hire date">
            <Input
              type="date"
              value={draft.hire_date}
              onChange={(e) => setDraft((d) => ({ ...d, hire_date: e.target.value }))}
            />
          </Field>
          <Field label="Pay type">
            <NativeSelect
              value={draft.pay_type}
              onChange={(e) => setDraft((d) => ({ ...d, pay_type: e.target.value }))}
            >
              <option value="monthly">Monthly</option>
              <option value="daily">Daily</option>
              <option value="hourly">Hourly</option>
            </NativeSelect>
          </Field>
          {can("employees.view_salary") && (
            <Field label="Base pay">
              <Input
                inputMode="decimal"
                value={draft.base_pay}
                onChange={(e) => setDraft((d) => ({ ...d, base_pay: e.target.value }))}
              />
            </Field>
          )}
        </div>
      </FormDialog>
    </>
  );
}

function LeaveTab() {
  const { can, canAny } = useAuth();
  const approver = can("leave.approve");
  const [status, setStatus] = useState("pending");
  const [page, setPage] = useState(1);
  const list = useGetPage<LeaveRequest>(approver ? "/leave-requests" : "/me/leave", {
    status: status || undefined,
    page,
  });
  const types = useGet<LeaveType[]>("/leave-types", undefined, {
    enabled: canAny("leave.request", "leave.approve", "departments.manage", "employees.view"),
  });
  const employees = useGet<Employee[]>(
    "/employees",
    { per_page: 100 },
    { enabled: approver && can("employees.view") },
  );
  const [open, setOpen] = useState(false);
  const blank = {
    employee_id: "",
    leave_type_id: "",
    start_date: today(),
    end_date: today(),
    reason: "",
  };
  const [draft, setDraft] = useState(blank);
  const refresh = ["/leave-requests", "/me/leave", "/attendance"];
  const request = useWrite<typeof draft, unknown>("POST", "/leave-requests", {
    invalidate: refresh,
    success: "Leave requested",
    onSuccess: () => {
      setOpen(false);
      setDraft(blank);
    },
  });
  const decide = useWrite<{ id: string; action: string }, unknown>(
    "POST",
    (b) => `/leave-requests/${b.id}/${b.action}`,
    { invalidate: refresh, success: "Leave updated", toBody: () => ({}) },
  );
  return (
    <>
      <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
        <NativeSelect
          aria-label="Filter by status"
          className="max-w-[12rem]"
          value={status}
          onChange={(e) => {
            setStatus(e.target.value);
            setPage(1);
          }}
        >
          <option value="">All</option>
          {["pending", "approved", "rejected", "cancelled"].map((v) => (
            <option key={v}>{v}</option>
          ))}
        </NativeSelect>
        {can("leave.request") && (
          <Button onClick={() => setOpen(true)}>
            <Plus size={16} /> Request leave
          </Button>
        )}
      </div>
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(l) => l.id}
        empty="No leave requests"
        columns={[
          { header: "Employee", cell: (l) => l.employee_name ?? "Me" },
          { header: "Type", cell: (l) => l.leave_type },
          {
            header: "Dates",
            cell: (l) => `${formatDate(l.start_date)} – ${formatDate(l.end_date)}`,
          },
          { header: "Days", align: "right", cell: (l) => l.days },
          { header: "Status", cell: (l) => <StatusBadge value={l.status} /> },
          {
            header: "",
            cell: (l) =>
              l.status === "pending" && (
                <div className="flex gap-2">
                  {approver && (
                    <>
                      <Button
                        size="sm"
                        onClick={() => decide.mutate({ id: l.id, action: "approve" })}
                      >
                        Approve
                      </Button>
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => decide.mutate({ id: l.id, action: "reject" })}
                      >
                        Reject
                      </Button>
                    </>
                  )}
                  {!approver && (
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() => decide.mutate({ id: l.id, action: "cancel" })}
                    >
                      Cancel
                    </Button>
                  )}
                </div>
              ),
          },
        ]}
      />
      <Pager meta={list.data?.meta} onPage={setPage} />
      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title="Request leave"
        busy={request.isPending}
        onSubmit={() =>
          request.mutate({ ...draft, employee_id: draft.employee_id || (undefined as never) })
        }
      >
        {approver && employees.data && (
          <Field label="Employee (leave empty for yourself)">
            <NativeSelect
              value={draft.employee_id}
              onChange={(e) => setDraft((d) => ({ ...d, employee_id: e.target.value }))}
            >
              <option value="">Myself</option>
              {employees.data.map((e) => (
                <option key={e.id} value={e.id}>
                  {e.name}
                </option>
              ))}
            </NativeSelect>
          </Field>
        )}
        <Field label="Leave type">
          <NativeSelect
            required
            value={draft.leave_type_id}
            onChange={(e) => setDraft((d) => ({ ...d, leave_type_id: e.target.value }))}
          >
            <option value="">Choose…</option>
            {types.data
              ?.filter((t) => t.is_active)
              .map((t) => (
                <option key={t.id} value={t.id}>
                  {t.name}
                </option>
              ))}
          </NativeSelect>
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label="From">
            <Input
              required
              type="date"
              value={draft.start_date}
              onChange={(e) => setDraft((d) => ({ ...d, start_date: e.target.value }))}
            />
          </Field>
          <Field label="To">
            <Input
              required
              type="date"
              value={draft.end_date}
              onChange={(e) => setDraft((d) => ({ ...d, end_date: e.target.value }))}
            />
          </Field>
        </div>
        <Field label="Reason">
          <Input
            value={draft.reason}
            onChange={(e) => setDraft((d) => ({ ...d, reason: e.target.value }))}
          />
        </Field>
      </FormDialog>
    </>
  );
}

function AttendanceTab() {
  const board = useGet<Today>("/attendance/today");
  const counts = board.data?.counts ?? {};
  return (
    <>
      <div className="mb-4 flex flex-wrap gap-2 text-sm">
        {Object.entries(counts).map(([k, v]) => (
          <span key={k} className="rounded-full border bg-card px-3 py-1">
            {k.replace("_", " ")}: <strong>{v}</strong>
          </span>
        ))}
      </div>
      <DataGrid
        rows={board.data?.employees}
        loading={board.isLoading}
        rowKey={(r) => r.employee_id}
        empty="No employees to show"
        columns={[
          {
            header: "Employee",
            cell: (r) => <span className="font-medium">{r.employee_name}</span>,
          },
          { header: "In", cell: (r) => time(r.first_in) },
          { header: "Out", cell: (r) => time(r.last_out) },
          { header: "State", cell: (r) => <StatusBadge value={r.state} /> },
        ]}
      />
    </>
  );
}

export default function People() {
  const { can } = useAuth();
  return (
    <>
      <PageHeader title="People" description="Employees, leave and daily attendance." />
      {can("attendance.clock") && <ClockCard />}
      <Tabs defaultValue={can("employees.view") ? "employees" : "leave"}>
        <TabsList className="mb-4">
          {can("employees.view") && <TabsTrigger value="employees">Employees</TabsTrigger>}
          <TabsTrigger value="leave">Leave</TabsTrigger>
          {can("attendance.view") && <TabsTrigger value="attendance">Today</TabsTrigger>}
        </TabsList>
        {can("employees.view") && (
          <TabsContent value="employees">
            <EmployeesTab />
          </TabsContent>
        )}
        <TabsContent value="leave">
          <LeaveTab />
        </TabsContent>
        {can("attendance.view") && (
          <TabsContent value="attendance">
            <AttendanceTab />
          </TabsContent>
        )}
      </Tabs>
    </>
  );
}
