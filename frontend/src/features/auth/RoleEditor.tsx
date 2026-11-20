import { useEffect, useMemo, useState } from "react";
import { Input } from "@/components/ui/input";
import { Field, FormDialog } from "@/components/shared/kit";
import { useGet, useWrite } from "@/hooks/useApi";

interface PermissionInfo {
  key: string;
  module: string;
  description: string;
  has_limit: boolean;
  limit_code?: string;
  is_sensitive: boolean;
}
interface Grant {
  key: string;
  limit: string | null;
}
interface RoleDetail {
  id: string;
  name: string;
  slug: string;
  description?: string | null;
  rank?: number;
  is_system: boolean;
  permissions: Grant[];
}

const LIMIT_LABEL: Record<string, string> = {
  sale_discount_pct: "Max sale discount %",
  discount_approve_pct: "Max discount approve %",
  refund_approve_amount: "Max refund amount",
  expense_approve_amount: "Max expense amount",
  price_change_approve_pct: "Max price change %",
};

/** Create a role, or edit the permissions & authority limits of an existing one. */
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
  const [description, setDescription] = useState("");
  const [rank, setRank] = useState("30");
  const [grants, setGrants] = useState<Record<string, Grant>>({});
  const locked = role.data?.slug === "admin" || role.data?.slug === "owner";

  useEffect(() => {
    if (!open) return;
    if (!roleId) {
      setName("");
      setDescription("");
      setRank("30");
      setGrants({});
    } else if (role.data) {
      setName(role.data.name);
      setDescription(role.data.description ?? "");
      setRank(String(role.data.rank ?? 30));
      setGrants(
        Object.fromEntries(
          (role.data.permissions ?? []).map((g) => [g.key, { key: g.key, limit: g.limit ?? null }]),
        ),
      );
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
      if (on) next[key] = { key, limit: null };
      else delete next[key];
      return next;
    });
  const patch = (key: string, limit: string | null) =>
    setGrants((g) => ({ ...g, [key]: { ...g[key], limit } }));

  const submit = () => {
    const body = {
      name,
      description,
      rank: Number(rank) || 30,
      permissions: Object.values(grants).map(({ key, limit }) => ({ key, limit })),
    };
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
          The Admin role always has every permission and cannot be changed.
        </p>
      )}
      <div className="grid gap-3 sm:grid-cols-[1fr_7rem]">
        <Field label="Role name">
          <Input
            required
            disabled={role.data?.is_system}
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </Field>
        <Field label="Rank (1–100)">
          <Input
            type="number"
            min={1}
            max={100}
            disabled={locked || role.data?.is_system}
            value={rank}
            onChange={(e) => setRank(e.target.value)}
          />
        </Field>
      </div>
      <Field label="Description">
        <Input
          disabled={locked || role.data?.is_system}
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder="Optional description of what this role covers"
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
                        <span className="ml-1.5 font-mono text-[11px] text-muted-foreground">
                          ({p.key})
                        </span>
                        {p.is_sensitive && (
                          <span className="ml-1.5 text-xs text-destructive">sensitive</span>
                        )}
                      </span>
                    </label>
                    {g && !locked && p.has_limit && (
                      <Input
                        aria-label={`Limit for ${p.key}`}
                        className="h-8 w-36"
                        inputMode="decimal"
                        placeholder={
                          p.limit_code ? (LIMIT_LABEL[p.limit_code] ?? "No limit") : "No limit"
                        }
                        value={g.limit ?? ""}
                        onChange={(e) => patch(p.key, e.target.value || null)}
                      />
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
