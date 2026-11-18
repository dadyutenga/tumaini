import { useEffect, useMemo, useState } from "react";
import { Input } from "@/components/ui/input";
import { Field, FormDialog, NativeSelect } from "@/components/shared/kit";
import { useGet, useWrite } from "@/hooks/useApi";

interface PermissionInfo {
  key: string;
  module: string;
  description: string;
  has_limit: boolean;
  is_sensitive: boolean;
}
interface Grant {
  key: string;
  scope: string;
  limit: string | null;
}
interface RoleDetail {
  id: string;
  name: string;
  slug: string;
  is_system: boolean;
  permissions: Grant[];
}

/** Create a role, or edit the permissions of an existing one. `roleId` null means a new role. */
export function RoleEditor({
  roleId,
  open,
  onClose,
}: {
  roleId: string | null;
  open: boolean;
  onClose: () => void;
}) {
  const catalogue = useGet<PermissionInfo[]>("/permissions", undefined, { enabled: open });
  const role = useGet<RoleDetail>(`/roles/${roleId}`, undefined, { enabled: open && !!roleId });
  const [name, setName] = useState("");
  const [grants, setGrants] = useState<Record<string, Grant>>({});
  const locked = role.data?.slug === "owner";

  useEffect(() => {
    if (!open) return;
    if (!roleId) {
      setName("");
      setGrants({});
    } else if (role.data) {
      setName(role.data.name);
      setGrants(Object.fromEntries(role.data.permissions.map((g) => [g.key, g])));
    }
  }, [open, roleId, role.data]);

  const save = useWrite<unknown, unknown>("PUT", `/roles/${roleId}`, {
    invalidate: ["/roles"],
    success: "Role saved",
    onSuccess: onClose,
  });
  const create = useWrite<unknown, unknown>("POST", "/roles", {
    invalidate: ["/roles"],
    success: "Role created",
    onSuccess: onClose,
  });
  const modules = useMemo(() => {
    const out = new Map<string, PermissionInfo[]>();
    for (const p of catalogue.data ?? []) out.set(p.module, [...(out.get(p.module) ?? []), p]);
    return [...out.entries()];
  }, [catalogue.data]);

  const toggle = (key: string, on: boolean) =>
    setGrants((g) => {
      const next = { ...g };
      if (on) next[key] = { key, scope: "branch", limit: null };
      else delete next[key];
      return next;
    });
  const patch = (key: string, change: Partial<Grant>) =>
    setGrants((g) => ({ ...g, [key]: { ...g[key], ...change } }));

  const submit = () => {
    const body = { name, permissions: Object.values(grants) };
    if (roleId) save.mutate(body);
    else create.mutate(body);
  };

  return (
    <FormDialog
      open={open}
      onOpenChange={(o) => !o && onClose()}
      title={roleId ? `Edit ${role.data?.name ?? "role"}` : "New role"}
      submitLabel={locked ? "Close" : "Save role"}
      busy={save.isPending || create.isPending}
      wide
      onSubmit={locked ? onClose : submit}
    >
      {locked && (
        <p className="text-sm text-muted-foreground">
          The Owner role always has every permission and cannot be changed.
        </p>
      )}
      <Field label="Role name">
        <Input
          required
          disabled={role.data?.is_system}
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
      </Field>
      <div className="max-h-[50vh] space-y-4 overflow-y-auto pr-1">
        {modules.map(([module, perms]) => (
          <section key={module}>
            <h3 className="mb-1 text-sm font-semibold capitalize">{module.replaceAll("_", " ")}</h3>
            <ul className="divide-y rounded-lg border">
              {perms.map((p) => {
                const g = grants[p.key];
                return (
                  <li key={p.key} className="flex flex-wrap items-center gap-2 px-3 py-2 text-sm">
                    <label className="flex flex-1 items-center gap-2">
                      <input
                        type="checkbox"
                        disabled={locked}
                        checked={locked || !!g}
                        onChange={(e) => toggle(p.key, e.target.checked)}
                      />
                      <span>
                        {p.description}
                        {p.is_sensitive && (
                          <span className="ml-1 text-xs text-destructive">sensitive</span>
                        )}
                      </span>
                    </label>
                    {g && !locked && (
                      <>
                        <NativeSelect
                          aria-label={`Scope for ${p.key}`}
                          className="h-8 w-28"
                          value={g.scope}
                          onChange={(e) => patch(p.key, { scope: e.target.value })}
                        >
                          <option value="own">Own</option>
                          <option value="branch">Branch</option>
                          <option value="all">All</option>
                        </NativeSelect>
                        {p.has_limit && (
                          <Input
                            aria-label={`Limit for ${p.key}`}
                            className="h-8 w-28"
                            inputMode="decimal"
                            placeholder="No limit"
                            value={g.limit ?? ""}
                            onChange={(e) => patch(p.key, { limit: e.target.value || null })}
                          />
                        )}
                      </>
                    )}
                  </li>
                );
              })}
            </ul>
          </section>
        ))}
      </div>
    </FormDialog>
  );
}
