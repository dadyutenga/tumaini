import { useEffect, useMemo, useState } from "react";
import { Input } from "@/components/ui/input";
import { Field, FormDialog } from "@/components/shared/kit";
import { useGet, useWrite } from "@/hooks/useApi";
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";
import type { Translations } from "@/features/i18n/i18n";

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

const limitLabel = (t: Translations): Record<string, string> => ({
  sale_discount_pct: t.admin.maxSaleDiscount,
  discount_approve_pct: t.admin.maxDiscountApprove,
  refund_approve_amount: t.admin.maxRefundAmount,
  expense_approve_amount: t.admin.maxExpenseAmount,
  price_change_approve_pct: t.admin.maxPriceChange,
});

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
  const { t } = useI18n();
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
    success: t.admin.roleSaved,
    onSuccess: onClose,
  });
  const create = useWrite<unknown, unknown>("POST", "/roles", {
    invalidate: ["/roles"],
    success: t.admin.roleCreated,
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
      title={
        roleId ? fmt(t.common.edit2, { name: role.data?.name ?? t.admin.role }) : t.admin.newRole
      }
      submitLabel={locked ? t.common.close : t.admin.saveRole}
      busy={save.isPending || create.isPending}
      wide
      onSubmit={locked ? onClose : submit}
    >
      {locked && <p className="text-sm text-muted-foreground">{t.admin.theAdminRoleAlwaysHas}</p>}
      <div className="grid gap-3 sm:grid-cols-[1fr_7rem]">
        <Field label={t.admin.roleName}>
          <Input
            required
            disabled={role.data?.is_system}
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </Field>
        <Field label={t.admin.rank1100}>
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
      <Field label={t.common.description}>
        <Input
          disabled={locked || role.data?.is_system}
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder={t.admin.optionalDescriptionOfWhatThis}
        />
      </Field>
      <div className="max-h-[50vh] space-y-4 overflow-y-auto pr-1">
        {modules.map(([module, perms]) => (
          <section key={module}>
            <h3 className="mb-1 text-sm font-semibold capitalize">
              {(t.admin.modules as Record<string, string>)[module] ?? module.replaceAll("_", " ")}
            </h3>
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
                        {(t.admin.permissions as Record<string, string>)[p.key] ?? p.description}
                        <span className="ml-1.5 font-mono text-[11px] text-muted-foreground">
                          ({p.key})
                        </span>
                        {p.is_sensitive && (
                          <span className="ml-1.5 text-xs text-destructive">
                            {t.admin.sensitive}
                          </span>
                        )}
                      </span>
                    </label>
                    {g && !locked && p.has_limit && (
                      <Input
                        aria-label={fmt(t.admin.limitFor, { key: p.key })}
                        className="h-8 w-36"
                        inputMode="decimal"
                        placeholder={
                          p.limit_code
                            ? (limitLabel(t)[p.limit_code] ?? t.admin.noLimit)
                            : t.admin.noLimit
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
