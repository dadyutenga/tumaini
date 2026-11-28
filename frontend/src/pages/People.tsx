import { useEffect, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { Clock, KeyRound, LogIn, LogOut, Plus, Search, ShieldCheck } from "lucide-react";
import { toast } from "sonner";
import { Badge } from "@/components/ui/badge";
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
  SegmentedChips,
  StatusBadge,
} from "@/components/shared/kit";
import { CopyButton } from "@/components/shared/Display";
import { useAuth } from "@/features/auth/auth";
import { errorMessage, useGet, useGetPage, useWrite } from "@/hooks/useApi";
import { api } from "@/lib/api";
import { formatDate, formatMoney, today } from "@/lib/formatters";
import type { Branch } from "@/types";
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";
import type { Translations } from "@/features/i18n/i18n";
import { statusLabel } from "@/features/i18n/status";

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
  const { t } = useI18n();
  const me = useGet<{ id: string; name: string }>("/me/employee");
  const refresh = ["/attendance", "/me"];
  const clockIn = useWrite("POST", "/attendance/clock-in", {
    invalidate: refresh,
    success: t.common.clockedIn,
  });
  const clockOut = useWrite("POST", "/attendance/clock-out", {
    invalidate: refresh,
    success: t.common.clockedOut,
  });
  if (!me.data) return null;
  return (
    <div className="od-card flex flex-wrap items-center gap-3.5 p-4">
      <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl border border-[var(--od-border)] bg-[var(--od-surface-subtle)] text-[var(--od-ink-secondary)]">
        <Clock size={18} />
      </div>
      <div className="mr-auto min-w-0">
        <p className="font-semibold text-[var(--od-ink)]">{me.data.name}</p>
        <p className="od-caption">{t.hr.recordYourOwnAttendanceFor}</p>
      </div>
      <div className="flex items-center gap-2">
        <Button onClick={() => clockIn.mutate({})} disabled={clockIn.isPending}>
          <LogIn size={15} /> {t.common.clockIn}
        </Button>
        <Button variant="outline" onClick={() => clockOut.mutate({})} disabled={clockOut.isPending}>
          <LogOut size={15} /> {t.common.clockOut}
        </Button>
      </div>
    </div>
  );
}

