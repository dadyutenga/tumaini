import { useEffect, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { Clock, KeyRound, LogIn, LogOut, Plus, ShieldCheck } from "lucide-react";
import { toast } from "sonner";
import { Badge } from "@/components/ui/badge";
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
import { CopyButton } from "@/components/shared/Display";
import { useAuth } from "@/features/auth/auth";
import { errorMessage, useGet, useGetPage, useWrite } from "@/hooks/useApi";
import { api } from "@/lib/api";
import { formatDate, formatMoney, today } from "@/lib/formatters";
import type { Branch } from "@/types";

interface Employee {
  id: string;
  employee_no: string;
  name: string;
  phone?: string | null;
  email?: string | null;
  position?: string | null;
  department_name?: string | null;
  position_name?: string | null;
  branch_id?: string | null;
  branch_name?: string;
  user_id?: string | null;
  role_name?: string | null;
  role_slug?: string | null;
  status: string;
  hire_date?: string;
  base_pay?: string;
}
interface RoleOption {
  id: string;
  name: string;
  slug: string;
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
  const { can, canAny } = useAuth();
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const [q, setQ] = useState("");
  const [page, setPage] = useState(1);
  const list = useGetPage<Employee>("/employees", { q: q || undefined, page });
  const canGrantAccess = can("security.user.manage");
  const roles = useGet<RoleOption[]>("/roles", undefined, { enabled: canGrantAccess });
  const branches = useGet<Branch[]>("/branches");
  const [open, setOpen] = useState(false);

  useEffect(() => {
    if (searchParams.get("new") === "1" && can("hr.employee.manage")) {
      setOpen(true);
      setSearchParams({}, { replace: true });
    }
  }, [searchParams, setSearchParams, can]);

  const blank = {
    name: "",
    phone: "",
    email: "",
    position: "",
    hire_date: today(),
    base_pay: "",
    grant_access: false,
    role_id: "",
    default_branch_id: "",
    password: "",
  };
  const [draft, setDraft] = useState(blank);
  const [creatingWithAccess, setCreatingWithAccess] = useState(false);

  const [accessTarget, setAccessTarget] = useState<Employee | null>(null);
  const [accessForm, setAccessForm] = useState({
    phone: "",
    email: "",
    role_id: "",
    default_branch_id: "",
    password: "",
  });
  const [grantedBanner, setGrantedBanner] = useState<{
    name: string;
    role: string;
    password: string;
  } | null>(null);

  const create = useWrite<Record<string, unknown>, Employee>("POST", "/employees", {
    invalidate: ["/employees", "/users"],
    success: "Employee added",
    onSuccess: () => {
      setOpen(false);
      setDraft(blank);
    },
  });

  const grantLogin = useWrite<Record<string, unknown>, unknown>(
    "POST",
    () => `/employees/${accessTarget?.id}/user`,
    {
      invalidate: ["/employees", "/users"],
      success: "System access granted",
      onSuccess: () => {
        if (accessTarget) {
          const roleName =
            roles.data?.find((r) => r.id === accessForm.role_id)?.name ?? "System user";
          setGrantedBanner({
            name: accessTarget.name,
            role: roleName,
            password: accessForm.password,
          });
        }
        setAccessTarget(null);
        setAccessForm({ phone: "", email: "", role_id: "", default_branch_id: "", password: "" });
      },
    },
  );

  async function submitNewEmployee() {
    const payload: Record<string, unknown> = {
      name: draft.name,
      phone: draft.phone || undefined,
      email: draft.email || undefined,
      position: draft.position,
      hire_date: draft.hire_date,
    };
    if (canSetSalary && draft.base_pay.trim() !== "") {
      payload.base_pay = draft.base_pay.trim();
    }
    if (!draft.grant_access || !canGrantAccess) {
      create.mutate(payload);
      return;
    }
    setCreatingWithAccess(true);
    try {
      const emp = await create.mutateAsync(payload);
      await api.post(`/employees/${emp.id}/user`, {
        name: draft.name,
        phone: draft.phone,
        email: draft.email,
        password: draft.password,
        role_id: draft.role_id,
        branch_ids: draft.default_branch_id ? [draft.default_branch_id] : [],
      });
      const roleName = roles.data?.find((r) => r.id === draft.role_id)?.name ?? "System user";
      setGrantedBanner({ name: draft.name, role: roleName, password: draft.password });
      toast.success("Employee added and system access granted");
      setOpen(false);
      setDraft(blank);
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setCreatingWithAccess(false);
    }
  }

  const canSeeSalary = canAny("hr.compensation.view", "hr.compensation.manage");
  const canSetSalary = can("hr.compensation.manage");
  const activeBranches = (branches.data ?? []).filter((b) => b.is_active);
  return (
    <>
      {grantedBanner && (
        <div className="mb-4 flex flex-wrap items-center justify-between gap-2 rounded-xl border border-primary/30 bg-primary/5 p-4 text-sm">
          <div>
            <p className="font-medium">
              System access granted for {grantedBanner.name} ({grantedBanner.role})
            </p>
            <p className="text-xs text-muted-foreground">
              Share this temporary password securely. They will change it on their first sign-in.
            </p>
          </div>
          <div className="flex items-center gap-2">
            <code className="rounded bg-muted px-2 py-1 font-mono">{grantedBanner.password}</code>
            <CopyButton text={grantedBanner.password} />
            <Button size="sm" variant="ghost" onClick={() => setGrantedBanner(null)}>
              Dismiss
            </Button>
          </div>
        </div>
      )}
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
        {can("hr.employee.manage") && (
          <Button onClick={() => setOpen(true)}>
            <Plus size={16} /> New employee
          </Button>
        )}
      </div>
      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(e) => e.id}
        empty="No employees yet. Create an employee first, then grant them Manager or Staff access."
        onRowClick={(e) => navigate(`/employees/${e.id}`)}
        columns={[
          { header: "No.", cell: (e) => e.employee_no },
          { header: "Name", cell: (e) => <span className="font-medium">{e.name}</span> },
          {
            header: "Position",
            cell: (e) => e.position || e.position_name || e.department_name || "-",
          },
          { header: "Location", cell: (e) => e.branch_name ?? "-" },
          { header: "Hired", cell: (e) => (e.hire_date ? formatDate(e.hire_date) : "-") },
          ...(canSeeSalary
            ? [
                {
                  header: "Base pay",
                  align: "right" as const,
                  cell: (e: Employee) => formatMoney(e.base_pay),
                },
              ]
            : []),
          {
            header: "System access",
            cell: (e) =>
              e.user_id ? (
                <Badge variant="secondary" className="gap-1">
                  <ShieldCheck size={12} className="text-primary" />
                  {e.role_name || "Active login"}
                </Badge>
              ) : canGrantAccess && e.status !== "terminated" ? (
                <Button
                  size="sm"
                  variant="outline"
                  className="h-7 px-2.5 text-xs"
                  onClick={(ev) => {
                    ev.stopPropagation();
                    setAccessTarget(e);
                    setAccessForm({
                      phone: e.phone ?? "",
                      email: e.email ?? "",
                      role_id: "",
                      default_branch_id: e.branch_id ?? "",
                      password: "",
                    });
                  }}
                >
                  <KeyRound size={12} /> Grant access
                </Button>
              ) : (
                <span className="text-xs text-muted-foreground">No login</span>
              ),
          },
          { header: "Status", cell: (e) => <StatusBadge value={e.status} /> },
        ]}
      />
      <Pager meta={list.data?.meta} onPage={setPage} />
      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title="New employee"
        description="Every Manager or Staff member starts here as an employee. You can also grant system login right away."
        busy={create.isPending || creatingWithAccess}
        onSubmit={() => void submitNewEmployee()}
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
              required={draft.grant_access && !draft.email.trim()}
              value={draft.phone}
              onChange={(e) => setDraft((d) => ({ ...d, phone: e.target.value }))}
            />
          </Field>
          <Field label="Email">
            <Input
              type="email"
              value={draft.email}
              onChange={(e) => setDraft((d) => ({ ...d, email: e.target.value }))}
            />
          </Field>
          <Field label="Position / job title">
            <Input
              value={draft.position}
              onChange={(e) => setDraft((d) => ({ ...d, position: e.target.value }))}
              placeholder="e.g. Shop Manager, Cashier, Designer"
            />
          </Field>
          <Field label="Hire date">
            <Input
              type="date"
              value={draft.hire_date}
              onChange={(e) => setDraft((d) => ({ ...d, hire_date: e.target.value }))}
            />
          </Field>
          {canSetSalary && (
            <Field label="Monthly basic salary" className="col-span-2">
              <Input
                inputMode="decimal"
                placeholder="Optional"
                value={draft.base_pay}
                onChange={(e) => setDraft((d) => ({ ...d, base_pay: e.target.value }))}
              />
            </Field>
          )}
        </div>
        {canGrantAccess && (
          <div className="space-y-3 rounded-xl border bg-muted/30 p-3">
            <label className="flex cursor-pointer items-center gap-2 text-sm font-medium">
              <input
                type="checkbox"
                checked={draft.grant_access}
                onChange={(e) => setDraft((d) => ({ ...d, grant_access: e.target.checked }))}
              />
              Grant system login now (Admin, Manager, or Staff)
            </label>
            {draft.grant_access && (
              <div className="grid grid-cols-2 gap-3 pt-1">
                <Field label="Role *">
                  <NativeSelect
                    required
                    value={draft.role_id}
                    onChange={(e) => setDraft((d) => ({ ...d, role_id: e.target.value }))}
                  >
                    <option value="">Choose role…</option>
                    {roles.data?.map((r) => (
                      <option key={r.id} value={r.id}>
                        {r.name}
                      </option>
                    ))}
                  </NativeSelect>
                </Field>
                <Field label="Default location">
                  <NativeSelect
                    value={draft.default_branch_id}
                    onChange={(e) => setDraft((d) => ({ ...d, default_branch_id: e.target.value }))}
                  >
                    <option value="">Main / default</option>
                    {activeBranches.map((b) => (
                      <option key={b.id} value={b.id}>
                        {b.name}
                      </option>
                    ))}
                  </NativeSelect>
                </Field>
                <Field label="Temporary password *" className="col-span-2">
                  <Input
                    required
                    minLength={8}
                    type="password"
                    value={draft.password}
                    onChange={(e) => setDraft((d) => ({ ...d, password: e.target.value }))}
                    placeholder="At least 8 characters"
                  />
                </Field>
              </div>
            )}
          </div>
        )}
      </FormDialog>

      <FormDialog
        open={accessTarget !== null}
        onOpenChange={(o) => !o && setAccessTarget(null)}
        title={`Grant system access — ${accessTarget?.name ?? ""}`}
        description="Assign a Manager, Staff, or custom role so this employee can sign in."
        submitLabel="Grant access"
        busy={grantLogin.isPending}
        onSubmit={() =>
          grantLogin.mutate({
            name: accessTarget?.name ?? "",
            phone: accessForm.phone,
            email: accessForm.email,
            role_id: accessForm.role_id,
            branch_ids: accessForm.default_branch_id ? [accessForm.default_branch_id] : [],
            password: accessForm.password,
          })
        }
      >
        <div className="grid grid-cols-2 gap-3">
          <Field label="Phone">
            <Input
              required={!accessForm.email.trim()}
              value={accessForm.phone}
              onChange={(e) => setAccessForm((f) => ({ ...f, phone: e.target.value }))}
            />
          </Field>
          <Field label="Email (optional)">
            <Input
              type="email"
              value={accessForm.email}
              onChange={(e) => setAccessForm((f) => ({ ...f, email: e.target.value }))}
            />
          </Field>
        </div>
        <div className="grid grid-cols-2 gap-3">
          <Field label="Role *">
            <NativeSelect
              required
              value={accessForm.role_id}
              onChange={(e) => setAccessForm((f) => ({ ...f, role_id: e.target.value }))}
            >
              <option value="">Choose role…</option>
              {roles.data?.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.name}
                </option>
              ))}
            </NativeSelect>
          </Field>
          <Field label="Default location">
            <NativeSelect
              value={accessForm.default_branch_id}
              onChange={(e) => setAccessForm((f) => ({ ...f, default_branch_id: e.target.value }))}
            >
              <option value="">Main / default</option>
              {activeBranches.map((b) => (
                <option key={b.id} value={b.id}>
                  {b.name}
                </option>
              ))}
            </NativeSelect>
          </Field>
        </div>
        <Field label="Temporary password *">
          <Input
            required
            minLength={8}
            type="password"
            value={accessForm.password}
            onChange={(e) => setAccessForm((f) => ({ ...f, password: e.target.value }))}
            placeholder="At least 8 characters"
          />
        </Field>
      </FormDialog>
    </>
  );
}

