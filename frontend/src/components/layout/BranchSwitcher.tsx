import { useEffect } from "react";
import { useAuth } from "@/features/auth/auth";
import { useBusiness } from "@/features/business/useBusiness";
import { useGet } from "@/hooks/useApi";
import { NativeSelect } from "@/components/shared/kit";
import type { Branch } from "@/types";
import { useI18n } from "@/features/i18n/i18n";

/**
 * Picks the active location sent as X-Branch-Id, among the branches of the business the switcher
 * shows. Hidden when there is nothing to choose.
 */
export function BranchSwitcher() {
  const { t } = useI18n();
  const { me, branchId, setBranchId } = useAuth();
  const { selected, works } = useBusiness();
  const { data } = useGet<Branch[]>("/branches");
  const active = (data ?? []).filter(
    (b) =>
      b.is_active &&
      works(b.business_unit) &&
      (selected === "all" || !b.business_unit || b.business_unit === selected),
  );
  // Someone limited to some branches cannot look at all of them at once.
  const limited = (me?.locations?.length ?? 0) > 0;
  const fits = !branchId || branchId === "all" || active.some((b) => b.id === branchId);
  const first = active[0]?.id;
  // With one branch to choose from, that is the branch: selling needs one and the picker is hidden.
  const only = active.length === 1 ? first : undefined;
  const mayLookEverywhere = !limited && Boolean(me?.is_owner || (data?.length ?? 0) > 1);

  // A branch of the business that was just switched away from would show nothing.
  useEffect(() => {
    if (!data) return;
    if (only) {
      if (branchId !== only) setBranchId(only);
      return;
    }
    if (fits) return;
    if (mayLookEverywhere) setBranchId("all");
    else if (first) setBranchId(first);
  }, [data, only, branchId, fits, mayLookEverywhere, first, setBranchId]);

  if (active.length <= 1 && (fits || only)) return null;
  return (
    <NativeSelect
      aria-label={t.common.activeLocation}
      className="h-8 w-28 text-xs sm:w-40"
      value={branchId ?? "all"}
      onChange={(e) => setBranchId(e.target.value)}
    >
      {!limited && <option value="all">{t.common.allLocations}</option>}
      {active.map((b) => (
        <option key={b.id} value={b.id}>
          {b.name}
        </option>
      ))}
    </NativeSelect>
  );
}