function EmployeesTab() {
  const { t } = useI18n();
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
    success: t.hr.employeeAdded,
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
      success: t.hr.systemAccessGranted,
      onSuccess: () => {
        if (accessTarget) {
          const roleName =
            roles.data?.find((r) => r.id === accessForm.role_id)?.name ?? t.hr.systemUser;
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
      const roleName = roles.data?.find((r) => r.id === draft.role_id)?.name ?? t.hr.systemUser;
      setGrantedBanner({ name: draft.name, role: roleName, password: draft.password });
      toast.success(t.hr.employeeAddedAndSystemAccess);
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
    <div className="space-y-4">
      {grantedBanner && (
        <div className="flex flex-wrap items-center justify-between gap-3 rounded-2xl border border-[var(--od-info-mark)]/30 bg-[var(--od-surface-subtle)] p-4 text-sm">
          <div>
            <p className="font-semibold text-[var(--od-ink)]">
              {fmt(t.hr.systemAccessGrantedFor, {
                name: grantedBanner.name,
                role: grantedBanner.role,
              })}
            </p>
            <p className="od-caption mt-0.5">{t.hr.shareThisTemporaryPasswordSecurely}</p>
          </div>
          <div className="flex items-center gap-2">
            <code className="od-mono rounded-lg border border-[var(--od-border-strong)] bg-[var(--od-surface)] px-2.5 py-1 text-xs font-medium text-[var(--od-ink)]">
              {grantedBanner.password}
            </code>
            <CopyButton text={grantedBanner.password} />
            <Button size="sm" variant="ghost" onClick={() => setGrantedBanner(null)}>
              {t.common.dismiss}
            </Button>
          </div>
        </div>
      )}

      <FilterBar className="justify-between">
        <div className="relative">
          <Search
            size={14}
            className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-[var(--od-ink-muted)]"
          />
          <Input
            aria-label={t.hr.searchEmployees}
            className="w-64 pl-8"
            placeholder={t.hr.searchNameNumberOrPhone}
            value={q}
            onChange={(e) => {
              setQ(e.target.value);
              setPage(1);
            }}
          />
        </div>
        {can("hr.employee.manage") && (
          <Button onClick={() => setOpen(true)}>
            <Plus size={16} /> {t.common.newEmployee}
          </Button>
        )}
      </FilterBar>

      <DataGrid
        rows={list.data?.data}
        loading={list.isLoading}
        rowKey={(e) => e.id}
        empty={t.hr.noEmployeesYetCreateAn}
        onRowClick={(e) => navigate(`/employees/${e.id}`)}
        columns={[
          {
            header: t.hr.no,
            cell: (e) => (
              <span className="od-mono text-xs text-[var(--od-ink-secondary)]">
                {e.employee_no}
              </span>
            ),
          },
          {
            header: t.common.name2,
            cell: (e) => <span className="font-medium text-[var(--od-ink)]">{e.name}</span>,
          },
          {
            header: t.hr.position,
            cell: (e) => e.position || e.position_name || e.department_name || "-",
          },
          { header: t.common.location, cell: (e) => e.branch_name ?? "-" },
          { header: t.hr.hired, cell: (e) => (e.hire_date ? formatDate(e.hire_date) : "-") },
          ...(canSeeSalary
            ? [
                {
                  header: t.common.basePay,
                  align: "right" as const,
                  cell: (e: Employee) => (
                    <span className="od-tabular font-medium text-[var(--od-ink)]">
                      {formatMoney(e.base_pay)}
                    </span>
                  ),
                },
              ]
            : []),
          {
            header: t.hr.systemAccess,
            cell: (e) =>
              e.user_id ? (
                <Badge variant="secondary" className="gap-1">
                  <ShieldCheck size={12} className="text-[var(--od-good)]" />
                  {e.role_name || t.hr.activeLogin}
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
                  <KeyRound size={12} /> {t.common.grantAccess}
                </Button>
              ) : (
                <span className="text-xs text-[var(--od-ink-muted)]">{t.hr.noLogin}</span>
              ),
          },
          { header: t.common.status, cell: (e) => <StatusBadge value={e.status} /> },
        ]}
      />
      <Pager meta={list.data?.meta} onPage={setPage} />

      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title={t.common.newEmployee}
        description={t.hr.everyManagerOrStaffMember}
        busy={create.isPending || creatingWithAccess}
        onSubmit={() => void submitNewEmployee()}
      >
        <Field label={t.hr.fullName}>
          <Input
            required
            value={draft.name}
            onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))}
          />
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label={t.common.phone}>
            <Input
              required={draft.grant_access && !draft.email.trim()}
              value={draft.phone}
              onChange={(e) => setDraft((d) => ({ ...d, phone: e.target.value }))}
            />
          </Field>
          <Field label={t.common.email}>
            <Input
              type="email"
              value={draft.email}
              onChange={(e) => setDraft((d) => ({ ...d, email: e.target.value }))}
            />
          </Field>
          <Field label={t.hr.positionJobTitle}>
            <Input
              value={draft.position}
              onChange={(e) => setDraft((d) => ({ ...d, position: e.target.value }))}
              placeholder={t.hr.eGShopManagerCashier}
            />
          </Field>
          <Field label={t.hr.hireDate}>
            <Input
              type="date"
              value={draft.hire_date}
              onChange={(e) => setDraft((d) => ({ ...d, hire_date: e.target.value }))}
            />
          </Field>
          {canSetSalary && (
            <Field label={t.hr.monthlyBasicSalary} className="col-span-2">
              <Input
                inputMode="decimal"
                className="od-tabular"
                placeholder={t.common.optional}
                value={draft.base_pay}
                onChange={(e) => setDraft((d) => ({ ...d, base_pay: e.target.value }))}
              />
            </Field>
          )}
        </div>
        {canGrantAccess && (
          <div className="space-y-3 rounded-xl border border-[var(--od-border)] bg-[var(--od-surface-subtle)] p-3.5">
            <label className="flex cursor-pointer items-center gap-2 text-sm font-medium text-[var(--od-ink)]">
              <input
                type="checkbox"
                checked={draft.grant_access}
                onChange={(e) => setDraft((d) => ({ ...d, grant_access: e.target.checked }))}
              />
              {t.hr.grantSystemLoginNowAdmin}
            </label>
            {draft.grant_access && (
              <div className="grid grid-cols-2 gap-3 pt-1">
                <Field label={t.common.role2}>
                  <NativeSelect
                    required
                    value={draft.role_id}
                    onChange={(e) => setDraft((d) => ({ ...d, role_id: e.target.value }))}
                  >
                    <option value="">{t.hr.chooseRole}</option>
                    {roles.data?.map((r) => (
                      <option key={r.id} value={r.id}>
                        {r.name}
                      </option>
                    ))}
                  </NativeSelect>
                </Field>
                <Field label={t.common.defaultLocation}>
                  <NativeSelect
                    value={draft.default_branch_id}
                    onChange={(e) => setDraft((d) => ({ ...d, default_branch_id: e.target.value }))}
                  >
                    <option value="">{t.common.mainDefault}</option>
                    {activeBranches.map((b) => (
                      <option key={b.id} value={b.id}>
                        {b.name}
                      </option>
                    ))}
                  </NativeSelect>
                </Field>
                <Field label={t.common.temporaryPassword} className="col-span-2">
                  <Input
                    required
                    minLength={8}
                    type="password"
                    value={draft.password}
                    onChange={(e) => setDraft((d) => ({ ...d, password: e.target.value }))}
                    placeholder={t.common.atLeast8Characters}
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
        title={fmt(t.hr.grantSystemAccess, { name: accessTarget?.name ?? "" })}
        description={t.hr.assignAManagerStaffOr}
        submitLabel={t.common.grantAccess}
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
          <Field label={t.common.phone}>
            <Input
              required={!accessForm.email.trim()}
              value={accessForm.phone}
              onChange={(e) => setAccessForm((f) => ({ ...f, phone: e.target.value }))}
            />
          </Field>
          <Field label={t.hr.emailOptional}>
            <Input
              type="email"
              value={accessForm.email}
              onChange={(e) => setAccessForm((f) => ({ ...f, email: e.target.value }))}
            />
          </Field>
        </div>
        <div className="grid grid-cols-2 gap-3">
          <Field label={t.common.role2}>
            <NativeSelect
              required
              value={accessForm.role_id}
              onChange={(e) => setAccessForm((f) => ({ ...f, role_id: e.target.value }))}
            >
              <option value="">{t.hr.chooseRole}</option>
              {roles.data?.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.name}
                </option>
              ))}
            </NativeSelect>
          </Field>
          <Field label={t.common.defaultLocation}>
            <NativeSelect
              value={accessForm.default_branch_id}
              onChange={(e) => setAccessForm((f) => ({ ...f, default_branch_id: e.target.value }))}
            >
              <option value="">{t.common.mainDefault}</option>
              {activeBranches.map((b) => (
                <option key={b.id} value={b.id}>
                  {b.name}
                </option>
              ))}
            </NativeSelect>
          </Field>
        </div>
        <Field label={t.common.temporaryPassword}>
          <Input
            required
            minLength={8}
            type="password"
            value={accessForm.password}
            onChange={(e) => setAccessForm((f) => ({ ...f, password: e.target.value }))}
            placeholder={t.common.atLeast8Characters}
          />
        </Field>
      </FormDialog>
    </div>
  );
}