interface MeLeaveRes {
  requests?: LeaveRequest[];
}

function LeaveTab() {
  const { can, canAny, user } = useAuth();
  const approver = can("hr.leave.approve");
  const canViewAll = canAny("hr.leave.approve", "hr.employee.view");
  const [status, setStatus] = useState("requested");
  const [page, setPage] = useState(1);
  const allList = useGetPage<LeaveRequest>(
    "/leave-requests",
    {
      status: status || undefined,
      page,
    },
    { enabled: canViewAll },
  );
  const myLeave = useGet<MeLeaveRes>("/me/leave", undefined, { enabled: !canViewAll });
  const rows = canViewAll
    ? allList.data?.data
    : (myLeave.data?.requests ?? []).filter((r) => !status || r.status === status);
  const loading = canViewAll ? allList.isLoading : myLeave.isLoading;
  const types = useGet<LeaveType[]>("/leave-types");
  const employees = useGet<Employee[]>(
    "/employees",
    { per_page: 100 },
    { enabled: approver && canAny("hr.employee.view", "hr.employee.manage") },
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
  const request = useWrite<Record<string, unknown>, unknown>("POST", "/leave-requests", {
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
          {["requested", "approved", "rejected", "cancelled"].map((v) => (
            <option key={v} value={v}>
              {v}
            </option>
          ))}
        </NativeSelect>
        <Button onClick={() => setOpen(true)}>
          <Plus size={16} /> Request leave
        </Button>
      </div>
      <DataGrid
        rows={rows}
        loading={loading}
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
            cell: (l) => {
              const isOwn = Boolean(user?.employee_id) && l.employee_id === user?.employee_id;
              return (
                (l.status === "requested" || l.status === "pending") && (
                  <div className="flex gap-2">
                    {approver && !isOwn && (
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
                    {(isOwn || !approver) && (
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => decide.mutate({ id: l.id, action: "cancel" })}
                      >
                        Cancel
                      </Button>
                    )}
                  </div>
                )
              );
            },
          },
        ]}
      />
      {canViewAll && <Pager meta={allList.data?.meta} onPage={setPage} />}
      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title="Request leave"
        busy={request.isPending}
        onSubmit={() => {
          const payload: Record<string, unknown> = {
            leave_type_id: draft.leave_type_id,
            start_date: draft.start_date,
            end_date: draft.end_date,
            reason: draft.reason,
          };
          if (draft.employee_id) payload.employee_id = draft.employee_id;
          request.mutate(payload);
        }}
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
  const { canAny } = useAuth();
  const canEmployees = canAny("hr.employee.view", "hr.employee.manage");
  const canAttendance = canAny("hr.attendance.manage", "hr.employee.view");
  return (
    <>
      <PageHeader title="People" description="Employees, leave and daily attendance." />
      <ClockCard />
      <Tabs defaultValue={canEmployees ? "employees" : "leave"}>
        <TabsList className="mb-4">
          {canEmployees && <TabsTrigger value="employees">Employees</TabsTrigger>}
          <TabsTrigger value="leave">Leave</TabsTrigger>
          {canAttendance && <TabsTrigger value="attendance">Today</TabsTrigger>}
        </TabsList>
        {canEmployees && (
          <TabsContent value="employees">
            <EmployeesTab />
          </TabsContent>
        )}
        <TabsContent value="leave">
          <LeaveTab />
        </TabsContent>
        {canAttendance && (
          <TabsContent value="attendance">
            <AttendanceTab />
          </TabsContent>
        )}
      </Tabs>
    </>
  );
}
