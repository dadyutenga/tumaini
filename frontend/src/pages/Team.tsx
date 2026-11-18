import { useState } from "react";
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
  const [page, setPage] = useState(1);
  const users = useGetPage<SessionUser>("/users", { page, per_page: 25 });
  const roles = useGet<Role[]>("/roles", undefined, { enabled: can("roles.view") });
  const branches = useGet<Branch[]>("/branches", undefined, { enabled: can("branches.view") });
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState(blank);
  const create = useWrite<typeof blank, unknown>("POST", "/users", {
    invalidate: ["/users"],
    success: "User created",
    onSuccess: () => {
      setOpen(false);
      setDraft(blank);
    },
  });
  const toggle = useWrite<SessionUser>(
    "POST",
    (u) => `/users/${u.id}/${u.is_active ? "deactivate" : "activate"}`,
    {
      invalidate: ["/users"],
      success: "User updated",
    },
  );
  const manage = can("users.manage");
  const branchNames = new Map((branches.data ?? []).map((b) => [b.id, b.name]));

  return (
    <>
      <PageHeader
        title="Team and roles"
        description="Who can sign in, at which branches, and what they may do."
        actions={
          manage && (
            <Button onClick={() => setOpen(true)}>
              <Plus size={16} /> New user
            </Button>
          )
        }
      />
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
                cell: (u) =>
                  manage && (
                    <Button variant="ghost" size="sm" onClick={() => toggle.mutate(u)}>
                      {u.is_active ? "Deactivate" : "Activate"}
                    </Button>
                  ),
              },
            ]}
          />
          <Pager meta={users.data?.meta} onPage={setPage} />
        </TabsContent>
        <TabsContent value="roles">
          <DataGrid
            rows={roles.data}
            loading={roles.isLoading}
            rowKey={(r) => r.id}
            columns={[
              { header: "Role", cell: (r) => <span className="font-medium">{r.name}</span> },
              { header: "Type", cell: (r) => (r.is_system ? "Built in" : "Custom") },
            ]}
          />
        </TabsContent>
      </Tabs>
      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title="New user"
        busy={create.isPending}
        onSubmit={() => create.mutate(draft)}
      >
        <Field label="Name">
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
        <Field label="Temporary password">
          <Input
            required
            minLength={8}
            type="password"
            value={draft.password}
            onChange={(e) => setDraft((d) => ({ ...d, password: e.target.value }))}
          />
        </Field>
        <Field label="Role">
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
    </>
  );
}
