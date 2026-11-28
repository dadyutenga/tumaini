import { useState } from "react";
import { Link } from "react-router-dom";
import { KeyRound, LogOut, Pencil, Plus, Shield, Trash2, UserPlus } from "lucide-react";
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
  StatusBadge,
} from "@/components/shared/kit";
import { ConfirmDialog, CopyButton } from "@/components/shared/Display";
import { useAuth } from "@/features/auth/auth";
import { useGet, useGetPage, useWrite } from "@/hooks/useApi";
import { RoleEditor } from "@/features/auth/RoleEditor";
import { UserPermissionsDialog } from "@/features/auth/UserPermissionsDialog";
import type { Branch, SessionUser } from "@/types";
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";

interface Role {
  id: string;
  name: string;
  slug: string;
  rank?: number;
  is_system: boolean;
}
interface EmployeeRef {
  id: string;
  employee_no: string;
  name: string;
  phone?: string | null;
  email?: string | null;
  position?: string | null;
  branch_id?: string | null;
  user_id?: string | null;
  status: string;
}

const blankGrant = {
  employee_id: "",
  name: "",
  phone: "",
  email: "",
  password: "",
  role_id: "",
  default_branch_id: "",
};

export default function Team() {
  const { t } = useI18n();
  const { can } = useAuth();
  const manage = can("security.user.manage");
  const [editingRole, setEditingRole] = useState<string | null | undefined>(undefined);
  const [retireRole, setRetireRole] = useState<Role | null>(null);
  const [page, setPage] = useState(1);
  const users = useGetPage<SessionUser>("/users", { page, per_page: 25 });
  const roles = useGet<Role[]>("/roles", undefined, { enabled: manage });
  const branches = useGet<Branch[]>("/branches");
  const employees = useGet<EmployeeRef[]>(
    "/employees",
    { per_page: 200 },
    { enabled: manage || can("hr.employee.view") },
  );

  const [grantOpen, setGrantOpen] = useState(false);
  const [editingUser, setEditingUser] = useState<SessionUser | null>(null);
  const [draft, setDraft] = useState(blankGrant);
  const [resetTarget, setResetTarget] = useState<SessionUser | null>(null);
  const [resetPw, setResetPw] = useState("");
  const [resetResult, setResetResult] = useState<{ name: string; password: string } | null>(null);
  const [kickTarget, setKickTarget] = useState<SessionUser | null>(null);
  const [permTarget, setPermTarget] = useState<SessionUser | null>(null);

  const grantAccess = useWrite<Record<string, unknown>, unknown>(
    "POST",
    () => `/employees/${draft.employee_id}/user`,
    {
      invalidate: ["/users", "/employees"],
      success: t.admin.systemAccessGrantedToEmployee,
      onSuccess: () => {
        setResetResult({ name: draft.name, password: draft.password });
        setGrantOpen(false);
        setDraft(blankGrant);
      },
    },
  );
  const update = useWrite<Record<string, unknown>, unknown>(
    "PUT",
    () => `/users/${editingUser?.id}`,
    {
      invalidate: ["/users", "/employees"],
      success: t.admin.userUpdated,
      onSuccess: () => {
        setEditingUser(null);
        setDraft(blankGrant);
      },
    },
  );
  const toggle = useWrite<SessionUser>(
    "POST",
    (u) => `/users/${u.id}/${u.is_active ? "deactivate" : "activate"}`,
    {
      invalidate: ["/users"],
      success: t.admin.userUpdated,
    },
  );
  const resetPassword = useWrite<{ password: string }, { temporary_password?: string }>(
    "POST",
    () => `/users/${resetTarget?.id}/reset-password`,
    {
      success: t.admin.passwordResetUserWillBe,
      onSuccess: (res) => {
        if (resetTarget) {
          setResetResult({
            name: resetTarget.name,
            password: res?.temporary_password ?? resetPw,
          });
        }
        setResetTarget(null);
        setResetPw("");
      },
    },
  );
  const forceSignOut = useWrite("DELETE", () => `/users/${kickTarget?.id}/sessions`, {
    success: t.admin.userSignedOutOfAll,
    onSuccess: () => setKickTarget(null),
  });
  const retire = useWrite("DELETE", () => `/roles/${retireRole?.id}`, {
    invalidate: ["/roles"],
    success: t.admin.roleRetired,
    onSuccess: () => setRetireRole(null),
  });

  const branchNames = new Map((branches.data ?? []).map((b) => [b.id, b.name]));
  const activeBranches = (branches.data ?? []).filter((b) => b.is_active);
  const employeeList = Array.isArray(employees.data) ? employees.data : [];
  const empById = new Map(employeeList.map((e) => [e.id, e]));
  const unlinkedEmployees = employeeList.filter((e) => !e.user_id && e.status !== "terminated");

  function onPickEmployee(empId: string) {
    const emp = empById.get(empId);
    if (!emp) {
      setDraft((d) => ({ ...d, employee_id: empId }));
      return;
    }
    setDraft((d) => ({
      ...d,
      employee_id: emp.id,
      name: emp.name,
      phone: emp.phone ?? "",
      email: emp.email ?? "",
      default_branch_id: emp.branch_id ?? d.default_branch_id,
    }));
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title={t.common.teamAndRoles}
        description={t.admin.everyManagerOrStaffMember}
        actions={
          manage && (
            <div className="flex flex-wrap gap-2">
              <Button variant="outline" asChild>
                <Link to="/people?new=1">
                  <UserPlus size={16} /> {t.common.newEmployee}
                </Link>
              </Button>
              <Button
                onClick={() => {
                  setDraft(blankGrant);
                  setGrantOpen(true);
                }}
              >
                <Plus size={16} /> {t.common.grantAccess}
              </Button>
            </div>
          )
        }
      />
      {resetResult && (
        <div className="flex flex-wrap items-center justify-between gap-3 rounded-2xl border border-[var(--od-info-mark)]/30 bg-[var(--od-surface-subtle)] p-4 text-sm">
          <div>
            <p className="font-semibold text-[var(--od-ink)]">
              {fmt(t.admin.temporaryPasswordFor, { name: resetResult.name })}
            </p>
            <p className="od-caption mt-0.5">{t.admin.shareThisSecurelyTheyMust}</p>
          </div>
          <div className="flex items-center gap-2">
            <code className="od-mono rounded-lg border border-[var(--od-border-strong)] bg-[var(--od-surface)] px-2.5 py-1 text-xs font-medium text-[var(--od-ink)]">
              {resetResult.password}
            </code>
            <CopyButton text={resetResult.password} />
            <Button size="sm" variant="ghost" onClick={() => setResetResult(null)}>
              {t.common.dismiss}
            </Button>
          </div>
        </div>
      )}
      <Tabs defaultValue="users">
        <TabsList className="mb-4">
          <TabsTrigger value="users">{t.admin.users}</TabsTrigger>
          <TabsTrigger value="roles">{t.admin.roles}</TabsTrigger>
        </TabsList>
        <TabsContent value="users">
          <DataGrid
            rows={users.data?.data}
            loading={users.isLoading}
            rowKey={(u) => u.id}
            columns={[
              {
                header: t.common.name2,
                cell: (u) => <span className="font-medium text-[var(--od-ink)]">{u.name}</span>,
              },
              {
                header: t.common.contact,
                cell: (u) => (
                  <span className="od-mono text-xs text-[var(--od-ink-secondary)]">
                    {u.phone ?? u.email ?? "-"}
                  </span>
                ),
              },
              {
                header: t.admin.employeeProfile,
                cell: (u) => {
                  if (u.employee_id) {
                    const emp = empById.get(u.employee_id);
                    return (
                      <Link
                        to={`/employees/${u.employee_id}`}
                        className="text-xs font-medium text-[var(--od-info)] hover:underline"
                      >
                        {emp
                          ? `${emp.employee_no} · ${emp.position || t.common.employee}`
                          : t.admin.viewProfile}
                      </Link>
                    );
                  }
                  return (
                    <Badge variant="outline" className="text-[11px] text-[var(--od-ink-muted)]">
                      {u.role_slug === "admin" || u.role_slug === "owner"
                        ? t.common.owner
                        : t.common.notLinked}
                    </Badge>
                  );
                },
              },
              {
                header: t.common.role,
                cell: (u) => (u.role_name ? <Badge variant="secondary">{u.role_name}</Badge> : "-"),
              },
              {
                header: t.common.defaultLocation,
                cell: (u) =>
                  (u.branch_ids ?? [])
                    .map((id) => branchNames.get(id) ?? id.slice(0, 6))
                    .join(", ") || "-",
              },
              {
                header: t.common.status,
                cell: (u) => <StatusBadge value={u.is_active ? "active" : "inactive"} />,
              },
              {
                header: "",
                className: "text-right",
                cell: (u) =>
                  manage && (
                    <div className="flex flex-wrap justify-end gap-1">
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => {
                          setDraft({
                            employee_id: u.employee_id ?? "",
                            name: u.name,
                            phone: u.phone ?? "",
                            email: u.email ?? "",
                            password: "",
                            role_id: u.role_id ?? "",
                            default_branch_id: (u.branch_ids ?? [])[0] ?? "",
                          });
                          setEditingUser(u);
                        }}
                      >
                        <Pencil size={13} /> {t.common.edit}
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => {
                          setResetPw("");
                          setResetTarget(u);
                        }}
                      >
                        <KeyRound size={13} /> {t.admin.resetPw}
                      </Button>
                      {u.role_slug !== "admin" && u.role_slug !== "owner" && (
                        <Button variant="ghost" size="sm" onClick={() => setPermTarget(u)}>
                          <Shield size={13} /> {t.admin.perms}
                        </Button>
                      )}
                      <Button variant="ghost" size="sm" onClick={() => setKickTarget(u)}>
                        <LogOut size={13} /> {t.common.signOut}
                      </Button>
                      <Button variant="ghost" size="sm" onClick={() => toggle.mutate(u)}>
                        {u.is_active ? t.common.deactivate : t.common.activate}
                      </Button>
                    </div>
                  ),
              },
            ]}
          />
          <Pager meta={users.data?.meta} onPage={setPage} />
        </TabsContent>
        <TabsContent value="roles" className="space-y-4">
          {manage && (
            <FilterBar className="justify-end">
              <Button onClick={() => setEditingRole(null)}>
                <Plus size={16} /> {t.admin.newRole}
              </Button>
            </FilterBar>
          )}
          <DataGrid
            rows={roles.data}
            loading={roles.isLoading}
            rowKey={(r) => r.id}
            columns={[
              {
                header: t.common.role,
                cell: (r) => <span className="font-medium text-[var(--od-ink)]">{r.name}</span>,
              },
              {
                header: t.admin.rank,
                align: "right",
                cell: (r) => <span className="od-tabular">{r.rank ?? "-"}</span>,
              },
              {
                header: t.common.type,
                cell: (r) => (
                  <Badge variant={r.is_system ? "secondary" : "outline"}>
                    {r.is_system ? t.admin.builtIn : t.common.custom}
                  </Badge>
                ),
              },
              {
                header: "",
                cell: (r) => (
                  <div className="flex justify-end gap-1.5">
                    <Button size="sm" variant="outline" onClick={() => setEditingRole(r.id)}>
                      {manage ? t.admin.editPermissions : t.common.view}
                    </Button>
                    {manage && !r.is_system && (
                      <Button
                        size="sm"
                        variant="ghost"
                        className="text-[var(--od-bad)] hover:text-[var(--od-bad)]"
                        onClick={() => setRetireRole(r)}
                      >
                        <Trash2 size={14} /> {t.admin.retire}
                      </Button>
                    )}
                  </div>
                ),
              },
            ]}
          />
        </TabsContent>
      </Tabs>

      <RoleEditor
        roleId={editingRole ?? null}
        open={editingRole !== undefined}
        onClose={() => setEditingRole(undefined)}
      />

      {/* Employee-first Grant Access Dialog */}
      <FormDialog
        open={grantOpen}
        onOpenChange={(o) => {
          if (!o) {
            setGrantOpen(false);
            setDraft(blankGrant);
          }
        }}
        title={t.admin.grantSystemAccessToAn}
        description={t.admin.toGetAManagerStaff}
        submitLabel={unlinkedEmployees.length > 0 ? t.common.grantAccess : t.common.close}
        busy={grantAccess.isPending}
        onSubmit={() => {
          if (unlinkedEmployees.length === 0) {
            setGrantOpen(false);
            return;
          }
          grantAccess.mutate({
            name: draft.name,
            phone: draft.phone,
            email: draft.email,
            password: draft.password,
            role_id: draft.role_id,
            branch_ids: draft.default_branch_id ? [draft.default_branch_id] : [],
          });
        }}
      >
        {unlinkedEmployees.length === 0 ? (
          <div className="space-y-3 rounded-xl border border-dashed border-[var(--od-border-strong)] bg-[var(--od-surface-subtle)] p-5 text-center">
            <p className="text-sm font-semibold text-[var(--od-ink)]">
              {t.admin.noEmployeesWaitingForA}
            </p>
            <p className="od-caption">{t.admin.everyActiveEmployeeAlreadyHas}</p>
            <Button asChild size="sm">
              <Link to="/people?new=1">
                <UserPlus size={14} /> {t.admin.createEmployeeFirst}
              </Link>
            </Button>
          </div>
        ) : (
          <>
            <Field label={t.admin.employee}>
              <NativeSelect
                required
                value={draft.employee_id}
                onChange={(e) => onPickEmployee(e.target.value)}
              >
                <option value="">{t.admin.chooseAnEmployee}</option>
                {unlinkedEmployees.map((e) => (
                  <option key={e.id} value={e.id}>
                    {e.name} ({e.employee_no}
                    {e.position ? ` · ${e.position}` : ""})
                  </option>
                ))}
              </NativeSelect>
            </Field>
            <div className="grid grid-cols-2 gap-3">
              <Field label={t.common.phone}>
                <Input
                  required={!draft.email.trim()}
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
            </div>
            <div className="grid grid-cols-2 gap-3">
              <Field label={t.common.role2}>
                <NativeSelect
                  required
                  value={draft.role_id}
                  onChange={(e) => setDraft((d) => ({ ...d, role_id: e.target.value }))}
                >
                  <option value="">{t.common.choose}</option>
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
            </div>
            <Field label={t.common.temporaryPassword}>
              <Input
                required
                minLength={8}
                type="password"
                value={draft.password}
                onChange={(e) => setDraft((d) => ({ ...d, password: e.target.value }))}
                placeholder={t.common.atLeast8Characters}
              />
            </Field>
          </>
        )}
      </FormDialog>

      {/* Edit Existing User Dialog (also supports linking an unlinked user to an employee) */}
      <FormDialog
        open={editingUser !== null}
        onOpenChange={(o) => {
          if (!o) {
            setEditingUser(null);
            setDraft(blankGrant);
          }
        }}
        title={fmt(t.common.edit2, { name: editingUser?.name ?? "" })}
        busy={update.isPending}
        onSubmit={() => {
          const payload: Record<string, unknown> = {
            name: draft.name,
            phone: draft.phone,
            email: draft.email,
            role_id: draft.role_id,
            branch_ids: draft.default_branch_id ? [draft.default_branch_id] : [],
          };
          if (draft.employee_id && !editingUser?.employee_id) {
            payload.employee_id = draft.employee_id;
          }
          update.mutate(payload);
        }}
      >
        <Field label={t.common.name}>
          <Input
            required
            value={draft.name}
            onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))}
          />
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label={t.common.phone}>
            <Input
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
        </div>
        <Field label={t.common.role2}>
          <NativeSelect
            required
            value={draft.role_id}
            onChange={(e) => setDraft((d) => ({ ...d, role_id: e.target.value }))}
          >
            <option value="">{t.common.choose}</option>
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
        {!editingUser?.employee_id && unlinkedEmployees.length > 0 && (
          <Field label={t.admin.linkToEmployeeProfile}>
            <NativeSelect
              value={draft.employee_id}
              onChange={(e) => setDraft((d) => ({ ...d, employee_id: e.target.value }))}
            >
              <option value="">{t.common.notLinked}</option>
              {unlinkedEmployees.map((e) => (
                <option key={e.id} value={e.id}>
                  {e.name} ({e.employee_no})
                </option>
              ))}
            </NativeSelect>
          </Field>
        )}
        {!editingUser?.employee_id && employees.isSuccess && unlinkedEmployees.length === 0 && (
          <p className="od-caption">
            {t.admin.thisAccountHasNoEmployee}{" "}
            <Link to="/people?new=1" className="font-medium text-[var(--od-info)] hover:underline">
              {t.common.people}
            </Link>
            {t.admin.thenEditThisAccountAgain}
          </p>
        )}
      </FormDialog>

      <FormDialog
        open={resetTarget !== null}
        onOpenChange={(o) => !o && setResetTarget(null)}
        title={fmt(t.admin.resetPasswordFor, { name: resetTarget?.name ?? "" })}
        description={t.admin.setANewTemporaryPassword}
        submitLabel={t.admin.resetPassword}
        busy={resetPassword.isPending}
        onSubmit={() => resetPassword.mutate({ password: resetPw })}
      >
        <Field label={t.admin.newTemporaryPassword}>
          <Input
            required
            minLength={8}
            type="password"
            value={resetPw}
            onChange={(e) => setResetPw(e.target.value)}
          />
        </Field>
      </FormDialog>
      <ConfirmDialog
        open={kickTarget !== null}
        onOpenChange={(o) => !o && setKickTarget(null)}
        title={fmt(t.admin.forceSignOut, { name: kickTarget?.name ?? "" })}
        description={t.admin.everyActiveSessionOnAll}
        onConfirm={() => forceSignOut.mutate(undefined)}
      />
      <ConfirmDialog
        open={retireRole !== null}
        onOpenChange={(o) => !o && setRetireRole(null)}
        title={fmt(t.admin.retireRole, { name: retireRole?.name ?? "" })}
        description={t.admin.retiredRolesAreMarkedInactive}
        onConfirm={() => retire.mutate(undefined)}
      />
      {permTarget && (
        <UserPermissionsDialog
          userId={permTarget.id}
          userName={permTarget.name}
          open
          onClose={() => setPermTarget(null)}
        />
      )}
    </div>
  );
}
