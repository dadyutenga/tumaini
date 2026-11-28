import { useEffect, useMemo, useState } from "react";
import { Shield } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { NativeSelect } from "@/components/shared/kit";
import { useGet, useWrite } from "@/hooks/useApi";
import { useI18n } from "@/features/i18n/i18n";
import { fmt } from "@/features/i18n/format";
import type { Translations } from "@/features/i18n/i18n";

interface PermCatalogItem {
  key: string;
  module: string;
  description: string;
  is_sensitive: boolean;
  has_limit: boolean;
}

interface UserPermOverride {
  permission_key: string;
  allowed: boolean;
  reason: string;
  expires_at: string | null;
}

interface UserPermsRes {
  user_id: string;
  role_slug: string;
  role_permissions: Array<{ permission_key: string }>;
  overrides: UserPermOverride[];
}

type Mode = "default" | "allow" | "revoke";

/** What a permission allows, in the user's language. */
const describe = (t: Translations, p: { key: string; description: string }) =>
  (t.admin.permissions as Record<string, string>)[p.key] ?? p.description;

export function UserPermissionsDialog({
  userId,
  userName,
  open,
  onClose,
}: {
  userId: string | null;
  userName: string;
  open: boolean;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const catalog = useGet<PermCatalogItem[]>("/permissions", undefined, { enabled: open });
  const data = useGet<UserPermsRes>(`/users/${userId}/permissions`, undefined, {
    enabled: open && Boolean(userId),
  });
  const [search, setSearch] = useState("");
  const [draft, setDraft] = useState<
    Record<string, { mode: Mode; reason: string; expires_at: string }>
  >({});

  useEffect(() => {
    if (!open || !data.data || !Array.isArray(data.data.overrides)) return;
    const next: Record<string, { mode: Mode; reason: string; expires_at: string }> = {};
    for (const ov of data.data.overrides) {
      next[ov.permission_key] = {
        mode: ov.allowed ? "allow" : "revoke",
        reason: ov.reason ?? "",
        expires_at: ov.expires_at ? ov.expires_at.slice(0, 10) : "",
      };
    }
    setDraft(next);
  }, [open, data.data]);

  const save = useWrite<{ overrides: UserPermOverride[] }, unknown>(
    "PUT",
    () => `/users/${userId}/permissions`,
    {
      invalidate: [`/users/${userId}/permissions`, "/users"],
      success: t.admin.userPermissionOverridesSaved,
      onSuccess: () => onClose(),
    },
  );

  const roleSet = useMemo(
    () => new Set((data.data?.role_permissions ?? []).map((rp) => rp.permission_key)),
    [data.data],
  );

  const grouped = useMemo(() => {
    const q = search.trim().toLowerCase();
    const map = new Map<string, PermCatalogItem[]>();
    for (const p of catalog.data ?? []) {
      if (
        q &&
        !p.key.toLowerCase().includes(q) &&
        !describe(t, p).toLowerCase().includes(q) &&
        !p.module.toLowerCase().includes(q)
      ) {
        continue;
      }
      const list = map.get(p.module) ?? [];
      list.push(p);
      map.set(p.module, list);
    }
    return [...map.entries()];
  }, [catalog.data, search, t]);

  function onSave() {
    const overrides: UserPermOverride[] = [];
    for (const [permission_key, v] of Object.entries(draft)) {
      if (v.mode === "default") continue;
      overrides.push({
        permission_key,
        allowed: v.mode === "allow",
        reason: v.reason.trim() || "Set by an administrator",
        expires_at: v.expires_at ? new Date(`${v.expires_at}T23:59:59Z`).toISOString() : null,
      });
    }
    save.mutate({ overrides });
  }

  const isLocked = data.data?.role_slug === "admin" || data.data?.role_slug === "owner";

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="flex max-h-[90vh] flex-col sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Shield size={17} /> {fmt(t.admin.customPermissions, { userName })}
          </DialogTitle>
          <DialogDescription>{t.admin.overrideIndividualPermissionsOnTop}</DialogDescription>
        </DialogHeader>

        {isLocked ? (
          <p className="py-8 text-center text-sm text-muted-foreground">
            {t.admin.adminAccountsAlwaysHoldEvery}
          </p>
        ) : (
          <>
            <div className="flex items-center gap-2">
              <Input
                placeholder={t.admin.filterPermissionsByModuleOr}
                value={search}
                onChange={(e) => setSearch(e.target.value)}
              />
              <Button variant="ghost" size="sm" onClick={() => setDraft({})}>
                {t.admin.resetAllToRoleDefault}
              </Button>
            </div>

            <div className="flex-1 space-y-4 overflow-y-auto pr-1">
              {grouped.map(([mod, items]) => (
                <div key={mod} className="rounded-xl border">
                  <div className="border-b bg-muted/40 px-3 py-1.5 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                    {mod}
                  </div>
                  <div className="divide-y">
                    {items.map((p) => {
                      const hasRole = roleSet.has(p.key);
                      const cur = draft[p.key] ?? {
                        mode: "default" as Mode,
                        reason: "",
                        expires_at: "",
                      };
                      return (
                        <div
                          key={p.key}
                          className="flex flex-wrap items-center justify-between gap-2 px-3 py-2 text-xs"
                        >
                          <div className="min-w-0 flex-1">
                            <div className="flex items-center gap-1.5">
                              <span className="font-mono font-medium">{p.key}</span>
                              <Badge
                                variant={hasRole ? "secondary" : "outline"}
                                className="text-[10px]"
                              >
                                {fmt(t.admin.role2, {
                                  value: hasRole ? t.admin.allow : t.admin.deny,
                                })}
                              </Badge>
                            </div>
                            <p className="text-muted-foreground">{describe(t, p)}</p>
                          </div>
                          <div className="flex flex-wrap items-center gap-1.5">
                            <NativeSelect
                              className="h-7 w-32 text-xs"
                              value={cur.mode}
                              onChange={(e) =>
                                setDraft((prev) => ({
                                  ...prev,
                                  [p.key]: { ...cur, mode: e.target.value as Mode },
                                }))
                              }
                            >
                              <option value="default">{t.admin.roleDefault}</option>
                              <option value="allow">{t.admin.allowOverride}</option>
                              <option value="revoke">{t.admin.revokeOverride}</option>
                            </NativeSelect>
                            {cur.mode !== "default" && (
                              <>
                                <Input
                                  className="h-7 w-36 text-xs"
                                  placeholder={t.common.reason}
                                  value={cur.reason}
                                  onChange={(e) =>
                                    setDraft((prev) => ({
                                      ...prev,
                                      [p.key]: { ...cur, reason: e.target.value },
                                    }))
                                  }
                                />
                                <Input
                                  type="date"
                                  title={t.admin.optionalExpiryDate}
                                  className="h-7 w-32 text-xs"
                                  value={cur.expires_at}
                                  onChange={(e) =>
                                    setDraft((prev) => ({
                                      ...prev,
                                      [p.key]: { ...cur, expires_at: e.target.value },
                                    }))
                                  }
                                />
                              </>
                            )}
                          </div>
                        </div>
                      );
                    })}
                  </div>
                </div>
              ))}
            </div>
          </>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t.common.cancel}
          </Button>
          {!isLocked && (
            <Button disabled={save.isPending} onClick={onSave}>
              {save.isPending ? t.common.saving : t.admin.saveOverrides}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
