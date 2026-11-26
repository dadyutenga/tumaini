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
      success: "System access granted to employee",
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
      success: "User updated",
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
      success: "User updated",
    },
  );
  const resetPassword = useWrite<{ password: string }, { temporary_password?: string }>(
    "POST",
    () => `/users/${resetTarget?.id}/reset-password`,
    {
      success: "Password reset. User will be asked to change it on next sign-in.",
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
    success: "User signed out of all devices",
    onSuccess: () => setKickTarget(null),
  });
  const retire = useWrite("DELETE", () => `/roles/${retireRole?.id}`, {
    invalidate: ["/roles"],
    success: "Role retired",
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
    <>
      <PageHeader
        title="Team and roles"
        description="Every Manager or Staff member starts as an Employee first, then receives system access and a role here or from People."
        actions={
          manage && (
            <div className="flex flex-wrap gap-2">
              <Button variant="outline" asChild>
                <Link to="/people?new=1">
                  <UserPlus size={16} /> New employee
                </Link>
              </Button>
              <Button
                onClick={() => {
                  setDraft(blankGrant);
                  setGrantOpen(true);
                }}
              >
                <Plus size={16} /> Grant access
              </Button>
            </div>
          )
        }
      />
      {resetResult && (
        <div className="mb-4 flex flex-wrap items-center justify-between gap-2 rounded-xl border border-primary/30 bg-primary/5 p-4 text-sm">
          <div>
            <p className="font-medium">Temporary password for {resetResult.name}</p>
            <p className="text-xs text-muted-foreground">
              Share this securely. They must change it when signing in.
            </p>
          </div>
          <div className="flex items-center gap-2">
            <code className="rounded bg-muted px-2 py-1 font-mono">{resetResult.password}</code>
            <CopyButton text={resetResult.password} />
            <Button size="sm" variant="ghost" onClick={() => setResetResult(null)}>
              Dismiss
            </Button>
          </div>
        </div>
      )}
      <Tabs defaultValue="users">
        <TabsList>
          <TabsTrigger value="users">Users</TabsTrigger>
          <TabsTrigger value="roles">Roles</TabsTrigger>
        </TabsList>
        <TabsContent value="users">
          <DataGrid
            rows={users.data?.data}
            loading={users.isLoading}
            rowKey={(u) => u.id}
            columns={[
              { header: "Name", cell: (u) => <span className="font-medium">{u.name}</span> },
              { header: "Contact", cell: (u) => u.phone ?? u.email ?? "-" },
              {
                header: "Employee profile",
                cell: (u) => {
                  if (u.employee_id) {
                    const emp = empById.get(u.employee_id);
                    return (
                      <Link
                        to={`/employees/${u.employee_id}`}
                        className="text-xs font-medium text-primary hover:underline"
                      >
                        {emp
                          ? `${emp.employee_no} · ${emp.position || "Employee"}`
                          : "View profile"}
                      </Link>
                    );
                  }
                  return (
                    <Badge variant="outline" className="text-[11px] text-muted-foreground">
                      {u.role_slug === "admin" || u.role_slug === "owner" ? "Owner" : "Not linked"}
                    </Badge>
                  );
                },
              },
              { header: "Role", cell: (u) => u.role_name },
              {
                header: "Default location",
                cell: (u) =>
                  (u.branch_ids ?? [])
                    .map((id) => branchNames.get(id) ?? id.slice(0, 6))
                    .join(", ") || "-",
              },
              {
                header: "Status",
                cell: (u) => <StatusBadge value={u.is_active ? "active" : "inactive"} />,
              },
              {
                header: "",
                className: "text-right",
                cell: (u) =>
                  manage && (
                    <div className="flex justify-end gap-1">
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
                        <Pencil size={14} /> Edit
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => {
                          setResetPw("");
                          setResetTarget(u);
                        }}
                      >
                        <KeyRound size={14} /> Reset PW
                      </Button>
                      {u.role_slug !== "admin" && u.role_slug !== "owner" && (
                        <Button variant="ghost" size="sm" onClick={() => setPermTarget(u)}>
                          <Shield size={14} /> Perms
                        </Button>
                      )}
                      <Button variant="ghost" size="sm" onClick={() => setKickTarget(u)}>
                        <LogOut size={14} /> Sign out
                      </Button>
                      <Button variant="ghost" size="sm" onClick={() => toggle.mutate(u)}>
                        {u.is_active ? "Deactivate" : "Activate"}
                      </Button>
                    </div>
                  ),
              },
            ]}
          />
          <Pager meta={users.data?.meta} onPage={setPage} />
        </TabsContent>
        <TabsContent value="roles">
          {manage && (
            <div className="mb-4 flex justify-end">
              <Button onClick={() => setEditingRole(null)}>
                <Plus size={16} /> New role
              </Button>
            </div>
          )}
          <DataGrid
            rows={roles.data}
            loading={roles.isLoading}
            rowKey={(r) => r.id}
            columns={[
              { header: "Role", cell: (r) => <span className="font-medium">{r.name}</span> },
              { header: "Rank", align: "right", cell: (r) => r.rank ?? "-" },
              { header: "Type", cell: (r) => (r.is_system ? "Built in" : "Custom") },
              {
                header: "",
                cell: (r) => (
                  <div className="flex justify-end gap-1.5">
                    <Button size="sm" variant="outline" onClick={() => setEditingRole(r.id)}>
                      {manage ? "Edit permissions" : "View"}
                    </Button>
                    {manage && !r.is_system && (
                      <Button
                        size="sm"
                        variant="ghost"
                        className="text-destructive"
                        onClick={() => setRetireRole(r)}
                      >
                        <Trash2 size={14} /> Retire
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
        title="Grant system access to an employee"
        description="To get a Manager, Staff, or Admin account, a person must be registered as an employee first."
        submitLabel={unlinkedEmployees.length > 0 ? "Grant access" : "Close"}
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
          <div className="space-y-3 rounded-xl border border-dashed bg-muted/30 p-5 text-center">
            <p className="text-sm font-medium">No employees waiting for a login</p>
            <p className="text-xs text-muted-foreground">
              Every active employee already has system access, or you haven&apos;t added an employee
              profile yet. Create the employee first, then assign their role.
            </p>
            <Button asChild size="sm">
              <Link to="/people?new=1">
                <UserPlus size={14} /> Create Employee First
              </Link>
            </Button>
          </div>
        ) : (
          <>
            <Field label="Employee *">
              <NativeSelect
                required
                value={draft.employee_id}
                onChange={(e) => onPickEmployee(e.target.value)}
              >
                <option value="">Choose an employee…</option>
                {unlinkedEmployees.map((e) => (
                  <option key={e.id} value={e.id}>
                    {e.name} ({e.employee_no}
                    {e.position ? ` · ${e.position}` : ""})
                  </option>
                ))}
              </NativeSelect>
            </Field>
            <div className="grid grid-cols-2 gap-3">
              <Field label="Phone">
                <Input
                  required={!draft.email.trim()}
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
            </div>
            <div className="grid grid-cols-2 gap-3">
              <Field label="Role *">
                <NativeSelect
                  required
                  value={draft.role_id}
                  onChange={(e) => setDraft((d) => ({ ...d, role_id: e.target.value }))}
                >
                  <option value="">Choose…</option>
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
            </div>
            <Field label="Temporary password *">
              <Input
                required
                minLength={8}
                type="password"
                value={draft.password}
                onChange={(e) => setDraft((d) => ({ ...d, password: e.target.value }))}
                placeholder="At least 8 characters"
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
        title={`Edit ${editingUser?.name ?? ""}`}
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
        <Field label="Name *">
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
          <Field label="Email">
            <Input
              type="email"
              value={draft.email}
              onChange={(e) => setDraft((d) => ({ ...d, email: e.target.value }))}
            />
          </Field>
        </div>
        <Field label="Role *">
          <NativeSelect
            required
            value={draft.role_id}
            onChange={(e) => setDraft((d) => ({ ...d, role_id: e.target.value }))}
          >
            <option value="">Choose…</option>
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
        {!editingUser?.employee_id && unlinkedEmployees.length > 0 && (
          <Field label="Link to employee profile">
            <NativeSelect
              value={draft.employee_id}
              onChange={(e) => setDraft((d) => ({ ...d, employee_id: e.target.value }))}
            >
              <option value="">Not linked</option>
              {unlinkedEmployees.map((e) => (
                <option key={e.id} value={e.id}>
                  {e.name} ({e.employee_no})
                </option>
              ))}
            </NativeSelect>
          </Field>
        )}
      </FormDialog>

      <FormDialog
        open={resetTarget !== null}
        onOpenChange={(o) => !o && setResetTarget(null)}
        title={`Reset password for ${resetTarget?.name ?? ""}`}
        description="Set a new temporary password. All active sessions for this user will be ended."
        submitLabel="Reset password"
        busy={resetPassword.isPending}
        onSubmit={() => resetPassword.mutate({ password: resetPw })}
      >
        <Field label="New temporary password *">
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
        title={`Force sign out ${kickTarget?.name ?? ""}?`}
        description="Every active session on all devices for this user will be ended immediately."
        onConfirm={() => forceSignOut.mutate(undefined)}
      />
      <ConfirmDialog
        open={retireRole !== null}
        onOpenChange={(o) => !o && setRetireRole(null)}
        title={`Retire role "${retireRole?.name ?? ""}"?`}
        description="Retired roles are marked inactive and can no longer be assigned. Reassign all users first."
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
    </>
  );
}
