import { useState } from "react";
import { KeyRound, LogOut, Pencil, Plus } from "lucide-react";
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
import type { Branch, SessionUser } from "@/types";

interface Role {
  id: string;
  name: string;
  slug: string;
  is_system: boolean;
}
const blank = {
  name: "",
  phone: "",
  email: "",
  password: "",
  role_id: "",
  branch_ids: [] as string[],
};

export default function Team() {
  const { can } = useAuth();
  const [editingRole, setEditingRole] = useState<string | null | undefined>(undefined);
  const [page, setPage] = useState(1);
  const users = useGetPage<SessionUser>("/users", { page, per_page: 25 });
  const roles = useGet<Role[]>("/roles", undefined, { enabled: can("roles.view") });
  const branches = useGet<Branch[]>("/branches", undefined, { enabled: can("branches.view") });
  const [open, setOpen] = useState(false);
  const [editingUser, setEditingUser] = useState<SessionUser | null>(null);
  const [draft, setDraft] = useState(blank);
  const [resetTarget, setResetTarget] = useState<SessionUser | null>(null);
  const [resetPw, setResetPw] = useState("");
  const [resetResult, setResetResult] = useState<{ name: string; password: string } | null>(null);
  const [kickTarget, setKickTarget] = useState<SessionUser | null>(null);

  const create = useWrite<typeof blank, unknown>("POST", "/users", {
    invalidate: ["/users"],
    success: "User created",
    onSuccess: () => {
      setOpen(false);
      setDraft(blank);
    },
  });
  const update = useWrite<Omit<typeof blank, "password">, unknown>(
    "PUT",
    () => `/users/${editingUser?.id}`,
    {
      invalidate: ["/users"],
      success: "User updated",
      onSuccess: () => {
        setEditingUser(null);
        setDraft(blank);
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

  const manage = can("users.manage");
  const canSessions = can("sessions.manage");
  const branchNames = new Map((branches.data ?? []).map((b) => [b.id, b.name]));

  return (
    <>
      <PageHeader
        title="Team and roles"
        description="Who can sign in, at which branches, and what they may do."
        actions={
          manage && (
            <Button
              onClick={() => {
                setDraft(blank);
                setOpen(true);
              }}
            >
              <Plus size={16} /> New user
            </Button>
          )
        }
      />
      {resetResult && (
        <div className="mb-4 flex flex-wrap items-center justify-between gap-2 rounded-xl border bg-card p-4 text-sm">
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
          {can("users.view") && <TabsTrigger value="users">Users</TabsTrigger>}
          {can("roles.view") && <TabsTrigger value="roles">Roles</TabsTrigger>}
        </TabsList>
        <TabsContent value="users">
          <DataGrid
            rows={users.data?.data}
            loading={users.isLoading}
            rowKey={(u) => u.id}
            columns={[
              { header: "Name", cell: (u) => <span className="font-medium">{u.name}</span> },
              { header: "Contact", cell: (u) => u.phone ?? u.email ?? "-" },
              { header: "Role", cell: (u) => u.role_name },
              {
                header: "Branches",
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
                  (manage || canSessions) && (
                    <div className="flex justify-end gap-1">
                      {manage && (
                        <>
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => {
                              setDraft({
                                name: u.name,
                                phone: u.phone ?? "",
                                email: u.email ?? "",
                                password: "",
                                role_id: u.role_id,
                                branch_ids: u.branch_ids ?? [],
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
                        </>
                      )}
                      {canSessions && (
                        <Button variant="ghost" size="sm" onClick={() => setKickTarget(u)}>
                          <LogOut size={14} /> Sign out
                        </Button>
                      )}
                      {manage && (
                        <Button variant="ghost" size="sm" onClick={() => toggle.mutate(u)}>
                          {u.is_active ? "Deactivate" : "Activate"}
                        </Button>
                      )}
                    </div>
                  ),
              },
            ]}
          />
          <Pager meta={users.data?.meta} onPage={setPage} />
        </TabsContent>
        <TabsContent value="roles">
          {can("roles.manage") && (
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
              { header: "Type", cell: (r) => (r.is_system ? "Built in" : "Custom") },
              {
                header: "",
                cell: (r) => (
                  <Button size="sm" variant="outline" onClick={() => setEditingRole(r.id)}>
                    {can("roles.manage") ? "Edit permissions" : "View"}
                  </Button>
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
      <FormDialog
        open={open || editingUser !== null}
        onOpenChange={(o) => {
          if (!o) {
            setOpen(false);
            setEditingUser(null);
            setDraft(blank);
          }
        }}
        title={editingUser ? `Edit ${editingUser.name}` : "New user"}
        busy={create.isPending || update.isPending}
        onSubmit={() => {
          if (editingUser) {
            const { password: _pw, ...rest } = draft;
            void _pw;
            update.mutate(rest);
          } else {
            create.mutate(draft);
          }
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
        {!editingUser && (
          <Field label="Temporary password *">
            <Input
              required
              minLength={8}
              type="password"
              value={draft.password}
              onChange={(e) => setDraft((d) => ({ ...d, password: e.target.value }))}
            />
          </Field>
        )}
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
        <Field label="Branches">
          <div className="space-y-1">
            {(branches.data ?? []).map((b) => (
              <label key={b.id} className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={draft.branch_ids.includes(b.id)}
                  onChange={(e) =>
                    setDraft((d) => ({
                      ...d,
                      branch_ids: e.target.checked
                        ? [...d.branch_ids, b.id]
                        : d.branch_ids.filter((x) => x !== b.id),
                    }))
                  }
                />
                {b.name}
              </label>
            ))}
          </div>
        </Field>
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
    </>
  );
}
