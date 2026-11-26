import { useAuth } from "@/features/auth/auth";
import { useGet } from "@/hooks/useApi";
import { NativeSelect } from "@/components/shared/kit";
import type { Branch } from "@/types";

/** Picks the active location sent as X-Branch-Id. Hidden when the business has only one location. */
export function BranchSwitcher() {
  const { branchId, setBranchId } = useAuth();
  const { data } = useGet<Branch[]>("/branches");
  const active = (data ?? []).filter((b) => b.is_active);
  if (active.length <= 1) return null;
  return (
    <NativeSelect
      aria-label="Active location"
      className="h-8 w-36 text-xs sm:w-40"
      value={branchId ?? "all"}
      onChange={(e) => setBranchId(e.target.value)}
    >
      <option value="all">All locations</option>
      {active.map((b) => (
        <option key={b.id} value={b.id}>
          {b.name}
        </option>
      ))}
    </NativeSelect>
  );
}