interface MeLeaveRes {
  requests?: LeaveRequest[];
}

const leaveStatusOptions = (t: Translations) => [
  { value: "", label: t.common.all },
  { value: "requested", label: t.common.requested },
  { value: "approved", label: t.common.approved },
  { value: "rejected", label: t.common.rejected },
  { value: "cancelled", label: t.common.cancelled },
];

function LeaveTab() {
  const { t } = useI18n();
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
    success: t.hr.leaveRequested,
    onSuccess: () => {
      setOpen(false);
      setDraft(blank);
    },
  });
  const decide = useWrite<{ id: string; action: string }, unknown>(
    "POST",
    (b) => `/leave-requests/${b.id}/${b.action}`,
    { invalidate: refresh, success: t.hr.leaveUpdated, toBody: () => ({}) },
  );
  return (
    <div className="space-y-4">
      <FilterBar className="justify-between">
        <SegmentedChips
          ariaLabel={t.common.filterByStatus}
          value={status}
          onChange={(v) => {
            setStatus(v);
            setPage(1);
          }}
          options={leaveStatusOptions(t)}
        />
        <Button onClick={() => setOpen(true)}>
          <Plus size={16} /> {t.hr.requestLeave}
        </Button>
      </FilterBar>
      <DataGrid
        rows={rows}
        loading={loading}
        rowKey={(l) => l.id}
        empty={t.hr.noLeaveRequests}
        columns={[
          {
            header: t.common.employee,
            cell: (l) => (
              <span className="font-medium text-[var(--od-ink)]">
                {l.employee_name ?? t.common.me}
              </span>
            ),
          },
          { header: t.common.type, cell: (l) => l.leave_type },
          {
            header: t.hr.dates,
            cell: (l) => `${formatDate(l.start_date)} – ${formatDate(l.end_date)}`,
          },
          {
            header: t.common.days,
            align: "right",
            cell: (l) => <span className="od-tabular">{l.days}</span>,
          },
          { header: t.common.status, cell: (l) => <StatusBadge value={l.status} /> },
          {
            header: "",
            cell: (l) => {
              const isOwn = Boolean(user?.employee_id) && l.employee_id === user?.employee_id;
              return (
                (l.status === "requested" || l.status === "pending") && (
                  <div className="flex justify-end gap-1.5">
                    {approver && !isOwn && (
                      <>
                        <Button
                          size="sm"
                          onClick={() => decide.mutate({ id: l.id, action: "approve" })}
                        >
                          {t.common.approve}
                        </Button>
                        <Button
                          size="sm"
                          variant="outline"
                          className="text-[var(--od-bad)] hover:text-[var(--od-bad)]"
                          onClick={() => decide.mutate({ id: l.id, action: "reject" })}
                        >
                          {t.common.reject}
                        </Button>
                      </>
                    )}
                    {(isOwn || !approver) && (
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => decide.mutate({ id: l.id, action: "cancel" })}
                      >
                        {t.common.cancel}
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
        title={t.hr.requestLeave}
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
          <Field label={t.hr.employeeLeaveEmptyForYourself}>
            <NativeSelect
              value={draft.employee_id}
              onChange={(e) => setDraft((d) => ({ ...d, employee_id: e.target.value }))}
            >
              <option value="">{t.hr.myself}</option>
              {employees.data.map((e) => (
                <option key={e.id} value={e.id}>
                  {e.name}
                </option>
              ))}
            </NativeSelect>
          </Field>
        )}
        <Field label={t.hr.leaveType}>
          <NativeSelect
            required
            value={draft.leave_type_id}
            onChange={(e) => setDraft((d) => ({ ...d, leave_type_id: e.target.value }))}
          >
            <option value="">{t.common.choose}</option>
            {types.data
              ?.filter((item) => item.is_active)
              .map((item) => (
                <option key={item.id} value={item.id}>
                  {item.name}
                </option>
              ))}
          </NativeSelect>
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label={t.common.from}>
            <Input
              required
              type="date"
              value={draft.start_date}
              onChange={(e) => setDraft((d) => ({ ...d, start_date: e.target.value }))}
            />
          </Field>
          <Field label={t.common.to}>
            <Input
              required
              type="date"
              value={draft.end_date}
              onChange={(e) => setDraft((d) => ({ ...d, end_date: e.target.value }))}
            />
          </Field>
        </div>
        <Field label={t.common.reason}>
          <Input
            value={draft.reason}
            onChange={(e) => setDraft((d) => ({ ...d, reason: e.target.value }))}
          />
        </Field>
      </FormDialog>
    </div>
  );
}

function AttendanceTab() {
  const { t } = useI18n();
  const board = useGet<Today>("/attendance/today");
  const counts = board.data?.counts ?? {};
  return (
    <div className="space-y-4">
      {Object.keys(counts).length > 0 && (
        <div className="flex flex-wrap gap-2">
          {Object.entries(counts).map(([k, v]) => (
            <div
              key={k}
              className="inline-flex items-center gap-2 rounded-full border border-[var(--od-border-strong)] bg-[var(--od-surface)] px-3.5 py-1.5 text-xs"
            >
              <span className="capitalize text-[var(--od-ink-secondary)]">{statusLabel(t, k)}</span>
              <span className="od-tabular font-semibold text-[var(--od-ink)]">{v}</span>
            </div>
          ))}
        </div>
      )}
      <DataGrid
        rows={board.data?.employees}
        loading={board.isLoading}
        rowKey={(r) => r.employee_id}
        empty={t.hr.noEmployeesToShow}
        columns={[
          {
            header: t.common.employee,
            cell: (r) => (
              <span className="font-medium text-[var(--od-ink)]">{r.employee_name}</span>
            ),
          },
          {
            header: t.hr.in,
            cell: (r) => <span className="od-mono text-xs">{time(r.first_in)}</span>,
          },
          {
            header: t.common.out,
            cell: (r) => <span className="od-mono text-xs">{time(r.last_out)}</span>,
          },
          { header: t.hr.state, cell: (r) => <StatusBadge value={r.state} /> },
        ]}
      />
    </div>
  );
}

export default function People() {
  const { t } = useI18n();
  const { canAny } = useAuth();
  const canEmployees = canAny("hr.employee.view", "hr.employee.manage");
  const canAttendance = canAny("hr.attendance.manage", "hr.employee.view");
  return (
    <div className="space-y-6">
      <PageHeader title={t.common.people} description={t.hr.employeesLeaveAndDailyAttendance} />
      <ClockCard />
      <Tabs defaultValue={canEmployees ? "employees" : "leave"}>
        <TabsList className="mb-4">
          {canEmployees && <TabsTrigger value="employees">{t.hr.employees}</TabsTrigger>}
          <TabsTrigger value="leave">{t.hr.leave}</TabsTrigger>
          {canAttendance && <TabsTrigger value="attendance">{t.common.today}</TabsTrigger>}
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
    </div>
  );
}
