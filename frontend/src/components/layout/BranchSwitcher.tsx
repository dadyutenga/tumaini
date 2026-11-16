import { useAuth } from "@/features/auth/auth";
import { useGet } from "@/hooks/useApi";
import { NativeSelect } from "@/components/shared/kit";
import type { Branch } from "@/types";

/** Picks the branch sent as X-Branch-Id. Hidden when the user has only one branch. */
export function BranchSwitcher() {
  const { me, branchId, setBranchId, can } = useAuth();
  const { data } = useGet<Branch[]>("/branches", undefined, { enabled: can("branches.view") });
  const allowed = me?.branches ?? [];
  if (allowed.length <= 1) return null;
  const names = new Map((data ?? []).map((b) => [b.id, b.name]));
  return (
    <NativeSelect
      aria-label="Active branch"
      className="h-9 w-40"
      value={branchId ?? ""}
      onChange={(e) => setBranchId(e.target.value)}
    >
      {allowed.map((id) => (
        <option key={id} value={id}>
          {names.get(id) ?? id.slice(0, 8)}
        </option>
      ))}
    </NativeSelect>
  );
}
